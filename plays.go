package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

// Plays: short videos of your best moments, made while you play. With it on, the graphics card keeps the last
// minute of the screen (ffmpeg, the card's own video encoder); when you kill someone, get a multikill or steal
// an objective, the seconds of that play are kept as a clip. After the game the AI scores them (0-100) and the
// best 3 go to the report; the best of all your games make a ranking.

type playCfg struct {
	On      bool   `json:"on"`
	Encoder string `json:"encoder,omitempty"` // the one that worked on this PC
}

type Play struct {
	Stamp  string    `json:"stamp"` // the game's report name
	Date   string    `json:"date"`
	N      int       `json:"n"`
	File   string    `json:"file"`
	T      float64   `json:"t"` // game time of the play
	Kills  int       `json:"kills"`
	Victim []string  `json:"victims"`
	Help   int       `json:"helpers"`
	HP     float64   `json:"hp"` // your health when it ended (0-1, -1 unknown)
	Steal  string    `json:"steal,omitempty"`
	Ace    bool      `json:"ace,omitempty"`
	Champ  string    `json:"champ"`
	Score  int       `json:"score"`
	Title  string    `json:"title"`
	Why    string    `json:"why"`
	AI     bool      `json:"ai,omitempty"`
	Start  time.Time `json:"-"`
	End    time.Time `json:"-"`
	gStart float64
	gEnd   float64
}

var (
	plMu      sync.Mutex
	plCfgRead bool
	plCfg     playCfg
	plSeen    = map[int]bool{}
	plCur     []*Play // plays of the game being played (not saved yet, or saved)
	plStamp   string
	plMe      Player
	plNames   map[string]Player
	plState   = "off"
	plErr     string
)

func playsRoot() string    { return filepath.Join(filepath.Dir(dataDir()), "jugades") }
func playsIndex() string   { return filepath.Join(playsRoot(), "jugades.json") }
func playsCfgFile() string { return profileFile("jugades.json") }

func playsLoadCfg() playCfg {
	plMu.Lock()
	defer plMu.Unlock()
	if !plCfgRead {
		plCfgRead = true
		if b, err := os.ReadFile(playsCfgFile()); err == nil {
			json.Unmarshal(b, &plCfg)
		}
	}
	return plCfg
}

func playsSaveCfg(c playCfg) {
	plMu.Lock()
	plCfg = c
	plMu.Unlock()
	if b, err := json.Marshal(c); err == nil {
		os.WriteFile(playsCfgFile(), b, 0o644)
	}
}

// playsGameStart: a game began (stamp: its report's name)
func playsGameStart(stamp string, me Player, all []Player) {
	if !playsLoadCfg().On || !playsSupported {
		return
	}
	names := map[string]Player{}
	for _, p := range all {
		for _, n := range []string{p.RiotIDGameName, p.SummonerName, p.RiotID, strings.SplitN(p.RiotID, "#", 2)[0]} {
			if n != "" {
				names[n] = p
			}
		}
	}
	plMu.Lock()
	plStamp, plMe, plNames, plCur, plSeen = stamp, me, names, nil, map[int]bool{}
	plMu.Unlock()
	go captureStart()
}

// playsSee: the game's events so far (every few seconds while playing)
func playsSee(events []Event, gameTime float64, hp float64) {
	plMu.Lock()
	defer plMu.Unlock()
	if plStamp == "" {
		return
	}
	me := pidOf(&plMe)
	isMe := func(n string) bool { p, ok := plNames[n]; return ok && pidOf(&p) == me }
	now := time.Now()
	wall := func(t float64) time.Time { return now.Add(-time.Duration((gameTime - t) * float64(time.Second))) }
	for _, ev := range events {
		if plSeen[ev.EventID] || gameTime-ev.EventTime > 40 {
			plSeen[ev.EventID] = true
			continue
		}
		plSeen[ev.EventID] = true
		var p *Play
		switch ev.EventName {
		case "ChampionKill":
			if !isMe(ev.KillerName) {
				continue
			}
			p = &Play{Kills: 1, Victim: []string{plNames[ev.VictimName].ChampionName}, Help: len(ev.Assisters)}
		case "DragonKill", "BaronKill", "HeraldKill", "AtakhanKill":
			if ev.Stolen != "True" || !isMe(ev.KillerName) {
				continue
			}
			p = &Play{Steal: map[string]string{"DragonKill": "drac", "BaronKill": "baró", "HeraldKill": "herald", "AtakhanKill": "Atakhan"}[ev.EventName]}
		case "Ace":
			if last := plLast(); last != nil && ev.EventTime-last.gEnd < 10 && plNames[ev.KillerName].Team == plMe.Team {
				last.Ace = true
			}
			continue
		default:
			continue
		}
		p.T, p.gStart, p.gEnd = ev.EventTime, ev.EventTime-11, ev.EventTime+4
		p.HP, p.Champ = hp, plMe.ChampionName
		// a kill a few seconds after the last play: the same play (a double, a triple...)
		if last := plLast(); last != nil && p.gStart < last.gEnd+3 && last.File == "" {
			last.gEnd = p.gEnd
			last.Kills += p.Kills
			last.Victim = append(last.Victim, p.Victim...)
			if p.Help < last.Help {
				last.Help = p.Help
			}
			if p.Steal != "" {
				last.Steal = p.Steal
			}
			last.HP = hp
			last.End = wall(last.gEnd)
			continue
		}
		p.Start, p.End = wall(p.gStart), wall(p.gEnd)
		p.N, p.Stamp = len(plCur)+1, plStamp
		plCur = append(plCur, p)
		logf("Jugades: %s al minut %d:%02d", playDesc(p), int(p.T)/60, int(p.T)%60)
	}
	// plays whose end has passed: cut their clip
	for _, p := range plCur {
		if p.File == "" && !p.End.IsZero() && now.After(p.End.Add(2*time.Second)) {
			p.File = "pending"
			go playCut(p)
		}
	}
}

func plLast() *Play {
	if len(plCur) == 0 {
		return nil
	}
	return plCur[len(plCur)-1]
}

func playDesc(p *Play) string {
	switch {
	case p.Steal != "":
		return "robatori de " + p.Steal
	case p.Kills >= 5:
		return "pentakill"
	case p.Kills == 4:
		return "quadrakill"
	case p.Kills == 3:
		return "triple kill"
	case p.Kills == 2:
		return "doble kill"
	case p.Help == 0:
		return "kill en solitari a " + strings.Join(p.Victim, ", ")
	}
	return "kill a " + strings.Join(p.Victim, ", ")
}

func playCut(p *Play) {
	dir := filepath.Join(playsRoot(), p.Stamp0())
	os.MkdirAll(dir, 0o755)
	name := fmt.Sprintf("jugada_%d.mp4", p.N)
	err := captureCut(p.Start, p.End, filepath.Join(dir, name))
	plMu.Lock()
	if err != nil {
		p.File = "error"
		logf("Jugades: no s'ha pogut tallar el clip %d: %v", p.N, err)
	} else {
		p.File = name
		logf("Jugades: clip %d guardat (%s)", p.N, playDesc(p))
	}
	plMu.Unlock()
}

func (p *Play) Stamp0() string {
	if p.Stamp != "" {
		return p.Stamp
	}
	plMu.Lock()
	defer plMu.Unlock()
	p.Stamp = plStamp
	return p.Stamp
}

// playsGameEnd: the game is over: stop recording, score the clips and keep the best
func playsGameEnd(date string, keep bool) {
	plMu.Lock()
	stamp, plays := plStamp, plCur
	plStamp, plCur = "", nil
	plMu.Unlock()
	captureStop()
	if stamp == "" || len(plays) == 0 {
		return
	}
	if !keep { // a remake: nothing worth keeping
		os.RemoveAll(filepath.Join(playsRoot(), stamp))
		return
	}
	go func() {
		time.Sleep(6 * time.Second) // the last clip may still be being cut
		var done []*Play
		plMu.Lock()
		for _, p := range plays {
			if strings.HasSuffix(p.File, ".mp4") {
				p.Stamp, p.Date = stamp, date
				done = append(done, p)
			}
		}
		plMu.Unlock()
		if len(done) == 0 {
			return
		}
		playsScore(done)
		playsAdd(done)
	}()
}

// playsScore: the AI scores each play (one request per game); without it, a simple rule
func playsScore(ps []*Play) {
	for _, p := range ps {
		s := 35 + 12*(p.Kills-1)
		if p.Help == 0 && p.Kills > 0 {
			s += 15
		}
		if p.HP >= 0 && p.HP < 0.2 {
			s += 12
		}
		if p.Steal != "" {
			s += 30
		}
		if p.Ace {
			s += 8
		}
		p.Score, p.Title = min(s, 95), strings.ToUpper(playDesc(p)[:1])+playDesc(p)[1:]
	}
	c := loadConfig()
	if c.AIKey == "" || !aiBudgetLeft() {
		return
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "Jugo %s. Aquestes són jugades meves d'una partida de League of Legends:\n", ps[0].Champ)
	for _, p := range ps {
		hp := "desconeguda"
		if p.HP >= 0 {
			hp = fmt.Sprintf("%.0f%%", p.HP*100)
		}
		fmt.Fprintf(&sb, "- id %d, minut %d:%02d: %d kills (%s), %d companys ajudant, la meva vida al final %s", p.N, int(p.T)/60, int(p.T)%60, p.Kills, strings.Join(p.Victim, ", "), p.Help, hp)
		if p.Steal != "" {
			sb.WriteString(", he robat el " + p.Steal)
		}
		if p.Ace {
			sb.WriteString(", acaba amb un ace")
		}
		sb.WriteString(".\n")
	}
	sys := `Puntues jugades de League of Legends de 0 a 100 segons com d'impressionants són per veure-les en vídeo: multikills, kills en solitari, sortir viu amb poca vida, robar objectius i decidir una baralla puntuen alt; una kill fàcil amb molta ajuda, baix. Respon només JSON: {"plays":[{"id":1,"score":72,"title":"títol curt i emocionant (màx 6 paraules)","why":"una frase de per què és bona"}]}. En català.`
	txt, model, err := geminiJSON(c.AIKey, sys, sb.String(), 1200)
	if err != nil {
		logf("Jugades: la IA no les ha pogut puntuar (%v); faig servir la puntuació simple", err)
		return
	}
	var ans struct {
		Plays []struct {
			ID    int    `json:"id"`
			Score int    `json:"score"`
			Title string `json:"title"`
			Why   string `json:"why"`
		} `json:"plays"`
	}
	if json.Unmarshal([]byte(txt), &ans) != nil {
		return
	}
	for _, a := range ans.Plays {
		for _, p := range ps {
			if p.N == a.ID && a.Score >= 0 && a.Score <= 100 {
				p.Score, p.AI = a.Score, true
				if a.Title != "" {
					p.Title = a.Title
				}
				p.Why = a.Why
			}
		}
	}
	logf("Jugades: %s ha puntuat %d jugades", model, len(ans.Plays))
}

// playsAdd: into the index; only the clips worth keeping stay on disk: those scored 80 or more, for good, and the 3
// best of each of the last 10 games (so the report shows them); the rest are deleted
func playsAdd(ps []*Play) {
	plMu.Lock()
	defer plMu.Unlock()
	all := playsReadLocked()
	for _, p := range ps {
		all = append(all, *p)
	}
	byGame := map[string][]int{}
	for i, p := range all {
		byGame[p.Stamp] = append(byGame[p.Stamp], i)
	}
	games := []string{}
	for g := range byGame {
		games = append(games, g)
	}
	sort.Sort(sort.Reverse(sort.StringSlice(games)))
	keep := map[int]bool{}
	for gi, g := range games {
		idx := byGame[g]
		sort.Slice(idx, func(a, b int) bool { return all[idx[a]].Score > all[idx[b]].Score })
		for k, i := range idx {
			if gi < 10 && k < 3 {
				keep[i] = true
			}
		}
	}
	for i, p := range all { // 80 or more: kept for good
		if p.Score >= 80 {
			keep[i] = true
		}
	}
	out := []Play{}
	for i, p := range all {
		if keep[i] {
			out = append(out, p)
		} else {
			os.Remove(filepath.Join(playsRoot(), p.Stamp, p.File))
		}
	}
	if b, err := json.MarshalIndent(out, "", " "); err == nil {
		os.MkdirAll(playsRoot(), 0o755)
		os.WriteFile(playsIndex(), b, 0o644)
	}
}

func playsReadLocked() []Play {
	var all []Play
	if b, err := os.ReadFile(playsIndex()); err == nil {
		json.Unmarshal(b, &all)
	}
	return all
}

var playFileRe = regexp.MustCompile(`^jugada_\d+\.mp4$`)
var playStampRe = regexp.MustCompile(`^[0-9]{4}-[0-9]{2}-[0-9]{2}_[0-9]{4}$`)

// /api/plays: GET ?stamp= (that game's) or all, best first; POST ?on=0/1. /api/playfile?g=&f= the video.
func playsHandler(send func(http.ResponseWriter, int, string, []byte)) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		q := req.URL.Query()
		if strings.HasSuffix(req.URL.Path, "/playfile") {
			g, f := q.Get("g"), q.Get("f")
			if !playStampRe.MatchString(g) || !playFileRe.MatchString(f) {
				http.NotFound(w, req)
				return
			}
			w.Header().Set("Content-Type", "video/mp4")
			http.ServeFile(w, req, filepath.Join(playsRoot(), g, f))
			return
		}
		if req.Method == http.MethodPost {
			c := playsLoadCfg()
			if v := q.Get("on"); v != "" {
				c.On = v == "1"
			}
			playsSaveCfg(c)
			logf("Jugades: %s", map[bool]string{true: "activat", false: "desactivat"}[c.On])
			if c.On && c.Encoder == "" {
				go captureProbe()
			}
			if !c.On {
				captureStop()
			}
		}
		plMu.Lock()
		all := playsReadLocked()
		st, e := plState, plErr
		plMu.Unlock()
		if g := q.Get("stamp"); g != "" {
			out := []Play{}
			for _, p := range all {
				if p.Stamp == g {
					out = append(out, p)
				}
			}
			all = out
		}
		sort.SliceStable(all, func(a, b int) bool { return all[a].Score > all[b].Score })
		var size int64
		filepath.Walk(playsRoot(), func(_ string, fi os.FileInfo, err error) error {
			if err == nil && !fi.IsDir() {
				size += fi.Size()
			}
			return nil
		})
		c := playsLoadCfg()
		b, _ := json.Marshal(map[string]any{"on": c.On, "supported": playsSupported, "encoder": c.Encoder, "state": st, "error": e, "plays": all, "mb": size >> 20})
		send(w, 200, "application/json", b)
	}
}

func playsSet(st, e string) {
	plMu.Lock()
	plState, plErr = st, e
	plMu.Unlock()
}

// geminiJSON: one request to Gemini that answers JSON (the model the app is using, then the others)
func geminiJSON(key, system, user string, maxTok int) (string, string, error) {
	body, _ := json.Marshal(map[string]any{
		"systemInstruction": map[string]any{"parts": []map[string]string{{"text": system}}},
		"contents":          []map[string]any{{"role": "user", "parts": []map[string]string{{"text": user}}}},
		"generationConfig":  map[string]any{"maxOutputTokens": maxTok, "temperature": 0.4, "responseMimeType": "application/json"},
	})
	if _, err := pickGeminiModel(key); err != nil {
		return "", "", err
	}
	gemMu.Lock()
	order := []string{}
	for _, m := range append([]string{gemModel}, gemCands...) {
		if m != "" && !time.Now().Before(gemBlocked[m]) && !strings.Contains(strings.Join(order, ","), m) {
			order = append(order, m)
		}
	}
	gemMu.Unlock()
	last := errors.New("la IA ha arribat al límit gratuït d'avui")
	for _, model := range order {
		aiUsedToday(1)
		req, _ := http.NewRequest("POST", gemBase()+"/v1beta/models/"+model+":generateContent", bytes.NewReader(body))
		req.Header.Set("x-goog-api-key", key)
		req.Header.Set("content-type", "application/json")
		r, err := vcHTTP.Do(req)
		if err != nil {
			last = err
			continue
		}
		raw, _ := io.ReadAll(io.LimitReader(r.Body, 2<<20))
		r.Body.Close()
		if r.StatusCode != 200 {
			last = fmt.Errorf("IA HTTP %d", r.StatusCode)
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
		if m := regexp.MustCompile(`(?s)\{.*\}`).FindString(txt); m != "" {
			return m, model, nil
		}
		last = errors.New("resposta sense JSON")
	}
	return "", "", last
}

var _ = math.Abs

func aiBudgetLeft() bool { return aiUsedToday(0) < 55 } // the page shares out 60 a day
