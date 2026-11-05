package main

import (
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"sync"
)

// Voice lines produced by the native recognizer, read by the page with /api/voice?since=N.
type voiceLine struct {
	Seq  int    `json:"seq"`
	Kind string `json:"kind"` // heard, ready, error, stopped
	Text string `json:"text"`
}

var (
	voiceMu    sync.Mutex
	voiceLines []voiceLine
	voiceSeq   int
)

func voicePush(kind, text string) {
	voiceMu.Lock()
	defer voiceMu.Unlock()
	voiceSeq++
	switch kind {
	case "ready":
		setPart("voice", true, text)
	case "error":
		setPart("voice", false, text)
	}
	voiceLines = append(voiceLines, voiceLine{voiceSeq, kind, text})
	if len(voiceLines) > 200 {
		voiceLines = voiceLines[len(voiceLines)-200:]
	}
}

func voiceHandlers(mux *http.ServeMux, send func(http.ResponseWriter, int, string, []byte)) {
	mux.HandleFunc("/api/voice", func(w http.ResponseWriter, req *http.Request) {
		if req.Header.Get("X-RiftMate") != "2" { // a window of an older version still open
			send(w, 410, "text/plain", []byte("old page"))
			return
		}
		since, _ := strconv.Atoi(req.URL.Query().Get("since"))
		voiceMu.Lock()
		out := []voiceLine{}
		for _, l := range voiceLines {
			if l.Seq > since {
				out = append(out, l)
			}
		}
		voiceMu.Unlock()
		b, _ := json.Marshal(map[string]any{"supported": voiceSupported, "running": voiceRunning(), "lines": out})
		send(w, 200, "application/json", b)
	})
	mux.HandleFunc("/api/voice/start", func(w http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodPost {
			send(w, 405, "text/plain", []byte("POST only"))
			return
		}
		var in struct {
			Champs []string `json:"champs"`
			Lanes  bool     `json:"lanes"` // also listen for "top Darius" (no game detected yet)
		}
		json.NewDecoder(io.LimitReader(req.Body, 8192)).Decode(&in)
		err := voiceStart(in.Champs, in.Lanes)
		msg := `{"ok":true}`
		if err != nil {
			b, _ := json.Marshal(map[string]string{"error": err.Error()})
			msg = string(b)
		}
		send(w, 200, "application/json", []byte(msg))
	})
	mux.HandleFunc("/api/voice/variants", func(w http.ResponseWriter, req *http.Request) {
		b, _ := json.Marshal(voiceVariants)
		send(w, 200, "application/json", b)
	})
	mux.HandleFunc("/api/voice/stop", func(w http.ResponseWriter, req *http.Request) {
		if req.Method == http.MethodPost {
			voiceStop()
		}
		send(w, 200, "application/json", []byte(`{"ok":true}`))
	})
}
