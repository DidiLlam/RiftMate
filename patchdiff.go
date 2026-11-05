package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"regexp"
	"sort"
	"strings"
	"sync"
)

// What the new patch changed for your champions and your items: Data Dragon's data of the current version against
// the previous one. Base stats, cooldowns, costs and ranges are reliable there; damage numbers sometimes aren't
// published, so "no changes" means no changes in the public data.

func ddBase() string {
	if b := os.Getenv("LT_DD_BASE"); b != "" { // tests
		return b
	}
	return "https://ddragon.leagueoflegends.com"
}

func ddGet(path string, out any) error {
	r, err := riotHTTP.Get(ddBase() + path)
	if err != nil {
		return err
	}
	defer r.Body.Close()
	if r.StatusCode != 200 {
		return fmt.Errorf("http_%d", r.StatusCode)
	}
	return json.NewDecoder(r.Body).Decode(out)
}

type ddSpell struct {
	Name         string `json:"name"`
	CooldownBurn string `json:"cooldownBurn"`
	CostBurn     string `json:"costBurn"`
	RangeBurn    string `json:"rangeBurn"`
	Tooltip      string `json:"tooltip"`
}
type ddChampFull struct {
	Name    string             `json:"name"`
	Stats   map[string]float64 `json:"stats"`
	Spells  []ddSpell          `json:"spells"`
	Passive struct {
		Name        string `json:"name"`
		Description string `json:"description"`
	} `json:"passive"`
}

var (
	patchMu    sync.Mutex
	patchCache = map[string][]string{} // "from|to|kind|id" -> lines
	tagRe      = regexp.MustCompile(`<[^>]+>`)
)

var statNames = map[string]string{"hp": "vida", "hpperlevel": "vida per nivell", "mp": "manà", "mpperlevel": "manà per nivell", "movespeed": "velocitat de moviment",
	"armor": "armadura", "armorperlevel": "armadura per nivell", "spellblock": "resistència màgica", "spellblockperlevel": "resistència màgica per nivell",
	"attackrange": "abast d'atac", "hpregen": "regeneració de vida", "mpregen": "regeneració de manà", "attackdamage": "dany d'atac",
	"attackdamageperlevel": "dany d'atac per nivell", "attackspeed": "velocitat d'atac", "attackspeedperlevel": "velocitat d'atac per nivell"}

func champDiff(from, to, id string) []string {
	key := from + "|" + to + "|c|" + id
	patchMu.Lock()
	if l, ok := patchCache[key]; ok {
		patchMu.Unlock()
		return l
	}
	patchMu.Unlock()
	get := func(v string) *ddChampFull {
		var d struct {
			Data map[string]ddChampFull `json:"data"`
		}
		if ddGet("/cdn/"+v+"/data/es_ES/champion/"+id+".json", &d) != nil {
			return nil
		}
		for _, c := range d.Data {
			return &c
		}
		return nil
	}
	a, b := get(from), get(to)
	if a == nil || b == nil {
		return nil
	}
	lines := []string{}
	keys := []string{}
	for k := range b.Stats {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if n, ok := statNames[k]; ok && a.Stats[k] != b.Stats[k] {
			lines = append(lines, fmt.Sprintf("%s %s → %s", n, fnum(a.Stats[k]), fnum(b.Stats[k])))
		}
	}
	letters := "QWER"
	for i := 0; i < len(b.Spells) && i < len(a.Spells) && i < 4; i++ {
		x, y := a.Spells[i], b.Spells[i]
		ch := []string{}
		if x.CooldownBurn != y.CooldownBurn {
			ch = append(ch, fmt.Sprintf("temps de recàrrega %s → %s", x.CooldownBurn, y.CooldownBurn))
		}
		if x.CostBurn != y.CostBurn {
			ch = append(ch, fmt.Sprintf("cost %s → %s", x.CostBurn, y.CostBurn))
		}
		if x.RangeBurn != y.RangeBurn {
			ch = append(ch, fmt.Sprintf("abast %s → %s", x.RangeBurn, y.RangeBurn))
		}
		if len(ch) == 0 && clean(x.Tooltip) != clean(y.Tooltip) {
			ch = append(ch, "ha canviat la descripció")
		}
		if len(ch) > 0 {
			lines = append(lines, fmt.Sprintf("%c (%s): %s", letters[i], y.Name, strings.Join(ch, ", ")))
		}
	}
	if clean(a.Passive.Description) != clean(b.Passive.Description) {
		lines = append(lines, fmt.Sprintf("passiva (%s): ha canviat la descripció", b.Passive.Name))
	}
	patchMu.Lock()
	patchCache[key] = lines
	patchMu.Unlock()
	return lines
}

func itemDiffs(from, to string, ids []string) map[string][]string {
	type it struct {
		Name        string              `json:"name"`
		Description string              `json:"description"`
		Gold        struct{ Total int } `json:"gold"`
		Stats       map[string]float64  `json:"stats"`
	}
	get := func(v string) map[string]it {
		var d struct {
			Data map[string]it `json:"data"`
		}
		ddGet("/cdn/"+v+"/data/es_ES/item.json", &d)
		return d.Data
	}
	a, b := get(from), get(to)
	out := map[string][]string{}
	for _, id := range ids {
		x, okx := a[id]
		y, oky := b[id]
		if !okx || !oky {
			continue
		}
		ch := []string{}
		if x.Gold.Total != y.Gold.Total {
			ch = append(ch, fmt.Sprintf("preu %d → %d", x.Gold.Total, y.Gold.Total))
		}
		for k, v := range y.Stats {
			if x.Stats[k] != v {
				ch = append(ch, fmt.Sprintf("%s %s → %s", k, fnum(x.Stats[k]), fnum(v)))
			}
		}
		if len(ch) == 0 && clean(x.Description) != clean(y.Description) {
			ch = append(ch, "ha canviat la descripció")
		}
		if len(ch) > 0 {
			out[y.Name] = ch
		}
	}
	return out
}

func clean(s string) string { return strings.Join(strings.Fields(tagRe.ReplaceAllString(s, " ")), " ") }
func fnum(f float64) string {
	s := fmt.Sprintf("%.3f", f)
	s = strings.TrimRight(strings.TrimRight(s, "0"), ".")
	return strings.Replace(s, ".", ",", 1)
}

func patchDiffHandler(send func(http.ResponseWriter, int, string, []byte)) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		var vs []string
		if err := ddGet("/api/versions.json", &vs); err != nil || len(vs) < 2 {
			send(w, 200, "application/json", []byte(`{"error":"no_versions"}`))
			return
		}
		from, to := vs[1], vs[0]
		champs := map[string][]string{}
		for _, id := range strings.Split(req.URL.Query().Get("champs"), ",") {
			if id = strings.TrimSpace(id); id != "" {
				if l := champDiff(from, to, id); len(l) > 0 {
					champs[id] = l
				}
			}
		}
		items := map[string][]string{}
		if ids := strings.Split(req.URL.Query().Get("items"), ","); len(ids) > 0 && ids[0] != "" {
			items = itemDiffs(from, to, ids)
		}
		raw, _ := json.Marshal(map[string]any{"from": from, "to": to, "champs": champs, "items": items})
		send(w, 200, "application/json", raw)
	}
}
