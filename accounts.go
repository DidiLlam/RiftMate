package main

// The player's accounts: a player can have more than one (play three games on one, three on another, to change
// air). Linked accounts share what the player is (reports, goals, errors, plays, guides); rank, LP, season and
// MMR stay each account's own.

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type acctInfo struct {
	ID      string `json:"id"`
	Main    bool   `json:"main,omitempty"`    // the first account of this PC
	Linked  bool   `json:"linked,omitempty"`  // one of the main account's player's other accounts
	NotMine bool   `json:"notmine,omitempty"` // someone else's
	Current bool   `json:"current,omitempty"`
	Games   int    `json:"games"`
}

// accountsSeen: every account with reports on this PC, and how many
func accountsSeen() map[string]*acctInfo {
	out := map[string]*acctInfo{}
	add := func(id string) *acctInfo {
		k := strings.ToLower(id)
		if out[k] == nil {
			out[k] = &acctInfo{ID: id}
		}
		return out[k]
	}
	dir := dataDir()
	if es, err := os.ReadDir(dir); err == nil {
		for _, e := range es {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
				continue
			}
			if b, err := os.ReadFile(filepath.Join(dir, e.Name())); err == nil {
				if acc := reportAccount(b); strings.Contains(acc, "#") {
					add(acc).Games++
				}
			}
		}
	}
	c := loadConfig()
	for _, id := range []string{c.Owner, c.MyRiotID} {
		if strings.Contains(id, "#") {
			add(id)
		}
	}
	for _, id := range c.Alts {
		add(id)
	}
	return out
}

// GET /api/accounts: the accounts; POST {"id": "Name#TAG", "mine": true|false} links one to the player, or marks it
// as someone else's (false); {"id": ..., "forget": true} undoes either
func accountsHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method == "POST" {
		var in struct {
			ID     string `json:"id"`
			Mine   bool   `json:"mine"`
			Forget bool   `json:"forget"`
		}
		json.NewDecoder(r.Body).Decode(&in)
		id := strings.TrimSpace(in.ID)
		k := strings.ToLower(id)
		c := loadConfig()
		if strings.Contains(id, "#") && !sameID(id, ownerID()) {
			if c.Alts == nil {
				c.Alts = map[string]string{}
			}
			delete(c.Alts, k)
			nm := c.NotMine[:0]
			for _, x := range c.NotMine {
				if x != k {
					nm = append(nm, x)
				}
			}
			c.NotMine = nm
			if !in.Forget {
				if in.Mine {
					c.Alts[k] = id
				} else {
					c.NotMine = append(c.NotMine, k)
				}
			}
			saveConfig(c)
			meMu.Lock()
			meAt = meAt.AddDate(-1, 0, 0) // the header asks again
			meMu.Unlock()
			logf("Comptes: %s %s", id, map[bool]string{true: "és un altre compte teu", false: "és d'una altra persona"}[in.Mine && !in.Forget])
		}
	}
	c := loadConfig()
	seen := accountsSeen()
	list := []acctInfo{}
	for k, a := range seen {
		a.Main = sameID(a.ID, c.Owner)
		_, a.Linked = c.Alts[k]
		a.NotMine = contains(c.NotMine, k)
		a.Current = sameID(a.ID, c.MyRiotID)
		list = append(list, *a)
	}
	sort.Slice(list, func(i, j int) bool {
		if list[i].Main != list[j].Main {
			return list[i].Main
		}
		if list[i].Linked != list[j].Linked {
			return list[i].Linked
		}
		return list[i].Games > list[j].Games
	})
	w.Header().Set("content-type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"accounts": list, "owner": c.Owner, "current": c.MyRiotID})
}
