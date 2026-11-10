package main

import (
	"encoding/json"
	"net/http"
	"os"
	"strconv"
	"sync"
	"time"
)

// Push to talk: with it on, a phrase only counts if you were holding the key (or the mouse side button) while
// saying it. Whatever the microphone picks up the rest of the time (your duo, the game, Discord) is ignored.
type pttCfg struct {
	On  bool `json:"on"`
	Key int  `json:"key"` // Windows virtual key: 5/6 mouse side buttons, 0x14 Caps Lock, 0xA5 AltGr
}

var (
	pttMu   sync.Mutex
	pttC    = pttCfg{Key: 5}
	pttRead bool
	pttLast time.Time // last time the key was seen held
)

func pttFile() string { return profileFile("prem-per-parlar.json") }

func pttGet() pttCfg {
	pttMu.Lock()
	defer pttMu.Unlock()
	if !pttRead {
		pttRead = true
		if b, err := os.ReadFile(pttFile()); err == nil {
			json.Unmarshal(b, &pttC)
		}
	}
	return pttC
}

func pttValidKey(k int) bool { return k == 5 || k == 6 || k == 0x14 || k == 0xA5 || k == 0x04 }

// pttAllows: may this phrase, heard just now, count?
func pttAllows() bool {
	c := pttGet()
	if !c.On {
		return true
	}
	pttMu.Lock()
	defer pttMu.Unlock()
	// the result arrives a moment after you stop talking, and you may let go a bit early
	return time.Since(pttLast) < 2500*time.Millisecond
}

func pttSeen() {
	pttMu.Lock()
	pttLast = time.Now()
	pttMu.Unlock()
}

func pttHandler(send func(http.ResponseWriter, int, string, []byte)) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		c := pttGet()
		if req.Method == http.MethodPost {
			q := req.URL.Query()
			if v := q.Get("on"); v != "" {
				c.On = v == "1"
			}
			if k, err := strconv.Atoi(q.Get("key")); err == nil && pttValidKey(k) {
				c.Key = k
			}
			pttMu.Lock()
			pttC = c
			pttMu.Unlock()
			if b, err := json.Marshal(c); err == nil {
				os.WriteFile(pttFile(), b, 0o644)
			}
			logf("Prem per parlar: %+v", c)
		}
		pttMu.Lock()
		held := time.Since(pttLast) < 300*time.Millisecond
		pttMu.Unlock()
		b, _ := json.Marshal(map[string]any{"on": c.On, "key": c.Key, "held": held})
		send(w, 200, "application/json", b)
	}
}
