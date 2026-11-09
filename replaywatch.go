package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Watching a moment of a past game in the game's own replay: the client downloads the replay (.rofl) and opens it,
// and the game's replay API (port 2999, switched on in game.cfg) jumps to the minute. Nothing is recorded while you play.

var (
	rwMu    sync.Mutex
	rwState = map[string]any{"busy": false, "text": ""}
	rwOpen  string // game id of the replay this app opened
)

func rwSet(busy bool, ok bool, text string) {
	rwMu.Lock()
	rwState = map[string]any{"busy": busy, "ok": ok, "text": text, "at": time.Now().Unix()}
	rwMu.Unlock()
	if text != "" {
		logf("Repetició: %s", text)
	}
}

func replayAPIBase() string {
	if b := os.Getenv("LT_REPLAY_BASE"); b != "" {
		return b
	}
	return "https://127.0.0.1:2999"
}

// watching a replay or spectating: the live game API answers too, but it isn't your game (no coach, no recorder)
var (
	phaseMu   sync.Mutex
	phaseAt   time.Time
	phaseLast string
)

func gameflowPhase() string {
	phaseMu.Lock()
	defer phaseMu.Unlock()
	if time.Since(phaseAt) < 4*time.Second {
		return phaseLast
	}
	phaseAt = time.Now()
	var s string
	if code, err := lcuGet("/lol-gameflow/v1/gameflow-phase", &s); err != nil || code != 200 {
		s = ""
	}
	phaseLast = s
	return s
}

func watchingReplay() bool { return gameflowPhase() == "WatchInProgress" }

// enableReplayAPI adds EnableReplayApi=1 under [General] in the game's game.cfg (the game reads it when it starts).
func enableReplayAPI() (bool, error) {
	var dir string
	if code, err := lcuGet("/data-store/v1/install-dir", &dir); err != nil || code != 200 || dir == "" {
		if err == nil {
			err = fmt.Errorf("install-dir %d", code)
		}
		return false, err
	}
	p := filepath.Join(filepath.FromSlash(dir), "Config", "game.cfg")
	b, err := os.ReadFile(p)
	if err != nil {
		return false, err
	}
	s := strings.ReplaceAll(string(b), "\r\n", "\n")
	re := regexp.MustCompile(`(?mi)^EnableReplayApi\s*=\s*(\S*)\s*$`)
	if m := re.FindStringSubmatch(s); m != nil {
		if m[1] == "1" {
			return false, nil
		}
		s = re.ReplaceAllString(s, "EnableReplayApi=1")
	} else if i := strings.Index(s, "[General]"); i >= 0 {
		j := i + len("[General]")
		s = s[:j] + "\nEnableReplayApi=1" + s[j:]
	} else {
		s = "[General]\nEnableReplayApi=1\n" + s
	}
	s = strings.ReplaceAll(s, "\n", "\r\n")
	if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
		return false, err
	}
	logf("Repetició: activada l'API del replay a %s", p)
	return true, nil
}

func replayPlayback(t float64) error {
	raw, _ := json.Marshal(map[string]any{"time": t, "paused": false, "speed": 1.0})
	req, _ := http.NewRequest("POST", replayAPIBase()+"/replay/playback", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	r, err := liveClient.Do(req)
	if err != nil {
		return err
	}
	r.Body.Close()
	if r.StatusCode != 200 {
		return fmt.Errorf("status %d", r.StatusCode)
	}
	return nil
}

func replayLoaded() bool {
	r, err := liveClient.Get(replayAPIBase() + "/replay/playback")
	if err != nil {
		return false
	}
	defer r.Body.Close()
	var pb struct {
		Length float64 `json:"length"`
	}
	json.NewDecoder(r.Body).Decode(&pb)
	return r.StatusCode == 200 && pb.Length > 0
}

func replayWatch(gameID string, t float64) {
	defer func() {
		if e := recover(); e != nil {
			rwSet(false, false, fmt.Sprint("error: ", e))
		}
	}()
	jump := t - 10
	if jump < 0 {
		jump = 0
	}
	// already open (another moment of the same replay): just jump
	if watchingReplay() && rwOpen != gameID {
		rwSet(false, false, "ja tens una altra repetició oberta: tanca-la i torna-ho a provar")
		return
	}
	if watchingReplay() && replayLoaded() {
		if err := replayPlayback(jump); err == nil {
			rwSet(false, true, fmt.Sprintf("al minut %d:%02d", int(jump)/60, int(jump)%60))
			return
		}
	}
	if err := replayOpen(gameID, func(text string) { rwSet(true, true, text) }); err != nil {
		rwSet(false, false, err.Error())
		return
	}
	time.Sleep(1500 * time.Millisecond)
	if err := replayPlayback(jump); err == nil {
		rwSet(false, true, fmt.Sprintf("oberta al minut %d:%02d", int(jump)/60, int(jump)%60))
		return
	}
	rwSet(false, true, "oberta, però no he pogut saltar al minut: fes servir la barra de temps del replay (l'API s'activa la propera vegada que obris el LoL)")
}

// errReplayNoAPI: the replay opened, but its API (game.cfg) wasn't on when the game started
var errReplayNoAPI = fmt.Errorf("la repetició s'ha obert, però l'API del replay encara no estava activada (ho estarà la propera vegada)")

// replayOpen downloads (if needed) and opens a game's replay in the LoL, and waits until it has loaded.
func replayOpen(gameID string, status func(string)) error {
	if _, err := enableReplayAPI(); err != nil {
		logf("Repetició: no he pogut activar l'API del replay (%v)", err)
	}
	var meta struct {
		State string `json:"state"`
	}
	for i := 0; ; i++ {
		code, err := lcuGet("/lol-replays/v1/metadata/"+gameID, &meta)
		if err != nil || code != 200 {
			return fmt.Errorf("no trobo el client del LoL obert")
		}
		switch meta.State {
		case "watch":
		case "download", "lost", "error":
			if i == 0 || meta.State != "download" || i%10 == 0 {
				status("descarregant la repetició…")
				lcuDo("POST", "/lol-replays/v1/rofls/"+gameID+"/download/graceful", map[string]any{"componentType": "replay-button_match-history"}, nil)
			}
		case "checking", "downloading", "":
			status("descarregant la repetició…")
		case "incompatible":
			return fmt.Errorf("la repetició és d'un pedaç anterior i el LoL ja no la pot obrir")
		case "missingOrExpired", "unsupported":
			return fmt.Errorf("Riot ja no té aquesta repetició (només es guarden uns dies)")
		default:
			status("esperant la repetició (" + meta.State + ")…")
		}
		if meta.State == "watch" {
			break
		}
		if i > 90 {
			return fmt.Errorf("la descàrrega triga massa: torna-ho a provar d'aquí una estona")
		}
		time.Sleep(2 * time.Second)
	}
	if code, err := lcuDo("POST", "/lol-replays/v1/rofls/"+gameID+"/watch", map[string]any{"componentType": "replay-button_match-history"}, nil); err != nil || code >= 300 {
		return fmt.Errorf("el client no ha volgut obrir la repetició (%d)", code)
	}
	rwOpen = gameID
	status("obrint el LoL amb la repetició…")
	for i := 0; i < 120; i++ { // the game takes a while to load
		time.Sleep(time.Second)
		if replayLoaded() {
			return nil
		}
	}
	return errReplayNoAPI
}

func replayWatchHandler(send func(http.ResponseWriter, int, string, []byte)) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		if req.Method == http.MethodPost {
			id := req.URL.Query().Get("match")
			if i := strings.LastIndex(id, "_"); i >= 0 {
				id = id[i+1:]
			}
			t, _ := strconv.ParseFloat(req.URL.Query().Get("t"), 64)
			if _, err := strconv.ParseInt(id, 10, 64); err != nil {
				send(w, 400, "application/json", []byte(`{"error":"match"}`))
				return
			}
			rwMu.Lock()
			busy, _ := rwState["busy"].(bool)
			rwMu.Unlock()
			if !busy {
				rwSet(true, true, "preparant la repetició…")
				go replayWatch(id, t)
			}
		}
		rwMu.Lock()
		b, _ := json.Marshal(rwState)
		rwMu.Unlock()
		send(w, 200, "application/json", b)
	}
}

// ddImgHandler: Data Dragon pictures through the app (same origin), so the share picture can be saved with them.
// Only Data Dragon's own paths, kept in memory for the session.
var (
	ddImgMu    sync.Mutex
	ddImgCache = map[string][]byte{}
)

var ddImgPath = regexp.MustCompile(`^cdn/(img/champion/(splash|loading)/[A-Za-z]+_\d+\.jpg|[0-9.]+/img/champion/[A-Za-z]+\.png)$`)

func ddImgHandler(w http.ResponseWriter, req *http.Request) {
	p := req.URL.Query().Get("p")
	if !ddImgPath.MatchString(p) {
		http.NotFound(w, req)
		return
	}
	ddImgMu.Lock()
	b := ddImgCache[p]
	ddImgMu.Unlock()
	if b == nil {
		r, err := riotHTTP.Get(ddBase() + "/" + p)
		if err != nil {
			http.Error(w, err.Error(), 502)
			return
		}
		defer r.Body.Close()
		b, _ = io.ReadAll(io.LimitReader(r.Body, 8<<20))
		if r.StatusCode != 200 {
			http.Error(w, "no image", 502)
			return
		}
		ddImgMu.Lock()
		if len(ddImgCache) > 60 {
			ddImgCache = map[string][]byte{}
		}
		ddImgCache[p] = b
		ddImgMu.Unlock()
	}
	if strings.HasSuffix(p, ".png") {
		w.Header().Set("Content-Type", "image/png")
	} else {
		w.Header().Set("Content-Type", "image/jpeg")
	}
	w.Header().Set("Cache-Control", "max-age=86400")
	w.Write(b)
}
