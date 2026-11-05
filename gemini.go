package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Google Gemini (free tier from Google AI Studio, no card needed). The best "Flash" model available to the key
// is picked from the model list, so the app keeps working when Google renames or retires models.
var (
	gemMu    sync.Mutex
	gemModel string   // the model that answered last
	gemCands []string // every usable Flash model, best first
)

// when the model list can't be read: the models Google's documentation names, best first
var gemFallbacks = []string{"gemini-3.8-flash", "gemini-3.5-flash", "gemini-3.5-flash-lite", "gemini-2.5-flash"}

// one attempt must not keep the player waiting: a saturated model is skipped after a few seconds
var gemHTTP = &http.Client{Timeout: 8 * time.Second}

func gemBase() string {
	if b := os.Getenv("LT_GEMINI_BASE"); b != "" { // tests
		return b
	}
	return "https://generativelanguage.googleapis.com"
}

var gemVerRe = regexp.MustCompile(`gemini-(\d+(?:\.\d+)?)-flash`)

func pickGeminiModel(key string) (string, error) {
	gemMu.Lock()
	defer gemMu.Unlock()
	if gemModel != "" {
		return gemModel, nil
	}
	req, _ := http.NewRequest("GET", gemBase()+"/v1beta/models?pageSize=200", nil)
	req.Header.Set("x-goog-api-key", key)
	r, err := aiHTTP.Do(req)
	if err != nil {
		return "", err
	}
	defer r.Body.Close()
	var out struct {
		Models []struct {
			Name    string   `json:"name"`
			Methods []string `json:"supportedGenerationMethods"`
		} `json:"models"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	json.NewDecoder(r.Body).Decode(&out)
	if r.StatusCode != 200 {
		msg := fmt.Sprintf("HTTP %d", r.StatusCode)
		if out.Error != nil {
			msg += ": " + out.Error.Message
		}
		if strings.HasPrefix(key, "AIza") && r.StatusCode == 400 && out.Error != nil && strings.Contains(strings.ToLower(out.Error.Message), "api key not valid") {
			return "", errors.New(msg)
		}
		// newer "AQ." auth keys may not be allowed to list models: use the model of Google's own examples
		logf("IA: no puc llistar els models de Gemini (%s): faig servir %s", msg, strings.Join(gemFallbacks, ", "))
		gemCands = gemFallbacks
		gemModel = gemCands[0]
		return gemModel, nil
	}
	type cand struct {
		name string
		ver  float64
		lite bool
	}
	cs := []cand{}
	for _, m := range out.Models {
		n := strings.TrimPrefix(m.Name, "models/")
		gen := false
		for _, x := range m.Methods {
			if x == "generateContent" {
				gen = true
			}
		}
		if !gen || !strings.Contains(n, "flash") {
			continue
		}
		bad := false
		for _, w := range []string{"image", "tts", "audio", "live", "thinking", "exp", "preview", "embedding", "8b"} {
			if strings.Contains(n, w) {
				bad = true
			}
		}
		if bad {
			continue
		}
		v := 0.0
		if mm := gemVerRe.FindStringSubmatch(n); mm != nil {
			v, _ = strconv.ParseFloat(mm[1], 64)
		}
		cs = append(cs, cand{n, v, strings.Contains(n, "lite")})
	}
	if len(cs) == 0 {
		return "", errors.New("no hi ha cap model Flash disponible per a aquesta clau")
	}
	// newest version first; within a version the full Flash before Flash-Lite; "latest" aliases last
	sort.Slice(cs, func(i, j int) bool {
		if cs[i].ver != cs[j].ver {
			return cs[i].ver > cs[j].ver
		}
		if cs[i].lite != cs[j].lite {
			return !cs[i].lite
		}
		return len(cs[i].name) < len(cs[j].name)
	})
	gemCands = nil
	for _, c := range cs {
		if c.ver == 0 || c.ver >= 2 {
			gemCands = append(gemCands, c.name)
		}
	}
	if len(gemCands) == 0 {
		gemCands = []string{cs[0].name}
	}
	gemModel = gemCands[0]
	// the one that answered last time (saved), if it is still offered: the newest ones are often saturated
	if saved := loadConfig().AIModel; saved != "" {
		for _, c := range gemCands {
			if c == saved {
				gemModel = saved
			}
		}
	}
	logf("IA: models de Gemini disponibles: %s (primer %s)", strings.Join(gemCands, ", "), gemModel)
	return gemModel, nil
}

// geminiAsk tries the last model that worked, then the others: the free tier is the first to be refused when a
// model is saturated ("high demand", 503) or over its per-model limit (429).
func geminiAsk(key, system string, msgs []map[string]string) (string, error) {
	if _, err := pickGeminiModel(key); err != nil {
		return "", err
	}
	gemMu.Lock()
	order := []string{gemModel}
	for _, c := range gemCands {
		if c != gemModel {
			order = append(order, c)
		}
	}
	gemMu.Unlock()
	if len(order) > 4 {
		order = order[:4]
	}
	// hedged: the next model starts when the current one fails or hasn't answered in 3 seconds; the first answer wins
	type res struct {
		m   string
		ans string
		err error
	}
	ch := make(chan res, len(order))
	start := func(m string) { go func() { a, e := geminiOnce(key, m, system, msgs); ch <- res{m, a, e} }() }
	next, running := 1, 1
	start(order[0])
	deadline := time.After(16 * time.Second)
	var last error
	for running > 0 {
		var r res
		select {
		case r = <-ch:
		case <-time.After(3 * time.Second):
			if next < len(order) {
				logf("IA: %s triga, provo també %s", order[next-1], order[next])
				start(order[next])
				next++
				running++
			}
			continue
		case <-deadline:
			if last == nil {
				last = errors.New("Timeout: cap model ha respost")
			}
			return "", last
		}
		running--
		if r.err == nil {
			gemMu.Lock()
			if gemModel != r.m {
				logf("IA: ara faig servir %s", r.m)
			}
			gemModel = r.m
			if c := loadConfig(); c.AIModel != r.m {
				c.AIModel = r.m
				saveConfig(c)
			}
			gemMu.Unlock()
			return r.ans, nil
		}
		last = r.err
		if !gemRetryable(r.err) {
			return "", r.err
		}
		logf("IA: %s no respon (%v), provo el següent", r.m, r.err)
		if next < len(order) {
			start(order[next])
			next++
			running++
		}
	}
	return "", last
}

func gemRetryable(err error) bool {
	e := err.Error()
	for _, k := range []string{"HTTP 500", "HTTP 502", "HTTP 503", "HTTP 504", "HTTP 404", "limit_gratuit", "Timeout", "deadline", "resposta buida", "sense text", "EOF", "reset"} {
		if strings.Contains(e, k) {
			return true
		}
	}
	return false
}

func geminiOnce(key, model, system string, msgs []map[string]string) (string, error) {
	contents := []map[string]any{}
	for _, m := range msgs {
		role := "user"
		if m["role"] == "assistant" {
			role = "model"
		}
		contents = append(contents, map[string]any{"role": role, "parts": []map[string]string{{"text": m["content"]}}})
	}
	call := func(withThinking bool) (int, []byte, error) {
		gc := map[string]any{"maxOutputTokens": 600, "temperature": 0.6}
		if withThinking {
			gc["thinkingConfig"] = map[string]any{"thinkingBudget": 0} // answer straight away: speed matters in game
		}
		body, _ := json.Marshal(map[string]any{"systemInstruction": map[string]any{"parts": []map[string]string{{"text": system}}},
			"contents": contents, "generationConfig": gc})
		req, _ := http.NewRequest("POST", gemBase()+"/v1beta/models/"+model+":generateContent", bytes.NewReader(body))
		req.Header.Set("x-goog-api-key", key)
		req.Header.Set("content-type", "application/json")
		r, err := gemHTTP.Do(req)
		if err != nil {
			return 0, nil, err
		}
		defer r.Body.Close()
		raw, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		return r.StatusCode, raw, nil
	}
	t0 := time.Now()
	code, raw, err := call(true)
	if err == nil && code == 400 {
		code, raw, err = call(false) // model that doesn't accept the thinking setting
	}
	if err != nil {
		return "", err
	}
	// Google's newer Interactions API: used when generateContent refuses the key type ("AQ." auth keys) or is gone
	if code == 401 || code == 403 || code == 404 {
		if ans, ierr := geminiInteractions(key, model, system, msgs); ierr == nil {
			logf("IA (%s, Interactions, %.1f s)", model, time.Since(t0).Seconds())
			return ans, nil
		} else {
			logf("IA: generateContent HTTP %d i Interactions: %v", code, ierr)
		}
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
			In  int `json:"promptTokenCount"`
			Out int `json:"candidatesTokenCount"`
		} `json:"usageMetadata"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	json.Unmarshal(raw, &out)
	if code != 200 {
		if code == 429 {
			return "", errors.New("limit_gratuit")
		}
		if out.Error != nil {
			return "", fmt.Errorf("HTTP %d: %s", code, out.Error.Message)
		}
		return "", fmt.Errorf("HTTP %d", code)
	}
	var sb strings.Builder
	if len(out.Candidates) > 0 {
		for _, p := range out.Candidates[0].Content.Parts {
			if !p.Thought {
				sb.WriteString(p.Text)
			}
		}
	}
	ans := strings.TrimSpace(sb.String())
	if ans == "" {
		return "", errors.New("resposta buida")
	}
	logf("IA (%s, %d+%d tokens, %.1f s)", model, out.Usage.In, out.Usage.Out, time.Since(t0).Seconds())
	return ans, nil
}

// geminiInteractions: POST /v1beta/interactions (stateless). The conversation so far goes inside the input text.
func geminiInteractions(key, model, system string, msgs []map[string]string) (string, error) {
	var sb strings.Builder
	if len(msgs) > 1 {
		sb.WriteString("Conversa anterior:\n")
		for _, m := range msgs[:len(msgs)-1] {
			who := "Jugador"
			if m["role"] == "assistant" {
				who = "Coach"
			}
			sb.WriteString(who + ": " + m["content"] + "\n")
		}
		sb.WriteString("\n")
	}
	sb.WriteString(msgs[len(msgs)-1]["content"])
	try := func(withSystem bool) (int, map[string]any, error) {
		in := sb.String()
		body := map[string]any{"model": model, "store": false,
			"generation_config": map[string]any{"thinking_level": "low", "max_output_tokens": 600}}
		if withSystem {
			body["system_instruction"] = system
		} else {
			in = system + "\n\n" + in
		}
		body["input"] = in
		b, _ := json.Marshal(body)
		req, _ := http.NewRequest("POST", gemBase()+"/v1beta/interactions", bytes.NewReader(b))
		req.Header.Set("x-goog-api-key", key)
		req.Header.Set("content-type", "application/json")
		r, err := gemHTTP.Do(req)
		if err != nil {
			return 0, nil, err
		}
		defer r.Body.Close()
		var out map[string]any
		json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&out)
		return r.StatusCode, out, nil
	}
	code, out, err := try(true)
	if err == nil && code == 400 {
		code, out, err = try(false)
	}
	if err != nil {
		return "", err
	}
	if code != 200 {
		if e, ok := out["error"].(map[string]any); ok {
			return "", fmt.Errorf("HTTP %d: %v", code, e["message"])
		}
		return "", fmt.Errorf("HTTP %d", code)
	}
	if t, ok := out["output_text"].(string); ok && strings.TrimSpace(t) != "" {
		return strings.TrimSpace(t), nil
	}
	// steps[...].content[...].text, newest step first, skipping thoughts
	if steps, ok := out["steps"].([]any); ok {
		for i := len(steps) - 1; i >= 0; i-- {
			st, _ := steps[i].(map[string]any)
			if st == nil {
				continue
			}
			if t, _ := st["type"].(string); strings.Contains(t, "thought") || st["role"] == "user" {
				continue
			}
			var parts []string
			switch c := st["content"].(type) {
			case string:
				parts = append(parts, c)
			case []any:
				for _, x := range c {
					if m, ok := x.(map[string]any); ok {
						if tp, _ := m["type"].(string); tp == "" || tp == "text" {
							if t, ok := m["text"].(string); ok {
								parts = append(parts, t)
							}
						}
					}
				}
			}
			if ans := strings.TrimSpace(strings.Join(parts, "")); ans != "" {
				return ans, nil
			}
		}
	}
	return "", errors.New("resposta sense text")
}
