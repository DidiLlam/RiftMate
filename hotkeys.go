package main

import (
	"encoding/json"
	"net/http"
	"sync"
	"time"
)

type hotkeyPress struct {
	Row  int    `json:"row"`  // 0..4: enemy top, jungle, mid, ADC, support
	Kind string `json:"kind"` // flash / other
	At   int64  `json:"at"`
}

var (
	hkMu    sync.Mutex
	hkQueue []hotkeyPress
)

func hotkeyPush(row int, kind string) {
	hkMu.Lock()
	hkQueue = append(hkQueue, hotkeyPress{row, kind, time.Now().UnixMilli()})
	if len(hkQueue) > 20 {
		hkQueue = hkQueue[len(hkQueue)-20:]
	}
	hkMu.Unlock()
	logf("Drecera: fila %d, %s", row+1, kind)
}

// /api/hotkeys: the presses since the last time the page asked (and forgets them). POST ?row=&kind= simulates one (tests).
func hotkeysHandler(send func(http.ResponseWriter, int, string, []byte)) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		if req.Method == http.MethodPost {
			var row int
			json.Unmarshal([]byte(req.URL.Query().Get("row")), &row)
			hotkeyPush(row, req.URL.Query().Get("kind"))
		}
		hkMu.Lock()
		out := hkQueue
		hkQueue = nil
		hkMu.Unlock()
		if out == nil {
			out = []hotkeyPress{}
		}
		b, _ := json.Marshal(out)
		send(w, 200, "application/json", b)
	}
}
