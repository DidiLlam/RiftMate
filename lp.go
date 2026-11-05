package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"
)

type leagueEntry struct {
	QueueType    string `json:"queueType"`
	Tier         string `json:"tier"`
	Rank         string `json:"rank"`
	LeaguePoints int    `json:"leaguePoints"`
	Wins         int    `json:"wins"`
	Losses       int    `json:"losses"`
	HotStreak    bool   `json:"hotStreak,omitempty"`
}

var tierOrder = []string{"IRON", "BRONZE", "SILVER", "GOLD", "PLATINUM", "EMERALD", "DIAMOND", "MASTER", "GRANDMASTER", "CHALLENGER"}

// absLP puts tier, division and LP on one scale (400 per tier, 100 per division) so a promotion counts right.
func absLP(e leagueEntry) int {
	t := 0
	for i, x := range tierOrder {
		if x == e.Tier {
			t = i
		}
	}
	if t >= 7 { // Master and above share one ladder
		return 7*400 + e.LeaguePoints
	}
	div := map[string]int{"IV": 0, "III": 100, "II": 200, "I": 300}[e.Rank]
	return t*400 + div + e.LeaguePoints
}

func leagueEntries(riotID string) (map[string]leagueEntry, error) {
	c := loadConfig()
	puuid, err := puuidFor(&c, riotID)
	if err != nil {
		return nil, err
	}
	var list []leagueEntry
	if err := riotGetHost(c, platformHost(c), "/lol/league/v4/entries/by-puuid/"+puuid, &list); err != nil {
		return nil, err
	}
	out := map[string]leagueEntry{}
	for _, e := range list {
		if e.QueueType == "RANKED_SOLO_5x5" || e.QueueType == "RANKED_FLEX_SR" {
			out[e.QueueType] = e
		}
	}
	return out, nil
}

var (
	lpMu      sync.Mutex
	lpBefore  = map[string]map[string]leagueEntry{} // by game date
	lpPending = map[string]string{}                 // games still waiting for Riot's new rank: date -> Riot ID
	lpLoaded  bool
)

// The rank at the start of each game and the games still waiting for their LP are kept on disk: closing or
// updating the app in the minutes after a game (while Riot updates the rank) doesn't lose that game's LP.
func lpStateFile() string { return profileFile("lp_pendent.json") }

func loadLPState() {
	if lpLoaded {
		return
	}
	lpLoaded = true
	var st struct {
		Before  map[string]map[string]leagueEntry `json:"before"`
		Pending map[string]string                 `json:"pending"`
	}
	if b, err := os.ReadFile(lpStateFile()); err == nil && json.Unmarshal(b, &st) == nil {
		for k, v := range st.Before {
			if _, ok := lpBefore[k]; !ok {
				lpBefore[k] = v
			}
		}
		for k, v := range st.Pending {
			lpPending[k] = v
		}
	}
}

func saveLPState() {
	// only the last day: older entries are of games that will never be finished
	cut := time.Now().Add(-24 * time.Hour).Format("2006-01-02 15:04")
	for k := range lpBefore {
		if k < cut {
			delete(lpBefore, k)
		}
	}
	b, _ := json.Marshal(map[string]any{"before": lpBefore, "pending": lpPending})
	os.WriteFile(lpStateFile(), b, 0o644)
}

// lpResume: at start, the games whose LP was still being waited for when the app closed
func lpResume() {
	lpMu.Lock()
	loadLPState()
	todo := map[string]string{}
	for k, v := range lpPending {
		todo[k] = v
	}
	lpMu.Unlock()
	for date, id := range todo {
		logf("LP: reprenc l'espera de la partida %s", date)
		go lpAtEnd(date, id)
	}
}

// lpAtStart remembers the ranked state when a game starts.
func lpAtStart(date, riotID string) {
	setMyRiotID(riotID)
	if loadConfig().RiotKey == "" {
		return
	}
	e, err := leagueEntries(riotID)
	if err != nil {
		logf("LP a l'inici: %v", err)
		return
	}
	lpMu.Lock()
	lpBefore[date] = e
	saveLPState()
	lpMu.Unlock()
	logf("LP a l'inici guardats (%d cues)", len(e))
}

// lpAtEnd waits for Riot to update the ranked state, then stores the LP change in the report.
func lpAtEnd(date, riotID string) {
	lpMu.Lock()
	loadLPState()
	before, ok := lpBefore[date]
	lpPending[date] = riotID
	saveLPState()
	lpMu.Unlock()
	defer func() { // done (or given up): forget this game
		lpMu.Lock()
		delete(lpBefore, date)
		delete(lpPending, date)
		saveLPState()
		lpMu.Unlock()
	}()
	if !ok || loadConfig().RiotKey == "" {
		return
	}
	for i := 0; i < 20; i++ {
		time.Sleep(retryWait())
		after, err := leagueEntries(riotID)
		if err != nil {
			logf("LP al final, intent %d: %v", i+1, err)
			continue
		}
		for q, a := range after {
			b, had := before[q]
			games := a.Wins + a.Losses
			if (had && games == b.Wins+b.Losses) || (!had && games == 0) {
				continue // this queue was not played (or Riot hasn't updated it yet)
			}
			lp := map[string]any{"queue": q, "after": a, "win": !had || a.Wins > b.Wins}
			if had {
				lp["before"] = b
				lp["delta"] = absLP(a) - absLP(b)
			}
			go rankJSON(true) // refresh the ranked card
			if err := saveLP(date, lp); err != nil {
				logf("LP: no s'ha pogut guardar: %v", err)
			} else {
				logf("LP guardats per a %s: %s %v", date, q, lp["delta"])
			}
			return
		}
	}
	logf("LP: Riot no ha actualitzat el rang en 20 minuts (normal o personalitzada?)")
}

func saveLP(date string, lp map[string]any) error {
	mapMu.Lock()
	defer mapMu.Unlock()
	g, _, err := readGame(date)
	if err != nil {
		return err
	}
	// safety check: the ranked result must match the game's result (a win can't lose LP and the other way round)
	if win, ok := lp["win"].(bool); ok {
		if res, _ := g.Report["result"].(string); (res == "Victòria" && !win) || (res == "Derrota" && win) {
			return fmt.Errorf("el resultat de Riot (victòria=%v) no coincideix amb el de la partida (%s): no ho guardo", win, res)
		}
	}
	if d, ok := lp["delta"].(int); ok {
		if res, _ := g.Report["result"].(string); (res == "Victòria" && d < 0) || (res == "Derrota" && d > 0) {
			return fmt.Errorf("canvi de LP %+d incoherent amb el resultat (%s): no ho guardo", d, res)
		}
	}
	g.Report["lp"] = lp
	b, _ := json.Marshal(g)
	return os.WriteFile(gameFile(date), b, 0o644)
}

func platformHost(c Config) string {
	p := strings.ToLower(c.Platform)
	if p == "" {
		p = "euw1"
	}
	return p
}

// retryWait is one minute (Riot updates ranked data with a delay); tests set LT_TEST_FAST.
func retryWait() time.Duration {
	if os.Getenv("LT_TEST_FAST") != "" {
		return 2 * time.Second
	}
	return time.Minute
}
