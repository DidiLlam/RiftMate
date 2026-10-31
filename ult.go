package main

import (
	"encoding/json"
	"os"
	"regexp"
	"sync"
)

// Ultimate (R) data of a champion from Data Dragon: name, base cooldown per rank and icon file.
type ultInfo struct {
	Name string    `json:"name"`
	CD   []float64 `json:"cd"`
	Img  string    `json:"img"`
}

var (
	ultMu    sync.Mutex
	ultCache = map[string][]byte{}
	ddIDRe   = regexp.MustCompile(`^[A-Za-z0-9]{2,30}$`)
)

// ultJSON returns {"name","cd","img"} for a Data Dragon champion id ("Darius", "MonkeyKing").
func ultJSON(id, locale string) []byte {
	if !ddIDRe.MatchString(id) {
		return []byte(`{}`)
	}
	if locale != "es_ES" && locale != "en_US" {
		locale = "en_US"
	}
	key := id + "/" + locale
	ultMu.Lock()
	defer ultMu.Unlock()
	if b, ok := ultCache[key]; ok {
		return b
	}
	var raw struct {
		Data map[string]struct {
			Spells []struct {
				Name     string    `json:"name"`
				Cooldown []float64 `json:"cooldown"`
				Image    struct {
					Full string `json:"full"`
				} `json:"image"`
			} `json:"spells"`
		} `json:"data"`
	}
	var err error
	if dir := os.Getenv("LT_CHAMP_DIR"); dir != "" { // tests
		var fh *os.File
		if fh, err = os.Open(dir + "/" + id + ".json"); err == nil {
			defer fh.Close()
			err = json.NewDecoder(fh).Decode(&raw)
		}
	} else {
		r, e := riotHTTP.Get("https://ddragon.leagueoflegends.com/cdn/" + dataDragonVersion() + "/data/" + locale + "/champion/" + id + ".json")
		if err = e; err == nil {
			defer r.Body.Close()
			err = json.NewDecoder(r.Body).Decode(&raw)
		}
	}
	if err != nil {
		logf("No he pogut llegir la ulti de %s: %v", id, err)
		return []byte(`{}`)
	}
	out := ultInfo{}
	for _, c := range raw.Data {
		if len(c.Spells) >= 4 {
			r := c.Spells[3]
			out = ultInfo{stripTags(r.Name), r.Cooldown, r.Image.Full}
		}
	}
	b, _ := json.Marshal(out)
	ultCache[key] = b
	return b
}
