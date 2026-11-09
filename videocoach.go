package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// The video coach: after a game, the app opens its replay, records a short clip of each of your deaths with the
// replay's own recorder, and the AI (Gemini, which understands video) watches them and says what went wrong.
// The result is saved in the game's report ("video") and the page reads it out.

type vcDeath struct {
	T     float64  `json:"t"`
	Kind  string   `json:"kind"`
	By    []string `json:"by"`
	Clip  string   `json:"clip,omitempty"`
	Error string   `json:"error,omitempty"` // what went wrong (AI)
	Fix   string   `json:"fix,omitempty"`   // what to do next time (AI)
}

type vcResult struct {
	At      int64     `json:"at"`
	Model   string    `json:"model"`
	Lang    string    `json:"lang"`
	Summary string    `json:"summary"`
	Deaths  []vcDeath `json:"deaths"`
	Note    string    `json:"note,omitempty"`
}

var (
	vcMu     sync.Mutex
	vcState  = map[string]any{"busy": false}
	vcQueue  []string // game dates waiting for the replay
	vcStop   bool
	vcLang   = "es" // language the explanation is written in (the voice's)
	vcMode   = "auto"
	vcActive bool
)

func vcSet(busy bool, date, text string) {
	vcMu.Lock()
	vcState = map[string]any{"busy": busy, "date": date, "text": text, "at": time.Now().Unix()}
	vcMu.Unlock()
	if text != "" {
		logf("Coach de vídeo: %s", text)
	}
}

func clipsRoot() string { return filepath.Join(filepath.Dir(dataDir()), "clips") }

// vcEnqueue: called when a game's Riot data (and so its match id) has arrived
func vcEnqueue(date string, manual bool) {
	c := loadConfig()
	if c.AIKey == "" {
		return
	}
	vcMu.Lock()
	if !manual && vcMode != "auto" {
		vcMu.Unlock()
		return
	}
	for _, d := range vcQueue {
		if d == date {
			vcMu.Unlock()
			return
		}
	}
	vcQueue = append(vcQueue, date)
	start := !vcActive
	vcActive = true
	vcMu.Unlock()
	if start {
		go vcLoop()
	}
}

func vcLoop() {
	defer func() {
		if e := recover(); e != nil {
			logf("Coach de vídeo: error %v", e)
		}
		vcMu.Lock()
		vcActive = false
		vcMu.Unlock()
	}()
	for {
		vcMu.Lock()
		if len(vcQueue) == 0 {
			vcMu.Unlock()
			return
		}
		date := vcQueue[0]
		vcQueue = vcQueue[1:]
		vcStop = false
		vcMu.Unlock()
		if err := vcAnalyse(date); err != nil {
			vcSet(false, date, "no s'ha pogut fer: "+err.Error())
		}
	}
}

// the client must be free: not queueing, not in champion select or a game. Waits up to 20 minutes.
func vcWaitFree(date string) bool {
	for i := 0; i < 60; i++ {
		ph := gameflowPhase()
		if ph == "None" || ph == "Lobby" || ph == "EndOfGame" {
			if !liveNow.Load() {
				return true
			}
		}
		if i == 0 {
			vcSet(true, date, "esperant que el client estigui lliure per obrir la repetició…")
		}
		vcMu.Lock()
		stop := vcStop
		vcMu.Unlock()
		if stop {
			return false
		}
		time.Sleep(20 * time.Second)
	}
	return false
}

func vcStopped() bool {
	vcMu.Lock()
	defer vcMu.Unlock()
	return vcStop
}

func vcAnalyse(date string) error {
	g, rec, err := readGame(date)
	if err != nil {
		return err
	}
	if v, ok := g.Report["video"].(map[string]any); ok && v["summary"] != nil {
		return nil // already done
	}
	mm, _ := g.Report["map"].(map[string]any)
	matchID, _ := mm["match_id"].(string)
	if i := strings.LastIndex(matchID, "_"); i >= 0 {
		matchID = matchID[i+1:]
	}
	if matchID == "" && g.GameID > 0 {
		matchID = fmt.Sprint(g.GameID)
	}
	if matchID == "" {
		return errors.New("encara no tinc la partida de Riot")
	}
	// the deaths: up to 4, the ones of the first 25 minutes first (they decide the most), in order
	var all []vcDeath
	if raw, err := json.Marshal(g.Report["deaths"]); err == nil {
		json.Unmarshal(raw, &all)
	}
	if len(all) == 0 {
		vcSaveResult(date, vcResult{At: time.Now().Unix(), Summary: "", Note: "cap mort", Deaths: []vcDeath{}})
		vcSet(false, date, "cap mort a analitzar")
		return nil
	}
	pick := []vcDeath{}
	for _, d := range all {
		if d.T < 1500 && len(pick) < 4 {
			pick = append(pick, d)
		}
	}
	for _, d := range all {
		if d.T >= 1500 && len(pick) < 4 {
			pick = append(pick, d)
		}
	}
	sort.Slice(pick, func(i, j int) bool { return pick[i].T < pick[j].T })
	if !vcWaitFree(date) {
		return errors.New("el client no ha estat lliure (estaves jugant o a la cua)")
	}
	vcSet(true, date, "obrint la repetició per mirar les teves morts…")
	err = replayOpen(matchID, func(t string) { vcSet(true, date, t) })
	// just after the game Riot may not have the replay ready yet: try again a few minutes later
	for try := 0; err != nil && strings.Contains(err.Error(), "Riot ja no té") && try < 10 && !vcStopped(); try++ {
		vcSet(true, date, "Riot encara està preparant la repetició: ho torno a provar d'aquí un minut")
		for k := 0; k < 6 && !vcStopped(); k++ {
			time.Sleep(10 * time.Second)
		}
		if !vcWaitFree(date) {
			return errors.New("el client no ha estat lliure (estaves jugant o a la cua)")
		}
		err = replayOpen(matchID, func(t string) { vcSet(true, date, t) })
	}
	if err != nil {
		if err == errReplayNoAPI {
			killReplay()
			// the API is on now (game.cfg): the next time the game starts it will work
			return errors.New("he activat l'API del replay: es farà a partir de la propera partida")
		}
		return err
	}
	defer func() { time.Sleep(time.Second); killReplay() }()
	time.Sleep(2 * time.Second)
	name := rec.Me.RiotIDGameName
	if name == "" {
		name = strings.SplitN(rec.Me.RiotID, "#", 2)[0]
	}
	if name == "" {
		name = rec.Me.SummonerName
	}
	// camera on you, the interface on (the minimap is what tells where everybody was)
	replayPost("/replay/render", map[string]any{"cameraMode": "top", "selectionName": name, "cameraAttached": true,
		"interfaceMinimap": true, "interfaceTimeline": true, "interfaceScore": true, "interfaceChat": false})
	dir := filepath.Join(clipsRoot(), strings.NewReplacer(":", "", " ", "_").Replace(date))
	os.MkdirAll(dir, 0o755)
	clips := [][]byte{}
	used := []vcDeath{}
	for i, d := range pick {
		if vcStopped() || gameflowPhase() != "WatchInProgress" {
			return errors.New("aturat (s'ha tancat la repetició)")
		}
		vcSet(true, date, fmt.Sprintf("gravant la mort %d de %d (minut %d:%02d)…", i+1, len(pick), int(d.T)/60, int(d.T)%60))
		file := filepath.Join(dir, fmt.Sprintf("mort_%d.webm", int(d.T)))
		b, err := vcRecord(d.T-22, d.T+4, file)
		if err != nil {
			logf("Coach de vídeo: no he pogut gravar la mort del minut %d:%02d: %v", int(d.T)/60, int(d.T)%60, err)
			continue
		}
		total := len(b)
		for _, c := range clips {
			total += len(c)
		}
		if total > 18<<20 { // Gemini takes up to 20 MB inline
			break
		}
		d.Clip = filepath.Base(file)
		clips = append(clips, b)
		used = append(used, d)
	}
	killReplay()
	if len(clips) == 0 {
		return errors.New("no s'ha pogut gravar cap clip de la repetició")
	}
	vcSet(true, date, fmt.Sprintf("la IA està mirant %d clips…", len(clips)))
	res, err := vcAsk(rec, g.Report, used, clips)
	if err != nil {
		return err
	}
	vcSaveResult(date, res)
	vcPrune()
	vcSet(false, date, "fet")
	return nil
}

func replayPost(path string, body any) error {
	raw, _ := json.Marshal(body)
	req, _ := http.NewRequest("POST", replayAPIBase()+path, bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	r, err := liveClient.Do(req)
	if err != nil {
		return err
	}
	r.Body.Close()
	if r.StatusCode != 200 {
		return fmt.Errorf("status %d", r.StatusCode)
	}
	return nil
}

// vcRecord: the replay's own recorder, from..to seconds of game time, into a .webm file
func vcRecord(from, to float64, file string) ([]byte, error) {
	if from < 0 {
		from = 0
	}
	os.Remove(file)
	if err := replayPost("/replay/playback", map[string]any{"time": from, "paused": true, "speed": 1.0}); err != nil {
		return nil, err
	}
	time.Sleep(800 * time.Millisecond)
	if err := replayPost("/replay/recording", map[string]any{"recording": true, "path": file, "codec": "webm", "startTime": from, "endTime": to,
		"currentTime": from, "framesPerSecond": 12, "width": 854, "height": 480, "lossless": false, "enforceFrameRate": true, "replaySpeed": 1.0}); err != nil {
		return nil, err
	}
	replayPost("/replay/playback", map[string]any{"paused": false, "speed": 1.0})
	deadline := time.Now().Add(time.Duration(to-from+25) * time.Second)
	for time.Now().Before(deadline) {
		time.Sleep(time.Second)
		var st struct {
			Recording bool `json:"recording"`
		}
		r, err := liveClient.Get(replayAPIBase() + "/replay/recording")
		if err == nil {
			json.NewDecoder(r.Body).Decode(&st)
			r.Body.Close()
			if !st.Recording {
				break
			}
		}
	}
	replayPost("/replay/recording", map[string]any{"recording": false})
	time.Sleep(1500 * time.Millisecond) // the file is finished writing
	b, err := os.ReadFile(file)
	if err != nil {
		return nil, err
	}
	if len(b) < 10000 {
		return nil, fmt.Errorf("clip buit (%d bytes)", len(b))
	}
	return b, nil
}

var vcHTTP = &http.Client{Timeout: 150 * time.Second}

const vcSystem = `Ets un coach de League of Legends de nivell Challenger. Mires clips de les morts d'un jugador de Platí (el campió que segueix la càmera és el seu) i li dius, amb franquesa i en concret, per què ha mort i què havia de fer.
Mira sobretot: la seva posició respecte a la seva onada i les torres, si el jungla rival era visible al minimapa abans, quants rivals li faltaven al mapa, si tenia visió (wards), si ha entrat sol, si ha gastat el Flash o les habilitats clau abans, i on eren els seus aliats.
La repetició mostra tot el mapa, sense boira de guerra: separa el que ell podia saber (wards, rivals que havien sortit al minimapa, rivals que faltaven) del que no podia veure.
No t'inventis el que no es veu al clip: si no ho pots saber, digues-ho. Res de consells genèrics.
Respon NOMÉS amb JSON vàlid, sense res més, amb aquesta forma:
{"deaths":[{"clip":1,"error":"què ha fet malament (1 o 2 frases)","fix":"què havia de fer (1 frase)"}],"summary":"el patró principal de totes les morts i el consell més important per a la propera partida (1 o 2 frases)"}`

func vcAsk(rec *Record, report map[string]any, deaths []vcDeath, clips [][]byte) (vcResult, error) {
	c := loadConfig()
	vcMu.Lock()
	lang := vcLang
	vcMu.Unlock()
	langName := map[string]string{"es": "castellà (d'Espanya)", "en": "anglès", "ca": "català"}[lang]
	if langName == "" {
		langName, lang = "castellà (d'Espanya)", "es"
	}
	parts := []map[string]any{}
	champ := rec.Me.ChampionName
	opp, _ := report["opponent"].(string)
	parts = append(parts, map[string]any{"text": fmt.Sprintf("El jugador porta %s (posició %s)%s. Escriu els textos del JSON en %s, parlant-li de tu.", champ, rec.Me.Position,
		map[bool]string{true: ", contra " + opp, false: ""}[opp != ""], langName)})
	for i, d := range deaths {
		who := strings.Join(d.By, ", ")
		parts = append(parts, map[string]any{"text": fmt.Sprintf("Clip %d: mort al minut %d:%02d (el clip comença 22 s abans i acaba 4 s després). L'app ho ha classificat com a \"%s\"; l'han matat: %s.",
			i+1, int(d.T)/60, int(d.T)%60, d.Kind, who)})
		parts = append(parts, map[string]any{"inline_data": map[string]any{"mime_type": "video/webm", "data": base64.StdEncoding.EncodeToString(clips[i])}})
	}
	body, _ := json.Marshal(map[string]any{
		"systemInstruction": map[string]any{"parts": []map[string]string{{"text": vcSystem}}},
		"contents":          []map[string]any{{"role": "user", "parts": parts}},
		"generationConfig":  map[string]any{"maxOutputTokens": 1500, "temperature": 0.4, "responseMimeType": "application/json"},
	})
	if _, err := pickGeminiModel(c.AIKey); err != nil {
		return vcResult{}, err
	}
	gemMu.Lock()
	order := []string{}
	for _, m := range append([]string{gemModel}, gemCands...) {
		if m == "" || time.Now().Before(gemBlocked[m]) {
			continue
		}
		dup := false
		for _, o := range order {
			dup = dup || o == m
		}
		if !dup {
			order = append(order, m)
		}
	}
	gemMu.Unlock()
	if len(order) == 0 {
		return vcResult{}, errors.New("la IA ha arribat al límit gratuït d'avui")
	}
	var last error
	for _, model := range order {
		aiUsedToday(1)
		t0 := time.Now()
		req, _ := http.NewRequest("POST", gemBase()+"/v1beta/models/"+model+":generateContent", bytes.NewReader(body))
		req.Header.Set("x-goog-api-key", c.AIKey)
		req.Header.Set("content-type", "application/json")
		r, err := vcHTTP.Do(req)
		if err != nil {
			last = err
			continue
		}
		raw, _ := io.ReadAll(io.LimitReader(r.Body, 2<<20))
		r.Body.Close()
		if r.StatusCode == 429 {
			if strings.Contains(string(raw), "PerDay") {
				gemMu.Lock()
				la := pacificLoc()
				n := time.Now().In(la)
				gemBlocked[model] = time.Date(n.Year(), n.Month(), n.Day()+1, 0, 1, 0, 0, la)
				gemMu.Unlock()
			}
			last = errors.New("límit gratuït de la IA")
			continue
		}
		if r.StatusCode != 200 {
			last = fmt.Errorf("IA HTTP %d: %s", r.StatusCode, strings.TrimSpace(string(raw[:min(len(raw), 200)])))
			continue
		}
		var out struct {
			Candidates []struct {
				Content struct {
					Parts []struct {
						Text    string `json:"text"`
						Thought bool   `json:"thought"`
					} `json:"parts"`
				} `json:"content"`
			} `json:"candidates"`
			Usage struct {
				In int `json:"promptTokenCount"`
			} `json:"usageMetadata"`
		}
		json.Unmarshal(raw, &out)
		txt := ""
		if len(out.Candidates) > 0 {
			for _, p := range out.Candidates[0].Content.Parts {
				if !p.Thought {
					txt += p.Text
				}
			}
		}
		txt = strings.TrimSpace(txt)
		if m := regexp.MustCompile(`(?s)\{.*\}`).FindString(txt); m != "" {
			txt = m
		}
		var ans struct {
			Deaths []struct {
				Clip  int    `json:"clip"`
				Error string `json:"error"`
				Fix   string `json:"fix"`
			} `json:"deaths"`
			Summary string `json:"summary"`
		}
		if err := json.Unmarshal([]byte(txt), &ans); err != nil || ans.Summary == "" {
			last = fmt.Errorf("la IA no ha respost en el format esperat")
			logf("Coach de vídeo: resposta no vàlida de %s: %.300s", model, txt)
			continue
		}
		logf("Coach de vídeo: %s ha mirat %d clips (%d tokens, %.0f s)", model, len(clips), out.Usage.In, time.Since(t0).Seconds())
		res := vcResult{At: time.Now().Unix(), Model: model, Lang: lang, Summary: ans.Summary, Deaths: deaths}
		for _, a := range ans.Deaths {
			if a.Clip >= 1 && a.Clip <= len(res.Deaths) {
				res.Deaths[a.Clip-1].Error, res.Deaths[a.Clip-1].Fix = a.Error, a.Fix
			}
		}
		return res, nil
	}
	return vcResult{}, last
}

func vcSaveResult(date string, res vcResult) {
	mapMu.Lock()
	defer mapMu.Unlock()
	g, _, err := readGame(date)
	if err != nil {
		return
	}
	g.Report["video"] = res
	b, _ := json.Marshal(g)
	os.WriteFile(gameFile(date), b, 0o644)
}

// vcPrune keeps the clips of the last 5 analysed games
func vcPrune() {
	ents, err := os.ReadDir(clipsRoot())
	if err != nil {
		return
	}
	names := []string{}
	for _, e := range ents {
		if e.IsDir() {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	for len(names) > 5 {
		os.RemoveAll(filepath.Join(clipsRoot(), names[0]))
		names = names[1:]
	}
}

var clipName = regexp.MustCompile(`^[0-9_-]+$`)

func videoCoachHandler(send func(http.ResponseWriter, int, string, []byte)) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		q := req.URL.Query()
		if req.Method == http.MethodPost {
			vcMu.Lock()
			if l := q.Get("lang"); l == "es" || l == "ca" || l == "en" {
				vcLang = l
			}
			if m := q.Get("mode"); m == "auto" || m == "manual" {
				vcMode = m
			}
			if q.Get("stop") != "" {
				vcStop = true
				vcQueue = nil
			}
			vcMu.Unlock()
			if q.Get("stop") != "" && gameflowPhase() == "WatchInProgress" {
				killReplay()
			}
			if d := q.Get("date"); d != "" {
				vcEnqueue(d, true)
			}
		}
		vcMu.Lock()
		out := map[string]any{"mode": vcMode, "lang": vcLang, "queue": len(vcQueue)}
		for k, v := range vcState {
			out[k] = v
		}
		b, _ := json.Marshal(out)
		vcMu.Unlock()
		send(w, 200, "application/json", b)
	}
}

// clipHandler serves a recorded clip: /api/clip?g=2026-11-09_1305&f=mort_412.webm
func clipHandler(w http.ResponseWriter, req *http.Request) {
	gd, f := req.URL.Query().Get("g"), req.URL.Query().Get("f")
	if !clipName.MatchString(gd) || !regexp.MustCompile(`^mort_\d+\.webm$`).MatchString(f) {
		http.NotFound(w, req)
		return
	}
	w.Header().Set("Content-Type", "video/webm")
	http.ServeFile(w, req, filepath.Join(clipsRoot(), gd, f))
}

// curGameID: the id of the game being played, read from the client's gameflow session (the replay's id)
var curGameID atomic.Int64

func captureGameID() {
	for i := 0; i < 20; i++ {
		var sess struct {
			GameData struct {
				GameID int64 `json:"gameId"`
			} `json:"gameData"`
		}
		if code, err := lcuGet("/lol-gameflow/v1/session", &sess); err == nil && code == 200 && sess.GameData.GameID > 0 {
			curGameID.Store(sess.GameData.GameID)
			logf("Partida %d (per a la repetició)", sess.GameData.GameID)
			return
		}
		time.Sleep(15 * time.Second)
	}
}
