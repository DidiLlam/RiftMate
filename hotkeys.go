package main

import (
	"encoding/json"
	"net/http"
	"sync"
	"time"
)

type hotkeyPress struct {
	Row  int    `json:"row"`  // 0..4: enemy top, jungle, mid, ADC, support
	Kind string `json:"kind"` // flash / other (and R / Zhonya from the overlay)
	Src  string `json:"src"`  // key / phone / overlay
	At   int64  `json:"at"`
}

var (
	hkMu    sync.Mutex
	hkQueue []hotkeyPress
)

func hotkeyPush(row int, kind string) { hotkeyPushFrom(row, kind, "key") }

func hotkeyPushFrom(row int, kind, src string) {
	if row < 0 || row > 4 || (kind != "flash" && kind != "other" && kind != "R" && kind != "Zhonya") {
		return
	}
	hkMu.Lock()
	hkQueue = append(hkQueue, hotkeyPress{row, kind, src, time.Now().UnixMilli()})
	if len(hkQueue) > 20 {
		hkQueue = hkQueue[len(hkQueue)-20:]
	}
	hkMu.Unlock()
	logf("Drecera (%s): fila %d, %s", src, row+1, kind)
}

// /api/hotkeys: the presses since the last time the page asked (and forgets them). POST ?row=&kind= simulates one (tests).
func hotkeysHandler(send func(http.ResponseWriter, int, string, []byte)) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		if req.Method == http.MethodPost {
			var row int
			json.Unmarshal([]byte(req.URL.Query().Get("row")), &row)
			src := req.URL.Query().Get("src")
			if src == "" {
				src = "overlay"
			}
			hotkeyPushFrom(row, req.URL.Query().Get("kind"), src)
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
