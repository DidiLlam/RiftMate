package main

import (
	"bufio"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Duo: the spell timers shared with your duo partner (who also has RiftMate). You both type the same code;
// what one marks shows on the other's app and overlay. The messages go through ntfy.sh (a free public relay)
// on a channel named after the code (hashed), and say only: champion, spell, game time it comes back.

type duoCfg struct {
	Code string `json:"code"`
	Me   string `json:"me"` // this install, to skip our own messages
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
)

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

// duoStart: listen on the channel of the current code (one listener at a time)
func duoStart() {
	c := duoLoad()
	duoMu.Lock()
	duoGen++
	gen := duoGen
	duoConn = false
	duoMu.Unlock()
	if len(c.Code) < 4 {
		return
	}
	go func() {
		wait := time.Second
		for {
			duoMu.Lock()
			alive := gen == duoGen
			duoMu.Unlock()
			if !alive {
				return
			}
			err := duoListen(gen, c)
			duoMu.Lock()
			duoConn = false
			duoMu.Unlock()
			if err != nil {
				logf("Duo: connexió perduda (%v), reintento", err)
			}
			time.Sleep(wait)
			if wait < time.Minute {
				wait *= 2
			}
		}
	}()
}

var duoHTTP = &http.Client{} // the listener stays open; no overall timeout

func duoListen(gen int, c duoCfg) error {
	req, _ := http.NewRequest("GET", duoBase()+"/"+duoTopic(c.Code)+"/json", nil)
	r, err := duoHTTP.Do(req)
	if err != nil {
		return err
	}
	defer r.Body.Close()
	if r.StatusCode != 200 {
		return &httpErr{r.StatusCode}
	}
	sc := bufio.NewScanner(r.Body)
	sc.Buffer(make([]byte, 64<<10), 64<<10)
	for sc.Scan() {
		duoMu.Lock()
		if gen != duoGen {
			duoMu.Unlock()
			return nil
		}
		duoConn = true
		duoMu.Unlock()
		var ev struct {
			Event   string `json:"event"`
			Message string `json:"message"`
		}
		if json.Unmarshal(sc.Bytes(), &ev) != nil || ev.Event != "message" {
			continue
		}
		var m map[string]any
		if json.Unmarshal([]byte(ev.Message), &m) != nil {
			continue
		}
		from, _ := m["from"].(string)
		if from == c.Me {
			continue
		}
		duoMu.Lock()
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
	if len(c.Code) < 4 {
		return nil
	}
	msg["from"] = c.Me
	b, _ := json.Marshal(msg)
	cl := &http.Client{Timeout: 8 * time.Second}
	r, err := cl.Post(duoBase()+"/"+duoTopic(c.Code), "text/plain", strings.NewReader(string(b)))
	if err != nil {
		return err
	}
	io.Copy(io.Discard, r.Body)
	r.Body.Close()
	if r.StatusCode != 200 {
		return &httpErr{r.StatusCode}
	}
	return nil
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
		res := map[string]any{"code": duoC.Code, "connected": duoConn, "lines": out, "seq": duoSeq}
		duoMu.Unlock()
		b, _ := json.Marshal(res)
		send(w, 200, "application/json", b)
	}
}
