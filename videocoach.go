package main

import (
	"bytes"
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
	Improve string    `json:"improve,omitempty"` // (a game without deaths) what's still to improve
	Clean   bool      `json:"clean,omitempty"`   // no deaths
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
		logf("Coach de morts: %s", text)
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
			logf("Coach de morts: error %v", e)
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

func vcStopped() bool {
	vcMu.Lock()
	defer vcMu.Unlock()
	return vcStop
}

// vcAnalyse: the AI explains each death from Riot's timeline (where, who, with what damage, who was near,
// where the enemy jungler was, your wards). Nothing opens on screen.
func vcAnalyse(date string) error {
	g, rec, err := readGame(date)
	if err != nil {
		return err
	}
	if v, ok := g.Report["video"].(map[string]any); ok && v["summary"] != nil && v["summary"] != "" {
		return nil // already done
	}
	mm, _ := g.Report["map"].(map[string]any)
	if mm == nil || mm["death_info"] == nil { // an older game: read Riot's data again (now with the deaths' detail)
		vcSet(true, date, "llegint la partida de Riot…")
		if _, err := addMap(date); err != nil {
			return errors.New("encara no tinc les dades de Riot d'aquesta partida")
		}
		if g, rec, err = readGame(date); err != nil {
			return err
		}
		mm, _ = g.Report["map"].(map[string]any)
		if mm == nil || mm["death_info"] == nil {
			return errors.New("Riot no té el detall d'aquesta partida")
		}
	}
	var info []DeathInfo
	if raw, err := json.Marshal(mm["death_info"]); err == nil {
		json.Unmarshal(raw, &info)
	}
	if len(info) == 0 { // no deaths: what was good and what's still to improve, from the kills and the numbers
		var kills []KillInfo
		if raw, err := json.Marshal(mm["kill_info"]); err == nil {
			json.Unmarshal(raw, &kills)
		}
		var st MatchStats
		if raw, err := json.Marshal(mm["stats"]); err == nil {
			json.Unmarshal(raw, &st)
		}
		vcSet(true, date, "la IA està mirant la partida (no has mort cap vegada)…")
		res, err := vcAsk(rec, g.Report, nil, nil, cleanGameText(rec, g.Report, kills, st))
		if err != nil {
			return err
		}
		res.Clean = true
		vcSaveResult(date, res)
		vcSet(false, date, "fet")
		return nil
	}
	if len(info) > 8 {
		info = info[:8]
	}
	// the app's own label for each death (gank, 1v1, teamfight...)
	var mine []vcDeath
	if raw, err := json.Marshal(g.Report["deaths"]); err == nil {
		json.Unmarshal(raw, &mine)
	}
	deaths := make([]vcDeath, len(info))
	for i, d := range info {
		deaths[i] = vcDeath{T: d.T, By: append([]string{d.Killer}, d.Helpers...)}
		for _, m := range mine {
			if m.T > d.T-15 && m.T < d.T+15 {
				deaths[i].Kind = m.Kind
			}
		}
	}
	vcSet(true, date, fmt.Sprintf("la IA està mirant les teves %d morts…", len(deaths)))
	res, err := vcAsk(rec, g.Report, deaths, info, "")
	if err != nil {
		return err
	}
	vcSaveResult(date, res)
	vcSet(false, date, "fet")
	return nil
}

var vcHTTP = &http.Client{Timeout: 90 * time.Second}

const vcSystem = `Ets un coach de League of Legends de nivell Challenger. Analitzes les morts d'un jugador de Platí a partir de les dades de la cronologia de Riot de la partida i li dius, amb franquesa i en concret, per què ha mort i què havia de fer.
Per a cada mort tens: el minut, el lloc, qui l'ha matat i qui ha ajudat, el dany que ha rebut (de qui i amb quines habilitats), el seu nivell i el del que l'ha matat, la diferència d'or, quants aliats i rivals hi havia a prop el minut d'abans, on era el jungla rival el minut d'abans i quants wards havia posat ell els 3 minuts anteriors.
Fes-ne una lectura de coach: per exemple, si ha mort a la jungla rival amb 0 wards i 3 rivals a prop, o si el jungla rival era al seu costat del mapa el minut d'abans, o si ha lluitat amb 2 nivells menys, o si gairebé tot el dany ve d'una habilitat que podia esquivar o esperar. Les posicions són d'un minut abans: no t'hi jugues més del que diuen.
No t'inventis el que les dades no diuen. Res de consells genèrics. Si dues morts tenen la mateixa causa, digues-ho al resum.
Respon NOMÉS amb JSON vàlid, sense res més, amb aquesta forma:
{"deaths":[{"n":1,"error":"què ha fet malament (1 o 2 frases)","fix":"què havia de fer (1 frase)"}],"summary":"el patró principal de totes les morts i el consell més important per a la propera partida (1 o 2 frases)"}`

// cleanGameText: a game without deaths, for the AI: your kills (where, solo or with whom) and your numbers
func cleanGameText(rec *Record, report map[string]any, kills []KillInfo, st MatchStats) string {
	var sb strings.Builder
	sb.WriteString("No ha mort cap vegada en tota la partida. ")
	m := st.Me
	fmt.Fprintf(&sb, "KDA %d/%d/%d, farm %d, %d de dany a campions (%.0f%% del dany de l'equip), participació en kills %.0f%%, visió %d (wards posats %d, destruïts %d, de control %d), %d solo kills, %d plaques.",
		m.K, m.D, m.A, m.CS, m.Damage, m.DamageShare*100, m.KP*100, m.Vision, m.Wards, m.WardsKilled, m.Control, m.SoloKills, m.Plates)
	if mn, ok := report["duration"].(float64); ok && mn > 0 {
		fmt.Fprintf(&sb, " Durada %.0f minuts (%.1f de farm per minut).", mn/60, float64(m.CS)/(mn/60))
	}
	if st.Opp != nil {
		o := st.Opp
		fmt.Fprintf(&sb, " El seu rival de línia (%s): KDA %d/%d/%d, farm %d, visió %d.", displayName(o.Champ), o.K, o.D, o.A, o.CS, o.Vision)
	}
	for _, d := range st.Diffs {
		fmt.Fprintf(&sb, " Al minut %d: %+d d'or, %+d de farm respecte al rival.", d.Min, d.Gold, d.CS)
	}
	if len(kills) > 0 {
		sb.WriteString("\nKills i assistències:")
		for _, k := range kills {
			fmt.Fprintf(&sb, "\n- minut %d:%02d, %s a %s", int(k.T)/60, int(k.T)%60, map[bool]string{true: "kill seva", false: "assistència"}[k.Mine], k.Victim)
			if len(k.Helpers) == 0 {
				sb.WriteString(" (sol)")
			} else {
				sb.WriteString(" amb " + strings.Join(k.Helpers, ", "))
			}
			sb.WriteString(", a " + k.Zone)
		}
	}
	return sb.String()
}

const vcCleanSystem = `Ets un coach de League of Legends de nivell Challenger. El jugador, de Platí, ha fet una partida sense morir. Amb les dades de Riot que tens, digues-li en concret què ha fet bé (per què ha funcionat: on i com ha matat, si ha jugat amb el jungla, si ha anat sol...) i què encara pot millorar, que sempre hi ha alguna cosa (farm, visió, convertir l'avantatge en objectius...), comparant amb el que faria un jugador Challenger.
No t'inventis el que les dades no diuen. Res de consells genèrics.
Respon NOMÉS amb JSON vàlid, sense res més, amb aquesta forma:
{"deaths":[],"summary":"què ha fet bé (1 o 2 frases)","improve":"què ha de millorar i com (1 o 2 frases)"}`

func vcAsk(rec *Record, report map[string]any, deaths []vcDeath, info []DeathInfo, clean string) (vcResult, error) {
	c := loadConfig()
	vcMu.Lock()
	lang := vcLang
	vcMu.Unlock()
	langName := map[string]string{"es": "castellà (d'Espanya)", "en": "anglès", "ca": "català"}[lang]
	if langName == "" {
		langName, lang = "castellà (d'Espanya)", "es"
	}
	opp, _ := report["opponent"].(string)
	var sb strings.Builder
	fmt.Fprintf(&sb, "El jugador porta %s (posició %s)%s. Escriu els textos del JSON en %s, parlant-li de tu.\n", rec.Me.ChampionName, rec.Me.Position,
		map[bool]string{true: ", contra " + opp, false: ""}[opp != ""], langName)
	sb.WriteString(clean)
	for i, d := range info {
		fmt.Fprintf(&sb, "\nMort %d, minut %d:%02d, a %s. ", i+1, int(d.T)/60, int(d.T)%60, d.Zone)
		if deaths[i].Kind != "" {
			fmt.Fprintf(&sb, "L'app ho ha classificat com a \"%s\". ", deaths[i].Kind)
		}
		fmt.Fprintf(&sb, "L'ha matat %s", d.Killer)
		if len(d.Helpers) > 0 {
			fmt.Fprintf(&sb, " amb l'ajuda de %s", strings.Join(d.Helpers, ", "))
		}
		sb.WriteString(". ")
		if len(d.Dmg) > 0 {
			parts := []string{}
			for _, x := range d.Dmg {
				parts = append(parts, fmt.Sprintf("%s %d (%s)", x.Champ, x.Dmg, x.What))
			}
			sb.WriteString("Dany rebut: " + strings.Join(parts, "; ") + ". ")
		}
		if d.Lvl[0] > 0 && d.Lvl[1] > 0 {
			fmt.Fprintf(&sb, "Nivell seu %d, del que l'ha matat %d; diferència d'or %+d. ", d.Lvl[0], d.Lvl[1], d.Gold)
		}
		fmt.Fprintf(&sb, "El minut d'abans hi havia %d aliats i %d rivals a menys de 2000 unitats d'aquell lloc. ", d.Allies, d.Enemies)
		if d.JgWas != "" {
			fmt.Fprintf(&sb, "El jungla rival: %s. ", d.JgWas)
		}
		fmt.Fprintf(&sb, "Wards posats els 3 minuts anteriors: %d.", d.Wards)
	}
	body, _ := json.Marshal(map[string]any{
		"systemInstruction": map[string]any{"parts": []map[string]string{{"text": map[bool]string{true: vcCleanSystem, false: vcSystem}[clean != ""]}}},
		"contents":          []map[string]any{{"role": "user", "parts": []map[string]string{{"text": sb.String()}}}},
		"generationConfig":  map[string]any{"maxOutputTokens": 2000, "temperature": 0.4, "responseMimeType": "application/json"},
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
		aiUsedToday(1)
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
				N     int    `json:"n"`
				Error string `json:"error"`
				Fix   string `json:"fix"`
			} `json:"deaths"`
			Summary string `json:"summary"`
			Improve string `json:"improve"`
		}
		if err := json.Unmarshal([]byte(txt), &ans); err != nil || ans.Summary == "" {
			last = fmt.Errorf("la IA no ha respost en el format esperat")
			logf("Coach de morts: resposta no vàlida de %s: %.300s", model, txt)
			continue
		}
		logf("Coach de morts: %s ha explicat %d morts (%d tokens, %.0f s)", model, len(deaths), out.Usage.In, time.Since(t0).Seconds())
		res := vcResult{At: time.Now().Unix(), Model: model, Lang: lang, Summary: ans.Summary, Improve: ans.Improve, Deaths: deaths}
		if res.Deaths == nil {
			res.Deaths = []vcDeath{}
		}
		for _, a := range ans.Deaths {
			if a.N >= 1 && a.N <= len(res.Deaths) {
				res.Deaths[a.N-1].Error, res.Deaths[a.N-1].Fix = a.Error, a.Fix
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
			duoGameStart(sess.GameData.GameID)
			return
		}
		time.Sleep(15 * time.Second)
	}
}
