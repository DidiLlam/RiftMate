package main

// Study on the phone: the app reads your last games, finds the macro mistakes that repeat (the same ones the reports
// show), and sends a small summary to your phone's study page (the "Macro" course), so its questions start from
// your own mistakes. Each player has his own link code: your brother sees his, you see yours.
// The summary goes through ntfy.sh (the relay the duo timers already use) under a topic made from that code; the
// phone keeps the last one it got. No names or Riot ids go in it: champions, minutes, places and counts.

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

const studyPage = "https://didillam.github.io/RiftMate/estudi/"

type studyCfg struct {
	Code   string `json:"code"`              // XXXXXXXX (shown XXXX-XXXX)
	SentAt int64  `json:"sent_at,omitempty"` // last time the summary was sent
	Hash   string `json:"hash,omitempty"`    // of the last summary sent (resend only when it changes, or every 6 h)
	Err    string `json:"err,omitempty"`
}

var studyMu sync.Mutex

func studyFile() string { return profileFile("estudi.json") }

func studyLoad() studyCfg {
	var c studyCfg
	if b, err := os.ReadFile(studyFile()); err == nil {
		json.Unmarshal(b, &c)
	}
	if len(c.Code) != 8 {
		const abc = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789" // no 0/O, 1/I: easy to type on a phone
		b := make([]byte, 8)
		rand.Read(b)
		for i := range b {
			b[i] = abc[int(b[i])%len(abc)]
		}
		c.Code = string(b)
		studySave(c)
	}
	return c
}

func studySave(c studyCfg) {
	b, _ := json.Marshal(c)
	os.WriteFile(studyFile(), b, 0o644)
}

func studyTopic(code string) string { return "riftmate-estudi-" + strings.ToLower(code) }

// ---- the summary ----

type studyEx struct {
	T  string `json:"t"`            // the mistake's kind (see studyKinds)
	D  string `json:"d"`            // date, dd/mm
	C  string `json:"c"`            // your champion
	M  string `json:"m"`            // minute, m:ss
	Z  string `json:"z,omitempty"`  // where
	E  int    `json:"e,omitempty"`  // enemies near
	A  int    `json:"a,omitempty"`  // allies near
	X  string `json:"x,omitempty"`  // an extra (the objective lost, the lead thrown...)
	Ok bool   `json:"ok,omitempty"` // the game was won
}

type studyProfile struct {
	V     int                `json:"v"`
	At    int64              `json:"at"`
	Games int                `json:"games"`
	Role  string             `json:"role"`
	Champ string             `json:"champ"`
	Rate  map[string]float64 `json:"rate"`  // each kind: how many per game
	Score map[string]float64 `json:"score"` // each lesson: how much it shows in your games (0-100)
	Ex    []studyEx          `json:"ex"`
}

// the kinds of mistake and the lessons of the course they belong to
var studyKinds = map[string][]string{
	"gank":    {"visio"},            // killed by the jungler's gank before minute 14
	"endins":  {"visio", "onades"},  // died in the river or the enemy jungle with no ward placed in the 3 minutes before
	"sol":     {"migjoc"},           // caught with 2 or more enemies than allies near, after minute 14
	"preobj":  {"objectius"},        // died less than 90 s before the enemy took an epic monster
	"objs":    {"objectius"},        // the enemy took 3 or more epic monsters than your team
	"farm":    {"onades", "tornar"}, // under 7 CS a minute (not jungle or support)
	"kp":      {"roam"},             // under 45% kill participation
	"llencat": {"avantatge"},        // lost a game you led by 2,000 gold or more at minute 15
	"torre":   {"onades", "tornar"}, // died to a tower or minions
}

func studyBuild() studyProfile {
	p := studyProfile{V: 1, At: time.Now().Unix(), Rate: map[string]float64{}, Score: map[string]float64{}}
	type rep struct {
		Champion string   `json:"champion"`
		Role     string   `json:"role"`
		Result   string   `json:"result"`
		Date     string   `json:"date"`
		Duration float64  `json:"duration"`
		KP       *float64 `json:"kp"`
		CSRate   *struct {
			Total *float64 `json:"total"`
		} `json:"cs_rate"`
		Deaths     []Death     `json:"deaths"`
		Objectives []Objective `json:"objectives"`
		Map        *MapData    `json:"map"`
	}
	count := map[string]int{}
	roles, champs := map[string]int{}, map[string]int{}
	exs := []studyEx{}
	for _, raw := range history() {
		if p.Games >= 25 {
			break
		}
		var r rep
		if json.Unmarshal(raw, &r) != nil || r.Duration < 15*60 || r.Champion == "" {
			continue // remakes and very short games say nothing about macro
		}
		if r.Map != nil && r.Map.Queue != 0 && r.Map.Queue != 420 && r.Map.Queue != 440 && r.Map.Queue != 400 && r.Map.Queue != 430 && r.Map.Queue != 490 {
			continue // not Summoner's Rift 5v5
		}
		p.Games++
		roles[r.Role]++
		champs[r.Champion]++
		won := strings.HasPrefix(r.Result, "Vict") || (r.Map != nil && r.Map.Win != nil && *r.Map.Win)
		d := ""
		if len(r.Date) >= 10 {
			d = r.Date[8:10] + "/" + r.Date[5:7]
		}
		mm := func(t float64) string { return fmt.Sprintf("%d:%02d", int(t)/60, int(t)%60) }
		add := func(e studyEx) {
			count[e.T]++
			e.D, e.C, e.Ok = d, displayName(r.Champion), won
			exs = append(exs, e)
		}
		for _, dt := range r.Deaths {
			if dt.T < 840 && strings.HasPrefix(dt.Kind, "gank") {
				add(studyEx{T: "gank", M: mm(dt.T)})
			}
			if dt.Kind == "torre o minions" {
				add(studyEx{T: "torre", M: mm(dt.T)})
			}
			for _, o := range r.Objectives {
				if !o.Ours && o.T-dt.T >= 0 && o.T-dt.T <= 90 {
					add(studyEx{T: "preobj", M: mm(dt.T), X: o.Obj})
					break
				}
			}
		}
		if r.Map != nil {
			for _, di := range r.Map.Deaths {
				if di.T < 1500 && di.Wards == 0 && (strings.Contains(di.Zone, "jungla rival") || strings.HasPrefix(di.Zone, "riu")) {
					add(studyEx{T: "endins", M: mm(di.T), Z: di.Zone, E: di.Enemies, A: di.Allies})
				} else if di.T >= 840 && di.Enemies >= di.Allies+2 && di.Enemies < 5 {
					add(studyEx{T: "sol", M: mm(di.T), Z: di.Zone, E: di.Enemies, A: di.Allies})
				}
			}
			for _, g := range r.Map.Gold {
				if g[0] >= 900 && g[0] < 960 && g[1]-g[2] >= 2000 && !won {
					add(studyEx{T: "llencat", M: "15:00", X: fmt.Sprintf("+%d", g[1]-g[2])})
				}
			}
		}
		ours, theirs := 0, 0
		for _, o := range r.Objectives {
			if o.Ours {
				ours++
			} else {
				theirs++
			}
		}
		if theirs-ours >= 3 {
			add(studyEx{T: "objs", M: mm(r.Duration), X: fmt.Sprintf("%d-%d", ours, theirs)})
		}
		if r.Role != "JUNGLE" && r.Role != "UTILITY" && r.CSRate != nil && r.CSRate.Total != nil && *r.CSRate.Total < 7 {
			add(studyEx{T: "farm", M: mm(r.Duration), X: fmt.Sprintf("%.1f", *r.CSRate.Total)})
		}
		if r.KP != nil && *r.KP < 45 {
			add(studyEx{T: "kp", M: mm(r.Duration), X: fmt.Sprintf("%.0f%%", *r.KP)})
		}
	}
	top := func(m map[string]int) string {
		best, n := "", 0
		for k, v := range m {
			if v > n {
				best, n = k, v
			}
		}
		return best
	}
	p.Role, p.Champ = top(roles), displayName(top(champs))
	if p.Games == 0 {
		return p
	}
	for k, n := range count {
		p.Rate[k] = math.Round(100*float64(n)/float64(p.Games)) / 100
	}
	// each lesson: the per-game rate of its mistakes, against what would be "already good" (one every 4 games)
	raw := map[string]float64{}
	for k, ls := range studyKinds {
		for _, l := range ls {
			raw[l] += p.Rate[k] / float64(len(ls))
		}
	}
	for l, v := range raw {
		p.Score[l] = math.Min(100, math.Round(100*v/(v+0.25)))
	}
	// the examples (newest first, as the history is): two of each kind first, then the rest, at most 14 (the
	// summary must stay small)
	seen, pick, rest := map[string]int{}, []studyEx{}, []studyEx{}
	for _, e := range exs {
		if seen[e.T] < 2 {
			seen[e.T]++
			pick = append(pick, e)
		} else {
			rest = append(rest, e)
		}
	}
	p.Ex = append(pick, rest...)
	if len(p.Ex) > 14 {
		p.Ex = p.Ex[:14]
	}
	return p
}

// studySend: the summary to the phone, if it changed (or every 6 hours: the relay keeps messages only 12 hours)
func studySend(force bool) error {
	studyMu.Lock()
	defer studyMu.Unlock()
	c := studyLoad()
	p := studyBuild()
	if p.Games == 0 {
		return nil
	}
	b, _ := json.Marshal(p)
	for len(b) > 3800 && len(p.Ex) > 0 { // ntfy's message limit is 4 KB
		p.Ex = p.Ex[:len(p.Ex)-1]
		b, _ = json.Marshal(p)
	}
	pc := p
	pc.At = 0
	hb, _ := json.Marshal(pc)
	sum := sha256.Sum256(hb)
	h := hex.EncodeToString(sum[:8])
	if !force && h == c.Hash && time.Since(time.Unix(c.SentAt, 0)) < 6*time.Hour {
		return nil
	}
	cl := &http.Client{Timeout: 10 * time.Second}
	r, err := cl.Post(duoBase()+"/"+studyTopic(c.Code), "text/plain", strings.NewReader(string(b)))
	if err == nil {
		io.Copy(io.Discard, r.Body)
		r.Body.Close()
		if r.StatusCode != 200 {
			err = fmt.Errorf("HTTP %d", r.StatusCode)
		}
	}
	if err != nil {
		c.Err = err.Error()
		studySave(c)
		logf("Estudi: no he pogut enviar el resum al mòbil: %v", err)
		return err
	}
	c.SentAt, c.Hash, c.Err = time.Now().Unix(), h, ""
	studySave(c)
	logf("Estudi: resum enviat al mòbil (%d partides, %d exemples, %d bytes)", p.Games, len(p.Ex), len(b))
	return nil
}

func studyLoop() {
	time.Sleep(90 * time.Second)
	for {
		if !liveNow.Load() {
			studySend(false)
		}
		time.Sleep(20 * time.Minute)
	}
}

// /api/study: GET the code, the link and the last send; POST ?send=1 sends now; POST ?new=1 makes a new code
func studyHandler(w http.ResponseWriter, req *http.Request) {
	q := req.URL.Query()
	if req.Method == http.MethodPost && q.Get("new") == "1" {
		studyMu.Lock()
		c := studyLoad()
		c.Code, c.Hash = "", ""
		studySave(c)
		studyMu.Unlock()
		studyLoad()
	}
	var sendErr string
	if req.Method == http.MethodPost {
		if err := studySend(true); err != nil {
			sendErr = err.Error()
		}
	}
	c := studyLoad()
	p := studyBuild()
	code := c.Code[:4] + "-" + c.Code[4:]
	b, _ := json.Marshal(map[string]any{"code": code, "url": studyPage + "#" + c.Code, "page": studyPage,
		"sent_at": c.SentAt, "error": firstNonEmpty(sendErr, c.Err), "games": p.Games, "score": p.Score, "rate": p.Rate})
	w.Header().Set("Content-Type", "application/json")
	w.Write(b)
}

func firstNonEmpty(a ...string) string {
	for _, s := range a {
		if s != "" {
			return s
		}
	}
	return ""
}
