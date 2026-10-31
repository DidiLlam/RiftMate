package main

import (
	"context"
	"crypto/tls"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

//go:embed page.html
var page []byte

//go:embed map11.png
var mapPNG []byte

//go:embed benvinguda.jpg
var welcomeJPG []byte

const (
	appName = "RiftMate"
	oldName = "LoL Temporitzador" // name of the first versions (data is migrated)
	version = "2.0.8"
	port    = 8765
	liveURL = "https://127.0.0.1:2999/liveclientdata/allgamedata"
)

// The game serves this API on localhost with Riot's own self-signed certificate.
var liveClient = &http.Client{
	Timeout:   1500 * time.Millisecond,
	Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}},
}

func fetchLiveRaw() ([]byte, error) {
	r, err := liveClient.Get(liveURL)
	if err != nil {
		return nil, err
	}
	defer r.Body.Close()
	if r.StatusCode != 200 {
		return nil, fmt.Errorf("status %d", r.StatusCode)
	}
	return io.ReadAll(r.Body)
}

// migrateOldData moves reports, settings and log from the old "LoL Temporitzador" folder.
func migrateOldData() {
	newRoot := filepath.Dir(dataDir())
	oldRoot := filepath.Join(filepath.Dir(newRoot), oldName)
	if _, err := os.Stat(newRoot); err == nil {
		return
	}
	if _, err := os.Stat(oldRoot); err != nil {
		return
	}
	if err := os.Rename(oldRoot, newRoot); err == nil {
		logf("Dades de la versio anterior traslladades de %s", oldRoot)
	}
}

func dataDir() string {
	base := os.Getenv("LOCALAPPDATA")
	if base == "" {
		base, _ = os.UserConfigDir()
	}
	if base == "" {
		base = "."
	}
	return filepath.Join(base, appName, "partides")
}

// ------------------------------------------------------------ recorder

type Recorder struct {
	warnedNoMe bool
	mu         sync.Mutex
	rec        *Record
	lastSeen   time.Time
}

func (r *Recorder) loop() {
	live := false
	for {
		raw, err := fetchLiveRaw()
		if (err == nil) != live {
			live = err == nil
			if live {
				logf("API del joc: disponible (hi ha una partida carregada)")
			} else {
				logf("API del joc: ja no respon (%v)", err)
			}
		}
		if err == nil {
			var g AllGame
			if e := json.Unmarshal(raw, &g); e == nil {
				r.step(&g)
			} else {
				logf("API del joc: resposta no llegible: %v", e)
			}
		} else {
			r.mu.Lock()
			gone := r.rec != nil && time.Since(r.lastSeen) > 8*time.Second
			r.mu.Unlock()
			if gone {
				r.finish("")
			}
		}
		time.Sleep(3 * time.Second)
	}
}

func pidOf(p *Player) string {
	if p.RiotID != "" {
		return p.RiotID
	}
	return p.SummonerName
}

func (r *Recorder) step(g *AllGame) {
	if len(g.AllPlayers) == 0 || g.GameData.GameTime == nil {
		return
	}
	t := *g.GameData.GameTime
	r.mu.Lock()
	r.lastSeen = time.Now()
	if r.rec == nil {
		for _, e := range g.Events.Events {
			if e.EventName == "GameEnd" {
				r.mu.Unlock()
				return // already over when we arrived
			}
		}
		myID := g.ActivePlayer.RiotID
		if myID == "" {
			myID = g.ActivePlayer.SummonerName
		}
		var me *Player
		for i := range g.AllPlayers {
			p := &g.AllPlayers[i]
			if myID != "" && (p.RiotID == myID || p.SummonerName == myID) {
				me = p
			}
		}
		if me == nil {
			if !r.warnedNoMe {
				r.warnedNoMe = true
				logf("Partida detectada pero no trobo el jugador actiu %q entre els %d jugadors (espectador?)", myID, len(g.AllPlayers))
			}
			r.mu.Unlock()
			return // spectating or unknown
		}
		r.warnedNoMe = false
		logf("Enregistrant partida: %s (%s, posicio %q, equip %s), %d jugadors, minut %.1f", me.ChampionName, myID, me.Position, me.Team, len(g.AllPlayers), t/60)
		r.rec = &Record{Date: time.Now().Format("2006-01-02 15:04"), Me: *me}
		go lpAtStart(r.rec.Date, pidOf(me))
	}
	rec := r.rec
	myID := pidOf(&rec.Me)
	pos := strings.ToUpper(rec.Me.Position)
	var me, opp *Player
	for i := range g.AllPlayers {
		p := &g.AllPlayers[i]
		if pidOf(p) == myID {
			me = p
		} else if p.Team != rec.Me.Team && pos != "" && strings.ToUpper(p.Position) == pos {
			opp = p
		}
	}
	if me != nil {
		s := Snap{T: t, CS: me.Scores.CreepScore, Lvl: ip(me.Level)}
		if opp != nil {
			s.OppCS, s.OppLvl = ip(opp.Scores.CreepScore), ip(opp.Level)
		}
		rec.Snaps = append(rec.Snaps, s)
	}
	rec.Players = g.AllPlayers
	rec.Events = g.Events.Events
	rec.Duration = t
	var end *Event
	for i := range g.Events.Events {
		if g.Events.Events[i].EventName == "GameEnd" {
			end = &g.Events.Events[i]
		}
	}
	r.mu.Unlock()
	if end != nil {
		r.finish(end.Result)
	}
}

func (r *Recorder) finish(result string) {
	r.mu.Lock()
	rec := r.rec
	r.rec = nil
	r.mu.Unlock()
	if rec == nil {
		return
	}
	logf("Final de partida (resultat %q, durada %.1f min, %d instantanies, %d esdeveniments)", result, rec.Duration/60, len(rec.Snaps), len(rec.Events))
	if rec.Duration < 180 {
		logf("Partida de menys de 3 minuts: no es guarda")
		return // remakes and very short games are skipped
	}
	switch result {
	case "Win":
		rec.Result = "Victòria"
	case "Lose":
		rec.Result = "Derrota"
	default:
		rec.Result = result
	}
	var report any
	func() {
		defer func() {
			if e := recover(); e != nil {
				report = map[string]any{"error": fmt.Sprint(e), "date": rec.Date}
				logf("ERROR analitzant la partida: %v", e)
			}
		}()
		report = analyse(rec)
	}()
	dir := dataDir()
	os.MkdirAll(dir, 0o755)
	stamp := strings.NewReplacer(":", "", " ", "_").Replace(rec.Date)
	b, _ := json.Marshal(map[string]any{"report": report, "raw": rec, "end_unix": time.Now().Unix()})
	if err := os.WriteFile(filepath.Join(dir, stamp+".json"), b, 0o644); err != nil {
		logf("ERROR guardant l'informe: %v", err)
	} else {
		logf("Informe guardat: %s.json", stamp)
	}
	go autoMap(rec.Date)
	go func() { time.Sleep(4 * time.Minute); seasonSync() }() // the new game into the season stats
	go lpAtEnd(rec.Date, pidOf(&rec.Me))
}

func history() []json.RawMessage {
	out := []json.RawMessage{}
	dir := dataDir()
	cur := currentID()
	entries, err := os.ReadDir(dir)
	if err != nil {
		return out
	}
	names := []string{}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".json") {
			names = append(names, e.Name())
		}
	}
	sort.Sort(sort.Reverse(sort.StringSlice(names)))
	for _, n := range names {
		b, err := os.ReadFile(filepath.Join(dir, n))
		if err != nil {
			continue
		}
		var f struct {
			Report json.RawMessage `json:"report"`
		}
		if !reportIsMine(b, cur) {
			continue
		}
		if json.Unmarshal(b, &f) == nil && len(f.Report) > 0 {
			out = append(out, f.Report)
		}
	}
	return out
}

// archiveHistory moves every saved report to partides/arxiu/<date time>, so the history starts empty.
func archiveHistory() (int, string, error) {
	dir := dataDir()
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0, "", nil
	}
	dest := filepath.Join(dir, "arxiu", time.Now().Format("2006-01-02_1504"))
	n := 0
	cur := currentID()
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		if b, err := os.ReadFile(filepath.Join(dir, e.Name())); err != nil || !reportIsMine(b, cur) {
			continue // only the reports of the account in use
		}
		if err := os.MkdirAll(dest, 0o755); err != nil {
			return n, dest, err
		}
		if err := os.Rename(filepath.Join(dir, e.Name()), filepath.Join(dest, e.Name())); err != nil {
			return n, dest, err
		}
		n++
	}
	return n, dest, nil
}

// ------------------------------------------------------------ server

func serve(ln net.Listener) {
	rec := &Recorder{}
	go rec.loop()
	mux := http.NewServeMux()
	srv := &http.Server{Handler: mux}
	send := func(w http.ResponseWriter, code int, ctype string, body []byte) {
		w.Header().Set("Content-Type", ctype)
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(code)
		w.Write(body)
	}
	mux.HandleFunc("/", func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path != "/" && req.URL.Path != "/index.html" {
			send(w, 404, "text/plain", []byte("not found"))
			return
		}
		send(w, 200, "text/html; charset=utf-8", page)
	})
	mux.HandleFunc("/api/allgamedata", func(w http.ResponseWriter, req *http.Request) {
		if req.Header.Get("X-RiftMate") != "2" { // a window of an older version still open: keep it quiet
			send(w, 410, "text/plain", []byte("old page"))
			return
		}
		b, err := fetchLiveRaw()
		if err != nil {
			send(w, 503, "application/json", []byte(`{"ingame":false}`))
			return
		}
		send(w, 200, "application/json", b)
	})
	mux.HandleFunc("/api/history/reset", func(w http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodPost {
			send(w, 405, "text/plain", []byte("POST only"))
			return
		}
		n, dest, err := archiveHistory()
		out := map[string]any{"moved": n, "folder": dest}
		if err != nil {
			out["error"] = err.Error()
		}
		logf("Historial posat a zero: %d informes moguts a %s (%v)", n, dest, err)
		b, _ := json.Marshal(out)
		send(w, 200, "application/json", b)
	})
	mux.HandleFunc("/api/history", func(w http.ResponseWriter, req *http.Request) {
		b, _ := json.Marshal(history())
		send(w, 200, "application/json; charset=utf-8", b)
	})
	mux.HandleFunc("/api/config", func(w http.ResponseWriter, req *http.Request) {
		if req.Method == http.MethodPost {
			var in struct {
				RiotKey  *string `json:"riot_key"`
				AIKey    *string `json:"ai_key"`
				Region   string  `json:"region"`
				Platform string  `json:"platform"`
			}
			json.NewDecoder(io.LimitReader(req.Body, 4096)).Decode(&in)
			c := loadConfig()
			if in.RiotKey != nil {
				c.RiotKey = strings.TrimSpace(*in.RiotKey)
			}
			if in.AIKey != nil {
				c.AIKey = strings.TrimSpace(*in.AIKey)
			}
			if in.Region != "" {
				c.Region = in.Region
			}
			if in.Platform != "" {
				c.Platform = in.Platform
			}
			saveConfig(c)
			logf("Configuracio desada (clau de Riot: %v, clau d'IA: %v, regio %s)", c.RiotKey != "", c.AIKey != "", c.Region)
		}
		c := loadConfig()
		masked := ""
		if len(c.RiotKey) > 8 {
			masked = c.RiotKey[:5] + "…" + c.RiotKey[len(c.RiotKey)-4:]
		}
		b, _ := json.Marshal(map[string]any{"has_key": c.RiotKey != "", "key_hint": masked, "has_ai_key": c.AIKey != "", "ai_key_hint": keyHint(c.AIKey), "region": c.Region, "platform": platformHost(c)})
		send(w, 200, "application/json", b)
	})
	mux.HandleFunc("/map11.png", func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Cache-Control", "max-age=86400")
		w.Header().Set("Content-Type", "image/png")
		w.Write(mapPNG)
	})
	mux.HandleFunc("/benvinguda.jpg", func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Cache-Control", "max-age=86400")
		w.Header().Set("Content-Type", "image/jpeg")
		w.Write(welcomeJPG)
	})
	mux.HandleFunc("/api/champselect", func(w http.ResponseWriter, req *http.Request) {
		b, _ := json.Marshal(champSelect())
		send(w, 200, "application/json", b)
	})
	mux.HandleFunc("/api/ddragon", func(w http.ResponseWriter, req *http.Request) {
		b, _ := json.Marshal(map[string]string{"version": dataDragonVersion()})
		send(w, 200, "application/json", b)
	})
	prefsHandlers(mux, send)
	mux.HandleFunc("/api/patch", patchHandler(send))
	mux.HandleFunc("/api/rank", rankHandler(send))
	mux.HandleFunc("/api/me", meHandler(send))
	updateHandlers(mux, send, func(exe string) {
		time.Sleep(300 * time.Millisecond)
		voiceStop()
		// the new copy first (it waits for the port): when the server stops, main() returns and this process ends
		if err := startUpdated(exe); err != nil {
			logf("Actualització: no he pogut obrir la versió nova: %v", err)
		}
		srv.Shutdown(context.Background())
		os.Exit(0)
	})
	mux.HandleFunc("/api/emblem", emblemHandler)
	mux.HandleFunc("/api/season", seasonHandler(send))
	mux.HandleFunc("/api/scout", scoutHandler(send))
	aiHandlers(mux, send)
	mux.HandleFunc("/api/ult", func(w http.ResponseWriter, req *http.Request) {
		q := req.URL.Query()
		send(w, 200, "application/json", ultJSON(q.Get("id"), q.Get("lang")))
	})
	mux.HandleFunc("/api/items", func(w http.ResponseWriter, req *http.Request) {
		send(w, 200, "application/json", itemsJSON(req.URL.Query().Get("lang")))
	})
	mux.HandleFunc("/api/fetchmap", func(w http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodPost {
			send(w, 405, "text/plain", []byte("POST only"))
			return
		}
		var in struct {
			Date string `json:"date"`
		}
		json.NewDecoder(io.LimitReader(req.Body, 4096)).Decode(&in)
		status, err := addMap(in.Date)
		logf("Mapa demanat manualment per a %s: %s %v", in.Date, status, err)
		out := map[string]string{"status": status}
		if err != nil {
			out["error"] = err.Error()
		}
		b, _ := json.Marshal(out)
		send(w, 200, "application/json", b)
	})
	mux.HandleFunc("/api/log", func(w http.ResponseWriter, req *http.Request) {
		if req.Method == http.MethodPost {
			b, _ := io.ReadAll(io.LimitReader(req.Body, 2000))
			logf("Pagina: %s", strings.TrimSpace(string(b)))
		}
		send(w, 200, "application/json", []byte(`{"ok":true}`))
	})
	mux.HandleFunc("/api/openlog", func(w http.ResponseWriter, req *http.Request) {
		if req.Method == http.MethodPost {
			openFolder(logPath())
		}
		send(w, 200, "application/json", []byte(`{"ok":true}`))
	})
	voiceHandlers(mux, send)
	mux.HandleFunc("/api/quit", func(w http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodPost {
			send(w, 405, "text/plain", []byte("POST only"))
			return
		}
		send(w, 200, "application/json", []byte(`{"ok":true}`))
		logf("Programa tancat des de la pagina")
		voiceStop()
		go func() {
			time.Sleep(300 * time.Millisecond)
			srv.Shutdown(context.Background())
			os.Exit(0)
		}()
	})
	srv.Serve(ln)
}

func main() {
	migrateOldData() // first, before anything writes to the new folder
	ownerID()        // the account this PC already knew keeps its data where it is
	if handleInstallFlags(os.Args[1:]) {
		return
	}
	url := fmt.Sprintf("http://localhost:%d/", port)
	updated := false
	for _, a := range os.Args[1:] {
		updated = updated || a == "--updated"
	}
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	for i := 0; err != nil && updated && i < 40; i++ { // just updated: the old copy is still closing
		time.Sleep(500 * time.Millisecond)
		ln, err = net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	}
	if err != nil {
		// already running: just show the window again
		logf("Ja estava obert: nomes es mostra la finestra (%v)", err)
		openApp(url)
		return
	}
	logf("---- %s %s iniciat (dades a %s)", appName, version, filepath.Dir(dataDir()))
	go cleanupOldExe()
	if !updated { // after an update the open window reloads itself
		go func() {
			time.Sleep(500 * time.Millisecond)
			openApp(url)
		}()
	}
	go func() {
		time.Sleep(5 * time.Second)
		fixUnknownResults()
		seasonSync() // season win rate per champion: first time reads the whole season, then only new games
	}()
	serve(ln)
}
