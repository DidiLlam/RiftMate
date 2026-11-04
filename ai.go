package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

// In-game AI coach: the page sends the question, the state of the game and the recent conversation; the
// program asks Claude (Anthropic API, with the user's own key) and returns a short answer to be spoken.
const aiModel = "claude-haiku-5-5"

var aiHTTP = &http.Client{Timeout: 20 * time.Second}

type aiTurn struct {
	Role string `json:"role"` // user | assistant
	Text string `json:"text"`
}

func aiSystem(lang string) string {
	name := map[string]string{"ca": "català", "es": "castellano", "en": "English"}[lang]
	if name == "" {
		name = "català"
	}
	return `Ets el coach de League of Legends del jugador mentre juga una partida rankeada. Ell et parla per veu i la teva resposta es llegirà en veu alta.
Regles:
- Respon en ` + name + `, amb una o dues frases curtes (màxim 35 paraules). Res de llistes, emojis ni format: només text per dir en veu alta.
- Fes servir només l'estat de la partida que t'envio i el coneixement general i estable del joc. Si et pregunta una cosa que no surt a l'estat (on és un rival, si té el Flash...), digues que no ho saps en lloc d'inventar-ho.
- Les xifres concretes de pedaç (danys, temps de recàrrega exactes, preus) poden haver canviat: no les afirmis si no surten a l'estat.
- Sigues directe i pràctic: digues què fer ara. Si la pregunta no és del joc, respon breument i torna a la partida.
- Els temps d'encanteris i objectius de l'estat són els que porta l'app; tracta'ls com a aproximats.`
}

// aiReviewSystem: after the game, reading the report (not spoken in the middle of a game)
func aiReviewSystem(lang string) string {
	name := map[string]string{"ca": "català", "es": "castellano", "en": "English"}[lang]
	if name == "" {
		name = "català"
	}
	return `Ets el coach de League of Legends del jugador. La partida s'acaba d'acabar i t'envio el seu informe.
Regles:
- Respon en ` + name + ` amb exactament tres lliçons, cadascuna en una frase curta que comenci amb un verb, separades per punt (màxim 70 paraules en total). Res de llistes numerades, emojis ni format: es llegirà en veu alta.
- Les lliçons han de sortir de les dades de l'informe (morts, farm, visió, objectius, rival, taula), no consells genèrics. Si ha anat bé, una de les tres pot ser què ha de mantenir.
- No inventis xifres que no siguin a l'informe.`
}

func aiAsk(question, state, lang string, history []aiTurn) (string, error) {
	return aiAskMode(question, state, lang, history, "")
}

func aiAskMode(question, state, lang string, history []aiTurn, mode string) (string, error) {
	c := loadConfig()
	if c.AIKey == "" {
		return "", errors.New("no_key")
	}
	msgs := []map[string]string{}
	for _, h := range history {
		if (h.Role == "user" || h.Role == "assistant") && strings.TrimSpace(h.Text) != "" {
			msgs = append(msgs, map[string]string{"role": h.Role, "content": h.Text})
		}
	}
	// the API needs alternating turns starting with the user
	for len(msgs) > 0 && msgs[0]["role"] != "user" {
		msgs = msgs[1:]
	}
	if len(msgs) > 12 {
		msgs = msgs[len(msgs)-12:]
		for len(msgs) > 0 && msgs[0]["role"] != "user" {
			msgs = msgs[1:]
		}
	}
	content := question
	system := aiSystem(lang)
	if mode == "review" {
		system = aiReviewSystem(lang)
		content = "Informe de la partida:\n" + state + "\n\n" + question
	} else if strings.TrimSpace(state) != "" {
		content = "Estat de la partida ara mateix:\n" + state + "\n\nPregunta del jugador: " + question
	}
	if len(msgs) > 0 && msgs[len(msgs)-1]["role"] == "user" {
		msgs = msgs[:len(msgs)-1]
	}
	msgs = append(msgs, map[string]string{"role": "user", "content": content})
	// Anthropic keys start with "sk-ant-"; any other key is a (free) Google AI Studio key for Gemini
	if !strings.HasPrefix(c.AIKey, "sk-ant-") {
		ans, err := geminiAsk(c.AIKey, system, msgs)
		if err == nil {
			logf("IA: \"%s\" -> \"%s\"", question, ans)
		}
		return ans, err
	}
	body, _ := json.Marshal(map[string]any{"model": aiModel, "max_tokens": 300, "system": system, "messages": msgs})
	base := "https://api.anthropic.com"
	if b := os.Getenv("LT_AI_BASE"); b != "" { // tests
		base = b
	}
	req, _ := http.NewRequest("POST", base+"/v1/messages", bytes.NewReader(body))
	req.Header.Set("x-api-key", c.AIKey)
	req.Header.Set("anthropic-version", "2023-06-01")
	req.Header.Set("content-type", "application/json")
	t0 := time.Now()
	r, err := aiHTTP.Do(req)
	if err != nil {
		return "", err
	}
	defer r.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	var out struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		Error *struct {
			Type    string `json:"type"`
			Message string `json:"message"`
		} `json:"error"`
		Usage struct {
			In  int `json:"input_tokens"`
			Out int `json:"output_tokens"`
		} `json:"usage"`
	}
	json.Unmarshal(raw, &out)
	if r.StatusCode != 200 {
		msg := fmt.Sprintf("HTTP %d", r.StatusCode)
		if out.Error != nil {
			msg += ": " + out.Error.Message
		}
		return "", errors.New(msg)
	}
	var sb strings.Builder
	for _, b := range out.Content {
		if b.Type == "text" {
			sb.WriteString(b.Text)
		}
	}
	ans := strings.TrimSpace(sb.String())
	logf("IA (%s, %d+%d tokens, %.1f s): \"%s\" -> \"%s\"", aiModel, out.Usage.In, out.Usage.Out, time.Since(t0).Seconds(), question, ans)
	return ans, nil
}

func keyHint(k string) string {
	if len(k) <= 8 {
		return ""
	}
	return k[:4] + "…" + k[len(k)-4:]
}

func aiHandlers(mux *http.ServeMux, send func(http.ResponseWriter, int, string, []byte)) {
	// check the saved key right away: Gemini lists its models, Anthropic answers a one-word question
	mux.HandleFunc("/api/ai/check", func(w http.ResponseWriter, req *http.Request) {
		c := loadConfig()
		res := map[string]string{}
		switch {
		case c.AIKey == "":
			res["error"] = "no_key"
		case !strings.HasPrefix(c.AIKey, "sk-ant-"):
			gemMu.Lock()
			gemModel = ""
			gemMu.Unlock()
			if m, err := pickGeminiModel(c.AIKey); err != nil {
				res["error"] = err.Error()
			} else if _, err := geminiAsk(c.AIKey, aiSystem("ca"), []map[string]string{{"role": "user", "content": "Digues només: hola."}}); err != nil {
				res["error"] = err.Error() // a real one-line question: listing models isn't enough to prove the key works
			} else {
				_ = m
				gemMu.Lock()
				res["model"] = gemModel // the one that actually answered
				gemMu.Unlock()
			}
		default:
			if _, err := aiAsk("Digues només: hola.", "", "ca", nil); err != nil {
				res["error"] = err.Error()
			} else {
				res["model"] = aiModel
			}
		}
		logf("IA: comprovacio de la clau: %v", res)
		b, _ := json.Marshal(res)
		send(w, 200, "application/json", b)
	})
	mux.HandleFunc("/api/ask", func(w http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodPost {
			send(w, 405, "text/plain", []byte("POST only"))
			return
		}
		var in struct {
			Question string   `json:"question"`
			State    string   `json:"state"`
			Lang     string   `json:"lang"`
			History  []aiTurn `json:"history"`
			Mode     string   `json:"mode"` // "" question in the game, "review" lessons of a report
			Date     string   `json:"date"` // review: the report it is saved into
		}
		json.NewDecoder(io.LimitReader(req.Body, 64<<10)).Decode(&in)
		ans, err := aiAskMode(strings.TrimSpace(in.Question), in.State, in.Lang, in.History, in.Mode)
		if err == nil && in.Mode == "review" && in.Date != "" {
			if g, _, e := readGame(in.Date); e == nil {
				g.Report["ai_review"] = ans
				if b, e := json.Marshal(g); e == nil {
					os.WriteFile(gameFile(in.Date), b, 0o644)
				}
			}
		}
		res := map[string]string{"answer": ans}
		if err != nil {
			res["error"] = err.Error()
			if err.Error() != "no_key" {
				logf("IA: error %v", err)
			}
		}
		b, _ := json.Marshal(res)
		send(w, 200, "application/json", b)
	})
}
