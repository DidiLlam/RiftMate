package main

import (
	"encoding/json"
	"net/http"
	"sort"
	"time"
)

// When you play best, from this season's ranked games: the first game of each session against the rest (a session
// ends after 90 minutes without playing), by time of day and by day of the week.
type wl struct {
	W int `json:"w"`
	L int `json:"l"`
}

func (x *wl) add(win bool) {
	if win {
		x.W++
	} else {
		x.L++
	}
}

func whenJSON() []byte {
	sc := loadSeason()
	type g struct {
		end int64
		win bool
	}
	gs := []g{}
	for _, x := range sc.Games {
		if !x.Remake && (x.Queue == 420 || x.Queue == 440) && x.End > 0 {
			gs = append(gs, g{x.End, x.Win})
		}
	}
	sort.Slice(gs, func(i, j int) bool { return gs[i].end < gs[j].end })
	var first, rest, second wl
	hours := map[string]*wl{}
	days := map[int]*wl{}
	for i, x := range gs {
		newSession := i == 0 || x.end-gs[i-1].end > 90*60+40*60 // the previous game ended more than ~90 minutes before this one started
		secondOfSession := i > 0 && !newSession && (i == 1 || gs[i-1].end-gs[i-2].end > 130*60)
		switch {
		case newSession:
			first.add(x.win)
		case secondOfSession:
			second.add(x.win)
			rest.add(x.win)
		default:
			rest.add(x.win)
		}
		t := time.Unix(x.end-25*60, 0).Local() // roughly when it started
		h := t.Hour()
		band := map[bool]string{true: "matí", false: ""}[h >= 6 && h < 14]
		switch {
		case h >= 14 && h < 18:
			band = "tarda"
		case h >= 18 && h < 22:
			band = "vespre"
		case h >= 22 || h < 6:
			band = "nit"
		}
		if hours[band] == nil {
			hours[band] = &wl{}
		}
		hours[band].add(x.win)
		wd := int(t.Weekday())
		if days[wd] == nil {
			days[wd] = &wl{}
		}
		days[wd].add(x.win)
	}
	raw, _ := json.Marshal(map[string]any{"games": len(gs), "first": first, "second": second, "rest": rest, "hours": hours, "days": days})
	return raw
}

func whenHandler(send func(http.ResponseWriter, int, string, []byte)) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) { send(w, 200, "application/json", whenJSON()) }
}
