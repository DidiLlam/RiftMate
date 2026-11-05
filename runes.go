package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// At champion select: the Challenger rune page and item set of your champion, written into the League client (as
// tools like Porofessor or Blitz do). Only pages and sets made by RiftMate are ever replaced; yours are left alone.

const runePrefix = "RiftMate"

// applyChallenger: champ is the Data Dragon id ("Leblanc"). Returns what was done, in words for the page.
func applyChallenger(champ string) (map[string]any, error) {
	b := loadBench()
	var gs []benchGame
	for ch, list := range b.Games {
		if strings.EqualFold(ch, champ) {
			gs, champ = list, ch
		}
	}
	if len(gs) < 5 {
		return map[string]any{"ok": false, "why": "no_data"}, nil
	}
	sm := summarize(champ, gs)
	out := map[string]any{"ok": true, "champ": sm.Name, "games": sm.Games}
	// 1) runes
	if sm.Page != nil {
		var pages []struct {
			ID         int64  `json:"id"`
			Name       string `json:"name"`
			IsEditable bool   `json:"isEditable"`
			IsActive   bool   `json:"isActive"`
		}
		if code, err := lcuGet("/lol-perks/v1/pages", &pages); err != nil || code != 200 {
			return nil, fmt.Errorf("client_%d", code)
		}
		for _, p := range pages {
			if strings.HasPrefix(p.Name, runePrefix) {
				lcuDo("DELETE", fmt.Sprintf("/lol-perks/v1/pages/%d", p.ID), nil, nil)
			}
		}
		page := map[string]any{"name": runePrefix + " " + sm.Name, "primaryStyleId": sm.Page.Prim, "subStyleId": sm.Page.Sub,
			"selectedPerkIds": sm.Page.Perks, "current": true}
		code, _ := lcuDo("POST", "/lol-perks/v1/pages", page, nil)
		out["runes"] = code >= 200 && code < 300
		if !(code >= 200 && code < 300) {
			out["runes_why"] = "full" // no free page: the client refuses a new one
		}
		out["rune_share"] = sm.Page.Share
	}
	// 2) item set (Starting items, their build, other items they finish)
	var me struct {
		SummonerID int64 `json:"summonerId"`
	}
	if code, _ := lcuGet("/lol-summoner/v1/current-summoner", &me); code == 200 && me.SummonerID > 0 && len(sm.Build) > 0 {
		path := fmt.Sprintf("/lol-item-sets/v1/item-sets/%d/sets", me.SummonerID)
		var sets map[string]any
		if code, _ := lcuGet(path, &sets); code == 200 && sets != nil {
			list, _ := sets["itemSets"].([]any)
			keep := []any{}
			for _, x := range list {
				if m, ok := x.(map[string]any); ok {
					if t, _ := m["title"].(string); strings.HasPrefix(t, runePrefix) {
						continue
					}
				}
				keep = append(keep, x)
			}
			items := func(ids []int) []map[string]any {
				o := []map[string]any{}
				seen := map[int]bool{}
				for _, id := range ids {
					if id > 0 && !seen[id] {
						seen[id] = true
						o = append(o, map[string]any{"id": fmt.Sprint(id), "count": 1})
					}
				}
				return o
			}
			blocks := []map[string]any{}
			if len(sm.Start) > 0 {
				blocks = append(blocks, map[string]any{"type": "Inici dels Challenger", "items": items(sm.Start)})
			}
			blocks = append(blocks, map[string]any{"type": fmt.Sprintf("Build dels Challenger (%d%%)", sm.Share["build"]), "items": items(sm.Build)})
			rest := []int{}
			for _, x := range sm.Items {
				in := false
				for _, y := range sm.Build {
					in = in || x == y
				}
				if !in {
					rest = append(rest, x)
				}
			}
			if len(rest) > 0 {
				blocks = append(blocks, map[string]any{"type": "Altres objectes que fan", "items": items(rest)})
			}
			key := champKeyOf(champ)
			set := map[string]any{"title": runePrefix + " " + sm.Name, "type": "custom", "map": "any", "mode": "any", "priority": false, "sortrank": 0,
				"associatedChampions": []int{key}, "associatedMaps": []int{11}, "blocks": blocks, "uid": fmt.Sprintf("riftmate-%s-%d", strings.ToLower(champ), time.Now().Unix())}
			sets["itemSets"] = append(keep, set)
			sets["timestamp"] = time.Now().UnixMilli()
			code, _ := lcuDo("PUT", path, sets, nil)
			out["items"] = code >= 200 && code < 300
		}
	}
	logf("Client: runes i objectes dels Challenger per a %s: %v", sm.Name, out)
	setPart("runes", out["runes"] == true || out["items"] == true, fmt.Sprintf("%s: runes %v, objectes %v", sm.Name, out["runes"] == true, out["items"] == true))
	return out, nil
}

func runesHandler(send func(http.ResponseWriter, int, string, []byte)) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		res, err := applyChallenger(req.URL.Query().Get("champ"))
		if err != nil {
			res = map[string]any{"ok": false, "why": err.Error()}
		}
		raw, _ := json.Marshal(res)
		send(w, 200, "application/json", raw)
	}
}
