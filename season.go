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
	"time"
)

// Season win rate per champion. Riot's match history (match-v5) is read once and kept on disk; afterwards only
// new games are fetched. The current ranked season is exactly as many games as the wins + losses Riot shows for
// the queue, newest first (remakes don't count), so the totals match the rank card.

const seasonStart = 1767830400 // 8 January 2026 00:00 UTC: start of the 2026 ranked year (patch 26.01)

type seasonGame struct {
	Queue  int    `json:"q"`
	Champ  string `json:"c"`
	Win    bool   `json:"w"`
	K      int    `json:"k"`
	D      int    `json:"d"`
	A      int    `json:"a"`
	End    int64  `json:"e"`
	Remake bool   `json:"r,omitempty"`
	Opp    string `json:"o,omitempty"`  // lane opponent's champion
	Pos    string `json:"p,omitempty"`  // my position
	Opp2   bool   `json:"o2,omitempty"` // opponent already looked for (also when there was none)
}

type seasonCache struct {
	PUUID string                `json:"puuid"`
	Games map[string]seasonGame `json:"games"`
	IDs   map[int][]string      `json:"ids"`  // match ids of each queue, newest first
	Need  map[int]int           `json:"need"` // wins + losses Riot showed for each queue
}

var (
	seasonMu     sync.Mutex
	seasonBusy   bool
	seasonDone   int
	seasonTotal  int
	seasonErr    string
	seasonLastOK time.Time
)

func seasonFile() string { return profileFile("temporada.json") }

func loadSeason() seasonCache {
	var c seasonCache
	if b, err := os.ReadFile(seasonFile()); err == nil {
		json.Unmarshal(b, &c)
	}
	if c.Games == nil {
		c.Games = map[string]seasonGame{}
	}
	return c
}

// seasonSync reads the match ids of each ranked queue (newest first) until it has as many games as
// Riot's wins + losses, fetching the details of the ones not on disk yet.
func seasonSync() {
	seasonMu.Lock()
	if seasonBusy {
		seasonMu.Unlock()
		return
	}
	seasonBusy, seasonDone, seasonTotal, seasonErr = true, 0, 0, ""
	seasonMu.Unlock()
	defer func() { seasonMu.Lock(); seasonBusy = false; seasonMu.Unlock() }()
	fail := func(e string) { seasonMu.Lock(); seasonErr = e; seasonMu.Unlock(); logf("Temporada: %s", e) }

	c := loadConfig()
	id := myRiotID()
	if c.RiotKey == "" || id == "" {
		fail(map[bool]string{true: "no_key", false: "no_riot_id"}[c.RiotKey == ""])
		return
	}
	puuid, err := puuidFor(&c, id)
	if err != nil {
		fail(err.Error())
		return
	}
	ent, err := leagueEntries(id)
	if err != nil {
		fail(err.Error())
		return
	}
	cache := loadSeason()
	if cache.PUUID != puuid {
		cache = seasonCache{PUUID: puuid, Games: map[string]seasonGame{}}
	}
	need := map[int]int{}
	for q, e := range ent {
		need[map[string]int{"RANKED_SOLO_5x5": 420, "RANKED_FLEX_SR": 440}[q]] = e.Wins + e.Losses
	}
	// 1) ids per queue, newest first
	ids := map[int][]string{}
	for q, n := range need {
		if n == 0 {
			continue
		}
		for start := 0; start < 2000; start += 100 {
			var page []string
			path := fmt.Sprintf("/lol/match/v5/matches/by-puuid/%s/ids?queue=%d&startTime=%d&start=%d&count=100", puuid, q, seasonStart, start)
			if err := riotGetQuiet(c, path, &page); err != nil {
				fail(err.Error())
				return
			}
			ids[q] = append(ids[q], page...)
			if len(page) < 100 || len(ids[q]) >= n+40 { // enough ids (a few extra for remakes)
				break
			}
		}
	}
	missing := []string{}
	for _, list := range ids {
		for _, m := range list {
			// games saved before the lane opponent was recorded are read again once
			if g, ok := cache.Games[m]; !ok || (!g.Opp2 && !g.Remake) {
				missing = append(missing, m)
			}
		}
	}
	seasonMu.Lock()
	seasonTotal = len(missing)
	seasonMu.Unlock()
	if len(missing) > 0 {
		logf("Temporada: %d partides noves per llegir de Riot", len(missing))
	}
	// 2) details of the games not on disk (about 1.2 s apart: a personal key allows 100 calls every 2 minutes)
	for i, m := range missing {
		for wait := 0; wait < 60; wait++ { // the loading-screen scouting goes first
			scoutMu.Lock()
			b := scoutBusy
			scoutMu.Unlock()
			if !b {
				break
			}
			time.Sleep(time.Second)
		}
		var md matchDTO
		err := riotGetQuiet(c, "/lol/match/v5/matches/"+m, &md)
		for tries := 0; err != nil && err.Error() == "rate_limited" && tries < 6; tries++ {
			time.Sleep(15 * time.Second)
			err = riotGetQuiet(c, "/lol/match/v5/matches/"+m, &md)
		}
		if err != nil {
			fail(err.Error())
			break
		}
		for _, p := range md.Info.Participants {
			if p.PUUID == puuid {
				g := seasonGame{Queue: md.Info.QueueID, Champ: p.ChampionName, Win: p.Win, K: p.Kills, D: p.Deaths, A: p.Assists,
					End: md.Info.GameEndTimestamp / 1000, Remake: p.EarlySurr, Pos: p.TeamPosition, Opp2: true}
				for _, o := range md.Info.Participants {
					if o.TeamID != p.TeamID && p.TeamPosition != "" && o.TeamPosition == p.TeamPosition {
						g.Opp = o.ChampionName
					}
				}
				cache.Games[m] = g
			}
		}
		seasonMu.Lock()
		seasonDone = i + 1
		seasonMu.Unlock()
		if (i+1)%20 == 0 || i == len(missing)-1 {
			b, _ := json.Marshal(cache)
			os.WriteFile(seasonFile(), b, 0o644)
		}
		if os.Getenv("LT_TEST_FAST") == "" {
			time.Sleep(1200 * time.Millisecond)
		}
	}
	cache.IDs, cache.Need = ids, need
	b, _ := json.Marshal(cache)
	os.WriteFile(seasonFile(), b, 0o644)
	seasonMu.Lock()
	if seasonErr == "" {
		seasonLastOK = time.Now()
	}
	seasonMu.Unlock()
	logf("Temporada actualitzada: %d partides guardades", len(cache.Games))
}

func riotGetQuiet(c Config, path string, out any) error {
	return riotGetHostQ(c, c.Region, path, out, true)
}

type oppSeason struct {
	Champ  string `json:"champ"`
	Name   string `json:"name"`
	Games  int    `json:"games"`
	Wins   int    `json:"wins"`
	Losses int    `json:"losses"`
}

type champSeason struct {
	Vs     []oppSeason `json:"vs"`
	Champ  string      `json:"champ"`
	Name   string      `json:"name"`
	Games  int         `json:"games"`
	Wins   int         `json:"wins"`
	Losses int         `json:"losses"`
	WR     int         `json:"wr"`
	KDA    float64     `json:"kda"`
	Form   string      `json:"form"` // last 10 results with this champion in this queue, newest first: W / L
}

// champForm: recent form of one champion over every ranked queue (newest first), for the coach's comments
type champForm struct {
	Name    string  `json:"name"`
	Form    string  `json:"form"` // last 10 results, newest first
	KDA     float64 `json:"kda"`  // KDA of those games
	SeasonK float64 `json:"season_kda"`
	Games   int     `json:"games"` // ranked games with it this season
	Last    int64   `json:"last"`  // end of the last game (unix)
}

func seasonJSON() []byte {
	cache := loadSeason()
	ids, need := cache.IDs, cache.Need
	seasonMu.Lock()
	out := map[string]any{"busy": seasonBusy, "done": seasonDone, "total": seasonTotal, "error": seasonErr, "synced": cache.IDs != nil}
	seasonMu.Unlock()
	queues := map[string]any{}
	all := []seasonGame{}
	for q, n := range need {
		per := map[string]*champSeason{}
		k, d, a := map[string]int{}, map[string]int{}, map[string]int{}
		counted := 0
		for _, m := range ids[q] {
			if counted >= n {
				break
			}
			g, ok := cache.Games[m]
			if !ok || g.Remake {
				continue
			}
			counted++
			cs := per[g.Champ]
			if cs == nil {
				cs = &champSeason{Champ: g.Champ, Name: displayName(g.Champ)}
				per[g.Champ] = cs
			}
			cs.Games++
			if len(cs.Form) < 10 {
				cs.Form += map[bool]string{true: "W", false: "L"}[g.Win]
			}
			all = append(all, g)
			if g.Win {
				cs.Wins++
			} else {
				cs.Losses++
			}
			k[g.Champ] += g.K
			d[g.Champ] += g.D
			a[g.Champ] += g.A
			if g.Opp != "" {
				i := -1
				for j := range cs.Vs {
					if cs.Vs[j].Champ == g.Opp {
						i = j
					}
				}
				if i < 0 {
					cs.Vs = append(cs.Vs, oppSeason{Champ: g.Opp, Name: displayName(g.Opp)})
					i = len(cs.Vs) - 1
				}
				cs.Vs[i].Games++
				if g.Win {
					cs.Vs[i].Wins++
				} else {
					cs.Vs[i].Losses++
				}
			}
		}
		list := []champSeason{}
		for ch, cs := range per {
			cs.WR = (cs.Wins*100 + cs.Games/2) / cs.Games
			den := d[ch]
			if den == 0 {
				den = 1
			}
			cs.KDA = float64(int(float64(k[ch]+a[ch])/float64(den)*100+0.5)) / 100
			sort.Slice(cs.Vs, func(i, j int) bool { return cs.Vs[i].Games > cs.Vs[j].Games })
			list = append(list, *cs)
		}
		sort.Slice(list, func(i, j int) bool {
			if list[i].Games != list[j].Games {
				return list[i].Games > list[j].Games
			}
			return list[i].WR > list[j].WR
		})
		queues[map[int]string{420: "RANKED_SOLO_5x5", 440: "RANKED_FLEX_SR"}[q]] = map[string]any{"champions": list, "counted": counted, "expected": n}
	}
	out["queues"] = queues
	// recent form per champion, every ranked queue together
	sort.Slice(all, func(i, j int) bool { return all[i].End > all[j].End })
	form := map[string]*champForm{}
	fk := map[string][3]int{}
	sk := map[string][3]int{}
	for _, g := range all {
		f := form[g.Champ]
		if f == nil {
			f = &champForm{Name: displayName(g.Champ), Last: g.End}
			form[g.Champ] = f
		}
		f.Games++
		x := sk[g.Champ]
		sk[g.Champ] = [3]int{x[0] + g.K, x[1] + g.D, x[2] + g.A}
		if len(f.Form) < 10 {
			f.Form += map[bool]string{true: "W", false: "L"}[g.Win]
			y := fk[g.Champ]
			fk[g.Champ] = [3]int{y[0] + g.K, y[1] + g.D, y[2] + g.A}
		}
	}
	kda := func(v [3]int) float64 {
		d := v[1]
		if d == 0 {
			d = 1
		}
		return float64(int(float64(v[0]+v[2])/float64(d)*100+0.5)) / 100
	}
	for ch, f := range form {
		f.KDA, f.SeasonK = kda(fk[ch]), kda(sk[ch])
	}
	out["form"] = form
	b, _ := json.Marshal(out)
	return b
}

func seasonHandler(send func(http.ResponseWriter, int, string, []byte)) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		if req.Method == http.MethodPost || strings.Contains(req.URL.RawQuery, "sync=1") {
			go seasonSync()
			time.Sleep(150 * time.Millisecond) // let it start, so this answer already says it's reading
		}
		send(w, 200, "application/json", seasonJSON())
	}
}

// seasonGamesHandler: the season's Solo/Duo games one by one (oldest first), for the LP history's longer ranges.
// /api/season/games?days=30
func seasonGamesHandler(send func(http.ResponseWriter, int, string, []byte)) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		days, _ := strconv.Atoi(req.URL.Query().Get("days"))
		if days <= 0 || days > 400 {
			days = 30
		}
		from := time.Now().Add(-time.Duration(days) * 24 * time.Hour).Unix()
		c := loadSeason()
		type g struct {
			End   int64  `json:"e"`
			Champ string `json:"c"`
			Win   bool   `json:"w"`
			K     int    `json:"k"`
			D     int    `json:"d"`
			A     int    `json:"a"`
			Opp   string `json:"o,omitempty"`
		}
		out := []g{}
		for _, m := range c.Games {
			if m.Queue != 420 || m.Remake || m.End < from {
				continue
			}
			out = append(out, g{m.End, m.Champ, m.Win, m.K, m.D, m.A, m.Opp})
		}
		sort.Slice(out, func(i, j int) bool { return out[i].End < out[j].End })
		b, _ := json.Marshal(map[string]any{"games": out})
		send(w, 200, "application/json", b)
	}
}
