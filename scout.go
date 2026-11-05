package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// Loading-screen scouting: for each rival, the public Riot data any stats site shows — rank and win rate,
// mastery with the champion they picked and their last ranked games — so the page can warn about one-tricks,
// players on a winning streak or first-timers. Names are only used to look the data up, never shown or spoken.

type scoutIn struct {
	RiotID string `json:"riotId"`
	Champ  string `json:"champ"` // Data Dragon id ("Ahri", "MonkeyKing")
	Pos    string `json:"pos"`
	Ally   bool   `json:"ally,omitempty"` // a player of my own team
}

type scoutOut struct {
	Champ      string      `json:"champ"`
	Pos        string      `json:"pos"`
	Ally       bool        `json:"ally,omitempty"`
	RiotID     string      `json:"riotId,omitempty"`
	Tier       string      `json:"tier,omitempty"`
	Rank       string      `json:"rank,omitempty"`
	LP         int         `json:"lp"`
	Wins       int         `json:"wins"`
	Losses     int         `json:"losses"`
	Hot        bool        `json:"hot"`
	Mastery    int         `json:"mastery"`
	MasteryLvl int         `json:"masteryLvl"`
	MasteryOK  bool        `json:"masteryOk"`
	Recent     int         `json:"recent"`     // last ranked games looked at
	ChampGames int         `json:"champGames"` // of those, with this champion
	ChampWins  int         `json:"champWins"`
	LastPlay   int64       `json:"lastPlay"`
	Games      []scoutGame `json:"games"`
	Premade    []string    `json:"premade,omitempty"` // champions (Data Dragon ids) of this game who played their last games with him
	WithMe     int         `json:"withMe,omitempty"`  // of his last games, how many with you on his team
	Error      string      `json:"error,omitempty"`
	puuid      string
	mates      map[string]int // puuid -> games on his team, in his last games
}

type scoutGame struct {
	Champ string `json:"c"`
	Win   bool   `json:"w"`
	K     int    `json:"k"`
	D     int    `json:"d"`
	A     int    `json:"a"`
}

// Riot allows a personal key 20 calls per second: the scouting spaces its calls (5 rivals in parallel)
var scoutTick = time.NewTicker(70 * time.Millisecond)

func scoutGet(c Config, host, path string, out any) error {
	var err error
	for try := 0; try < 4; try++ {
		<-scoutTick.C
		if host == "" {
			err = riotGetQuiet(c, path, out)
		} else {
			err = riotGetHostQ(c, host, path, out, true)
		}
		if err == nil || err.Error() != "rate_limited" {
			return err
		}
		time.Sleep(time.Duration(2+try*2) * time.Second)
	}
	return err
}

var (
	scoutMu   sync.Mutex
	scoutKey  string
	scoutBusy bool
	scoutRes  []scoutOut
)

func champKeyByID(id string) int {
	championByKey(0)
	ddChampMu.Lock()
	defer ddChampMu.Unlock()
	for k, c := range ddChamps {
		if strings.EqualFold(c.ID, id) {
			var n int
			fmt.Sscan(k, &n)
			return n
		}
	}
	return 0
}

func scoutOne(c Config, in scoutIn) scoutOut {
	out := scoutOut{Champ: in.Champ, Pos: in.Pos, Ally: in.Ally, RiotID: in.RiotID, mates: map[string]int{}}
	puuid, err := puuidLookup(c, in.RiotID)
	if err != nil {
		out.Error = err.Error()
		return out
	}
	out.puuid = puuid
	var list []leagueEntry
	if scoutGet(c, platformHost(c), "/lol/league/v4/entries/by-puuid/"+puuid, &list) == nil {
		for _, e := range list {
			if e.QueueType == "RANKED_SOLO_5x5" {
				out.Tier, out.Rank, out.LP, out.Wins, out.Losses, out.Hot = e.Tier, e.Rank, e.LeaguePoints, e.Wins, e.Losses, e.HotStreak
			}
		}
	}
	if key := champKeyByID(in.Champ); key > 0 {
		var m struct {
			Level    int   `json:"championLevel"`
			Points   int   `json:"championPoints"`
			LastPlay int64 `json:"lastPlayTime"`
		}
		err := scoutGet(c, platformHost(c), fmt.Sprintf("/lol/champion-mastery/v4/champion-masteries/by-puuid/%s/by-champion/%d", puuid, key), &m)
		if err == nil {
			out.Mastery, out.MasteryLvl, out.MasteryOK, out.LastPlay = m.Points, m.Level, true, m.LastPlay/1000
		} else if err.Error() == "not_found" {
			out.MasteryOK = true // never played it
		}
	}
	// last 7 ranked games (fewer calls: the nine players fit in the key's limit and are ready in seconds) (Solo/Duo and Flex), newest first
	var ids []string
	if scoutGet(c, "", fmt.Sprintf("/lol/match/v5/matches/by-puuid/%s/ids?type=ranked&start=0&count=7", puuid), &ids) == nil {
		for _, id := range ids {
			var md matchDTO
			if scoutGet(c, "", "/lol/match/v5/matches/"+id, &md) != nil {
				continue
			}
			team := 0
			for _, p := range md.Info.Participants {
				if p.PUUID == puuid {
					team = p.TeamID
				}
			}
			for _, p := range md.Info.Participants {
				if p.TeamID == team && p.PUUID != puuid && p.PUUID != "" {
					out.mates[p.PUUID]++
				}
			}
			for _, p := range md.Info.Participants {
				if p.PUUID == puuid && !p.EarlySurr {
					out.Recent++
					out.Games = append(out.Games, scoutGame{p.ChampionName, p.Win, p.Kills, p.Deaths, p.Assists})
					if strings.EqualFold(p.ChampionName, in.Champ) {
						out.ChampGames++
						if p.Win {
							out.ChampWins++
						}
					}
				}
			}
		}
	}
	return out
}

// puuidLookup: account-v1 without caching (rivals change every game)
func puuidLookup(c Config, riotID string) (string, error) {
	parts := strings.SplitN(riotID, "#", 2)
	if len(parts) != 2 || parts[0] == "" {
		return "", fmt.Errorf("no_riot_id")
	}
	var acc struct {
		PUUID string `json:"puuid"`
	}
	if err := scoutGet(c, "", "/riot/account/v1/accounts/by-riot-id/"+url.PathEscape(parts[0])+"/"+url.PathEscape(parts[1]), &acc); err != nil {
		return "", err
	}
	return acc.PUUID, nil
}

func runScout(key string, players []scoutIn) {
	c := loadConfig()
	t0 := time.Now()
	res := make([]scoutOut, len(players))
	var wg sync.WaitGroup
	for i, p := range players {
		wg.Add(1)
		go func(i int, p scoutIn) { // five rivals in parallel: about 70 calls, well within a personal key's limits
			defer wg.Done()
			res[i] = scoutOne(c, p)
		}(i, p)
	}
	wg.Wait()
	markPremades(res, c.PUUIDs[c.MyRiotID])
	scoutMu.Lock()
	if scoutKey == key {
		scoutRes, scoutBusy = res, false
	}
	scoutMu.Unlock()
	logf("Rivals analitzats en %.1f s", time.Since(t0).Seconds())
}

func scoutHandler(send func(http.ResponseWriter, int, string, []byte)) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		if req.Method == http.MethodPost {
			var in struct {
				Players []scoutIn `json:"players"`
			}
			json.NewDecoder(io.LimitReader(req.Body, 16<<10)).Decode(&in)
			ids := []string{}
			for _, p := range in.Players {
				ids = append(ids, p.RiotID)
			}
			key := strings.Join(ids, "|")
			scoutMu.Lock()
			if key != scoutKey && len(in.Players) > 0 && len(in.Players) <= 9 && loadConfig().RiotKey != "" {
				scoutKey, scoutBusy, scoutRes = key, true, nil
				go runScout(key, in.Players)
			}
			scoutMu.Unlock()
		}
		scoutMu.Lock()
		b, _ := json.Marshal(map[string]any{"busy": scoutBusy, "results": scoutRes, "has_key": loadConfig().RiotKey != ""})
		scoutMu.Unlock()
		send(w, 200, "application/json", b)
	}
}

// markPremades: two players of the same team who were teammates in 2 or more of each other's last games queued
// together (a duo, or a flex group). Groups are joined: A with B and B with C is a group of three.
func markPremades(res []scoutOut, me string) {
	n := len(res)
	group := make([]int, n)
	for i := range group {
		group[i] = i
	}
	var find func(int) int
	find = func(i int) int {
		if group[i] != i {
			group[i] = find(group[i])
		}
		return group[i]
	}
	for i := 0; i < n; i++ {
		if me != "" && res[i].Ally {
			res[i].WithMe = res[i].mates[me]
		}
		for j := i + 1; j < n; j++ {
			if res[i].Ally != res[j].Ally || res[i].puuid == "" || res[j].puuid == "" {
				continue
			}
			if res[i].mates[res[j].puuid] >= 2 || res[j].mates[res[i].puuid] >= 2 {
				group[find(i)] = find(j)
			}
		}
	}
	var found []string
	for i := 0; i < n; i++ {
		for j := 0; j < n; j++ {
			if i != j && find(i) == find(j) {
				res[i].Premade = append(res[i].Premade, res[j].Champ)
			}
		}
		if len(res[i].Premade) > 0 {
			found = append(found, res[i].Champ)
		}
	}
	if len(found) > 0 {
		logf("Premades: %s", strings.Join(found, ", "))
	}
}
