package main

import (
	"encoding/json"
	"io"
	"os"
	"strings"
	"sync"
)

// Item data from Data Dragon, trimmed to what the in-game coach needs:
// name, total and own cost, components, whether it builds into something, tags.
type ddItem struct {
	Name  string   `json:"name"`
	Total int      `json:"total"`
	Base  int      `json:"base"`
	From  []string `json:"from,omitempty"`
	Final bool     `json:"final"` // completed item on Summoner's Rift (nothing builds out of it)
	Comp  bool     `json:"comp"`  // builds into something else (a component)
	Tags  []string `json:"tags,omitempty"`
}

var (
	itemsMu    sync.Mutex
	itemsCache = map[string][]byte{}
)

// itemsJSON returns the trimmed item list for a Data Dragon locale (en_US, es_ES), cached in memory.
func itemsJSON(locale string) []byte {
	if locale != "es_ES" && locale != "en_US" {
		locale = "en_US"
	}
	itemsMu.Lock()
	defer itemsMu.Unlock()
	if b, ok := itemsCache[locale]; ok {
		return b
	}
	var body io.Reader
	if f := os.Getenv("LT_ITEMS_JSON"); f != "" { // tests
		if fh, err := os.Open(f); err == nil {
			defer fh.Close()
			body = fh
		}
	} else if r, err := riotHTTP.Get("https://ddragon.leagueoflegends.com/cdn/" + dataDragonVersion() + "/data/" + locale + "/item.json"); err == nil {
		defer r.Body.Close()
		body = r.Body
	}
	if body == nil {
		return []byte(`{}`)
	}
	var raw struct {
		Data map[string]struct {
			Name string   `json:"name"`
			From []string `json:"from"`
			Into []string `json:"into"`
			Tags []string `json:"tags"`
			Gold struct {
				Base        int  `json:"base"`
				Total       int  `json:"total"`
				Purchasable bool `json:"purchasable"`
			} `json:"gold"`
			Maps             map[string]bool `json:"maps"`
			InStore          *bool           `json:"inStore"`
			RequiredAlly     string          `json:"requiredAlly"`
			RequiredChampion string          `json:"requiredChampion"`
		} `json:"data"`
	}
	if err := json.NewDecoder(body).Decode(&raw); err != nil {
		logf("No he pogut llegir els objectes de Data Dragon (%s): %v", locale, err)
		return []byte(`{}`)
	}
	out := map[string]ddItem{}
	for id, it := range raw.Data {
		if !it.Maps["11"] || !it.Gold.Purchasable || it.RequiredAlly != "" || it.RequiredChampion != "" || (it.InStore != nil && !*it.InStore) {
			continue
		}
		consumable := false
		for _, t := range it.Tags {
			if t == "Consumable" || t == "Trinket" {
				consumable = true
			}
		}
		out[id] = ddItem{
			Name:  stripTags(it.Name),
			Total: it.Gold.Total,
			Base:  it.Gold.Base,
			From:  it.From,
			Final: len(it.Into) == 0 && !consumable && it.Gold.Total >= 2000,
			Comp:  len(it.Into) > 0 && !consumable,
			Tags:  it.Tags,
		}
	}
	b, _ := json.Marshal(out)
	itemsCache[locale] = b
	logf("Objectes de Data Dragon carregats (%s): %d", locale, len(out))
	return b
}

func stripTags(s string) string {
	for {
		i := strings.Index(s, "<")
		if i < 0 {
			return s
		}
		j := strings.Index(s[i:], ">")
		if j < 0 {
			return s
		}
		s = s[:i] + s[i+j+1:]
	}
}
