package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
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
	Ranks    bool   `json:"ranks,omitempty"` // the board has every player's rank (read when the game is opened)
}

var (
	histMu    sync.Mutex
	histFiles = map[string]map[string]histMatch{}
)

func histFile() string { return viewFile("historial.json") }

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
	id := viewID()
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
	return matchesByIDs(c, id, puuid, ids, nil)
}

// matchesByIDs: these games as history cards (the ones not read yet, from Riot); extra goes into the answer
func matchesByIDs(c Config, id, puuid string, ids []string, extra map[string]any) ([]byte, error) {
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
	res := map[string]any{"matches": out, "riot_id": id, "platform": platformHost(c)}
	for k, v := range extra {
		res[k] = v
	}
	return json.Marshal(res)
}

// champMatchesJSON: the season's games with one champion (and, if given, against one lane opponent), newest
// first, from the season already read: /api/matches?champ=Leblanc&vs=Sylas&queue=420&start=0&count=10
func champMatchesJSON(champ, vs string, queue, start, count int) ([]byte, error) {
	c := loadConfig()
	id := viewID()
	if c.RiotKey == "" {
		return nil, fmt.Errorf("no_key")
	}
	puuid, err := puuidFor(&c, id)
	if err != nil {
		return nil, err
	}
	sc := loadSeason()
	type gm struct {
		id  string
		end int64
	}
	list := []gm{}
	for mid, g := range sc.Games {
		if g.Remake || !strings.EqualFold(g.Champ, champ) || (queue != 0 && g.Queue != queue) || (vs != "" && !strings.EqualFold(g.Opp, vs)) {
			continue
		}
		list = append(list, gm{mid, g.End})
	}
	sort.Slice(list, func(i, j int) bool { return list[i].end > list[j].end })
	total := len(list)
	ids := []string{}
	for i := start; i < total && i < start+count; i++ {
		ids = append(ids, list[i].id)
	}
	return matchesByIDs(c, id, puuid, ids, map[string]any{"total": total, "start": start})
}

func matchesHandler(send func(http.ResponseWriter, int, string, []byte)) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		start, _ := strconv.Atoi(req.URL.Query().Get("start"))
		count, _ := strconv.Atoi(req.URL.Query().Get("count"))
		if count <= 0 || count > 20 {
			count = 10
		}
		var b []byte
		var err error
		if ch := req.URL.Query().Get("champ"); ch != "" {
			q, _ := strconv.Atoi(req.URL.Query().Get("queue"))
			b, err = champMatchesJSON(ch, req.URL.Query().Get("vs"), q, start, count)
		} else {
			b, err = matchesJSON(start, count)
		}
		if err != nil {
			b, _ = json.Marshal(map[string]string{"error": err.Error()})
		}
		send(w, 200, "application/json", b)
	}
}

// ranksForMatch: the Solo/Duo rank of the ten players of one game of the history (11 calls), read when you open it,
// and kept with the game. It's their rank now, not on the day of the game.
func ranksForMatch(mid string) ([]byte, error) {
	c := loadConfig()
	puuid, err := puuidFor(&c, viewID())
	if err != nil {
		return nil, err
	}
	var m matchDTO
	if err := riotGetQuiet(c, "/lol/match/v5/matches/"+mid, &m); err != nil {
		return nil, err
	}
	me := -1
	for _, p := range m.Info.Participants {
		if p.PUUID == puuid {
			me = p.ParticipantID
		}
	}
	histMu.Lock()
	defer histMu.Unlock()
	cache := loadHist()
	h, ok := cache[mid]
	if !ok {
		return nil, fmt.Errorf("not_found")
	}
	h.Board, h.Ranks = makeBoardOpt(c, &m, me, true), true
	cache[mid] = h
	if b, err := json.Marshal(cache); err == nil {
		os.WriteFile(histFile(), b, 0o644)
	}
	h.QueueTxt = queueName[h.Queue]
	return json.Marshal(h)
}

func matchRanksHandler(send func(http.ResponseWriter, int, string, []byte)) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		b, err := ranksForMatch(req.URL.Query().Get("id"))
		if err != nil {
			b, _ = json.Marshal(map[string]string{"error": err.Error()})
		}
		send(w, 200, "application/json", b)
	}
}
