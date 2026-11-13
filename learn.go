package main

// The champions you're learning (from the Guides tab) and your own champion mastery (for the pick advice).

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"sync"
	"time"
)

func learnFile() string { return profileFile("aprenent.json") }

// learnList: the champions marked "I'm learning it" (Data Dragon ids, like "Yasuo"), at most 5
func learnList() []string {
	var l []string
	if b, err := os.ReadFile(learnFile()); err == nil {
		json.Unmarshal(b, &l)
	}
	return l
}

func learnHandler(w http.ResponseWriter, req *http.Request) {
	if req.Method == http.MethodPost {
		var l []string
		if b, err := io.ReadAll(io.LimitReader(req.Body, 4096)); err == nil && json.Unmarshal(b, &l) == nil {
			if len(l) > 5 {
				l = l[:5]
			}
			b, _ := json.Marshal(l)
			os.WriteFile(learnFile(), b, 0o644)
			logf("Guies: aprenent %v", l)
			go func() { // read the Challenger games of the new ones (their build, runes and skill order)
				time.Sleep(2 * time.Second)
				if !liveNow.Load() {
					benchRun()
				}
			}()
		}
	}
	b, _ := json.Marshal(learnList())
	w.Header().Set("Content-Type", "application/json")
	w.Write(b)
}

// your mastery points with every champion (Riot), kept for an hour
var (
	myMastMu sync.Mutex
	myMast   map[string]int
	myMastAt time.Time
)

func masteryHandler(w http.ResponseWriter, req *http.Request) {
	myMastMu.Lock()
	defer myMastMu.Unlock()
	if myMast == nil || time.Since(myMastAt) > time.Hour {
		c := loadConfig()
		sc := loadSeason()
		if c.RiotKey != "" && sc.PUUID != "" {
			var list []struct {
				ChampionID int `json:"championId"`
				Points     int `json:"championPoints"`
			}
			if err := scoutGet(c, platformHost(c), "/lol/champion-mastery/v4/champion-masteries/by-puuid/"+sc.PUUID, &list); err == nil {
				m := map[string]int{}
				for _, x := range list {
					if ch := championByKey(x.ChampionID); ch.ID != "" {
						m[ch.ID] = x.Points
					}
				}
				myMast, myMastAt = m, time.Now()
			}
		}
	}
	b, _ := json.Marshal(myMast)
	w.Header().Set("Content-Type", "application/json")
	w.Write(b)
}

// the learning champions that aren't already among the ones you play most, in the order marked
func learnExtra(have []string) []string {
	in := map[string]bool{}
	for _, c := range have {
		in[c] = true
	}
	out := []string{}
	for _, c := range learnList() {
		if !in[c] {
			out = append(out, c)
		}
	}
	return out
}
