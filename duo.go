package main

import (
	"bufio"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Duo: the spell timers shared with your teammates who also have RiftMate. Automatically: everyone on your team
// in this game listens on a channel named after the game and the team (hashed), so your duo needs nothing set up.
// Optionally also a code (to share outside that). The messages go through ntfy.sh (a free public relay) and say
// only: champion, spell, game time it comes back.

type duoCfg struct {
	Code   string `json:"code"`
	Me     string `json:"me"`                // this install, to skip our own messages
	NoAuto bool   `json:"no_auto,omitempty"` // don't share with teammates automatically
}

type duoLine struct {
	Seq  int             `json:"seq"`
	Msg  json.RawMessage `json:"msg"`
	From string          `json:"-"`
}

var (
	duoMu     sync.Mutex
	duoC      duoCfg
	duoRead   bool
	duoLines  []duoLine
	duoSeq    int
	duoConn   bool
	duoGen    int // bumped when the code changes: the old listener stops
	duoLastIn time.Time
	duoGameCh string                // the channel of this game and team ("" outside a game)
	duoSeen   = map[string]bool{}   // message ids already taken (the same one can come by both channels)
	curTeam   atomic.Value          // "ORDER" / "CHAOS" of the game being played
	duoGameAt time.Time             // when this game's channel started (to read what teammates sent before we connected)
	duoLastID = map[string]string{} // the last message id read on each channel: after a cut, carry on from there
)

// duoGameStart: called when the game id is known (and with 0 at the end): listen on this game's team channel
func duoGameStart(gameID int64) {
	t, _ := curTeam.Load().(string)
	ch := ""
	if gameID > 0 && t != "" && !duoLoad().NoAuto {
		h := sha256.Sum256([]byte(fmt.Sprintf("riftmate-game:%d:%s", gameID, t)))
		ch = "riftmate-" + hex.EncodeToString(h[:])[:24]
	}
	duoMu.Lock()
	same := ch == duoGameCh
	duoGameCh = ch
	if !same {
		duoGameAt = time.Now()
	}
	duoMu.Unlock()
	if !same {
		if ch != "" {
			logf("Duo: canal de l'equip d'aquesta partida actiu")
		}
		duoStart()
	}
}

// the channels in use now: this game's team and, if there is one, the code's
func duoChannels(c duoCfg) []string {
	duoMu.Lock()
	defer duoMu.Unlock()
	out := []string{}
	if duoGameCh != "" {
		out = append(out, duoGameCh)
	}
	if len(c.Code) >= 4 {
		out = append(out, duoTopic(c.Code))
	}
	return out
}

func duoBase() string {
	if b := os.Getenv("LT_DUO_BASE"); b != "" {
		return strings.TrimRight(b, "/")
	}
	return "https://ntfy.sh"
}

func duoFile() string { return profileFile("duo.json") }

func duoLoad() duoCfg {
	duoMu.Lock()
	defer duoMu.Unlock()
	if !duoRead {
		duoRead = true
		if b, err := os.ReadFile(duoFile()); err == nil {
			json.Unmarshal(b, &duoC)
		}
		if duoC.Me == "" {
			b := make([]byte, 6)
			rand.Read(b)
			duoC.Me = hex.EncodeToString(b)
			duoSaveLocked()
		}
	}
	return duoC
}

func duoSaveLocked() {
	if b, err := json.Marshal(duoC); err == nil {
		os.WriteFile(duoFile(), b, 0o644)
	}
}

func duoTopic(code string) string {
	h := sha256.Sum256([]byte("riftmate-duo:" + strings.ToUpper(code)))
	return "riftmate-" + hex.EncodeToString(h[:])[:24]
}

func duoCleanCode(s string) string {
	var b strings.Builder
	for _, r := range strings.ToUpper(s) {
		if (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		}
	}
	if b.Len() > 12 {
		return b.String()[:12]
	}
	return b.String()
}

func duoNewCode() string {
	const abc = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"
	b := make([]byte, 6)
	rand.Read(b)
	for i := range b {
		b[i] = abc[int(b[i])%len(abc)]
	}
	return string(b)
}

// duoStart: (re)listen on the channels in use (one listener each; older listeners stop)
func duoStart() {
	c := duoLoad()
	duoMu.Lock()
	duoGen++
	gen := duoGen
	duoConn = false
	duoMu.Unlock()
	for _, ch := range duoChannels(c) {
		go func(ch string) {
			wait := time.Second
			for {
				duoMu.Lock()
				alive := gen == duoGen
				duoMu.Unlock()
				if !alive {
					return
				}
				t0 := time.Now()
				err := duoListen(gen, c, ch)
				duoMu.Lock()
				duoConn = false
				duoMu.Unlock()
				if err != nil {
					logf("Duo: connexió perduda (%v), reconnecto", err)
				}
				if time.Since(t0) > 30*time.Second {
					wait = time.Second
				}
				time.Sleep(wait)
				if wait < 15*time.Second {
					wait *= 2
				}
			}
		}(ch)
	}
}

var duoHTTP = &http.Client{} // the listener stays open; no overall timeout

func duoListen(gen int, c duoCfg, ch string) error {
	// read from where we left off (a message sent while the connection was down isn't lost); on the game's channel,
	// from the start of the game (a teammate whose app connected sooner may have marked something already)
	duoMu.Lock()
	since := duoLastID[ch]
	if since == "" && ch == duoGameCh && !duoGameAt.IsZero() {
		since = strconv.FormatInt(duoGameAt.Add(-2*time.Minute).Unix(), 10)
	}
	duoMu.Unlock()
	u := duoBase() + "/" + ch + "/json"
	if since != "" {
		u += "?since=" + since
	}
	req, _ := http.NewRequest("GET", u, nil)
	r, err := duoHTTP.Do(req)
	if err != nil {
		return err
	}
	defer r.Body.Close()
	if r.StatusCode != 200 {
		return &httpErr{r.StatusCode}
	}
	// ntfy sends a keepalive every 45 s: two minutes of nothing means the connection died without saying so
	alive := make(chan struct{}, 1)
	done := make(chan struct{})
	defer close(done)
	go func() {
		t := time.NewTimer(2 * time.Minute)
		defer t.Stop()
		for {
			select {
			case <-done:
				return
			case <-alive:
				if !t.Stop() {
					<-t.C
				}
				t.Reset(2 * time.Minute)
			case <-t.C:
				logf("Duo: el canal no respon fa 2 minuts, reconnecto")
				r.Body.Close()
				return
			}
		}
	}()
	sc := bufio.NewScanner(r.Body)
	sc.Buffer(make([]byte, 64<<10), 64<<10)
	for sc.Scan() {
		select {
		case alive <- struct{}{}:
		default:
		}
		duoMu.Lock()
		if gen != duoGen {
			duoMu.Unlock()
			return nil
		}
		duoConn = true
		duoMu.Unlock()
		var ev struct {
			ID      string `json:"id"`
			Event   string `json:"event"`
			Message string `json:"message"`
		}
		if json.Unmarshal(sc.Bytes(), &ev) != nil || ev.Event != "message" {
			continue
		}
		if ev.ID != "" {
			duoMu.Lock()
			duoLastID[ch] = ev.ID
			duoMu.Unlock()
		}
		var m map[string]any
		if json.Unmarshal([]byte(ev.Message), &m) != nil {
			continue
		}
		from, _ := m["from"].(string)
		mid, _ := m["mid"].(string)
		if from == c.Me {
			continue
		}
		duoMu.Lock()
		if mid != "" {
			if duoSeen[mid] {
				duoMu.Unlock()
				continue
			}
			if len(duoSeen) > 500 {
				duoSeen = map[string]bool{}
			}
			duoSeen[mid] = true
		}
		duoSeq++
		duoLines = append(duoLines, duoLine{Seq: duoSeq, Msg: json.RawMessage(ev.Message), From: from})
		if len(duoLines) > 100 {
			duoLines = duoLines[len(duoLines)-100:]
		}
		duoLastIn = time.Now()
		duoMu.Unlock()
		logf("Duo: rebut %s", ev.Message)
	}
	return sc.Err()
}

type httpErr struct{ code int }

func (e *httpErr) Error() string { return "HTTP " + strconv.Itoa(e.code) }

func duoSend(msg map[string]any) error {
	c := duoLoad()
	chs := duoChannels(c)
	if len(chs) == 0 {
		return nil
	}
	id := make([]byte, 6)
	rand.Read(id)
	msg["from"], msg["mid"] = c.Me, hex.EncodeToString(id)
	b, _ := json.Marshal(msg)
	cl := &http.Client{Timeout: 8 * time.Second}
	var last error
	for _, ch := range chs {
		r, err := cl.Post(duoBase()+"/"+ch, "text/plain", strings.NewReader(string(b)))
		if err != nil {
			last = err
			continue
		}
		io.Copy(io.Discard, r.Body)
		r.Body.Close()
		if r.StatusCode != 200 {
			last = &httpErr{r.StatusCode}
		}
	}
	if last == nil {
		logf("Duo: enviat %v %v (torna al segon %v)", msg["spell"], msg["champ"], msg["back"])
	}
	return last
}

// /api/duo: GET ?since=N (state and what your duo marked); POST ?code=X (join), ?new=1 (make a code), ?off=1;
// POST /api/duo/send with the timer as JSON body.
func duoHandler(send func(http.ResponseWriter, int, string, []byte)) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		if strings.HasSuffix(req.URL.Path, "/send") {
			if req.Method != http.MethodPost {
				send(w, 405, "text/plain", []byte("POST only"))
				return
			}
			var m map[string]any
			if err := json.NewDecoder(io.LimitReader(req.Body, 4096)).Decode(&m); err != nil {
				send(w, 400, "text/plain", []byte("bad json"))
				return
			}
			go func() {
				if err := duoSend(m); err != nil {
					logf("Duo: no s'ha pogut enviar (%v)", err)
				}
			}()
			send(w, 200, "application/json", []byte(`{"ok":true}`))
			return
		}
		duoLoad()
		if req.Method == http.MethodPost {
			q := req.URL.Query()
			duoMu.Lock()
			switch {
			case q.Get("off") == "1":
				duoC.Code = ""
			case q.Get("new") == "1":
				duoC.Code = duoNewCode()
			case q.Get("code") != "":
				duoC.Code = duoCleanCode(q.Get("code"))
			case q.Get("auto") != "":
				duoC.NoAuto = q.Get("auto") != "1"
			}
			duoSaveLocked()
			code := duoC.Code
			duoMu.Unlock()
			logf("Duo: codi %q", code)
			duoStart()
		}
		since, _ := strconv.Atoi(req.URL.Query().Get("since"))
		duoMu.Lock()
		out := []duoLine{}
		for _, l := range duoLines {
			if l.Seq > since {
				out = append(out, l)
			}
		}
		res := map[string]any{"code": duoC.Code, "auto": !duoC.NoAuto, "in_game": duoGameCh != "", "connected": duoConn, "lines": out, "seq": duoSeq}
		duoMu.Unlock()
		b, _ := json.Marshal(res)
		send(w, 200, "application/json", b)
	}
}
