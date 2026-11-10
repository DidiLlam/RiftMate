package main

import (
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
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
			Lang   string   `json:"lang"`
		}
		json.NewDecoder(io.LimitReader(req.Body, 8192)).Decode(&in)
		whisperSetLang(in.Lang)
		err := voiceStart(in.Champs, in.Lanes)
		msg := `{"ok":true}`
		if err != nil {
			b, _ := json.Marshal(map[string]string{"error": err.Error()})
			msg = string(b)
		}
		send(w, 200, "application/json", []byte(msg))
	})
	mux.HandleFunc("/api/voice/variants", func(w http.ResponseWriter, req *http.Request) {
		all := map[string][]string{}
		for c, v := range voiceVariants {
			all[c] = append([]string{}, v...)
		}
		for c, v := range loadConfig().VoiceAlias {
			all[c] = append(all[c], v...)
		}
		b, _ := json.Marshal(all)
		send(w, 200, "application/json", b)
	})
	// POST {champ, phrase}: a way you say a champion, kept for the Windows recognizer and the page;
	// POST {clear:true} forgets them all. GET lists them.
	mux.HandleFunc("/api/voice/learn", func(w http.ResponseWriter, req *http.Request) {
		if req.Method == http.MethodPost {
			var in struct {
				Champ, Phrase string
				Clear         bool
			}
			json.NewDecoder(io.LimitReader(req.Body, 4096)).Decode(&in)
			c := loadConfig()
			if in.Clear {
				c.VoiceAlias = nil
			} else if ph := strings.Join(strings.Fields(strings.TrimSpace(in.Phrase)), " "); in.Champ != "" && ph != "" && len(ph) <= 40 {
				if c.VoiceAlias == nil {
					c.VoiceAlias = map[string][]string{}
				}
				list := c.VoiceAlias[in.Champ]
				dup := false
				for _, x := range list {
					dup = dup || strings.EqualFold(x, ph)
				}
				if !dup {
					list = append(list, ph)
					if len(list) > 8 {
						list = list[len(list)-8:]
					}
					c.VoiceAlias[in.Champ] = list
					logf("Veu: \"%s\" és %s (après)", ph, in.Champ)
				}
			}
			saveConfig(c)
		}
		b, _ := json.Marshal(loadConfig().VoiceAlias)
		send(w, 200, "application/json", b)
	})
	mux.HandleFunc("/api/voice/stop", func(w http.ResponseWriter, req *http.Request) {
		if req.Method == http.MethodPost {
			voiceStop()
		}
		send(w, 200, "application/json", []byte(`{"ok":true}`))
	})
}
