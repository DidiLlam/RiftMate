package main

import (
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"
)

// The page's settings (browser localStorage) are mirrored to a file, so they survive a change of browser
// (Chrome -> Edge) or of browser profile.
func prefsFile() string { return filepath.Join(filepath.Dir(dataDir()), "preferencies.json") }

// Only the newest open window is active: an older window left open (after an update, say) stops itself
// instead of speaking twice.
var (
	pageMu   sync.Mutex
	pageSeq  int
	pageLast int
	pageBoot = strconv.FormatInt(time.Now().UnixNano(), 36) // changes every time the program starts
)

func prefsHandlers(mux *http.ServeMux, send func(http.ResponseWriter, int, string, []byte)) {
	mux.HandleFunc("/api/prefs", func(w http.ResponseWriter, req *http.Request) {
		if req.Method == http.MethodPost {
			b, err := io.ReadAll(io.LimitReader(req.Body, 2<<20))
			if err == nil && len(b) > 1 && b[0] == '{' {
				os.WriteFile(prefsFile(), b, 0o644)
				if req.URL.Query().Get("migrate") == "1" {
					logf("Ajustos copiats del perfil anterior del navegador (%d bytes)", len(b))
				}
			}
			send(w, 200, "application/json", []byte(`{"ok":true}`))
			return
		}
		b, err := os.ReadFile(prefsFile())
		if err != nil || len(b) == 0 {
			b = []byte(`{}`)
		}
		send(w, 200, "application/json", b)
	})
	mux.HandleFunc("/api/hello", func(w http.ResponseWriter, req *http.Request) {
		pageMu.Lock()
		pageSeq++
		pageLast = pageSeq
		n := pageSeq
		pageMu.Unlock()
		send(w, 200, "application/json", []byte(`{"id":`+strconv.Itoa(n)+`,"boot":"`+pageBoot+`"}`))
	})
	mux.HandleFunc("/api/active", func(w http.ResponseWriter, req *http.Request) {
		pageMu.Lock()
		n := pageLast
		pageMu.Unlock()
		send(w, 200, "application/json", []byte(`{"id":`+strconv.Itoa(n)+`,"boot":"`+pageBoot+`"}`))
	})
}

// waitForFile waits until path exists (or the timeout passes).
func waitForFile(path string, timeout time.Duration) bool {
	end := time.Now().Add(timeout)
	for time.Now().Before(end) {
		if _, err := os.Stat(path); err == nil {
			return true
		}
		time.Sleep(300 * time.Millisecond)
	}
	return false
}
