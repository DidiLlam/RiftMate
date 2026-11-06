package main

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
)

// A sentence the app has no translation for, translated by the AI before the voice reads it (the voice reads
// Spanish or English; a Catalan sentence read by a Spanish voice sounds wrong). Kept in memory, so each sentence
// is translated once.
var (
	trMu    sync.Mutex
	trCache = map[string]string{}
)

func translateHandler(send func(http.ResponseWriter, int, string, []byte)) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		var in struct{ Text, Lang string }
		json.NewDecoder(io.LimitReader(req.Body, 8192)).Decode(&in)
		in.Text = strings.TrimSpace(in.Text)
		lang := map[string]string{"es": "castellà", "en": "anglès"}[in.Lang]
		out := map[string]string{}
		key := in.Lang + "|" + in.Text
		trMu.Lock()
		cached, ok := trCache[key]
		trMu.Unlock()
		switch {
		case in.Text == "" || lang == "":
			out["error"] = "bad_request"
		case ok:
			out["text"] = cached
		default:
			c := loadConfig()
			if c.AIKey == "" {
				out["error"] = "no_key"
				break
			}
			sys := "Tradueix al " + lang + " el text que et donen, que una veu llegirà en una partida de League of Legends. Fes-lo natural i curt, com ho diria un jugador. Mantén tal qual els noms de campions, objectes, runes i encanteris, i els números. Respon només amb la traducció, sense cometes."
			ans, err := geminiAsk(c.AIKey, sys, []map[string]string{{"role": "user", "content": in.Text}})
			if err != nil {
				out["error"] = err.Error()
				break
			}
			ans = strings.Trim(strings.TrimSpace(ans), `"«»`)
			trMu.Lock()
			trCache[key] = ans
			trMu.Unlock()
			out["text"] = ans
		}
		b, _ := json.Marshal(out)
		send(w, 200, "application/json", b)
	}
}
