package main

import (
	"encoding/json"
	"net/http"
	"regexp"
	"sync"
	"time"
)

// The state of each part of the app, for the "Estat de l'app" panel: what worked last and the latest errors,
// so a problem can be seen (and copied) without reading the whole log.
type partState struct {
	OK  bool   `json:"ok"`
	Msg string `json:"msg"`
	At  int64  `json:"at"`
}

var (
	stMu     sync.Mutex
	stParts  = map[string]partState{}
	stErrors []string
	errRe    = regexp.MustCompile(`(?i)\berror\b|no respon|HTTP (40[0-35-9]|4[1-9]\d|5\d\d)|no puc|no he pogut|no_key|bad_key|aturat \(exit|rebutja`)
)

func setPart(part string, ok bool, msg string) {
	stMu.Lock()
	stParts[part] = partState{ok, msg, time.Now().Unix()}
	stMu.Unlock()
}

// noteLogLine keeps the last 25 lines that look like errors (called by logf)
func noteLogLine(line string) {
	if !errRe.MatchString(line) {
		return
	}
	stMu.Lock()
	stErrors = append(stErrors, time.Now().Format("15:04:05")+"  "+line)
	if len(stErrors) > 25 {
		stErrors = stErrors[len(stErrors)-25:]
	}
	stMu.Unlock()
}

func statusHandler(send func(http.ResponseWriter, int, string, []byte)) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		c := loadConfig()
		stMu.Lock()
		parts := map[string]partState{}
		for k, v := range stParts {
			parts[k] = v
		}
		errs := append([]string{}, stErrors...)
		stMu.Unlock()
		if _, ok := parts["riot"]; !ok {
			if c.RiotKey == "" {
				parts["riot"] = partState{false, "no_key", 0}
			}
		}
		if _, ok := parts["ai"]; !ok && c.AIKey == "" {
			parts["ai"] = partState{false, "no_key", 0}
		}
		b := loadBench()
		n := map[string]int{}
		for ch, g := range b.Games {
			n[ch] = len(g)
		}
		benchMu.Lock()
		bs := map[string]any{"busy": benchBusy, "msg": benchMsg, "done": benchDone, "goal": benchGoal, "updated": b.Updated, "games": n}
		benchMu.Unlock()
		raw, _ := json.Marshal(map[string]any{"version": version, "parts": parts, "errors": errs, "bench": bs, "voice": voiceRunning(),
			"riot_key": c.RiotKey != "", "ai_key": c.AIKey != "", "ai_model": c.AIModel, "ai_used": aiUsedToday(0), "riot_id": c.MyRiotID})
		send(w, 200, "application/json", raw)
	}
}
