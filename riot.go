package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ------------------------------------------------------------ config

type Config struct {
	RiotKey    string                            `json:"riot_key"`
	Region     string                            `json:"region"`
	Platform   string                            `json:"platform"`
	PUUIDs     map[string]string                 `json:"puuids"`
	AIKey      string                            `json:"ai_key,omitempty"`
	AIModel    string                            `json:"ai_model,omitempty"` // Gemini model that answered last
	MyRiotID   string                            `json:"my_riot_id,omitempty"`
	Peaks      map[string]leagueEntry            `json:"peaks,omitempty"` // highest rank seen this season, by queue
	Me         *meInfo                           `json:"me,omitempty"`
	Owner      string                            `json:"owner,omitempty"`       // first account of this PC (its data keeps the original files)
	VoiceAlias map[string][]string               `json:"voice_alias,omitempty"` // champion -> how you say it (learnt or trained)
	PeaksBy    map[string]map[string]leagueEntry `json:"peaks_by,omitempty"`    // peaks of the other accounts    // last known summoner name, level and icon (shown in the header)
}

var cfgMu sync.Mutex

func configPath() string { return filepath.Join(filepath.Dir(dataDir()), "config.json") }

func loadConfig() Config {
	cfgMu.Lock()
	defer cfgMu.Unlock()
	c := Config{Region: "europe", PUUIDs: map[string]string{}}
	if b, err := os.ReadFile(configPath()); err == nil {
		json.Unmarshal(b, &c)
	}
	if c.Region == "" {
		c.Region = "europe"
	}
	if c.PUUIDs == nil {
		c.PUUIDs = map[string]string{}
	}
	return c
}

func saveConfig(c Config) error {
	cfgMu.Lock()
	defer cfgMu.Unlock()
	os.MkdirAll(filepath.Dir(configPath()), 0o755)
	b, _ := json.MarshalIndent(c, "", "  ")
	return os.WriteFile(configPath(), b, 0o600)
}

// ------------------------------------------------------------ API client

var riotHTTP = &http.Client{Timeout: 10 * time.Second}

var errNoKey = errors.New("no_key")

// Riot's limits for a personal key: 20 calls a second and 100 every 2 minutes, counted per host (euw1, europe...).
// Every call waits here for its turn, so the scouting, the season and the Challenger reading never go over them;
// after a 429 the host is paused for the time Riot asks (Retry-After).
var (
	rlMu    sync.Mutex
	rlHits  = map[string][]time.Time{}
	rlUntil = map[string]time.Time{}
)

func rlWait(host string) {
	if os.Getenv("LT_TEST_FAST") != "" {
		return
	}
	for {
		rlMu.Lock()
		now := time.Now()
		h := rlHits[host]
		for len(h) > 0 && now.Sub(h[0]) >= 121*time.Second {
			h = h[1:]
		}
		var wait time.Duration
		if u := rlUntil[host]; now.Before(u) {
			wait = u.Sub(now)
		} else if len(h) >= 95 {
			wait = h[len(h)-95].Add(121 * time.Second).Sub(now)
		} else if n := len(h); n >= 18 && now.Sub(h[n-18]) < 1100*time.Millisecond {
			wait = h[n-18].Add(1100 * time.Millisecond).Sub(now)
		}
		if wait <= 0 {
			rlHits[host] = append(h, now)
			rlMu.Unlock()
			return
		}
		rlHits[host] = h
		rlMu.Unlock()
		time.Sleep(wait)
	}
}

func rlBlock(host string, d time.Duration) {
	rlMu.Lock()
	if u := time.Now().Add(d); u.After(rlUntil[host]) {
		rlUntil[host] = u
	}
	rlMu.Unlock()
}

func riotGet(c Config, path string, out any) error { return riotGetHost(c, c.Region, path, out) }

// riotGetHost calls a regional host (europe, americas…) or a platform host (euw1, na1…).
func riotGetHost(c Config, host, path string, out any) error {
	return riotGetHostQ(c, host, path, out, false)
}

// riotGetHostQ: quiet=true doesn't log successful calls (the season sync makes hundreds)
func riotGetHostQ(c Config, host, path string, out any, quiet bool) error {
	if c.RiotKey == "" {
		return errNoKey
	}
	base := "https://" + host + ".api.riotgames.com"
	if b := os.Getenv("LT_RIOT_BASE"); b != "" { // tests only
		base = b
	}
	req, _ := http.NewRequest("GET", base+path, nil)
	req.Header.Set("X-Riot-Token", c.RiotKey)
	rlWait(host)
	r, err := riotHTTP.Do(req)
	shown := path
	if i := strings.Index(shown, "by-puuid/"); i >= 0 {
		rest := shown[i+9:]
		if j := strings.Index(rest, "/"); j >= 0 {
			shown = shown[:i+9] + "..." + rest[j:]
		}
	}
	if err != nil {
		logf("Riot %s: error de connexio: %v", shown, err)
		return err
	}
	defer r.Body.Close()
	body, _ := io.ReadAll(r.Body)
	if !quiet || r.StatusCode != 200 {
		logf("Riot %s: HTTP %d", shown, r.StatusCode)
	}
	switch r.StatusCode {
	case 200:
		setPart("riot", true, "")
		return json.Unmarshal(body, out)
	case 401, 403:
		setPart("riot", false, "bad_key")
		return errors.New("bad_key")
	case 404:
		return errors.New("not_found")
	case 429:
		secs, _ := strconv.Atoi(r.Header.Get("Retry-After"))
		if secs <= 0 || secs > 120 {
			secs = 10
		}
		rlBlock(host, time.Duration(secs)*time.Second)
		return errors.New("rate_limited")
	default:
		return fmt.Errorf("http_%d", r.StatusCode)
	}
}

type matchDTO struct {
	Info struct {
		GameEndTimestamp int64 `json:"gameEndTimestamp"`
		GameDuration     int64 `json:"gameDuration"`
		QueueID          int   `json:"queueId"`
		Participants     []struct {
			ParticipantID int    `json:"participantId"`
			PUUID         string `json:"puuid"`
			ChampionName  string `json:"championName"`
			TeamID        int    `json:"teamId"`
			TeamPosition  string `json:"teamPosition"`
			Win           bool   `json:"win"`
			EarlySurr     bool   `json:"gameEndedInEarlySurrender"`
			Kills         int    `json:"kills"`
			Deaths        int    `json:"deaths"`
			Assists       int    `json:"assists"`
			GoldEarned    int    `json:"goldEarned"`
			Damage        int    `json:"totalDamageDealtToChampions"`
			VisionScore   int    `json:"visionScore"`
			WardsPlaced   int    `json:"wardsPlaced"`
			WardsKilled   int    `json:"wardsKilled"`
			ControlWards  int    `json:"visionWardsBoughtInGame"`
			Minions       int    `json:"totalMinionsKilled"`
			Neutral       int    `json:"neutralMinionsKilled"`
			TimeDead      int    `json:"totalTimeSpentDead"`
			ChampLevel    int    `json:"champLevel"`
			FirstBlood    bool   `json:"firstBloodKill"`
			LargestMulti  int    `json:"largestMultiKill"`
			Summoner1     int    `json:"summoner1Id"`
			Summoner2     int    `json:"summoner2Id"`
			Sum1Casts     int    `json:"summoner1Casts"`
			Sum2Casts     int    `json:"summoner2Casts"`
			Item0         int    `json:"item0"`
			Item1         int    `json:"item1"`
			Item2         int    `json:"item2"`
			Item3         int    `json:"item3"`
			Item4         int    `json:"item4"`
			Item5         int    `json:"item5"`
			Item6         int    `json:"item6"`
			GameName      string `json:"riotIdGameName"`
			TagLine       string `json:"riotIdTagline"`
			SummonerName  string `json:"summonerName"`
			Perks         struct {
				StatPerks struct {
					Defense int `json:"defense"`
					Flex    int `json:"flex"`
					Offense int `json:"offense"`
				} `json:"statPerks"`
				Styles []struct {
					Style      int `json:"style"`
					Selections []struct {
						Perk int `json:"perk"`
					} `json:"selections"`
				} `json:"styles"`
			} `json:"perks"`
			Challenges struct {
				SoloKills   float64 `json:"soloKills"`
				DamageShare float64 `json:"teamDamagePercentage"`
				KP          float64 `json:"killParticipation"`
				Plates      float64 `json:"turretPlatesTaken"`
			} `json:"challenges"`
		} `json:"participants"`
		Teams []struct {
			TeamID int  `json:"teamId"`
			Win    bool `json:"win"`
			Bans   []struct {
				ChampionID int `json:"championId"`
			} `json:"bans"`
			Objectives map[string]struct {
				Kills int `json:"kills"`
			} `json:"objectives"`
		} `json:"teams"`
	} `json:"info"`
}

type pos struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
}

type timelineDTO struct {
	Info struct {
		Frames []struct {
			Timestamp         int64 `json:"timestamp"`
			ParticipantFrames map[string]struct {
				Position  pos `json:"position"`
				TotalGold int `json:"totalGold"`
				XP        int `json:"xp"`
				Minions   int `json:"minionsKilled"`
				JungleCS  int `json:"jungleMinionsKilled"`
				Level     int `json:"level"`
			} `json:"participantFrames"`
			Events []struct {
				Type                    string `json:"type"`
				Timestamp               int64  `json:"timestamp"`
				KillerID                int    `json:"killerId"`
				VictimID                int    `json:"victimId"`
				AssistingParticipantIDs []int  `json:"assistingParticipantIds"`
				KillerTeamID            int    `json:"killerTeamId"`
				MonsterType             string `json:"monsterType"`
				ParticipantID           int    `json:"participantId"`
				ItemID                  int    `json:"itemId"`
				SkillSlot               int    `json:"skillSlot"`
				Position                *pos   `json:"position"`
				CreatorID               int    `json:"creatorId"`
				WardType                string `json:"wardType"`
				VictimDamageReceived    []struct {
					Name     string `json:"name"`
					Spell    string `json:"spellName"`
					Slot     int    `json:"spellSlot"`
					Basic    bool   `json:"basic"`
					Magic    int    `json:"magicDamage"`
					Physical int    `json:"physicalDamage"`
					True     int    `json:"trueDamage"`
				} `json:"victimDamageReceived"`
			} `json:"events"`
		} `json:"frames"`
	} `json:"info"`
}

type MapEvent struct {
	T     float64 `json:"t"`
	X     float64 `json:"x"`
	Y     float64 `json:"y"`
	Kind  string  `json:"kind"` // death, kill, assist, objective
	Label string  `json:"label"`
	Ours  bool    `json:"ours,omitempty"`
	Champ string  `json:"champ,omitempty"` // Data Dragon id of the other champion (icon)
}
type MapPlayer struct {
	ID    int    `json:"id"`
	Champ string `json:"champ"` // Data Dragon id
	Team  string `json:"team"`  // blue / red
	Pos   string `json:"pos"`   // TOP, JUNGLE, MIDDLE, BOTTOM, UTILITY
	Me    bool   `json:"me,omitempty"`
}
type MapFrame struct {
	T   float64           `json:"t"`
	Pos map[string][2]int `json:"pos"` // participant id -> x, y
}
type MapData struct {
	Side    string         `json:"side"`
	MatchID string         `json:"match_id"`
	Events  []MapEvent     `json:"events"`
	Path    []MapEvent     `json:"path"`
	Players []MapPlayer    `json:"players"`
	Frames  []MapFrame     `json:"frames"`
	Stats   *MatchStats    `json:"stats,omitempty"`
	Win     *bool          `json:"win,omitempty"` // official result
	Queue   int            `json:"queue,omitempty"`
	Board   *Board         `json:"board,omitempty"`   // the scoreboard of the 10 players
	Gold    [][3]int       `json:"gold,omitempty"`    // each minute: second, your team's gold, the enemy's
	Lanes14 map[string]int `json:"lanes14,omitempty"` // at minute 14, each lane: your team's gold minus the enemy's (TOP, JUNGLE, MIDDLE, BOT)
	WardT   [][2]float64   `json:"ward_t,omitempty"`  // your wards: second, 1 if it was a control ward (Riot doesn't say where)
	JgPath  [][3]float64   `json:"jg_path,omitempty"` // the enemy jungler each minute until 15: second, x, y (game units)
	JgChamp string         `json:"jg_champ,omitempty"`
	LensT   float64        `json:"lens_t,omitempty"` // when you bought Oracle Lens (0: never): from then on you only have the control wards you buy
	WardKl  int            `json:"ward_kills"`       // enemy wards you destroyed
	Fights  []MapFight     `json:"fights,omitempty"` // every champion kill of the game
	Deaths  []DeathInfo    `json:"death_info"`       // each of your deaths in detail (for the AI's explanation)
	Kills   []KillInfo     `json:"kill_info"`        // your kills and assists (when there's no death to explain)
}

// KillInfo: a kill you took part in
type KillInfo struct {
	T       float64  `json:"t"`
	Zone    string   `json:"zone"`
	Victim  string   `json:"victim"`
	Mine    bool     `json:"mine"`              // your kill (otherwise an assist)
	Helpers []string `json:"helpers,omitempty"` // allies who helped (besides you)
}

// DeathInfo: one of your deaths as Riot's timeline tells it
type DeathInfo struct {
	T       float64   `json:"t"`
	Zone    string    `json:"zone"`              // where (lane, river, which jungle)
	Killer  string    `json:"killer"`            // champion, or "torre" / "minions"
	Helpers []string  `json:"helpers,omitempty"` // the rest who helped
	Dmg     []DmgPart `json:"dmg,omitempty"`     // the damage you took, by source, largest first
	Lvl     [2]int    `json:"lvl"`               // your level, the killer's (at the minute before)
	Gold    int       `json:"gold"`              // your total gold minus the killer's (at the minute before)
	Allies  int       `json:"allies"`            // allies within ~2000 units of the place, the minute before
	Enemies int       `json:"enemies"`           // enemies within ~2000 units, the minute before
	JgWas   string    `json:"jg_was,omitempty"`  // where the enemy jungler was the minute before
	Wards   int       `json:"wards"`             // wards you had placed in the 3 minutes before
}
type DmgPart struct {
	Champ string `json:"champ"`
	What  string `json:"what"` // spells and basic attacks
	Dmg   int    `json:"dmg"`
}

// zoneName: a place on Summoner's Rift in words, from your side's point of view (blue base bottom-left)
func zoneName(x, y float64, side string) string {
	switch {
	case x < 3500 && y < 3500:
		return "base " + map[bool]string{true: "teva", false: "rival"}[side == "blue"]
	case x > 11500 && y > 11500:
		return "base " + map[bool]string{true: "rival", false: "teva"}[side == "blue"]
	case x < 2200 || y > 12800:
		return "carril de dalt"
	case y < 2200 || x > 12800:
		return "carril de baix"
	case x-y < 1400 && y-x < 1400:
		return "carril del mig"
	}
	d := x + y - 14800
	if d < 1500 && d > -1500 {
		if y > x {
			return "riu de dalt"
		}
		return "riu de baix"
	}
	part := map[bool]string{true: "part de dalt", false: "part de baix"}[y > x]
	if (d < 0) == (side == "blue") {
		return "la teva jungla (" + part + ")"
	}
	return "la jungla rival (" + part + ")"
}

// MapFight: one champion kill anywhere on the map
type MapFight struct {
	T      float64 `json:"t"`
	X      float64 `json:"x"`
	Y      float64 `json:"y"`
	Ours   bool    `json:"ours"` // killed by your team
	Victim string  `json:"v"`    // champion (Data Dragon id)
	Me     bool    `json:"me,omitempty"`
}

// Official end-of-game numbers for you and your lane opponent, and the differences at minutes 10 and 15.
type PlayerStats struct {
	Champ       string  `json:"champ"`
	K           int     `json:"k"`
	D           int     `json:"d"`
	A           int     `json:"a"`
	Gold        int     `json:"gold"`
	Damage      int     `json:"damage"`
	DamageShare float64 `json:"damage_share"` // 0-1 of the team's damage to champions
	KP          float64 `json:"kp"`           // 0-1
	Vision      int     `json:"vision"`
	Wards       int     `json:"wards"`
	WardsKilled int     `json:"wards_killed"`
	Control     int     `json:"control"`
	CS          int     `json:"cs"`
	SoloKills   int     `json:"solo_kills"`
	Plates      int     `json:"plates"`
	TimeDead    int     `json:"time_dead"`
}
type DiffAt struct {
	Min  int `json:"min"`
	Gold int `json:"gold"` // you minus opponent
	XP   int `json:"xp"`
	CS   int `json:"cs"`
}
type MatchStats struct {
	Me    PlayerStats  `json:"me"`
	Opp   *PlayerStats `json:"opp,omitempty"`
	Diffs []DiffAt     `json:"diffs"`
}

func puuidFor(c *Config, riotID string) (string, error) {
	if p := c.PUUIDs[riotID]; p != "" {
		return p, nil
	}
	parts := strings.SplitN(riotID, "#", 2)
	if len(parts) != 2 {
		return "", errors.New("no_riot_id")
	}
	var acc struct {
		PUUID string `json:"puuid"`
	}
	if err := riotGet(*c, "/riot/account/v1/accounts/by-riot-id/"+url.PathEscape(parts[0])+"/"+url.PathEscape(parts[1]), &acc); err != nil {
		return "", err
	}
	c.PUUIDs[riotID] = acc.PUUID
	// save on a fresh copy: never overwrite a key saved meanwhile from the settings
	fresh := loadConfig()
	if fresh.PUUIDs == nil {
		fresh.PUUIDs = map[string]string{}
	}
	fresh.PUUIDs[riotID] = acc.PUUID
	saveConfig(fresh)
	return acc.PUUID, nil
}

// displayName turns a Data Dragon id ("LeeSin", "MonkeyKing") into the name players know.
func displayName(id string) string {
	special := map[string]string{"MonkeyKing": "Wukong", "FiddleSticks": "Fiddlesticks", "Kaisa": "Kai'Sa", "KSante": "K'Sante", "Chogath": "Cho'Gath",
		"Khazix": "Kha'Zix", "Velkoz": "Vel'Koz", "KogMaw": "Kog'Maw", "RekSai": "Rek'Sai", "Belveth": "Bel'Veth", "DrMundo": "Dr. Mundo",
		"JarvanIV": "Jarvan IV", "Leblanc": "LeBlanc", "Nunu": "Nunu & Willump", "Renata": "Renata Glasc"}
	if v, ok := special[id]; ok {
		return v
	}
	var b strings.Builder
	for i, r := range id {
		if i > 0 && r >= 'A' && r <= 'Z' {
			b.WriteByte(' ')
		}
		b.WriteRune(r)
	}
	return b.String()
}

var monsterLabel = map[string]string{"DRAGON": "Drac", "BARON_NASHOR": "Baró", "RIFTHERALD": "Herald", "HORDE": "Larves", "ATAKHAN": "Atakhan"}

// fetchMap finds the Riot match for a recorded game and builds its map data.
func fetchMap(rec *Record, endUnix int64) (*MapData, error) {
	c := loadConfig()
	puuid, err := puuidFor(&c, rec.Me.RiotID)
	if err != nil {
		return nil, err
	}
	var ids []string
	if err := riotGet(c, "/lol/match/v5/matches/by-puuid/"+puuid+"/ids?start=0&count=5", &ids); err != nil {
		return nil, err
	}
	for _, id := range ids {
		var m matchDTO
		if err := riotGet(c, "/lol/match/v5/matches/"+id, &m); err != nil {
			return nil, err
		}
		me := -1
		team := 0
		riotChamp := ""
		champs := map[int]string{}
		teams := map[int]int{}
		for _, p := range m.Info.Participants {
			champs[p.ParticipantID], teams[p.ParticipantID] = p.ChampionName, p.TeamID
			if p.PUUID == puuid {
				me, team = p.ParticipantID, p.TeamID
				riotChamp = p.ChampionName
			}
		}
		end := m.Info.GameEndTimestamp / 1000
		diff := abs64(end - endUnix)
		// same player, and the same champion (Riot writes "Leblanc", "MonkeyKing"...) or an end time within 3 minutes
		ok := me >= 0 && end > 0 && ((sameChamp(riotChamp, rec.Me.ChampionName) && diff <= 15*60) || diff <= 3*60)
		if !ok {
			logf("Riot: la partida %s no es aquesta (campio %q/%q, diferencia d'hora %d s)", id, riotChamp, rec.Me.ChampionName, diff)
			continue
		}
		var tl timelineDTO
		if err := riotGet(c, "/lol/match/v5/matches/"+id+"/timeline", &tl); err != nil {
			return nil, err
		}
		md := &MapData{Side: map[int]string{100: "blue", 200: "red"}[team], MatchID: id, Events: []MapEvent{}, Path: []MapEvent{}, Players: []MapPlayer{}, Frames: []MapFrame{}}
		for _, p := range m.Info.Participants {
			md.Players = append(md.Players, MapPlayer{p.ParticipantID, p.ChampionName, map[int]string{100: "blue", 200: "red"}[p.TeamID], p.TeamPosition, p.ParticipantID == me})
		}
		key := fmt.Sprint(me)
		// official stats: me and the lane opponent (same position, other team)
		st := &MatchStats{Diffs: []DiffAt{}}
		oppID := 0
		var myPos string
		for _, p := range m.Info.Participants {
			if p.ParticipantID == me {
				myPos = p.TeamPosition
			}
		}
		for _, p := range m.Info.Participants {
			ps := PlayerStats{p.ChampionName, p.Kills, p.Deaths, p.Assists, p.GoldEarned, p.Damage, p.Challenges.DamageShare, p.Challenges.KP,
				p.VisionScore, p.WardsPlaced, p.WardsKilled, p.ControlWards, p.Minions + p.Neutral, int(p.Challenges.SoloKills), int(p.Challenges.Plates), p.TimeDead}
			if p.ParticipantID == me {
				st.Me = ps
			} else if p.TeamID != team && myPos != "" && p.TeamPosition == myPos {
				cp := ps
				st.Opp, oppID = &cp, p.ParticipantID
			}
		}
		if oppID > 0 {
			ok2 := fmt.Sprint(oppID)
			for _, f := range tl.Info.Frames {
				mn := int(f.Timestamp / 60000)
				if (mn == 10 || mn == 15 || mn == 20) && f.Timestamp%60000 < 30000 {
					a, okA := f.ParticipantFrames[key]
					b, okB := f.ParticipantFrames[ok2]
					if okA && okB {
						st.Diffs = append(st.Diffs, DiffAt{mn, a.TotalGold - b.TotalGold, a.XP - b.XP, (a.Minions + a.JungleCS) - (b.Minions + b.JungleCS)})
					}
				}
			}
		}
		md.Stats = st
		md.Board = makeBoard(c, &m, me)
		for _, p := range m.Info.Participants {
			if p.ParticipantID == me {
				w := p.Win
				md.Win = &w
			}
		}
		md.Queue = m.Info.QueueID
		// for the vision section: when you placed wards, and where the enemy jungler went during the laning phase
		ejg := 0
		for _, p := range m.Info.Participants {
			if p.TeamID != team && p.TeamPosition == "JUNGLE" {
				ejg = p.ParticipantID
			}
		}
		for _, f := range tl.Info.Frames {
			for _, e := range f.Events {
				if e.Type == "WARD_PLACED" && e.CreatorID == me && e.WardType != "UNDEFINED" {
					ctrl := 0.0
					if e.WardType == "CONTROL_WARD" {
						ctrl = 1
					}
					md.WardT = append(md.WardT, [2]float64{float64(e.Timestamp) / 1000, ctrl})
				}
				if e.Type == "ITEM_PURCHASED" && e.ParticipantID == me && e.ItemID == 3364 && md.LensT == 0 {
					md.LensT = float64(e.Timestamp) / 1000
				}
				if e.Type == "WARD_KILL" && e.KillerID == me {
					md.WardKl++
				}
			}
			if ejg != 0 && f.Timestamp >= 55000 && f.Timestamp <= 15*60000+5000 {
				if pf, ok := f.ParticipantFrames[strconv.Itoa(ejg)]; ok {
					md.JgPath = append(md.JgPath, [3]float64{float64(f.Timestamp / 1000), pf.Position.X, pf.Position.Y})
				}
			}
		}
		if ejg != 0 {
			md.JgChamp = displayName(champs[ejg])
		}
		for _, f := range tl.Info.Frames {
			g := [3]int{int(f.Timestamp / 1000), 0, 0}
			for k, pf := range f.ParticipantFrames {
				id, _ := strconv.Atoi(k)
				if teams[id] == team {
					g[1] += pf.TotalGold
				} else {
					g[2] += pf.TotalGold
				}
			}
			md.Gold = append(md.Gold, g)
			if f.Timestamp/60000 == 14 && md.Lanes14 == nil {
				md.Lanes14 = map[string]int{}
				for _, p := range m.Info.Participants {
					pf, ok := f.ParticipantFrames[strconv.Itoa(p.ParticipantID)]
					lane := map[string]string{"TOP": "TOP", "JUNGLE": "JUNGLE", "MIDDLE": "MIDDLE", "BOTTOM": "BOT", "UTILITY": "BOT"}[p.TeamPosition]
					if !ok || lane == "" {
						continue
					}
					if p.TeamID == team {
						md.Lanes14[lane] += pf.TotalGold
					} else {
						md.Lanes14[lane] -= pf.TotalGold
					}
				}
			}
			for _, e := range f.Events {
				if e.Type == "CHAMPION_KILL" && e.Position != nil {
					md.Fights = append(md.Fights, MapFight{float64(e.Timestamp) / 1000, e.Position.X, e.Position.Y, teams[e.KillerID] == team || (e.KillerID == 0 && teams[e.VictimID] != team), champs[e.VictimID], e.VictimID == me || e.KillerID == me})
				}
			}
			fr := MapFrame{T: float64(f.Timestamp) / 1000, Pos: map[string][2]int{}}
			for k, pf := range f.ParticipantFrames {
				fr.Pos[k] = [2]int{int(pf.Position.X), int(pf.Position.Y)}
			}
			md.Frames = append(md.Frames, fr)
			if pf, ok := f.ParticipantFrames[key]; ok && f.Timestamp > 0 {
				md.Path = append(md.Path, MapEvent{T: float64(f.Timestamp) / 1000, X: pf.Position.X, Y: pf.Position.Y, Kind: "path"})
			}
			for _, e := range f.Events {
				if e.Position == nil {
					continue
				}
				t := float64(e.Timestamp) / 1000
				switch e.Type {
				case "CHAMPION_KILL":
					helped := false
					for _, a := range e.AssistingParticipantIDs {
						if a == me {
							helped = true
						}
					}
					others := len(e.AssistingParticipantIDs)
					switch {
					case e.VictimID == me:
						by := displayName(champs[e.KillerID])
						if champs[e.KillerID] == "" {
							by = "torre o minions"
						}
						if others > 0 {
							by += fmt.Sprintf(" +%d", others)
						}
						md.Events = append(md.Events, MapEvent{t, e.Position.X, e.Position.Y, "death", "Mort per " + by, false, champs[e.KillerID]})
					case e.KillerID == me:
						md.Events = append(md.Events, MapEvent{t, e.Position.X, e.Position.Y, "kill", "Kill a " + displayName(champs[e.VictimID]), false, champs[e.VictimID]})
					case helped:
						md.Events = append(md.Events, MapEvent{t, e.Position.X, e.Position.Y, "assist", "Assistència a " + displayName(champs[e.VictimID]), false, champs[e.VictimID]})
					}
				case "ELITE_MONSTER_KILL":
					lbl := monsterLabel[e.MonsterType]
					if lbl == "" {
						lbl = "Objectiu"
					}
					md.Events = append(md.Events, MapEvent{t, e.Position.X, e.Position.Y, "objective", lbl, e.KillerTeamID == team, ""})
				}
			}
		}
		roles := map[int]string{}
		for _, p := range m.Info.Participants {
			roles[p.ParticipantID] = p.TeamPosition
		}
		md.Deaths = deathDetails(&tl, me, team, teams, champs, roles, md.Side)
		md.Kills = []KillInfo{}
		for _, f := range tl.Info.Frames {
			for _, e := range f.Events {
				if e.Type != "CHAMPION_KILL" || e.Position == nil || teams[e.VictimID] == team {
					continue
				}
				k := KillInfo{T: float64(e.Timestamp) / 1000, Zone: zoneName(e.Position.X, e.Position.Y, md.Side), Victim: displayName(champs[e.VictimID]), Mine: e.KillerID == me}
				in := k.Mine
				for _, a := range append([]int{e.KillerID}, e.AssistingParticipantIDs...) {
					if a == me {
						in = true
					} else if champs[a] != "" {
						k.Helpers = append(k.Helpers, displayName(champs[a]))
					}
				}
				if in {
					md.Kills = append(md.Kills, k)
				}
			}
		}
		logf("Mapa creat a partir de %s: %d esdeveniments, %d punts de recorregut, costat %s", id, len(md.Events), len(md.Path), md.Side)
		return md, nil
	}
	return nil, errors.New("not_yet")
}

func abs64(v int64) int64 {
	if v < 0 {
		return -v
	}
	return v
}

// ------------------------------------------------------------ report files

type savedGame struct {
	Report  map[string]any  `json:"report"`
	Raw     json.RawMessage `json:"raw"`
	EndUnix int64           `json:"end_unix"`
	GameID  int64           `json:"game_id,omitempty"` // from the client during the game (the replay's id)
}

func gameFile(date string) string {
	stamp := strings.NewReplacer(":", "", " ", "_").Replace(date)
	return filepath.Join(dataDir(), stamp+".json")
}

func readGame(date string) (*savedGame, *Record, error) {
	b, err := os.ReadFile(gameFile(date))
	if err != nil {
		return nil, nil, err
	}
	var g savedGame
	if err := json.Unmarshal(b, &g); err != nil {
		return nil, nil, err
	}
	var rec Record
	json.Unmarshal(g.Raw, &rec)
	if g.EndUnix == 0 { // older files: the report time is the end of the game
		if t, err := time.ParseInLocation("2006-01-02 15:04", rec.Date, time.Local); err == nil {
			g.EndUnix = t.Unix()
		}
	}
	return &g, &rec, nil
}

var mapMu sync.Mutex

// addMap downloads and stores the map for one saved game. Returns a short status code.
func addMap(date string) (string, error) {
	mapMu.Lock()
	defer mapMu.Unlock()
	g, rec, err := readGame(date)
	if err != nil {
		return "", err
	}
	// fetch again when the game's result is unknown (the game closed before its end event was read)
	if g.Report["map"] != nil && g.Report["result"] != nil && g.Report["result"] != "" {
		if mm, _ := g.Report["map"].(map[string]any); mm != nil && mm["board"] != nil && mm["gold"] != nil && mm["death_info"] != nil && mm["kill_info"] != nil {
			return "ok", nil
		} // saved before the scoreboard or the teams' gold existed: read it again (once)
	}
	md, err := fetchMap(rec, g.EndUnix)
	if err != nil {
		return "", err
	}
	g.Report["map"] = md
	if md.Win != nil {
		res := map[bool]string{true: "Victòria", false: "Derrota"}[*md.Win]
		if old, _ := g.Report["result"].(string); old != res {
			logf("Resultat de %s segons Riot: %s (abans %q)", date, res, old)
		}
		g.Report["result"] = res
	}
	if q := queueName[md.Queue]; q != "" {
		g.Report["queue"] = q
	}
	b, _ := json.Marshal(g)
	err = os.WriteFile(gameFile(date), b, 0o644)
	// a recent game whose Riot data came by another way (the app restarted for an update, the history asked for it):
	// its deaths are explained too
	if err == nil && time.Since(time.Unix(g.EndUnix, 0)) < 3*time.Hour {
		go vcEnqueue(date, false)
	}
	return "ok", err
}

// autoMap retries for a few minutes after a game: Riot publishes the match with some delay.
func autoMap(date string) {
	if loadConfig().RiotKey == "" {
		return
	}
	for i := 0; i < 12; i++ {
		time.Sleep(retryWait())
		_, err := addMap(date)
		logf("Mapa automatic, intent %d per a %s: %v", i+1, date, err)
		if err == nil {
			vcEnqueue(date, false) // the video coach: the replay exists now that Riot has the match
			return
		}
		if err.Error() != "not_yet" && err.Error() != "rate_limited" && !strings.Contains(err.Error(), "http_5") {
			return
		}
	}
}

var (
	ddMu      sync.Mutex
	ddVersion string
	ddAt      time.Time
)

// dataDragonVersion returns the current Data Dragon version (cached for 6 hours).
func dataDragonVersion() string {
	ddMu.Lock()
	defer ddMu.Unlock()
	if ddVersion != "" && time.Since(ddAt) < 6*time.Hour {
		return ddVersion
	}
	r, err := riotHTTP.Get("https://ddragon.leagueoflegends.com/api/versions.json")
	if err == nil {
		defer r.Body.Close()
		var vs []string
		if json.NewDecoder(r.Body).Decode(&vs) == nil && len(vs) > 0 {
			ddVersion, ddAt = vs[0], time.Now()
		}
	}
	if ddVersion == "" {
		return "16.21.1"
	}
	return ddVersion
}

// sameChamp compares a Riot match championName (an internal id like "Leblanc" or "MonkeyKing") with the
// live game's display name ("LeBlanc", "Wukong", "Nunu & Willump").
func sameChamp(riot, live string) bool {
	key := func(x string) string {
		var b strings.Builder
		for _, r := range strings.ToLower(x) {
			if r >= 'a' && r <= 'z' {
				b.WriteRune(r)
			}
		}
		return b.String()
	}
	alias := map[string]string{"monkeyking": "wukong", "nunu": "nunuwillump", "renata": "renataglasc"}
	a, b := key(riot), key(live)
	if v, ok := alias[a]; ok {
		a = v
	}
	if v, ok := alias[b]; ok {
		b = v
	}
	return a != "" && (a == b || strings.HasPrefix(a, b) || strings.HasPrefix(b, a))
}

var queueName = map[int]string{420: "Solo/Duo", 440: "Flex", 400: "Normal (reclutament)", 430: "Normal (a cegues)", 490: "Partida ràpida", 450: "ARAM", 0: ""}

// fixUnknownResults: saved games whose result was never read get it from Riot (one request at a time).
func fixUnknownResults() {
	if loadConfig().RiotKey == "" {
		return
	}
	dir := dataDir()
	entries, _ := os.ReadDir(dir)
	n := 0
	for i := len(entries) - 1; i >= 0 && n < 15; i-- {
		name := entries[i].Name()
		if !strings.HasSuffix(name, ".json") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			continue
		}
		var f struct {
			Report map[string]any `json:"report"`
		}
		if json.Unmarshal(b, &f) != nil || f.Report == nil {
			continue
		}
		if r, _ := f.Report["result"].(string); r != "" {
			continue
		}
		date, _ := f.Report["date"].(string)
		if date == "" {
			continue
		}
		n++
		st, err := addMap(date)
		logf("Resultat pendent de %s: %s %v", date, st, err)
		time.Sleep(1500 * time.Millisecond)
	}
}

// deathDetails reads each of your deaths from the timeline: where, who and with what, who was near the minute before
func deathDetails(tl *timelineDTO, me, team int, teams map[int]int, champs map[int]string, roles map[int]string, side string) []DeathInfo {
	out := []DeathInfo{}
	frames := tl.Info.Frames
	wards := []int64{}
	for _, f := range frames {
		for _, e := range f.Events {
			if e.Type == "WARD_PLACED" && e.CreatorID == me && e.WardType != "UNDEFINED" {
				wards = append(wards, e.Timestamp)
			}
		}
	}
	dist := func(a, b pos) float64 { dx, dy := a.X-b.X, a.Y-b.Y; return math.Sqrt(dx*dx + dy*dy) }
	jg := 0
	for id, r := range roles {
		if r == "JUNGLE" && teams[id] != team {
			jg = id
		}
	}
	for _, f := range frames {
		for _, e := range f.Events {
			if e.Type != "CHAMPION_KILL" || e.VictimID != me || e.Position == nil {
				continue
			}
			d := DeathInfo{T: float64(e.Timestamp) / 1000, Zone: zoneName(e.Position.X, e.Position.Y, side), Killer: displayName(champs[e.KillerID])}
			if champs[e.KillerID] == "" {
				d.Killer = "torre o minions"
			}
			for _, a := range e.AssistingParticipantIDs {
				d.Helpers = append(d.Helpers, displayName(champs[a]))
			}
			// the damage taken, grouped by source
			agg := map[string]*DmgPart{}
			order := []string{}
			for _, v := range e.VictimDamageReceived {
				src := displayName(v.Name)
				if src == "" {
					src = v.Name
				}
				if agg[src] == nil {
					agg[src] = &DmgPart{Champ: src}
					order = append(order, src)
				}
				agg[src].Dmg += v.Magic + v.Physical + v.True
				what := v.Spell
				if v.Basic {
					what = "atacs bàsics"
				} else if v.Slot >= 0 && v.Slot <= 3 {
					what = []string{"Q", "W", "E", "R"}[v.Slot]
				}
				if what != "" && !strings.Contains(agg[src].What, what) {
					if agg[src].What != "" {
						agg[src].What += ", "
					}
					agg[src].What += what
				}
			}
			for _, k := range order {
				if agg[k].Dmg > 0 {
					d.Dmg = append(d.Dmg, *agg[k])
				}
			}
			sort.Slice(d.Dmg, func(i, j int) bool { return d.Dmg[i].Dmg > d.Dmg[j].Dmg })
			if len(d.Dmg) > 4 {
				d.Dmg = d.Dmg[:4]
			}
			// the frame of the minute before the death
			fi := int(e.Timestamp / 60000)
			if fi >= len(frames) {
				fi = len(frames) - 1
			}
			if fi >= 0 {
				pf := frames[fi].ParticipantFrames
				if a, ok := pf[fmt.Sprint(me)]; ok {
					if b, ok := pf[fmt.Sprint(e.KillerID)]; ok {
						d.Lvl = [2]int{a.Level, b.Level}
						d.Gold = a.TotalGold - b.TotalGold
					} else {
						d.Lvl = [2]int{a.Level, 0}
					}
				}
				for k, p := range pf {
					id, _ := strconv.Atoi(k)
					if id == me || dist(p.Position, *e.Position) > 2000 {
						continue
					}
					if teams[id] == team {
						d.Allies++
					} else {
						d.Enemies++
					}
				}
				if j, ok := pf[fmt.Sprint(jg)]; ok && jg > 0 {
					d.JgWas = displayName(champs[jg]) + " era a " + zoneName(j.Position.X, j.Position.Y, side)
				}
			}
			for _, w := range wards {
				if w <= e.Timestamp && w > e.Timestamp-180000 {
					d.Wards++
				}
			}
			out = append(out, d)
		}
	}
	return out
}
