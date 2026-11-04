package main

import (
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// "Tu contra Challenger": real games of Challenger and Grandmaster players (this server) with the champions
// you play most, read slowly from Riot in the background, and summarised (farm at 10, deaths, vision, builds,
// runes, skill order...) to compare with you. Players are found by their champion mastery, games by their
// latest ranked matches. Everything stays on disk; a refresh adds only new games.

var liveNow atomic.Bool // a game is running (set by the recorder): no background reading then

type benchGame struct {
	Match    string  `json:"m"`
	Champ    string  `json:"c"`
	Pos      string  `json:"p"`
	Win      bool    `json:"w"`
	Mins     float64 `json:"min"`
	CS       int     `json:"cs"`
	CS10     int     `json:"cs10"`
	CS10Diff int     `json:"cs10d"`
	Gold10D  int     `json:"g10d"`
	K        int     `json:"k"`
	D        int     `json:"d"`
	A        int     `json:"a"`
	D14      int     `json:"d14"`
	KP       float64 `json:"kp"`
	Vision   int     `json:"vis"`
	Control  int     `json:"cw"`
	DmgShare float64 `json:"dmg"`
	Keystone int     `json:"ks"`
	Sub      int     `json:"sub"`
	Spells   string  `json:"sp"`
	Build    []int   `json:"b"`  // completed items in the order they were finished
	First    float64 `json:"fi"` // minute the first completed item was finished
	Skills   string  `json:"sk"` // order in which Q, W and E were maxed
	Tier     string  `json:"t"`
	HasPush  bool    `json:"hp"` // the wave numbers below were measured (games read since 2.0.25)
	Push     float64 `json:"pu"` // share of minutes 3-13 spent deep in the enemy half of the map
	DeepD    int     `json:"dd"` // deaths before 14 in the enemy half
	GankD    int     `json:"gd"` // deaths before 14 with the enemy jungler taking part
	Player   string  `json:"pl"` // start of the player's puuid (to count players)
}

type benchCache struct {
	Ver     int                    `json:"ver"`
	Updated int64                  `json:"updated"`
	Champs  []string               `json:"champs"`
	Players map[string][]string    `json:"players"` // champion -> puuids found
	Scanned map[string]bool        `json:"scanned"` // puuids already looked at (mastery)
	Seen    map[string]bool        `json:"seen"`    // match ids already read
	Games   map[string][]benchGame `json:"games"`   // champion -> games
}

var (
	benchMu   sync.Mutex
	benchBusy bool
	benchMsg  string
	benchDone int
	benchGoal int
)

func benchFile() string { return filepath.Join(filepath.Dir(dataDir()), "challenger.json") }

func loadBench() benchCache {
	var b benchCache
	if raw, err := os.ReadFile(benchFile()); err == nil {
		json.Unmarshal(raw, &b)
	}
	if b.Players == nil {
		b.Players = map[string][]string{}
	}
	if b.Scanned == nil {
		b.Scanned = map[string]bool{}
	}
	if b.Seen == nil {
		b.Seen = map[string]bool{}
	}
	if b.Ver < 2 { // games read before the wave numbers existed: read them again (the players found are kept)
		b.Ver, b.Games, b.Seen, b.Updated = 2, nil, nil, 0
	}
	if b.Seen == nil {
		b.Seen = map[string]bool{}
	}
	if b.Games == nil {
		b.Games = map[string][]benchGame{}
	}
	return b
}

func saveBench(b benchCache) {
	if raw, err := json.Marshal(b); err == nil {
		os.WriteFile(benchFile(), raw, 0o644)
	}
}

// the champions to study: the ones you play most this season (8 games or more), up to 3; and the position you play them in
func benchTargets() (champs []string, pos map[string]string) {
	cache := loadSeason()
	n, posN := map[string]int{}, map[string]map[string]int{}
	for _, g := range cache.Games {
		if g.Remake || g.Champ == "" {
			continue
		}
		n[g.Champ]++
		if posN[g.Champ] == nil {
			posN[g.Champ] = map[string]int{}
		}
		if g.Pos != "" {
			posN[g.Champ][g.Pos]++
		}
	}
	for c, k := range n {
		if k >= 8 {
			champs = append(champs, c)
		}
	}
	sort.Slice(champs, func(i, j int) bool { return n[champs[i]] > n[champs[j]] })
	if len(champs) > 3 {
		champs = champs[:3]
	}
	pos = map[string]string{}
	for _, c := range champs {
		best := 0
		for p, k := range posN[c] {
			if k > best {
				best, pos[c] = k, p
			}
		}
	}
	return
}

// champKeyOf: the numeric key of a champion id ("Leblanc" -> 7)
func champKeyOf(id string) int {
	championByKey(0)
	ddChampMu.Lock()
	defer ddChampMu.Unlock()
	for k, c := range ddChamps {
		if strings.EqualFold(c.ID, id) {
			n, _ := strconv.Atoi(k)
			return n
		}
	}
	return 0
}

// one call, spaced and polite: waits while a game, the scouting or the season reading need the key
func benchGet(c Config, platform bool, path string, out any) error {
	for i := 0; i < 600 && (liveNow.Load() || func() bool { scoutMu.Lock(); defer scoutMu.Unlock(); return scoutBusy }() ||
		func() bool { seasonMu.Lock(); defer seasonMu.Unlock(); return seasonBusy }()); i++ {
		time.Sleep(2 * time.Second)
	}
	if os.Getenv("LT_TEST_FAST") == "" {
		time.Sleep(1300 * time.Millisecond)
	}
	host := c.Region
	if platform {
		host = platformHost(c)
	}
	err := riotGetHostQ(c, host, path, out, true)
	for tries := 0; err != nil && err.Error() == "rate_limited" && tries < 5; tries++ {
		time.Sleep(20 * time.Second)
		err = riotGetHostQ(c, host, path, out, true)
	}
	return err
}

func benchSet(msg string, done, goal int) {
	benchMu.Lock()
	benchMsg, benchDone, benchGoal = msg, done, goal
	benchMu.Unlock()
}

const benchPerChamp = 25

func benchRun() {
	benchMu.Lock()
	if benchBusy {
		benchMu.Unlock()
		return
	}
	benchBusy = true
	benchMu.Unlock()
	defer func() { benchMu.Lock(); benchBusy = false; benchMu.Unlock() }()
	c := loadConfig()
	if c.RiotKey == "" {
		benchSet("no_key", 0, 0)
		return
	}
	champs, pos := benchTargets()
	if len(champs) == 0 {
		benchSet("no_champs", 0, 0)
		return
	}
	b := loadBench()
	b.Champs = champs
	keys := map[int]string{}
	for _, ch := range champs {
		if k := champKeyOf(ch); k > 0 {
			keys[k] = ch
		}
	}
	logf("Challenger: començo (%s)", strings.Join(champs, ", "))
	// 1) the ladder: Challenger, then Grandmaster
	type entry struct {
		PUUID string `json:"puuid"`
		LP    int    `json:"leaguePoints"`
	}
	type league struct {
		Tier    string  `json:"tier"`
		Entries []entry `json:"entries"`
	}
	ladder := []entry{}
	tierOf := map[string]string{}
	for _, q := range []string{"challengerleagues", "grandmasterleagues"} {
		var l league
		if err := benchGet(c, true, "/lol/league/v4/"+q+"/by-queue/RANKED_SOLO_5x5", &l); err != nil {
			logf("Challenger: no he pogut llegir %s: %v", q, err)
			continue
		}
		sort.Slice(l.Entries, func(i, j int) bool { return l.Entries[i].LP > l.Entries[j].LP })
		for _, e := range l.Entries {
			if e.PUUID != "" {
				ladder = append(ladder, e)
				tierOf[e.PUUID] = l.Tier
			}
		}
	}
	enough := func() bool {
		for _, ch := range champs {
			if len(b.Players[ch]) < 8 {
				return false
			}
		}
		return true
	}
	// 2) players whose top champions include ours
	scanned := 0
	for _, e := range ladder {
		if enough() || scanned >= 160 {
			break
		}
		if b.Scanned[e.PUUID] {
			continue
		}
		scanned++
		benchSet("players", scanned, 160)
		var top []struct {
			ChampionID int `json:"championId"`
			Points     int `json:"championPoints"`
		}
		if err := benchGet(c, true, "/lol/champion-mastery/v4/champion-masteries/by-puuid/"+e.PUUID+"/top?count=6", &top); err != nil {
			continue
		}
		b.Scanned[e.PUUID] = true
		for _, t := range top {
			if ch, ok := keys[t.ChampionID]; ok && t.Points >= 100000 {
				b.Players[ch] = append(b.Players[ch], e.PUUID)
			}
		}
		if scanned%20 == 0 {
			saveBench(b)
		}
	}
	saveBench(b)
	// 3) their latest ranked games with that champion (timeline included)
	items := finalItems()
	goal := benchPerChamp * len(champs)
	done := 0
	for _, ch := range champs {
		done += min(len(b.Games[ch]), benchPerChamp)
	}
	for _, ch := range champs {
		for _, pu := range b.Players[ch] {
			if len(b.Games[ch]) >= benchPerChamp {
				break
			}
			var ids []string
			if err := benchGet(c, false, "/lol/match/v5/matches/by-puuid/"+pu+"/ids?queue=420&start=0&count=10", &ids); err != nil {
				continue
			}
			got := 0
			for _, mid := range ids {
				if got >= 4 || len(b.Games[ch]) >= benchPerChamp {
					break
				}
				if b.Seen[mid] {
					continue
				}
				b.Seen[mid] = true
				var m matchDTO
				if err := benchGet(c, false, "/lol/match/v5/matches/"+mid, &m); err != nil {
					continue
				}
				g, ok := benchFromMatch(c, &m, mid, pu, ch, pos[ch], items)
				if !ok {
					continue
				}
				g.Tier, g.Player = tierOf[pu], pu[:min(12, len(pu))]
				b.Games[ch] = append(b.Games[ch], g)
				got++
				done++
				benchSet("games", done, goal)
				saveBench(b)
			}
		}
	}
	b.Updated = time.Now().Unix()
	saveBench(b)
	benchSet("", done, goal)
	logf("Challenger: fet (%d partides)", done)
}

// benchFromMatch turns one game of the player into a benchmark game (with its timeline); false if it isn't with that champion and position
func benchFromMatch(c Config, m *matchDTO, mid, puuid, champ, pos string, final map[int]bool) (benchGame, bool) {
	var g benchGame
	me, team := -1, 0
	for _, p := range m.Info.Participants {
		if p.PUUID == puuid {
			if !strings.EqualFold(p.ChampionName, champ) || p.EarlySurr || (pos != "" && p.TeamPosition != pos) {
				return g, false
			}
			me, team = p.ParticipantID, p.TeamID
			mins := math.Max(1, float64(m.Info.GameDuration)/60)
			g = benchGame{Match: mid, Champ: p.ChampionName, Pos: p.TeamPosition, Win: p.Win, Mins: math.Round(mins*10) / 10, CS: p.Minions + p.Neutral,
				K: p.Kills, D: p.Deaths, A: p.Assists, KP: p.Challenges.KP, Vision: p.VisionScore, Control: p.ControlWards, DmgShare: p.Challenges.DamageShare,
				Spells: spellNames[p.Summoner1] + "+" + spellNames[p.Summoner2]}
			if len(p.Perks.Styles) > 0 && len(p.Perks.Styles[0].Selections) > 0 {
				g.Keystone = p.Perks.Styles[0].Selections[0].Perk
			}
			if len(p.Perks.Styles) > 1 {
				g.Sub = p.Perks.Styles[1].Style
			}
		}
	}
	if me < 0 || m.Info.GameDuration < 15*60 {
		return g, false
	}
	opp, jg := 0, 0
	for _, p := range m.Info.Participants {
		if p.TeamID != team && p.TeamPosition == g.Pos {
			opp = p.ParticipantID
		}
		if p.TeamID != team && p.TeamPosition == "JUNGLE" {
			jg = p.ParticipantID
		}
	}
	var tl timelineDTO
	if err := benchGet(c, false, "/lol/match/v5/matches/"+mid+"/timeline", &tl); err != nil {
		return g, true // the game counts without the timeline numbers
	}
	key, okey := strconv.Itoa(me), strconv.Itoa(opp)
	lv := map[int]int{}
	order := ""
	g.HasPush = true
	pushN, pushT := 0, 0
	for _, f := range tl.Info.Frames {
		if mn := f.Timestamp / 60000; mn >= 3 && mn <= 13 {
			if a, ok := f.ParticipantFrames[key]; ok {
				pushT++
				if deepHalf(a.Position.X, a.Position.Y, team) {
					pushN++
				}
			}
		}
		if f.Timestamp >= 600000 && f.Timestamp < 660000 {
			a := f.ParticipantFrames[key]
			g.CS10 = a.Minions + a.JungleCS
			if o, ok := f.ParticipantFrames[okey]; ok && opp > 0 {
				g.CS10Diff = g.CS10 - (o.Minions + o.JungleCS)
				g.Gold10D = a.TotalGold - o.TotalGold
			}
		}
		for _, e := range f.Events {
			switch e.Type {
			case "CHAMPION_KILL":
				if e.VictimID == me && e.Timestamp < 14*60000 {
					g.D14++
					if e.Position != nil && enemyHalf(e.Position.X, e.Position.Y, team) {
						g.DeepD++
					}
					in := jg > 0 && e.KillerID == jg
					for _, x := range e.AssistingParticipantIDs {
						in = in || (jg > 0 && x == jg)
					}
					if in {
						g.GankD++
					}
				}
			case "ITEM_PURCHASED":
				if e.ParticipantID == me && final[e.ItemID] && len(g.Build) < 6 {
					dup := false
					for _, x := range g.Build {
						dup = dup || x == e.ItemID
					}
					if !dup {
						g.Build = append(g.Build, e.ItemID)
						if len(g.Build) == 1 {
							g.First = math.Round(float64(e.Timestamp)/6000) / 10
						}
					}
				}
			case "SKILL_LEVEL_UP":
				if e.ParticipantID == me && e.SkillSlot >= 1 && e.SkillSlot <= 3 {
					lv[e.SkillSlot]++
					if lv[e.SkillSlot] == 5 {
						order += string("QWE"[e.SkillSlot-1])
					}
				}
			}
		}
	}
	g.Skills = order
	if pushT > 0 {
		g.Push = float64(pushN) / float64(pushT)
	}
	return g, true
}

// the map is 14870 wide, blue base bottom-left, red top-right; the river is the line where both bases are
// equally far. "Deep" means well past it: about halfway from the river to the enemy's first tower.
func halfMargin(x, y float64, team int) float64 {
	own, en := 0.0, 14870.0
	if team == 200 {
		own, en = en, own
	}
	return math.Hypot(x-own, y-own) - math.Hypot(x-en, y-en)
}
func enemyHalf(x, y float64, team int) bool { return halfMargin(x, y, team) > 0 }
func deepHalf(x, y float64, team int) bool  { return halfMargin(x, y, team) > 2000 }

// finalItems: ids of completed items (nothing builds out of them)
func finalItems() map[int]bool {
	out := map[int]bool{}
	var m map[string]ddItem
	if json.Unmarshal(itemsJSON("en_US"), &m) == nil {
		for id, it := range m {
			if it.Final {
				n, _ := strconv.Atoi(id)
				out[n] = true
			}
		}
	}
	return out
}

// ---- summary for the page and the AI

type benchSummary struct {
	Champ    string         `json:"champ"`
	Name     string         `json:"name"`
	Games    int            `json:"games"`
	Players  int            `json:"players"`
	WR       int            `json:"wr"`
	CS10     float64        `json:"cs10"`
	CSMin    float64        `json:"csmin"`
	CS10Diff float64        `json:"cs10d"`
	Gold10D  float64        `json:"g10d"`
	K        float64        `json:"k"`
	D        float64        `json:"d"`
	A        float64        `json:"a"`
	D14      float64        `json:"d14"`
	KP       float64        `json:"kp"`
	VisMin   float64        `json:"vismin"`
	Control  float64        `json:"cw"`
	DmgShare float64        `json:"dmg"`
	First    float64        `json:"first"`
	Keystone int            `json:"ks"`
	Sub      int            `json:"sub"`
	Spells   string         `json:"spells"`
	Build    []int          `json:"build"`
	Skills   string         `json:"skills"`
	Push     *float64       `json:"push,omitempty"`  // % of minutes 3-13 deep in the enemy half
	DeepD    *float64       `json:"deepd,omitempty"` // deaths before 14 in the enemy half, per game
	GankD    *float64       `json:"gankd,omitempty"` // deaths before 14 with the enemy jungler in it, per game
	Share    map[string]int `json:"share"`           // how often the most common choice was taken, in %
}

func r1(x float64) float64 { return math.Round(x*10) / 10 }

func summarize(ch string, gs []benchGame) benchSummary {
	s := benchSummary{Champ: ch, Name: displayName(ch), Games: len(gs), Share: map[string]int{}}
	if len(gs) == 0 {
		return s
	}
	players := map[string]bool{}
	var w, n10 int
	var cs10, csmin, cs10d, g10d, k, d, a, d14, kp, vis, cw, dmg, first float64
	ks, sub, sp, sk, b3 := map[int]int{}, map[int]int{}, map[string]int{}, map[string]int{}, map[string]int{}
	nFirst := 0
	for _, g := range gs {
		players[g.Player] = true
		if g.Win {
			w++
		}
		if g.CS10 > 0 {
			cs10 += float64(g.CS10)
			cs10d += float64(g.CS10Diff)
			g10d += float64(g.Gold10D)
			n10++
		}
		csmin += float64(g.CS) / g.Mins
		k, d, a, d14, kp = k+float64(g.K), d+float64(g.D), a+float64(g.A), d14+float64(g.D14), kp+g.KP
		vis += float64(g.Vision) / g.Mins
		cw += float64(g.Control)
		dmg += g.DmgShare
		if g.First > 0 {
			first += g.First
			nFirst++
		}
		ks[g.Keystone]++
		sub[g.Sub]++
		sp[g.Spells]++
		if g.Skills != "" {
			sk[g.Skills[:1]]++
		}
		if len(g.Build) >= 2 {
			b3[fmt.Sprint(g.Build[:min(3, len(g.Build))])]++
		}
	}
	var pu, dd, gd, np float64
	for _, g := range gs {
		if g.HasPush {
			pu, dd, gd, np = pu+g.Push, dd+float64(g.DeepD), gd+float64(g.GankD), np+1
		}
	}
	if np >= 5 {
		a, b, c := math.Round(100*pu/np), r1(dd/np), r1(gd/np)
		s.Push, s.DeepD, s.GankD = &a, &b, &c
	}
	nf := float64(len(gs))
	s.WR = int(math.Round(100 * float64(w) / nf))
	if n10 > 0 {
		s.CS10, s.CS10Diff, s.Gold10D = r1(cs10/float64(n10)), r1(cs10d/float64(n10)), math.Round(g10d/float64(n10))
	}
	s.CSMin, s.K, s.D, s.A, s.D14 = r1(csmin/nf), r1(k/nf), r1(d/nf), r1(a/nf), r1(d14/nf)
	s.KP, s.VisMin, s.Control, s.DmgShare = math.Round(100*kp/nf), r1(vis/nf), r1(cw/nf), math.Round(100*dmg/nf)
	if nFirst > 0 {
		s.First = r1(first / float64(nFirst))
	}
	topI := func(m map[int]int) (int, int) {
		best, n := 0, 0
		for x, c := range m {
			if c > n {
				best, n = x, c
			}
		}
		return best, int(math.Round(100 * float64(n) / nf))
	}
	topS := func(m map[string]int) (string, int) {
		best, n := "", 0
		for x, c := range m {
			if c > n {
				best, n = x, c
			}
		}
		return best, int(math.Round(100 * float64(n) / nf))
	}
	s.Keystone, s.Share["ks"] = topI(ks)
	s.Sub, s.Share["sub"] = topI(sub)
	s.Spells, s.Share["spells"] = topS(sp)
	var first1 string
	first1, s.Share["skills"] = topS(sk)
	s.Skills = first1
	if bs, pc := topS(b3); bs != "" {
		s.Share["build"] = pc
		for _, f := range strings.Fields(strings.Trim(bs, "[]")) {
			n, _ := strconv.Atoi(f)
			s.Build = append(s.Build, n)
		}
	}
	s.Players = len(players)
	return s
}

func benchJSON() []byte {
	b := loadBench()
	out := map[string]any{}
	list := []benchSummary{}
	for _, ch := range b.Champs {
		list = append(list, summarize(ch, b.Games[ch]))
	}
	benchMu.Lock()
	out["busy"], out["msg"], out["done"], out["goal"] = benchBusy, benchMsg, benchDone, benchGoal
	benchMu.Unlock()
	out["updated"], out["champs"] = b.Updated, list
	raw, _ := json.Marshal(out)
	return raw
}

func benchHandler(send func(http.ResponseWriter, int, string, []byte)) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Query().Get("run") == "1" {
			go benchRun()
			time.Sleep(200 * time.Millisecond)
		}
		send(w, 200, "application/json", benchJSON())
	}
}

// benchAuto: once at start (after the season) and again when the data is a week old
func benchAuto() {
	for {
		if loadConfig().RiotKey != "" && time.Since(time.Unix(loadBench().Updated, 0)) > 7*24*time.Hour {
			benchRun()
		}
		time.Sleep(6 * time.Hour)
	}
}
