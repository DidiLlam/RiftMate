package main

import (
	"fmt"
	"math"
	"strings"
)

type Spell struct {
	DisplayName    string `json:"displayName"`
	RawDisplayName string `json:"rawDisplayName"`
}
type Player struct {
	RiotID         string `json:"riotId"`
	RiotIDGameName string `json:"riotIdGameName"`
	SummonerName   string `json:"summonerName"`
	ChampionName   string `json:"championName"`
	Team           string `json:"team"`
	Position       string `json:"position"`
	Level          int    `json:"level"`
	Scores         struct {
		CreepScore int     `json:"creepScore"`
		WardScore  float64 `json:"wardScore"`
	} `json:"scores"`
	SummonerSpells struct {
		One Spell `json:"summonerSpellOne"`
		Two Spell `json:"summonerSpellTwo"`
	} `json:"summonerSpells"`
}
type Event struct {
	EventID    int      `json:"EventID"`
	EventName  string   `json:"EventName"`
	EventTime  float64  `json:"EventTime"`
	KillerName string   `json:"KillerName"`
	VictimName string   `json:"VictimName"`
	Assisters  []string `json:"Assisters"`
	Result     string   `json:"Result"`
	Stolen     string   `json:"Stolen"`
}
type AllGame struct {
	ActivePlayer struct {
		RiotID        string `json:"riotId"`
		SummonerName  string `json:"summonerName"`
		ChampionStats struct {
			CurrentHealth float64 `json:"currentHealth"`
			MaxHealth     float64 `json:"maxHealth"`
		} `json:"championStats"`
	} `json:"activePlayer"`
	AllPlayers []Player `json:"allPlayers"`
	GameData   struct {
		GameTime *float64 `json:"gameTime"`
		GameMode string   `json:"gameMode"`
	} `json:"gameData"`
	Events struct {
		Events []Event `json:"Events"`
	} `json:"events"`
}
type Snap struct {
	T      float64 `json:"t"`
	CS     int     `json:"cs"`
	Lvl    *int    `json:"lvl"`
	OppCS  *int    `json:"opp_cs"`
	OppLvl *int    `json:"opp_lvl"`
}
type Record struct {
	Date     string   `json:"date"`
	Me       Player   `json:"me"`
	Players  []Player `json:"players"`
	Snaps    []Snap   `json:"snaps"`
	Events   []Event  `json:"events"`
	Duration float64  `json:"duration"`
	Result   string   `json:"result"`
	Mode     string   `json:"mode,omitempty"` // the game's mode (PRACTICETOOL is not kept)
}

type Death struct {
	T     float64  `json:"t"`
	Phase string   `json:"phase"`
	By    []string `json:"by"`
	Kind  string   `json:"kind"`
}
type KA struct {
	T      float64 `json:"t"`
	Phase  string  `json:"phase"`
	Victim *string `json:"victim"`
	Solo   bool    `json:"solo"`
	Roam   bool    `json:"roam"`
}
type RivalRoam struct {
	T      float64 `json:"t"`
	Victim *string `json:"victim"`
}
type Objective struct {
	T    float64 `json:"t"`
	Obj  string  `json:"obj"`
	Ours bool    `json:"ours"`
	Me   bool    `json:"me"`
}

const lanePhase, midPhase = 14 * 60.0, 25 * 60.0

func ip(v int) *int            { return &v }
func fp(v float64) *float64    { return &v }
func sp(v string) *string      { return &v }
func round1(v float64) float64 { return math.Round(v*10) / 10 }

func phase(t float64) string {
	if t < lanePhase {
		return "línia"
	}
	if t < midPhase {
		return "mitja"
	}
	return "final"
}

// valueAt: latest snapshot at or before t (nil if the game hadn't reached t).
func valueAt(snaps []Snap, t float64, get func(Snap) *int) *int {
	if len(snaps) == 0 || snaps[len(snaps)-1].T < t-15 {
		return nil
	}
	var best *Snap
	for i := range snaps {
		if snaps[i].T <= t {
			best = &snaps[i]
		}
	}
	if best == nil {
		return nil
	}
	return get(*best)
}

func analyse(rec *Record) map[string]any {
	myID := pidOf(&rec.Me)
	me := rec.Me
	for _, p := range rec.Players {
		if pidOf(&p) == myID {
			me = p
		}
	}
	// every name an event may use for a player
	names := map[string]Player{}
	for _, p := range rec.Players {
		if p.ChampionName != "" {
			if _, ok := names[p.ChampionName]; !ok {
				names[p.ChampionName] = p
			}
		}
	}
	for _, p := range rec.Players {
		for _, v := range []string{p.RiotID, p.RiotIDGameName, p.SummonerName} {
			if v != "" {
				names[v] = p
				names[strings.SplitN(v, "#", 2)[0]] = p
			}
		}
	}
	myTeam := me.Team
	pos := strings.ToUpper(me.Position)
	var opp, jungler *Player
	foes := []Player{}
	for _, p := range rec.Players {
		if p.Team != myTeam {
			foes = append(foes, p)
		}
	}
	for i := range foes {
		if pos != "" && strings.ToUpper(foes[i].Position) == pos && opp == nil {
			opp = &foes[i]
		}
		if strings.ToUpper(foes[i].Position) == "JUNGLE" && jungler == nil {
			jungler = &foes[i]
		}
	}
	if jungler == nil {
		for i := range foes {
			if strings.Contains(foes[i].SummonerSpells.One.RawDisplayName, "Smite") || strings.Contains(foes[i].SummonerSpells.Two.RawDisplayName, "Smite") {
				jungler = &foes[i]
				break
			}
		}
	}
	isMe := func(n string) bool { p, ok := names[n]; return ok && pidOf(&p) == myID }
	champ := func(n string) *string {
		if p, ok := names[n]; ok {
			return sp(p.ChampionName)
		}
		return nil
	}
	teamOf := func(n string) string {
		if p, ok := names[n]; ok {
			return p.Team
		}
		return ""
	}
	var oppChamp *string
	if opp != nil {
		oppChamp = sp(opp.ChampionName)
	}
	eq := func(a, b *string) bool { return a != nil && b != nil && *a == *b }

	deaths, kills, assists := []Death{}, []KA{}, []KA{}
	rivalRoams, objectives := []RivalRoam{}, []Objective{}
	teamKills := 0
	labels := map[string]string{"DragonKill": "Drac", "HeraldKill": "Herald", "BaronKill": "Baró", "HordeKill": "Larves", "AtakhanKill": "Atakhan"}
	for _, ev := range rec.Events {
		t, killer, victim, helpers := ev.EventTime, ev.KillerName, ev.VictimName, ev.Assisters
		if ev.EventName == "ChampionKill" {
			kTeam := teamOf(killer)
			if kTeam == "" {
				if teamOf(victim) == "CHAOS" {
					kTeam = "ORDER"
				} else {
					kTeam = "CHAOS"
				}
			}
			if kTeam == myTeam {
				teamKills++
			}
			enemies := []string{}
			for _, x := range append([]string{killer}, helpers...) {
				if c := champ(x); c != nil && teamOf(x) != myTeam {
					enemies = append(enemies, *c)
				}
			}
			meHelped := false
			for _, h := range helpers {
				if isMe(h) {
					meHelped = true
				}
			}
			if isMe(victim) {
				var kind string
				switch {
				case champ(killer) == nil:
					kind = "torre o minions"
				case len(enemies) >= 4:
					kind = "baralla d'equip"
				case len(enemies) == 1 && eq(champ(killer), oppChamp):
					kind = "1v1 contra el rival"
				case len(enemies) == 1:
					kind = "1v1 contra un altre"
				case jungler != nil && contains(enemies, jungler.ChampionName) && t < lanePhase:
					kind = "gank del jungla"
				default:
					kind = fmt.Sprintf("%d contra 1", len(enemies))
				}
				by := enemies
				if len(by) == 0 {
					by = []string{killer}
				}
				deaths = append(deaths, Death{t, phase(t), by, kind})
			} else if isMe(killer) || meHelped {
				v := champ(victim)
				e := KA{T: t, Phase: phase(t), Victim: v, Solo: isMe(killer) && len(helpers) == 0,
					Roam: t < lanePhase && v != nil && !eq(v, oppChamp)}
				if isMe(killer) {
					kills = append(kills, e)
				} else {
					assists = append(assists, e)
				}
			}
			// lane opponent helping elsewhere before 14'
			if opp != nil && t < lanePhase && teamOf(victim) == myTeam && !isMe(victim) {
				in := eq(champ(killer), oppChamp)
				for _, h := range helpers {
					if eq(champ(h), oppChamp) {
						in = true
					}
				}
				if in {
					rivalRoams = append(rivalRoams, RivalRoam{t, champ(victim)})
				}
			}
		} else if lbl, ok := labels[ev.EventName]; ok {
			ours, took := teamOf(killer) == myTeam, isMe(killer)
			for _, h := range helpers {
				if teamOf(h) == myTeam {
					ours = true
				}
				if isMe(h) {
					took = true
				}
			}
			objectives = append(objectives, Objective{t, lbl, ours, took})
		}
	}

	snaps, dur := rec.Snaps, rec.Duration
	csOf := func(s Snap) *int { return ip(s.CS) }
	oppCsOf := func(s Snap) *int { return s.OppCS }
	cs := map[string]*int{}
	ocs := map[string]*int{}
	// every 5 minutes from minute 10 until the end of the game
	for m := 10; float64(m*60) <= dur; m += 5 {
		k := fmt.Sprint(m)
		cs[k] = valueAt(snaps, float64(m*60), csOf)
		ocs[k] = valueAt(snaps, float64(m*60), oppCsOf)
	}
	// minute by minute, for the chart
	curve := [][3]*int{}
	for m := 1; float64(m*60) <= dur; m++ {
		curve = append(curve, [3]*int{ip(m), valueAt(snaps, float64(m*60), csOf), valueAt(snaps, float64(m*60), oppCsOf)})
	}
	var oppFinal *int
	if len(snaps) > 0 {
		oppFinal = snaps[len(snaps)-1].OppCS
	}
	lvl10 := []*int{valueAt(snaps, 600, func(s Snap) *int { return s.Lvl }), valueAt(snaps, 600, func(s Snap) *int { return s.OppLvl })}
	finalCS := 0
	if len(snaps) > 0 {
		finalCS = snaps[len(snaps)-1].CS
	}
	rate := func(a, b *int, t0, t1 float64) *float64 {
		if a == nil || b == nil || t1 <= t0 {
			return nil
		}
		return fp(round1(float64(*b-*a) / ((t1 - t0) / 60)))
	}
	csLane := rate(ip(0), cs["10"], 0, 600)
	var csMid, csLate *float64
	if dur > lanePhase+60 {
		endMid := math.Min(dur, midPhase)
		csMid = rate(valueAt(snaps, lanePhase, csOf), valueAt(snaps, endMid, csOf), lanePhase, endMid)
	}
	if dur > midPhase+60 {
		csLate = rate(valueAt(snaps, midPhase, csOf), ip(finalCS), midPhase, dur)
	}
	k, d, a := len(kills), len(deaths), len(assists)
	var kp *int
	if teamKills > 0 {
		kp = ip(int(math.Round(100 * float64(k+a) / float64(teamKills))))
	}
	duelW, duelL, deaths15, ganked := 0, 0, 0, 0
	for _, x := range kills {
		if x.Solo && eq(x.Victim, oppChamp) {
			duelW++
		}
	}
	for _, x := range deaths {
		if x.Kind == "1v1 contra el rival" {
			duelL++
		}
		if x.T < 15*60 {
			deaths15++
		}
		if x.Kind == "gank del jungla" {
			ganked++
		}
	}
	roams := []KA{}
	for _, x := range append(append([]KA{}, kills...), assists...) {
		if x.Roam {
			roams = append(roams, x)
		}
	}
	role := "MIDDLE"
	switch pos {
	case "TOP", "JUNGLE", "MIDDLE", "BOTTOM", "UTILITY":
		role = pos
	}
	laner := role == "TOP" || role == "MIDDLE" || role == "BOTTOM"
	vision := int(math.Round(me.Scores.WardScore))
	tips := []string{}
	if laner && cs["10"] != nil && *cs["10"] < 80 {
		tips = append(tips, fmt.Sprintf("Al minut 10 portaves %d de farm, i l'objectiu és 80: prioritza els últims cops a la línia.", *cs["10"]))
	}
	if role != "UTILITY" && csLane != nil && csMid != nil && *csLane > 0 && *csMid < *csLane-1 {
		tips = append(tips, fmt.Sprintf("El farm cau de %s a %s CS/min a la mitja partida: recull onades laterals entre baralles.", num(*csLane), num(*csMid)))
	}
	if deaths15 >= 2 {
		tips = append(tips, fmt.Sprintf("%d morts abans del minut 15. Revisa-les a la repetició: on era el jungla rival?", deaths15))
	}
	if ganked >= 2 {
		tips = append(tips, "T'han gankejat més d'un cop: no empenyis sense visió al riu quan el jungla rival no es veu.")
	}
	if len(rivalRoams) >= 2 && role == "JUNGLE" {
		tips = append(tips, fmt.Sprintf("El jungla rival ha fet %d ganks amb èxit abans del minut 14: segueix-li la ruta i juga a l'altra banda o contragankeja.", len(rivalRoams)))
	} else if len(rivalRoams) >= 2 {
		tips = append(tips, fmt.Sprintf("El teu rival ha participat en %d kills a altres línies abans del minut 14: segueix-lo quan desaparegui.", len(rivalRoams)))
	}
	if role == "JUNGLE" && len(roams) <= 1 {
		tips = append(tips, fmt.Sprintf("Només %d gank amb èxit abans del minut 14: busca línies amb l'onada a favor i el rival sense Flash.", len(roams)))
	}
	if role == "UTILITY" && dur > 900 && float64(vision)/(dur/60) < 1.5 {
		tips = append(tips, fmt.Sprintf("Puntuació de visió %d (%s per minut): posa més wards i compra'n de control.", vision, num(round1(float64(vision)/(dur/60)))))
	}
	if kp != nil && *kp < 45 {
		tips = append(tips, fmt.Sprintf("Participació en kills del %d%%: busca més roams quan tinguis l'onada empesa.", *kp))
	}
	if role != "UTILITY" && ocs["10"] != nil && cs["10"] != nil && *cs["10"]-*ocs["10"] >= 15 {
		tips = append(tips, fmt.Sprintf("Has guanyat la línia de farm (+%d CS al minut 10). Converteix-ho en roams o invasions.", *cs["10"]-*ocs["10"]))
	}
	if len(tips) == 0 {
		tips = append(tips, "Partida neta segons les dades: mantén el que has fet.")
	}
	var total *float64
	if dur > 0 {
		total = fp(round1(float64(finalCS) / (dur / 60)))
	}
	var result any
	if rec.Result != "" {
		result = rec.Result
	}
	return map[string]any{
		"date": rec.Date, "champion": me.ChampionName, "opponent": oppChamp, "result": result,
		"duration": dur, "kda": []int{k, d, a}, "kp": kp, "cs": cs, "opp_cs": ocs, "final_cs": finalCS, "opp_final_cs": oppFinal, "cs_curve": curve,
		"cs_rate": map[string]*float64{"línia": csLane, "mitja": csMid, "final": csLate, "total": total},
		"lvl10":   lvl10, "deaths": deaths, "kills": kills, "assists": assists, "roams": roams,
		"rival_roams": rivalRoams, "duel": []int{duelW, duelL}, "objectives": objectives,
		"deaths15": deaths15, "tips": tips, "role": role, "vision": vision,
	}
}

func contains(xs []string, v string) bool {
	for _, x := range xs {
		if x == v {
			return true
		}
	}
	return false
}

// num prints 5.0 as "5", 5.5 as "5.5"
func num(v float64) string {
	s := fmt.Sprintf("%.1f", v)
	return strings.TrimSuffix(s, ".0")
}
