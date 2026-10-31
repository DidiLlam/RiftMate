package main

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
)

// Data that changes with each patch, taken from Riot (Data Dragon) every time the program starts,
// so a new patch doesn't need a new RiftMate: champions (new ones too) and summoner spell cooldowns.

func champKey(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if r >= 'a' && r <= 'z' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// allChampions: the built-in list plus any champion Riot has released since
func allChampions() []string {
	championByKey(0) // loads champion.json once
	ddChampMu.Lock()
	defer ddChampMu.Unlock()
	out := append([]string{}, championList...)
	keys := map[string]bool{}
	for _, c := range championList {
		keys[champKey(c)] = true
	}
	for _, c := range ddChamps {
		k := champKey(c.Name)
		if k == "" || keys[k] {
			continue
		}
		dup := false
		for e := range keys { // "Renata Glasc" vs "Renata", "Nunu & Willump"...
			if strings.HasPrefix(k, e) || strings.HasPrefix(e, k) {
				dup = true
			}
		}
		if !dup {
			keys[k] = true
			out = append(out, c.Name)
		}
	}
	return out
}

var (
	sumMu  sync.Mutex
	sumCDs map[string]float64
)

// summonerCooldowns: base cooldown of each summoner spell in the current patch
func summonerCooldowns() map[string]float64 {
	sumMu.Lock()
	defer sumMu.Unlock()
	if sumCDs != nil {
		return sumCDs
	}
	var body io.Reader
	if f := os.Getenv("LT_SUMMONER_JSON"); f != "" { // tests
		if fh, err := os.Open(f); err == nil {
			defer fh.Close()
			body = fh
		}
	} else if r, err := riotHTTP.Get("https://ddragon.leagueoflegends.com/cdn/" + dataDragonVersion() + "/data/en_US/summoner.json"); err == nil {
		defer r.Body.Close()
		body = r.Body
	}
	if body == nil {
		return nil
	}
	var raw struct {
		Data map[string]struct {
			Cooldown []float64 `json:"cooldown"`
		} `json:"data"`
	}
	if json.NewDecoder(body).Decode(&raw) != nil {
		return nil
	}
	names := map[string]string{"SummonerFlash": "Flash", "SummonerDot": "Ignite", "SummonerTeleport": "Teleport", "SummonerHeal": "Heal",
		"SummonerExhaust": "Exhaust", "SummonerBarrier": "Barrier", "SummonerHaste": "Ghost", "SummonerBoost": "Cleanse", "SummonerSmite": "Smite"}
	out := map[string]float64{}
	for id, sp := range names {
		if d, ok := raw.Data[id]; ok && len(d.Cooldown) > 0 && d.Cooldown[0] > 0 {
			out[sp] = d.Cooldown[0]
		}
	}
	if len(out) > 0 {
		sumCDs = out
		logf("Temps dels encanteris del pedaç %s: %v", dataDragonVersion(), out)
	}
	return sumCDs
}

func patchHandler(send func(http.ResponseWriter, int, string, []byte)) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		v := dataDragonVersion()
		all := allChampions()
		b, _ := json.Marshal(map[string]any{"version": v, "patch": gamePatch(v), "champions": all[len(championList):], "cd": summonerCooldowns()})
		send(w, 200, "application/json", b)
	}
}

// gamePatch: Data Dragon "16.21.1" is game patch "26.21" (the season number is the year since 2026)
func gamePatch(v string) string {
	p := strings.Split(v, ".")
	if len(p) < 2 {
		return v
	}
	var major int
	for _, r := range p[0] {
		major = major*10 + int(r-'0')
	}
	if major >= 16 {
		return itoa(major+10) + "." + p[1]
	}
	return p[0] + "." + p[1]
}
