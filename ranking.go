package main

// The best plays of everyone who has the app: each app that takes part sends its plays of 90 points or more (the
// data and a light copy of the clip) to a shared channel of ntfy.sh, and keeps on disk everything it hears there.
// ntfy keeps messages 12 hours (and attachments 3 hours), so each app sends its best ones again every few hours,
// and keeps forever what it has already seen: the ranking grows on each computer even if nobody is always on.
// Taking part is the player's choice (off by default): it shows his name and his clips to the other players.

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const rankTopic = "riftmate-millors-jugades-v1-7q4k"

// only the really good ones: the ranking is for the best plays, not every decent one
const rankMin = 90

type rankEntry struct {
	ID    string `json:"id"`
	Name  string `json:"n"`
	Champ string `json:"c"`
	Score int    `json:"s"`
	Title string `json:"t"`
	Why   string `json:"w,omitempty"`
	Kills int    `json:"k,omitempty"`
	At    int64  `json:"at"` // when it was played (unix)
	Video bool   `json:"v,omitempty"`
	Mine  bool   `json:"mine,omitempty"`
	Seen  int64  `json:"seen,omitempty"` // when this app heard it
}

type rankCfg struct {
	On       bool             `json:"on"`
	Name     string           `json:"name,omitempty"`
	Sent     map[string]int64 `json:"sent,omitempty"`  // own play id -> last time sent (data)
	VidSent  map[string]int64 `json:"vsent,omitempty"` // own play id -> last time the clip was sent
	Since    string           `json:"since,omitempty"` // the last message id read from the channel
	LastPoll int64            `json:"last_poll,omitempty"`
}

var (
	bpMu      sync.Mutex
	myRiotNow atomic.Value // the Riot name of the player in the last game (gameName, without the #tag)
)

func rankDir() string     { return filepath.Join(filepath.Dir(dataDir()), "ranking") }
func rankCfgFile() string { return profileFile("ranking.json") }
func rankStoreFile() string {
	return filepath.Join(rankDir(), "ranking.json")
}

func rankLoad() rankCfg {
	var c rankCfg
	if b, err := os.ReadFile(rankCfgFile()); err == nil {
		json.Unmarshal(b, &c)
	}
	if c.Sent == nil {
		c.Sent = map[string]int64{}
	}
	if c.VidSent == nil {
		c.VidSent = map[string]int64{}
	}
	return c
}
func rankSave(c rankCfg) { b, _ := json.Marshal(c); os.WriteFile(rankCfgFile(), b, 0o644) }

func rankStore() map[string]rankEntry {
	m := map[string]rankEntry{}
	if b, err := os.ReadFile(rankStoreFile()); err == nil {
		json.Unmarshal(b, &m)
	}
	return m
}
func rankStoreSave(m map[string]rankEntry) {
	os.MkdirAll(rankDir(), 0o755)
	b, _ := json.Marshal(m)
	os.WriteFile(rankStoreFile(), b, 0o644)
}

func rankName(c rankCfg) string {
	if c.Name != "" {
		return c.Name
	}
	if v, _ := myRiotNow.Load().(string); v != "" {
		return strings.SplitN(v, "#", 2)[0]
	}
	return ""
}

// a play's id, the same on every computer: who, which game, which moment
func rankID(name string, p Play) string {
	s := sha256.Sum256([]byte(name + "|" + p.Stamp + "|" + p.File))
	return hex.EncodeToString(s[:8])
}

func playTime(p Play) int64 {
	if t, err := time.ParseInLocation("2006-01-02_1504", p.Stamp, time.Local); err == nil {
		return t.Add(time.Duration(p.T) * time.Second).Unix()
	}
	return time.Now().Unix()
}

// rankSendOwn: your plays of 60 or more, new ones first; the data again every 3 hours for 14 days (so others who
// were away get it), the clip at most once a day and only for your 3 best
func rankSendOwn() {
	c := rankLoad()
	name := rankName(c)
	if !c.On || name == "" {
		return
	}
	plMu.Lock()
	plays := playsReadLocked()
	plMu.Unlock()
	sort.Slice(plays, func(i, j int) bool { return plays[i].Score > plays[j].Score })
	now := time.Now().Unix()
	store := rankStore()
	top := 0
	for _, p := range plays {
		if p.Score < rankMin || !strings.HasPrefix(p.File, "jugada") {
			continue
		}
		at := playTime(p)
		if now-at > 14*86400 {
			continue
		}
		top++
		id := rankID(name, p)
		e := rankEntry{ID: id, Name: name, Champ: displayName(p.Champ), Score: p.Score, Title: p.Title, Why: p.Why, Kills: p.Kills, At: at}
		if len([]rune(e.Why)) > 300 {
			e.Why = string([]rune(e.Why)[:300])
		}
		sendVid := top <= 3 && now-c.VidSent[id] > 20*3600
		if !sendVid && now-c.Sent[id] < 3*3600 {
			continue
		}
		var vid string
		if sendVid {
			src := filepath.Join(playsRoot(), p.Stamp, p.File)
			vid = filepath.Join(os.TempDir(), "riftmate-rank-"+id+".mp4")
			if err := rankPreview(src, vid); err != nil {
				logf("Rànquing: no he pogut preparar el clip %s: %v", p.File, err)
				vid = ""
			}
		}
		e.Video = vid != "" || store[id].Video
		if err := rankPublish(e, vid); err != nil {
			logf("Rànquing: no he pogut enviar la jugada: %v", err)
			return
		}
		if vid != "" {
			os.MkdirAll(rankDir(), 0o755)
			os.Rename(vid, filepath.Join(rankDir(), id+".mp4"))
			c.VidSent[id] = now
		}
		c.Sent[id] = now
		e.Mine, e.Seen = true, now
		store[id] = e
		time.Sleep(2 * time.Second)
	}
	rankSave(c)
	rankStoreSave(store)
}

func rankPublish(e rankEntry, video string) error {
	b, _ := json.Marshal(e)
	var req *http.Request
	if video != "" {
		f, err := os.Open(video)
		if err != nil {
			return err
		}
		defer f.Close()
		// the text goes in the address (ntfy reads "message" there): headers can't carry accents safely
		req, _ = http.NewRequest("PUT", duoBase()+"/"+rankTopic+"?filename="+e.ID+".mp4&message="+url.QueryEscape(string(b)), f)
	} else {
		req, _ = http.NewRequest("POST", duoBase()+"/"+rankTopic, strings.NewReader(string(b)))
	}
	cl := &http.Client{Timeout: 2 * time.Minute}
	r, err := cl.Do(req)
	if err != nil {
		return err
	}
	defer r.Body.Close()
	if r.StatusCode != 200 {
		x, _ := io.ReadAll(io.LimitReader(r.Body, 300))
		return fmt.Errorf("HTTP %d %s", r.StatusCode, strings.TrimSpace(string(x)))
	}
	return nil
}

// rankPoll: everything new on the channel, kept; the clips downloaded while they're there (3 hours)
func rankPoll() {
	c := rankLoad()
	since := c.Since
	if since == "" {
		since = "all"
	}
	cl := &http.Client{Timeout: 30 * time.Second}
	r, err := cl.Get(duoBase() + "/" + rankTopic + "/json?poll=1&since=" + since)
	if err != nil {
		return
	}
	body, _ := io.ReadAll(io.LimitReader(r.Body, 8<<20))
	r.Body.Close()
	store := rankStore()
	n := 0
	for _, line := range strings.Split(string(body), "\n") {
		var m struct {
			ID         string `json:"id"`
			Event      string `json:"event"`
			Message    string `json:"message"`
			Attachment *struct {
				URL  string `json:"url"`
				Size int64  `json:"size"`
			} `json:"attachment"`
		}
		if json.Unmarshal([]byte(line), &m) != nil || m.Event != "message" {
			continue
		}
		c.Since = m.ID
		var e rankEntry
		if json.Unmarshal([]byte(m.Message), &e) != nil || e.ID == "" || len(e.ID) > 32 || e.Score < rankMin || e.Score > 100 || e.Name == "" {
			continue
		}
		old, had := store[e.ID]
		e.Mine, e.Seen = old.Mine, time.Now().Unix()
		if had {
			e.Seen = old.Seen
		}
		file := filepath.Join(rankDir(), e.ID+".mp4")
		if _, err := os.Stat(file); err == nil {
			e.Video = true
		} else if m.Attachment != nil && m.Attachment.Size < 16<<20 && strings.HasPrefix(m.Attachment.URL, duoBase()+"/file/") {
			if rankDownload(m.Attachment.URL, file) == nil {
				e.Video = true
			}
		} else {
			e.Video = false
		}
		store[e.ID] = e
		if !had {
			n++
		}
	}
	// at most 400 plays and 600 MB of clips: the oldest and lowest go first
	if len(store) > 400 {
		list := make([]rankEntry, 0, len(store))
		for _, e := range store {
			list = append(list, e)
		}
		sort.Slice(list, func(i, j int) bool {
			return list[i].Score*1000-int(time.Since(time.Unix(list[i].At, 0)).Hours()) > list[j].Score*1000-int(time.Since(time.Unix(list[j].At, 0)).Hours())
		})
		for _, e := range list[400:] {
			delete(store, e.ID)
			os.Remove(filepath.Join(rankDir(), e.ID+".mp4"))
		}
	}
	c.LastPoll = time.Now().Unix()
	rankSave(c)
	rankStoreSave(store)
	if n > 0 {
		logf("Rànquing: %d jugades noves d'altres jugadors", n)
	}
}

func rankDownload(url, file string) error {
	os.MkdirAll(rankDir(), 0o755)
	cl := &http.Client{Timeout: 3 * time.Minute}
	r, err := cl.Get(url)
	if err != nil {
		return err
	}
	defer r.Body.Close()
	if r.StatusCode != 200 {
		return fmt.Errorf("HTTP %d", r.StatusCode)
	}
	tmp := file + ".part"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	_, err = io.Copy(f, io.LimitReader(r.Body, 16<<20))
	f.Close()
	if err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, file)
}

func rankLoop() {
	time.Sleep(2 * time.Minute)
	for {
		if !liveNow.Load() {
			bpMu.Lock()
			rankPoll()
			rankSendOwn()
			bpMu.Unlock()
		}
		time.Sleep(10 * time.Minute)
	}
}

// /api/ranking: GET the list (best first) and the setting; POST ?on=0|1&name=… ; /api/rankfile?id= a clip
func bpHandler(w http.ResponseWriter, req *http.Request) {
	q := req.URL.Query()
	if req.Method == http.MethodPost {
		bpMu.Lock()
		c := rankLoad()
		if v := q.Get("on"); v != "" {
			c.On = v == "1"
		}
		if q.Has("name") {
			c.Name = strings.TrimSpace(q.Get("name"))
			if len([]rune(c.Name)) > 24 {
				c.Name = string([]rune(c.Name)[:24])
			}
		}
		rankSave(c)
		bpMu.Unlock()
		if q.Get("now") == "1" || q.Get("on") == "1" {
			go func() { bpMu.Lock(); rankPoll(); rankSendOwn(); bpMu.Unlock() }()
		}
	}
	c := rankLoad()
	store := rankStore()
	list := make([]rankEntry, 0, len(store))
	for _, e := range store {
		if e.Score >= rankMin { // ones kept from when the bar was lower don't show
			list = append(list, e)
		}
	}
	sort.Slice(list, func(i, j int) bool {
		if list[i].Score != list[j].Score {
			return list[i].Score > list[j].Score
		}
		return list[i].At > list[j].At
	})
	b, _ := json.Marshal(map[string]any{"on": c.On, "name": rankName(c), "custom": c.Name != "", "last_poll": c.LastPoll, "plays": list, "supported": playsSupported})
	w.Header().Set("Content-Type", "application/json")
	w.Write(b)
}

func rankFileHandler(w http.ResponseWriter, req *http.Request) {
	id := req.URL.Query().Get("id")
	if len(id) != 16 || strings.Trim(id, "0123456789abcdef") != "" {
		http.NotFound(w, req)
		return
	}
	http.ServeFile(w, req, filepath.Join(rankDir(), id+".mp4"))
}
