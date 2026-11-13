package main

// The champions you're learning (from the Guides tab) and your own champion mastery (for the pick advice).

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sync"
	"time"
)

func learnFile() string { return profileFile("aprenent.json") }

// learnList: the champions marked "I'm learning it" (Data Dragon ids, like "Yasuo"), at most 5
func learnList() []string {
	var l []string
	if b, err := os.ReadFile(learnFile()); err == nil {
		json.Unmarshal(b, &l)
	}
	return l
}

func learnHandler(w http.ResponseWriter, req *http.Request) {
	if req.Method == http.MethodPost {
		var l []string
		if b, err := io.ReadAll(io.LimitReader(req.Body, 4096)); err == nil && json.Unmarshal(b, &l) == nil {
			if len(l) > 5 {
				l = l[:5]
			}
			b, _ := json.Marshal(l)
			os.WriteFile(learnFile(), b, 0o644)
			logf("Guies: aprenent %v", l)
			go func() { // read the Challenger games of the new ones (their build, runes and skill order)
				time.Sleep(2 * time.Second)
				if !liveNow.Load() {
					benchRun()
				}
			}()
		}
	}
	b, _ := json.Marshal(learnList())
	w.Header().Set("Content-Type", "application/json")
	w.Write(b)
}

// your mastery points with every champion (Riot), kept for an hour
var (
	myMastMu sync.Mutex
	myMast   map[string]int
	myMastAt time.Time
)

func masteryHandler(w http.ResponseWriter, req *http.Request) {
	myMastMu.Lock()
	defer myMastMu.Unlock()
	if myMast == nil || time.Since(myMastAt) > time.Hour {
		c := loadConfig()
		sc := loadSeason()
		if c.RiotKey != "" && sc.PUUID != "" {
			var list []struct {
				ChampionID int `json:"championId"`
				Points     int `json:"championPoints"`
			}
			if err := scoutGet(c, platformHost(c), "/lol/champion-mastery/v4/champion-masteries/by-puuid/"+sc.PUUID, &list); err == nil {
				m := map[string]int{}
				for _, x := range list {
					if ch := championByKey(x.ChampionID); ch.ID != "" {
						m[ch.ID] = x.Points
					}
				}
				myMast, myMastAt = m, time.Now()
			}
		}
	}
	b, _ := json.Marshal(myMast)
	w.Header().Set("Content-Type", "application/json")
	w.Write(b)
}

// the learning champions that aren't already among the ones you play most, in the order marked
func learnExtra(have []string) []string {
	in := map[string]bool{}
	for _, c := range have {
		in[c] = true
	}
	out := []string{}
	for _, c := range learnList() {
		if !in[c] {
			out = append(out, c)
		}
	}
	return out
}

// ---- a guide for a champion without a written one: the AI writes it from the abilities of the current patch
// (passed from Riot's Data Dragon, so it doesn't invent the kit). Once per champion and patch.

type aiGuideCombo struct {
	N   string   `json:"n"`
	L   int      `json:"l"`
	S   []string `json:"s"`
	How string   `json:"how"`
}
type aiGuide struct {
	Role     string         `json:"role"`
	Tips     []string       `json:"tips"`
	Spikes   string         `json:"spikes"`
	Combos   []aiGuideCombo `json:"combos"`
	Practice string         `json:"practice"`
	Model    string         `json:"model,omitempty"`
	Error    string         `json:"error,omitempty"`
}

var guideStepRe = regexp.MustCompile(`^(AA|F|[QWER][23]?|W↩|R[QWE])$`)

func guideAIHandler(w http.ResponseWriter, req *http.Request) {
	var in struct {
		Champ, Name, Patch, Kit string
	}
	json.NewDecoder(io.LimitReader(req.Body, 32<<10)).Decode(&in)
	reply := func(g aiGuide) {
		b, _ := json.Marshal(g)
		w.Header().Set("Content-Type", "application/json")
		w.Write(b)
	}
	if in.Champ == "" || in.Kit == "" {
		reply(aiGuide{Error: "falten dades"})
		return
	}
	safe := regexp.MustCompile(`[^A-Za-z0-9_-]`).ReplaceAllString(in.Champ+"-"+in.Patch, "_")
	file := filepath.Join(filepath.Dir(profileFile("x")), "guies", safe+".json")
	if b, err := os.ReadFile(file); err == nil {
		w.Header().Set("Content-Type", "application/json")
		w.Write(b)
		return
	}
	c := loadConfig()
	if c.AIKey == "" {
		reply(aiGuide{Error: "Per fer la guia d'aquest campió cal la clau de Gemini (Ajustos)."})
		return
	}
	sys := `Ets un entrenador de League of Legends de nivell Challenger. Escrius en català, curt i concret, per a un jugador de Platí que vol aprendre un campió.
Fes servir NOMÉS les habilitats que et passo (són les del pedaç actual): no t'inventis efectes.
Respon un JSON: {"role": una frase (posició habitual i estil), "tips": 4 o 5 frases (com fer servir cada habilitat i l'error típic), "spikes": una frase (quan és fort), "combos": 3 o 4 combos de més fàcil a més difícil, cadascun {"n": nom curt, "l": 1 fàcil|2 mitjà|3 difícil, "s": les tecles en ordre, "how": una frase}, "practice": una frase de què practicar a l'eina de pràctica}.
A "s" només pots fer servir: "Q","W","E","R" (habilitats), "Q2","Q3","E2","R2" (segona o tercera activació), "AA" (cop bàsic), "F" (Flash). Només combos que el kit permet de veritat i que els jugadors fan servir.`
	txt, model, err := geminiJSON(c.AIKey, sys, fmt.Sprintf("Campió: %s (pedaç %s)\nHabilitats:\n%s", in.Name, in.Patch, in.Kit), 1500)
	if err != nil {
		reply(aiGuide{Error: "La IA no ha pogut fer la guia: " + err.Error()})
		return
	}
	var g aiGuide
	if json.Unmarshal([]byte(txt), &g) != nil || g.Role == "" || len(g.Tips) == 0 {
		reply(aiGuide{Error: "La IA ha tornat una guia que no s'entén; torna-ho a provar."})
		return
	}
	ok := g.Combos[:0]
	for _, cb := range g.Combos {
		good := len(cb.S) >= 2 && len(cb.S) <= 12
		for _, s := range cb.S {
			good = good && guideStepRe.MatchString(s)
		}
		if good {
			if cb.L < 1 || cb.L > 3 {
				cb.L = 2
			}
			ok = append(ok, cb)
		}
	}
	g.Combos, g.Model = ok, model
	b, _ := json.Marshal(g)
	os.MkdirAll(filepath.Dir(file), 0o755)
	os.WriteFile(file, b, 0o644)
	logf("Guies: guia de %s feta amb %s (%d combos)", in.Name, model, len(ok))
	w.Header().Set("Content-Type", "application/json")
	w.Write(b)
}
