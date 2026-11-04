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
	Error      string      `json:"error,omitempty"`
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
	out := scoutOut{Champ: in.Champ, Pos: in.Pos, Ally: in.Ally, RiotID: in.RiotID}
	puuid, err := puuidLookup(c, in.RiotID)
	if err != nil {
		out.Error = err.Error()
		return out
	}
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
	// last 12 ranked games (Solo/Duo and Flex), newest first
	var ids []string
	if scoutGet(c, "", fmt.Sprintf("/lol/match/v5/matches/by-puuid/%s/ids?type=ranked&start=0&count=12", puuid), &ids) == nil {
		for _, id := range ids {
			var md matchDTO
			if scoutGet(c, "", "/lol/match/v5/matches/"+id, &md) != nil {
				continue
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
