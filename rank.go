package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"image"
	"image/png"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Ranked card: current tier, LP and win rate of the account, plus the highest rank the app has seen this
// season. Refreshed on request (cached 60 s) and after every game.

type rankQueue struct {
	Queue  string       `json:"queue"`
	Tier   string       `json:"tier"`
	Rank   string       `json:"rank"`
	LP     int          `json:"lp"`
	Wins   int          `json:"wins"`
	Losses int          `json:"losses"`
	WR     int          `json:"wr"`
	Peak   *leagueEntry `json:"peak,omitempty"`
}

var (
	rankMu   sync.Mutex
	rankAt   time.Time
	rankLast []byte
)

// myRiotID: the player's Riot ID, from the open League client or from the last recorded game
func myRiotID() string {
	var s struct {
		GameName string `json:"gameName"`
		TagLine  string `json:"tagLine"`
	}
	if code, err := lcuGet("/lol-summoner/v1/current-summoner", &s); err == nil && code == 200 && s.GameName != "" && s.TagLine != "" {
		id := s.GameName + "#" + s.TagLine
		setMyRiotID(id)
		return id
	}
	return loadConfig().MyRiotID
}

func rankJSON(force bool) []byte {
	rankMu.Lock()
	defer rankMu.Unlock()
	if !force && rankLast != nil && time.Since(rankAt) < 60*time.Second {
		return rankLast
	}
	c := loadConfig()
	res := map[string]any{}
	id := myRiotID()
	switch {
	case c.RiotKey == "":
		res["error"] = "no_key"
	case id == "":
		res["error"] = "no_riot_id"
	default:
		ent, err := leagueEntries(id)
		if err != nil {
			res["error"] = err.Error()
			break
		}
		c = loadConfig()
		peaks := peaksFor(&c, id)
		changed := false
		out := []rankQueue{}
		for _, q := range []string{"RANKED_SOLO_5x5", "RANKED_FLEX_SR"} {
			e, ok := ent[q]
			if !ok {
				continue
			}
			if p, had := peaks[q]; !had || absLP(e) > absLP(p) {
				peaks[q] = e
				changed = true
			}
			p := peaks[q]
			// the client, when it's open, has the counts it shows you (Riot's API sometimes counts a game more)
			if lw, ll, ok := clientRankedWL(q, e.Tier, e.Rank); ok {
				e.Wins, e.Losses = lw, ll
			}
			rq := rankQueue{q, e.Tier, e.Rank, e.LeaguePoints, e.Wins, e.Losses, 0, &p}
			if g := e.Wins + e.Losses; g > 0 {
				rq.WR = (e.Wins*100 + g/2) / g // rounded to the nearest whole percent
			}
			out = append(out, rq)
		}
		if changed {
			saveConfig(c)
		}
		res["riot_id"] = id
		res["queues"] = out
	}
	rankLast, _ = json.Marshal(res)
	rankAt = time.Now()
	return rankLast
}

func rankHandler(send func(http.ResponseWriter, int, string, []byte)) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		send(w, 200, "application/json", rankJSON(req.URL.Query().Get("force") == "1"))
	}
}

// Official ranked emblems (the game's own files, served by Community Dragon), downloaded once and kept on disk.
var emblemTiers = map[string]bool{"iron": true, "bronze": true, "silver": true, "gold": true, "platinum": true, "emerald": true,
	"diamond": true, "master": true, "grandmaster": true, "challenger": true}

func emblemHandler(w http.ResponseWriter, req *http.Request) {
	t := strings.ToLower(req.URL.Query().Get("tier"))
	if !emblemTiers[t] {
		http.NotFound(w, req)
		return
	}
	dir := filepath.Join(filepath.Dir(dataDir()), "escuts")
	file := filepath.Join(dir, "emblem-"+t+".png")
	b, err := os.ReadFile(file)
	isPNG := func(x []byte) bool { return len(x) > 100 && bytes.HasPrefix(x, []byte("\x89PNG")) }
	if err == nil && isPNG(b) {
		// emblems saved by 1.12.1 still have the margin: crop them once
		if c, err := cropTransparent(b); err == nil && len(c) != len(b) {
			b = c
			os.WriteFile(file, b, 0o644)
		}
	}
	if err != nil || !isPNG(b) {
		r, err := riotHTTP.Get("https://raw.communitydragon.org/latest/plugins/rcp-fe-lol-static-assets/global/default/images/ranked-emblem/emblem-" + t + ".png")
		if err != nil {
			http.Error(w, err.Error(), 502)
			return
		}
		defer r.Body.Close()
		b, _ = io.ReadAll(io.LimitReader(r.Body, 8<<20))
		if r.StatusCode != 200 || !isPNG(b) {
			http.Error(w, "no emblem", 502)
			return
		}
		if c, err := cropTransparent(b); err == nil {
			b = c // the official files have a wide transparent margin around the emblem
		}
		os.MkdirAll(dir, 0o755)
		os.WriteFile(file, b, 0o644)
		logf("Escut oficial descarregat: %s", t)
	}
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "max-age=604800")
	w.Write(b)
}

// cropTransparent trims the fully transparent border of a PNG (keeps a small margin).
func cropTransparent(b []byte) ([]byte, error) {
	img, err := png.Decode(bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	r := img.Bounds()
	minX, minY, maxX, maxY := r.Max.X, r.Max.Y, r.Min.X, r.Min.Y
	for y := r.Min.Y; y < r.Max.Y; y++ {
		for x := r.Min.X; x < r.Max.X; x++ {
			// only clearly visible pixels count: the emblems have a wide, faint glow around them
			if _, _, _, a := img.At(x, y).RGBA(); a > 0x7000 {
				if x < minX {
					minX = x
				}
				if x > maxX {
					maxX = x
				}
				if y < minY {
					minY = y
				}
				if y > maxY {
					maxY = y
				}
			}
		}
	}
	if maxX <= minX || maxY <= minY {
		return nil, errors.New("empty image")
	}
	pad := (maxX - minX) / 30
	crop := image.Rect(minX-pad, minY-pad, maxX+1+pad, maxY+1+pad).Intersect(r)
	if crop == r {
		return b, nil
	}
	sub, ok := img.(interface {
		SubImage(image.Rectangle) image.Image
	})
	if !ok {
		return nil, errors.New("no subimage")
	}
	var out bytes.Buffer
	if err := png.Encode(&out, sub.SubImage(crop)); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

// clientRankedWL: wins and losses of a queue as the League client shows them (only when the client is open and is on
// the same tier and division, so it's the same account)
func clientRankedWL(queue, tier, div string) (int, int, bool) {
	var st struct {
		Queues []struct {
			QueueType string `json:"queueType"`
			Tier      string `json:"tier"`
			Division  string `json:"division"`
			Wins      int    `json:"wins"`
			Losses    int    `json:"losses"`
		} `json:"queues"`
	}
	if code, _ := lcuGet("/lol-ranked/v1/current-ranked-stats", &st); code != 200 {
		return 0, 0, false
	}
	for _, q := range st.Queues {
		if q.QueueType == queue && q.Tier == tier && q.Division == div && q.Wins+q.Losses > 0 {
			return q.Wins, q.Losses, true
		}
	}
	return 0, 0, false
}
