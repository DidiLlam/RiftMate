package main

import (
	"math"
	"sort"
	"strings"
)

// The scoreboard of a finished game (Riot's match data): the 10 players with champion, level, spells,
// runes, items, KDA, farm, kill participation, damage and rank, the totals and objectives of each team and
// the bans. Players are ranked 1-10 by the app's own score (KDA, kill participation, share of the team's
// damage, farm or vision, deaths), MVP for the best of the winners and ACE for the best of the losers.

type BoardPlayer struct {
	Name     string    `json:"name"`
	Tag      string    `json:"tag,omitempty"`
	Champ    string    `json:"champ"`
	Level    int       `json:"level"`
	Spells   [2]string `json:"spells"`
	Keystone int       `json:"keystone"`
	SubStyle int       `json:"sub"`
	Items    [7]int    `json:"items"`
	K        int       `json:"k"`
	D        int       `json:"d"`
	A        int       `json:"a"`
	CS       int       `json:"cs"`
	KP       float64   `json:"kp"`
	Damage   int       `json:"dmg"`
	Gold     int       `json:"gold"`
	Vision   int       `json:"vision"`
	Team     int       `json:"team"`
	Pos      string    `json:"pos"`
	Me       bool      `json:"me,omitempty"`
	Tier     string    `json:"tier,omitempty"`
	Rank     string    `json:"rank,omitempty"`
	Score    float64   `json:"score"`
	Place    int       `json:"place"`
	Badge    string    `json:"badge,omitempty"`
	FirstB   bool      `json:"fb,omitempty"`    // first blood
	Multi    int       `json:"multi,omitempty"` // largest multikill (2 double … 5 penta)
	Remake   bool      `json:"remake,omitempty"`
}

type BoardTeam struct {
	ID      int      `json:"id"`
	Win     bool     `json:"win"`
	K       int      `json:"k"`
	D       int      `json:"d"`
	A       int      `json:"a"`
	Gold    int      `json:"gold"`
	Damage  int      `json:"dmg"`
	Towers  int      `json:"towers"`
	Inhibs  int      `json:"inhibs"`
	Dragons int      `json:"dragons"`
	Barons  int      `json:"barons"`
	Heralds int      `json:"heralds"`
	Grubs   int      `json:"grubs"`
	Atakhan int      `json:"atakhan"`
	Bans    []string `json:"bans"`
}

type Board struct {
	Minutes float64       `json:"minutes"`
	Teams   []BoardTeam   `json:"teams"`
	Players []BoardPlayer `json:"players"`
}

var spellNames = map[int]string{4: "Flash", 14: "Ignite", 7: "Heal", 12: "Teleport", 3: "Exhaust", 21: "Barrier", 6: "Ghost", 1: "Cleanse", 11: "Smite"}

func makeBoard(c Config, m *matchDTO, me int) *Board { return makeBoardOpt(c, m, me, true) }

// makeBoardOpt: withRanks=false skips the 10 rank lookups (match history list: they would cost 10 calls a game)
func makeBoardOpt(c Config, m *matchDTO, me int, withRanks bool) *Board {
	b := &Board{Minutes: math.Max(1, float64(m.Info.GameDuration)/60)}
	teamIdx := map[int]int{}
	for _, t := range m.Info.Teams {
		bt := BoardTeam{ID: t.TeamID, Win: t.Win, Bans: []string{}}
		bt.Towers, bt.Inhibs, bt.Dragons, bt.Barons = t.Objectives["tower"].Kills, t.Objectives["inhibitor"].Kills, t.Objectives["dragon"].Kills, t.Objectives["baron"].Kills
		bt.Heralds, bt.Grubs, bt.Atakhan = t.Objectives["riftHerald"].Kills, t.Objectives["horde"].Kills, t.Objectives["atakhan"].Kills
		for _, ban := range t.Bans {
			if ban.ChampionID > 0 {
				if ch := championByKey(ban.ChampionID); ch.ID != "" {
					bt.Bans = append(bt.Bans, ch.ID)
				}
			}
		}
		teamIdx[t.TeamID] = len(b.Teams)
		b.Teams = append(b.Teams, bt)
	}
	for _, p := range m.Info.Participants {
		name := p.GameName
		if name == "" {
			name = p.SummonerName
		}
		bp := BoardPlayer{Name: name, Tag: p.TagLine, Champ: p.ChampionName, Level: p.ChampLevel,
			Spells: [2]string{spellNames[p.Summoner1], spellNames[p.Summoner2]},
			Items:  [7]int{p.Item0, p.Item1, p.Item2, p.Item3, p.Item4, p.Item5, p.Item6},
			K:      p.Kills, D: p.Deaths, A: p.Assists, CS: p.Minions + p.Neutral, KP: p.Challenges.KP, Damage: p.Damage,
			Gold: p.GoldEarned, Vision: p.VisionScore, Team: p.TeamID, Pos: p.TeamPosition, Me: p.ParticipantID == me,
			FirstB: p.FirstBlood, Multi: p.LargestMulti, Remake: p.EarlySurr}
		if len(p.Perks.Styles) > 0 && len(p.Perks.Styles[0].Selections) > 0 {
			bp.Keystone = p.Perks.Styles[0].Selections[0].Perk
		}
		if len(p.Perks.Styles) > 1 {
			bp.SubStyle = p.Perks.Styles[1].Style
		}
		if i, ok := teamIdx[p.TeamID]; ok {
			t := &b.Teams[i]
			t.K, t.D, t.A, t.Gold, t.Damage = t.K+p.Kills, t.D+p.Deaths, t.A+p.Assists, t.Gold+p.GoldEarned, t.Damage+p.Damage
		}
		// rank (Solo/Duo) of every player, public data
		if withRanks && p.PUUID != "" {
			var list []leagueEntry
			if riotGetHostQ(c, platformHost(c), "/lol/league/v4/entries/by-puuid/"+p.PUUID, &list, true) == nil {
				for _, e := range list {
					if e.QueueType == "RANKED_SOLO_5x5" {
						bp.Tier, bp.Rank = e.Tier, e.Rank
					}
				}
			}
		}
		b.Players = append(b.Players, bp)
	}
	// the app's score
	teamDmg := map[int]int{}
	for _, t := range b.Teams {
		teamDmg[t.ID] = t.Damage
	}
	for i := range b.Players {
		p := &b.Players[i]
		kda := math.Min(10, float64(p.K+p.A)/math.Max(1, float64(p.D)))
		share := float64(p.Damage) / math.Max(1, float64(teamDmg[p.Team]))
		perMin := float64(p.CS) / b.Minutes / 10
		if strings.EqualFold(p.Pos, "UTILITY") {
			perMin = float64(p.Vision) / b.Minutes / 2.5
		}
		p.Score = kda*0.35 + p.KP*3 + share*6 + math.Min(1.2, perMin)*1.5 - float64(p.D)/b.Minutes*2
		if i, ok := teamIdx[p.Team]; ok && b.Teams[i].Win {
			p.Score += 0.5
		}
		p.Score = math.Round(p.Score*100) / 100
	}
	order := make([]int, len(b.Players))
	for i := range order {
		order[i] = i
	}
	sort.SliceStable(order, func(i, j int) bool { return b.Players[order[i]].Score > b.Players[order[j]].Score })
	mvp, ace := false, false
	for place, i := range order {
		p := &b.Players[i]
		p.Place = place + 1
		win := false
		if t, ok := teamIdx[p.Team]; ok {
			win = b.Teams[t].Win
		}
		if win && !mvp {
			p.Badge, mvp = "MVP", true
		} else if !win && !ace {
			p.Badge, ace = "ACE", true
		}
	}
	return b
}
