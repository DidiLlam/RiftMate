package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// Profiles: everything that belongs to one League account (reports, season, peak rank) is kept apart, so a
// second account on the same PC (a brother, a smurf) doesn't mix with the first. The account is the one
// logged in to the League client, or the last one seen. The first account the app knew is the "owner": its
// data stays where it always was, so nothing has to be moved.

func sameID(a, b string) bool { return strings.EqualFold(strings.TrimSpace(a), strings.TrimSpace(b)) }

// ownerID: the first account of this PC (set once, from the account the app already knew).
func ownerID() string {
	c := loadConfig()
	if c.Owner == "" && c.MyRiotID != "" {
		c.Owner = c.MyRiotID
		saveConfig(c)
	}
	return c.Owner
}

// setMyRiotID records the account in use; the owner is fixed first, so the first account keeps its files.
func setMyRiotID(id string) {
	ownerID()
	if c := loadConfig(); id != "" && c.MyRiotID != id {
		c.MyRiotID = id
		saveConfig(c)
		if c.Owner == "" {
			ownerID()
		}
	}
}

// currentID: the account in use now (League client, else the last one seen); never calls Riot.
func currentID() string { return loadConfig().MyRiotID }

func isOwner(id string) bool { o := ownerID(); return o == "" || id == "" || sameID(o, id) }

// slug: a file-name-safe version of a Riot ID
func slug(id string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(id) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '#':
			b.WriteString("-")
		case r > 127:
			b.WriteRune(r)
		}
	}
	if b.Len() == 0 {
		return "compte"
	}
	return b.String()
}

// profileFile: name.ext for the owner, name-<account>.ext for any other account.
func profileFile(name string) string {
	root := filepath.Dir(dataDir())
	id := currentID()
	if isOwner(id) {
		return filepath.Join(root, name)
	}
	ext := filepath.Ext(name)
	return filepath.Join(root, strings.TrimSuffix(name, ext)+"-"+slug(id)+ext)
}

// reportAccount: the Riot ID of the player of a saved report file ("" for very old reports).
func reportAccount(b []byte) string {
	var f struct {
		Raw struct {
			Me struct {
				RiotID       string `json:"riotId"`
				SummonerName string `json:"summonerName"`
			} `json:"me"`
		} `json:"raw"`
	}
	json.Unmarshal(b, &f)
	if f.Raw.Me.RiotID != "" {
		return f.Raw.Me.RiotID
	}
	return f.Raw.Me.SummonerName
}

// reportIsMine: does this report belong to the account in use? Reports without an account go to the owner.
func reportIsMine(b []byte, cur string) bool {
	acc := reportAccount(b)
	if acc == "" || !strings.Contains(acc, "#") {
		return isOwner(cur)
	}
	return cur == "" || sameID(acc, cur)
}

// peaksFor: the highest rank seen this season by an account (the owner keeps the original field).
func peaksFor(c *Config, id string) map[string]leagueEntry {
	if isOwner(id) {
		if c.Peaks == nil {
			c.Peaks = map[string]leagueEntry{}
		}
		return c.Peaks
	}
	if c.PeaksBy == nil {
		c.PeaksBy = map[string]map[string]leagueEntry{}
	}
	k := strings.ToLower(id)
	if c.PeaksBy[k] == nil {
		c.PeaksBy[k] = map[string]leagueEntry{}
	}
	return c.PeaksBy[k]
}

var _ = os.Stat
