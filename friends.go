package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"sync"
	"time"
)

// Friends: your last 7 days of Solo/Duo next to theirs, read from Riot (they don't need the app).
// The same numbers for everyone, all from Riot's match data, so the comparison is fair.

type friendGame struct {
	Win            bool    `json:"win"`
	K              int     `json:"k"`
	D              int     `json:"d"`
	A              int     `json:"a"`
	CS             int     `json:"cs"`
	CS10           float64 `json:"cs10"`
	Min            float64 `json:"min"`
	Vision         int     `json:"vision"`
	Champ          string  `json:"champ"`
	Role           string  `json:"role"`
	Start          int64   `json:"start"`
	Remake         bool    `json:"remake,omitempty"`
	KP             float64 `json:"kp"`
	DmgShare       float64 `json:"dmg"`
	TookFirstBlood bool    `json:"fb,omitempty"`
}

type friendRow struct {
	RiotID string       `json:"riot_id"`
	Me     bool         `json:"me,omitempty"`
	Rank   *leagueEntry `json:"rank,omitempty"`
	Games  []friendGame `json:"games"`
	Error  string       `json:"error,omitempty"`
	At     int64        `json:"at"`
}

type friendsCache struct {
	PUUID map[string]string     `json:"puuid"`   // riot id -> puuid
	Games map[string]friendGame `json:"games"`   // matchID|puuid -> that player's game
	Rows  map[string]friendRow  `json:"rows"`    // riot id -> last result
	List  []string              `json:"friends"` // the friends you added, in order
}

var (
	frMu    sync.Mutex
	frBusy  bool
	frCache *friendsCache
)

func friendsFile() string { return profileFile("amics.json") }

func friendsLoad() *friendsCache {
	if frCache != nil {
		return frCache
	}
	c := &friendsCache{}
	if b, err := os.ReadFile(friendsFile()); err == nil {
		json.Unmarshal(b, c)
	}
	if c.PUUID == nil {
		c.PUUID = map[string]string{}
	}
	if c.Games == nil {
		c.Games = map[string]friendGame{}
	}
	if c.Rows == nil {
		c.Rows = map[string]friendRow{}
	}
	frCache = c
	return c
}

func friendsSave() {
	// keep the file small: games older than 15 days go
	cut := time.Now().Add(-15*24*time.Hour).Unix() * 1000
	for k, g := range frCache.Games {
		if g.Start < cut {
			delete(frCache.Games, k)
		}
	}
	if b, err := json.Marshal(frCache); err == nil {
		os.WriteFile(friendsFile(), b, 0o644)
	}
}

func friendPUUID(c Config, riotID string) (string, error) {
	frMu.Lock()
	p := friendsLoad().PUUID[strings.ToLower(riotID)]
	frMu.Unlock()
	if p != "" {
		return p, nil
	}
	if p := c.PUUIDs[riotID]; p != "" {
		return p, nil
	}
	parts := strings.SplitN(riotID, "#", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", fmt.Errorf("escriu el Riot ID sencer, amb el #")
	}
	var acc struct {
		PUUID    string `json:"puuid"`
		GameName string `json:"gameName"`
		TagLine  string `json:"tagLine"`
	}
	if err := riotGet(c, "/riot/account/v1/accounts/by-riot-id/"+url.PathEscape(parts[0])+"/"+url.PathEscape(parts[1]), &acc); err != nil {
		if strings.Contains(err.Error(), "404") || strings.Contains(err.Error(), "not_found") {
			return "", fmt.Errorf("Riot no troba %s", riotID)
		}
		return "", err
	}
	frMu.Lock()
	friendsLoad().PUUID[strings.ToLower(riotID)] = acc.PUUID
	frMu.Unlock()
	return acc.PUUID, nil
}

type matchV5 struct {
	Info struct {
		GameStartTimestamp int64   `json:"gameStartTimestamp"`
		GameDuration       float64 `json:"gameDuration"`
		Participants       []struct {
			PUUID                       string `json:"puuid"`
			TeamID                      int    `json:"teamId"`
			Win                         bool   `json:"win"`
			Kills                       int    `json:"kills"`
			Deaths                      int    `json:"deaths"`
			Assists                     int    `json:"assists"`
			TotalMinionsKilled          int    `json:"totalMinionsKilled"`
			NeutralMinionsKilled        int    `json:"neutralMinionsKilled"`
			VisionScore                 int    `json:"visionScore"`
			ChampionName                string `json:"championName"`
			TeamPosition                string `json:"teamPosition"`
			TotalDamageDealtToChampions int    `json:"totalDamageDealtToChampions"`
			FirstBloodKill              bool   `json:"firstBloodKill"`
			GameEndedInEarlySurrender   bool   `json:"gameEndedInEarlySurrender"`
			Challenges                  struct {
				LaneMinionsFirst10Minutes float64 `json:"laneMinionsFirst10Minutes"`
				JungleCsBefore10Minutes   float64 `json:"jungleCsBefore10Minutes"`
				KillParticipation         float64 `json:"killParticipation"`
				TeamDamagePercentage      float64 `json:"teamDamagePercentage"`
			} `json:"challenges"`
		} `json:"participants"`
	} `json:"info"`
}

// friendRead: one player's Solo/Duo games of the last 7 days and current rank
func friendRead(c Config, riotID string, me bool) friendRow {
	row := friendRow{RiotID: riotID, Me: me, Games: []friendGame{}, At: time.Now().Unix()}
	pu, err := friendPUUID(c, riotID)
	if err != nil {
		row.Error = err.Error()
		return row
	}
	var list []leagueEntry
	if riotGetHostQ(c, platformHost(c), "/lol/league/v4/entries/by-puuid/"+pu, &list, true) == nil {
		for i := range list {
			if list[i].QueueType == "RANKED_SOLO_5x5" {
				row.Rank = &list[i]
			}
		}
	}
	since := time.Now().Add(-7 * 24 * time.Hour).Unix()
	var ids []string
	if err := riotGetHostQ(c, c.Region, fmt.Sprintf("/lol/match/v5/matches/by-puuid/%s/ids?queue=420&startTime=%d&start=0&count=30", pu, since), &ids, true); err != nil {
		row.Error = "Riot no respon: " + err.Error()
		return row
	}
	for _, id := range ids {
		key := id + "|" + pu
		frMu.Lock()
		g, ok := friendsLoad().Games[key]
		frMu.Unlock()
		if !ok {
			var m matchV5
			if err := riotGetHostQ(c, c.Region, "/lol/match/v5/matches/"+id, &m, true); err != nil {
				continue
			}
			for _, p := range m.Info.Participants {
				if p.PUUID != pu {
					continue
				}
				g = friendGame{Win: p.Win, K: p.Kills, D: p.Deaths, A: p.Assists, CS: p.TotalMinionsKilled + p.NeutralMinionsKilled,
					CS10: p.Challenges.LaneMinionsFirst10Minutes + p.Challenges.JungleCsBefore10Minutes, Min: m.Info.GameDuration / 60,
					Vision: p.VisionScore, Champ: p.ChampionName, Role: p.TeamPosition, Start: m.Info.GameStartTimestamp,
					Remake: m.Info.GameDuration < 300 || p.GameEndedInEarlySurrender, KP: p.Challenges.KillParticipation,
					DmgShare: p.Challenges.TeamDamagePercentage, TookFirstBlood: p.FirstBloodKill}
				ok = true
			}
			if !ok {
				continue
			}
			frMu.Lock()
			friendsLoad().Games[key] = g
			frMu.Unlock()
		}
		if !g.Remake {
			row.Games = append(row.Games, g)
		}
	}
	sort.Slice(row.Games, func(i, j int) bool { return row.Games[i].Start > row.Games[j].Start })
	return row
}

func friendsRefresh(force bool) {
	frMu.Lock()
	if frBusy {
		frMu.Unlock()
		return
	}
	frBusy = true
	fc := friendsLoad()
	who := append([]string{}, fc.List...)
	rows := fc.Rows
	frMu.Unlock()
	defer func() { frMu.Lock(); frBusy = false; friendsSave(); frMu.Unlock() }()
	c := loadConfig()
	if c.RiotKey == "" {
		return
	}
	all := append([]string{c.MyRiotID}, who...)
	for i, id := range all {
		if id == "" {
			continue
		}
		// fresh enough: 30 min, or 5 min for one that failed (a wrong Riot ID isn't asked again on every refresh)
		if r, ok := rows[id]; ok && !force && time.Since(time.Unix(r.At, 0)) < map[bool]time.Duration{true: 5 * time.Minute, false: 30 * time.Minute}[r.Error != ""] {
			continue
		}
		row := friendRead(c, id, i == 0)
		frMu.Lock()
		friendsLoad().Rows[id] = row
		frMu.Unlock()
		logf("Amics: %s, %d partides de Solo/Duo en 7 dies%s", id, len(row.Games), map[bool]string{true: " (" + row.Error + ")", false: ""}[row.Error != ""])
	}
}

func friendsHandler(send func(http.ResponseWriter, int, string, []byte)) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		q := req.URL.Query()
		if req.Method == http.MethodPost {
			frMu.Lock()
			fc := friendsLoad()
			if a := strings.TrimSpace(q.Get("add")); a != "" {
				dup := false
				for _, x := range fc.List {
					dup = dup || strings.EqualFold(x, a)
				}
				if !dup && len(fc.List) < 8 {
					fc.List = append(fc.List, a)
				}
			}
			if d := q.Get("del"); d != "" {
				out := []string{}
				for _, x := range fc.List {
					if !strings.EqualFold(x, d) {
						out = append(out, x)
					}
				}
				fc.List = out
				delete(fc.Rows, d)
			}
			friendsSave()
			frMu.Unlock()
			go friendsRefresh(q.Get("refresh") != "")
		} else {
			go friendsRefresh(false)
		}
		c := loadConfig()
		frMu.Lock()
		fc := friendsLoad()
		out := map[string]any{"busy": frBusy, "friends": fc.List, "me": c.MyRiotID}
		rows := []friendRow{}
		for _, id := range append([]string{c.MyRiotID}, fc.List...) {
			if r, ok := fc.Rows[id]; ok {
				rows = append(rows, r)
			}
		}
		out["rows"] = rows
		b, _ := json.Marshal(out)
		frMu.Unlock()
		send(w, 200, "application/json", b)
	}
}
