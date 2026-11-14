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
	"strings"
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
	// generous room: the newer models think before answering, and that thinking counts against the limit (a guide cut
	// in half isn't JSON). A second try if the first answer can't be read.
	var g aiGuide
	model := ""
	for try := 0; try < 2; try++ {
		user := fmt.Sprintf("Campió: %s (pedaç %s)\nHabilitats:\n%s", in.Name, in.Patch, in.Kit)
		if try > 0 {
			user += "\n\nRespon només el JSON, complet i tancat, amb els camps exactes que t'he dit."
		}
		txt, m, err := geminiJSON(c.AIKey, sys, user, 8192)
		if err != nil {
			reply(aiGuide{Error: "La IA no ha pogut fer la guia: " + err.Error()})
			return
		}
		if gg, ok := parseAIGuide(txt); ok {
			g, model = gg, m
			break
		}
		logf("Guies: la IA (%s) ha tornat una guia de %s que no s'entén (%d caràcters): %.300s", m, in.Name, len(txt), txt)
	}
	if g.Role == "" {
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

// parseAIGuide: reads the AI's guide without failing on small differences (a level written as "2" or "fàcil",
// tips as objects instead of sentences)
func parseAIGuide(txt string) (aiGuide, bool) {
	var m map[string]any
	if json.Unmarshal([]byte(txt), &m) != nil {
		return aiGuide{}, false
	}
	str := func(v any) string {
		switch x := v.(type) {
		case string:
			return strings.TrimSpace(x)
		case map[string]any:
			for _, k := range []string{"text", "tip", "t", "how", "description"} {
				if s, ok := x[k].(string); ok {
					return strings.TrimSpace(s)
				}
			}
			parts := []string{}
			for _, v := range x {
				if s, ok := v.(string); ok {
					parts = append(parts, s)
				}
			}
			return strings.Join(parts, ": ")
		case []any:
			parts := []string{}
			for _, e := range x {
				if s, ok := e.(string); ok {
					parts = append(parts, s)
				}
			}
			return strings.Join(parts, " ")
		}
		return ""
	}
	g := aiGuide{Role: str(m["role"]), Spikes: str(m["spikes"]), Practice: str(m["practice"])}
	if l, ok := m["tips"].([]any); ok {
		for _, t := range l {
			if s := str(t); s != "" {
				g.Tips = append(g.Tips, s)
			}
		}
	} else if s := str(m["tips"]); s != "" {
		g.Tips = []string{s}
	}
	if l, ok := m["combos"].([]any); ok {
		for _, e := range l {
			c, ok := e.(map[string]any)
			if !ok {
				continue
			}
			cb := aiGuideCombo{N: str(c["n"]), How: str(c["how"])}
			switch x := c["l"].(type) {
			case float64:
				cb.L = int(x)
			case string:
				lx := strings.ToLower(x)
				switch {
				case strings.Contains(lx, "1") || strings.Contains(lx, "fàcil") || strings.Contains(lx, "facil"):
					cb.L = 1
				case strings.Contains(lx, "3") || strings.Contains(lx, "difícil") || strings.Contains(lx, "dificil"):
					cb.L = 3
				default:
					cb.L = 2
				}
			}
			switch x := c["s"].(type) {
			case []any:
				for _, st := range x {
					if s, ok := st.(string); ok {
						cb.S = append(cb.S, strings.ToUpper(strings.TrimSpace(s)))
					}
				}
			case string: // "Q > E > AA"
				for _, f := range strings.FieldsFunc(x, func(r rune) bool { return r == '>' || r == ',' || r == ' ' || r == '+' || r == '-' }) {
					cb.S = append(cb.S, strings.ToUpper(f))
				}
			}
			if cb.N != "" {
				g.Combos = append(g.Combos, cb)
			}
		}
	}
	return g, g.Role != "" && len(g.Tips) > 0
}
