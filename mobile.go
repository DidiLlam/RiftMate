package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"strconv"
	"sync"
	"time"
)

// The phone as a second screen: when it's on, a second small server listens on the home network (port 8766) and
// serves only a phone page with the timers (read from the PC's page every second) and the taps on it (they go
// in as if they were the keyboard shortcuts). Every request needs the secret in the address the QR code gives.

type mobileCfg struct {
	On    bool   `json:"on"`
	Token string `json:"token"`
}

var (
	mobMu     sync.Mutex
	mobState  = []byte(`{}`)
	mobLn     net.Listener
	mobPort   = 8766
	mobSeenAt time.Time
)

func mobileFile() string { return profileFile("mobil.json") }

func mobileLoad() mobileCfg {
	var c mobileCfg
	if b, err := os.ReadFile(mobileFile()); err == nil {
		json.Unmarshal(b, &c)
	}
	if c.Token == "" {
		b := make([]byte, 12)
		rand.Read(b)
		c.Token = hex.EncodeToString(b)
		mobileSave(c)
	}
	return c
}

func mobileSave(c mobileCfg) {
	if b, err := json.Marshal(c); err == nil {
		os.WriteFile(mobileFile(), b, 0o600)
	}
}

func lanIPs() []string {
	out := []string{}
	addrs, _ := net.InterfaceAddrs()
	for _, a := range addrs {
		if n, ok := a.(*net.IPNet); ok && n.IP.To4() != nil && n.IP.IsPrivate() && !n.IP.IsLoopback() {
			out = append(out, n.IP.String())
		}
	}
	return out
}

func mobileStart() {
	mobMu.Lock()
	defer mobMu.Unlock()
	if mobLn != nil {
		return
	}
	ln, err := net.Listen("tcp", fmt.Sprintf("0.0.0.0:%d", mobPort))
	if err != nil {
		logf("Mòbil: no puc escoltar al port %d: %v", mobPort, err)
		return
	}
	mobLn = ln
	tok := mobileLoad().Token
	mux := http.NewServeMux()
	ok := func(r *http.Request) bool { return r.URL.Query().Get("t") == tok }
	mux.HandleFunc("/m", func(w http.ResponseWriter, r *http.Request) {
		if !ok(r) {
			http.Error(w, "no", 403)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		w.Write([]byte(mobilePage))
	})
	mux.HandleFunc("/m/state", func(w http.ResponseWriter, r *http.Request) {
		if !ok(r) {
			http.Error(w, "no", 403)
			return
		}
		mobMu.Lock()
		b := mobState
		mobSeenAt = time.Now()
		mobMu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		w.Write(b)
	})
	mux.HandleFunc("/m/tap", func(w http.ResponseWriter, r *http.Request) {
		if !ok(r) || r.Method != http.MethodPost {
			http.Error(w, "no", 403)
			return
		}
		row, _ := strconv.Atoi(r.URL.Query().Get("row"))
		kind := r.URL.Query().Get("kind")
		if row >= 0 && row < 5 && (kind == "flash" || kind == "other") {
			hotkeyPush(row, kind)
		}
		w.WriteHeader(204)
	})
	go http.Serve(ln, mux)
	logf("Mòbil: escoltant a la xarxa de casa (%v, port %d)", lanIPs(), mobPort)
}

func mobileStop() {
	mobMu.Lock()
	defer mobMu.Unlock()
	if mobLn != nil {
		mobLn.Close()
		mobLn = nil
		logf("Mòbil: apagat")
	}
}

// /api/mobile (the PC's page): GET the state of the phone screen; POST ?on=1/0 to switch it; POST ?new=1 a new secret.
// /api/mirror: POST the timers for the phone (JSON body).
func mobileHandler(send func(http.ResponseWriter, int, string, []byte)) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		c := mobileLoad()
		if req.Method == http.MethodPost {
			if v := req.URL.Query().Get("on"); v != "" {
				c.On = v == "1"
			}
			if req.URL.Query().Get("new") != "" {
				b := make([]byte, 12)
				rand.Read(b)
				c.Token = hex.EncodeToString(b)
				mobileStop()
			}
			mobileSave(c)
			if c.On {
				mobileStart()
			} else {
				mobileStop()
			}
		}
		urls := []string{}
		for _, ip := range lanIPs() {
			urls = append(urls, fmt.Sprintf("http://%s:%d/m?t=%s", ip, mobPort, c.Token))
		}
		mobMu.Lock()
		seen := !mobSeenAt.IsZero() && time.Since(mobSeenAt) < 10*time.Second
		running := mobLn != nil
		mobMu.Unlock()
		b, _ := json.Marshal(map[string]any{"on": c.On, "running": running, "urls": urls, "connected": seen})
		send(w, 200, "application/json", b)
	}
}

func mirrorHandler(w http.ResponseWriter, req *http.Request) {
	if req.Method == http.MethodGet { // the overlay reads the same state
		mobMu.Lock()
		b := mobState
		mobMu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		w.Write(b)
		return
	}
	if req.Method != http.MethodPost {
		http.Error(w, "post", 405)
		return
	}
	var v json.RawMessage
	if err := json.NewDecoder(http.MaxBytesReader(w, req.Body, 64<<10)).Decode(&v); err != nil {
		http.Error(w, "json", 400)
		return
	}
	mobMu.Lock()
	mobState = v
	mobMu.Unlock()
	w.WriteHeader(204)
}

func mobileAuto() {
	if mobileLoad().On {
		mobileStart()
	}
}
