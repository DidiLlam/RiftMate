package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"sort"
	"strconv"
	"sync"
)

// Match history tab: the account's latest games from Riot (every queue), each as a scoreboard. Games already
// read are kept on disk (one file per account), so only new ones cost requests.
type histMatch struct {
	ID       string `json:"id"`
	Queue    int    `json:"queue"`
	QueueTxt string `json:"queue_txt"`
	End      int64  `json:"end"`      // unix seconds
	Duration int64  `json:"duration"` // seconds
	Board    *Board `json:"board"`
}

var (
	histMu    sync.Mutex
	histFiles = map[string]map[string]histMatch{}
)

func histFile() string { return profileFile("historial.json") }

func loadHist() map[string]histMatch {
	f := histFile()
	if m, ok := histFiles[f]; ok {
		return m
	}
	m := map[string]histMatch{}
	if b, err := os.ReadFile(f); err == nil {
		json.Unmarshal(b, &m)
	}
	histFiles[f] = m
	return m
}

func matchesJSON(start, count int) ([]byte, error) {
	c := loadConfig()
	id := myRiotID()
	if c.RiotKey == "" {
		return nil, fmt.Errorf("no_key")
	}
	if id == "" {
		return nil, fmt.Errorf("no_riot_id")
	}
	puuid, err := puuidFor(&c, id)
	if err != nil {
		return nil, err
	}
	var ids []string
	if err := riotGet(c, fmt.Sprintf("/lol/match/v5/matches/by-puuid/%s/ids?type=ranked&start=%d&count=%d", puuid, start, count), &ids); err != nil {
		return nil, err
	}
	histMu.Lock()
	defer histMu.Unlock()
	cache := loadHist()
	changed := false
	out := []histMatch{}
	for _, mid := range ids {
		h, ok := cache[mid]
		if !ok {
			var m matchDTO
			if err := riotGetQuiet(c, "/lol/match/v5/matches/"+mid, &m); err != nil {
				logf("Historial: no he pogut llegir %s: %v", mid, err)
				continue
			}
			me := -1
			for _, p := range m.Info.Participants {
				if p.PUUID == puuid {
					me = p.ParticipantID
				}
			}
			h = histMatch{ID: mid, Queue: m.Info.QueueID, End: m.Info.GameEndTimestamp / 1000, Duration: m.Info.GameDuration, Board: makeBoardOpt(c, &m, me, false)}
			cache[mid] = h
			changed = true
		}
		if h.Queue != 420 && h.Queue != 440 { // only ranked (games cached by an earlier version may be others)
			continue
		}
		h.QueueTxt = queueName[h.Queue]
		if h.QueueTxt == "" {
			h.QueueTxt = "Altres"
		}
		out = append(out, h)
	}
	if changed {
		if b, err := json.Marshal(cache); err == nil {
			os.WriteFile(histFile(), b, 0o644)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].End > out[j].End })
	return json.Marshal(map[string]any{"matches": out, "riot_id": id, "platform": platformHost(c)})
}

func matchesHandler(send func(http.ResponseWriter, int, string, []byte)) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		start, _ := strconv.Atoi(req.URL.Query().Get("start"))
		count, _ := strconv.Atoi(req.URL.Query().Get("count"))
		if count <= 0 || count > 20 {
			count = 10
		}
		b, err := matchesJSON(start, count)
		if err != nil {
			b, _ = json.Marshal(map[string]string{"error": err.Error()})
		}
		send(w, 200, "application/json", b)
	}
}
