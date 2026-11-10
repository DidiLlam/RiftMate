package main

import (
	"encoding/json"
	"io"
	"net/http"
	"sync"
)

// The enemy spells you marked during the game (the page sends its count as it changes); at the end they go into
// the report, and with Riot's numbers (how many times each one was really used) the report says how many you caught.
var (
	marksMu  sync.Mutex
	marksNow map[string]int
)

func marksTake() map[string]int {
	marksMu.Lock()
	defer marksMu.Unlock()
	m := marksNow
	marksNow = nil
	return m
}

func marksHandler(send func(http.ResponseWriter, int, string, []byte)) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodPost {
			send(w, 405, "text/plain", []byte("POST only"))
			return
		}
		var m map[string]int
		if err := json.NewDecoder(io.LimitReader(req.Body, 8192)).Decode(&m); err != nil || len(m) > 60 {
			send(w, 400, "text/plain", []byte("bad"))
			return
		}
		marksMu.Lock()
		marksNow = m
		marksMu.Unlock()
		send(w, 200, "application/json", []byte(`{"ok":true}`))
	}
}
