package main

// Videos of high-elo and pro players with a champion, for the guides: the AI looks them up on the internet (Gemini
// with Google Search) and every video it names is checked against YouTube itself (oEmbed: it exists, it can be
// embedded, and its title is about that champion) before it's shown. Kept a week per champion.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

type proVid struct {
	ID     string `json:"id"`
	Title  string `json:"title"`
	Author string `json:"author"`
	Kind   string `json:"kind,omitempty"` // proview | plays | guide
}
type proVids struct {
	At     int64    `json:"at"`
	Champ  string   `json:"champ"`
	Vids   []proVid `json:"vids"`
	Error  string   `json:"error,omitempty"`
	Cached bool     `json:"cached,omitempty"`
}

var searchHTTP = &http.Client{Timeout: 90 * time.Second} // a search takes longer than an answer
var proMu sync.Mutex
var proBusy = map[string]bool{}

func ytBase() string {
	if b := os.Getenv("LT_YT_BASE"); b != "" {
		return b
	}
	return "https://www.youtube.com"
}

var ytID = regexp.MustCompile(`(?:youtube\.com/(?:watch\?(?:[^"'\s]*&)?v=|shorts/|embed/|live/)|youtu\.be/)([A-Za-z0-9_-]{11})`)

// ytCheck: the video exists, can be embedded and is about the champion (by its title)
func ytCheck(id, champ string) (proVid, bool) {
	u := ytBase() + "/oembed?format=json&url=" + url.QueryEscape("https://www.youtube.com/watch?v="+id)
	r, err := aiHTTP.Get(u)
	if err != nil {
		return proVid{}, false
	}
	defer r.Body.Close()
	if r.StatusCode != 200 { // 401: embedding off; 404/400: doesn't exist
		return proVid{}, false
	}
	var o struct {
		Title  string `json:"title"`
		Author string `json:"author_name"`
	}
	if json.NewDecoder(io.LimitReader(r.Body, 64<<10)).Decode(&o) != nil || o.Title == "" {
		return proVid{}, false
	}
	n := func(s string) string {
		return strings.NewReplacer(" ", "", "'", "", ".", "", "&", "").Replace(strings.ToLower(s))
	}
	if !strings.Contains(n(o.Title), n(champ)) {
		return proVid{}, false
	}
	return proVid{ID: id, Title: o.Title, Author: o.Author}, true
}

// searchModels: the models to try for a search, best first. Searching with Google isn't free on every model (on
// some the free quota for it is zero), so when one says "quota" the next is tried; the one that worked is kept.
var searchModel string

func searchModels(key string) []string {
	first, _ := pickGeminiModel(key)
	names := []string{}
	req, _ := http.NewRequest("GET", gemBase()+"/v1beta/models?pageSize=200", nil)
	req.Header.Set("x-goog-api-key", key)
	if r, err := aiHTTP.Do(req); err == nil {
		var out struct {
			Models []struct {
				Name    string   `json:"name"`
				Methods []string `json:"supportedGenerationMethods"`
			} `json:"models"`
		}
		json.NewDecoder(r.Body).Decode(&out)
		r.Body.Close()
		for _, m := range out.Models {
			n := strings.TrimPrefix(m.Name, "models/")
			ok := false
			for _, x := range m.Methods {
				ok = ok || x == "generateContent"
			}
			if ok && strings.Contains(n, "flash") && !strings.Contains(n, "image") && !strings.Contains(n, "tts") && !strings.Contains(n, "live") && !strings.Contains(n, "audio") {
				names = append(names, n)
			}
		}
	}
	// the free tier has searches on the 2.5 and 2.0 Flash models: those go first after the one that worked before
	rank := func(n string) int {
		switch {
		case n == searchModel:
			return 0
		case n == "gemini-2.5-flash":
			return 1
		case n == "gemini-2.5-flash-lite":
			return 2
		case n == "gemini-2.0-flash":
			return 3
		case n == first:
			return 4
		case strings.Contains(n, "preview") || strings.Contains(n, "exp"):
			return 9
		}
		return 5
	}
	if first != "" && !contains(names, first) {
		names = append(names, first)
	}
	sort.SliceStable(names, func(i, j int) bool { return rank(names[i]) < rank(names[j]) })
	if len(names) > 6 {
		names = names[:6]
	}
	return names
}

// gemSearch: one question to Gemini with Google Search on; the plain text of the answer
func gemSearch(key, prompt string) (string, error) {
	models := searchModels(key)
	if len(models) == 0 {
		return "", fmt.Errorf("no hi ha cap model disponible")
	}
	body, _ := json.Marshal(map[string]any{
		"contents":         []map[string]any{{"role": "user", "parts": []map[string]string{{"text": prompt}}}},
		"tools":            []map[string]any{{"google_search": map[string]any{}}},
		"generationConfig": map[string]any{"maxOutputTokens": 4096, "temperature": 0.2},
	})
	var lastErr error
	for _, model := range models {
		req, _ := http.NewRequest("POST", gemBase()+"/v1beta/models/"+model+":generateContent", bytes.NewReader(body))
		req.Header.Set("x-goog-api-key", key)
		req.Header.Set("content-type", "application/json")
		t0 := time.Now()
		r, err := searchHTTP.Do(req)
		if err != nil {
			lastErr = err
			continue
		}
		raw, _ := io.ReadAll(io.LimitReader(r.Body, 2<<20))
		r.Body.Close()
		if r.StatusCode != 200 {
			quota := ""
			if m := regexp.MustCompile(`"quotaId":\s*"([^"]+)"`).FindSubmatch(raw); m != nil {
				quota = string(m[1])
			}
			logf("IA amb cerca: %s HTTP %d %s", model, r.StatusCode, quota)
			if r.StatusCode == 429 {
				lastErr = errQuota
			} else {
				lastErr = fmt.Errorf("HTTP %d", r.StatusCode)
			}
			continue // quota or a model without search: the next one
		}
		aiUsedToday(1)
		var out struct {
			Candidates []struct {
				Content struct {
					Parts []struct {
						Text    string `json:"text"`
						Thought bool   `json:"thought"`
					} `json:"parts"`
				} `json:"content"`
			} `json:"candidates"`
		}
		json.Unmarshal(raw, &out)
		var sb strings.Builder
		if len(out.Candidates) > 0 {
			for _, p := range out.Candidates[0].Content.Parts {
				if !p.Thought {
					sb.WriteString(p.Text)
				}
			}
		}
		searchModel = model
		logf("IA amb cerca (%s, %.1f s)", model, time.Since(t0).Seconds())
		return sb.String(), nil
	}
	return "", lastErr
}

var errQuota = fmt.Errorf("quota")

func proVidsFile(champ string) string {
	return profileFile("pro_videos_" + regexp.MustCompile(`[^A-Za-z0-9]`).ReplaceAllString(champ, "") + ".json")
}

// GET /api/provids?champ=Ekko[&name=Ekko][&fresh=1]
func proVidsHandler(w http.ResponseWriter, r *http.Request) {
	champ := r.URL.Query().Get("champ")
	name := r.URL.Query().Get("name")
	if name == "" {
		name = champ
	}
	reply := func(v proVids) { w.Header().Set("content-type", "application/json"); json.NewEncoder(w).Encode(v) }
	if champ == "" {
		reply(proVids{Error: "falta el campió"})
		return
	}
	var cached proVids
	if b, err := os.ReadFile(proVidsFile(champ)); err == nil {
		json.Unmarshal(b, &cached)
	}
	if len(cached.Vids) > 0 && time.Since(time.Unix(cached.At, 0)) < 7*24*time.Hour && r.URL.Query().Get("fresh") == "" {
		cached.Cached = true
		reply(cached)
		return
	}
	c := loadConfig()
	if c.AIKey == "" {
		cached.Error = "Per buscar vídeos de pros cal la clau de la IA (Ajustos)."
		reply(cached)
		return
	}
	proMu.Lock()
	if proBusy[champ] {
		proMu.Unlock()
		cached.Error = "Ja els estic buscant: torna-ho a mirar d'aquí a un moment."
		reply(cached)
		return
	}
	proBusy[champ] = true
	proMu.Unlock()
	defer func() { proMu.Lock(); delete(proBusy, champ); proMu.Unlock() }()

	year := time.Now().Year()
	prompt := fmt.Sprintf(`Search YouTube for videos of professional or Challenger/Grandmaster League of Legends players playing %[1]s, as recent as possible (%[2]d or %[3]d, current patch if you can).
I want three kinds:
- "proview": full games or long parts from the player's own camera (pro view, Korean Challenger, high elo gameplay of %[1]s).
- "plays": highlights or outplay montages of %[1]s by pros or one-tricks.
- "guide": a guide or combo tutorial for %[1]s by a high elo player or coach.
Give 4 of each if you can find them. Only videos that you actually found in the search, with their real link; never invent one.
Answer only with JSON, no other text: [{"url":"https://www.youtube.com/watch?v=...","kind":"proview|plays|guide"}]`, name, year, year-1)
	seen := map[string]bool{}
	got := []proVid{}
	for try := 0; try < 2 && len(got) < 3; try++ {
		txt, err := gemSearch(c.AIKey, prompt)
		if err != nil {
			logf("Vídeos de pros (%s): %v", name, err)
			if len(cached.Vids) > 0 {
				cached.Cached = true
				reply(cached)
				return
			}
			msg := "La IA no ha pogut buscar vídeos: " + err.Error()
			if err == errQuota {
				msg = "Google no deixa fer cerques a internet gratuïtes amb la teva clau ara mateix (he provat tots els models). Si és el límit del dia, es reinicia a les 9 del matí. Mentrestant, tens les cerques de YouTube de sota."
			}
			reply(proVids{Champ: champ, Error: msg})
			return
		}
		// the kind next to each link, when the answer is the JSON asked for
		kinds := map[string]string{}
		var arr []struct{ URL, Kind string }
		if m := regexp.MustCompile(`(?s)\[.*\]`).FindString(txt); m != "" && json.Unmarshal([]byte(m), &arr) == nil {
			for _, a := range arr {
				if id := ytID.FindStringSubmatch(a.URL); id != nil {
					kinds[id[1]] = a.Kind
				}
			}
		}
		for _, m := range ytID.FindAllStringSubmatch(txt, -1) {
			id := m[1]
			if seen[id] || len(got) >= 12 {
				continue
			}
			seen[id] = true
			if v, ok := ytCheck(id, name); ok {
				v.Kind = kinds[id]
				got = append(got, v)
			}
		}
		logf("Vídeos de pros (%s): %d enllaços, %d comprovats a YouTube", name, len(seen), len(got))
		prompt += "\nSome links you gave before didn't exist or weren't about this champion: search again and copy the links exactly from the results."
	}
	res := proVids{At: time.Now().Unix(), Champ: champ, Vids: got}
	if len(got) == 0 {
		if len(cached.Vids) > 0 {
			cached.Cached = true
			reply(cached)
			return
		}
		res.Error = "La IA no ha trobat cap vídeo que existeixi de veritat amb aquest campió."
		reply(res)
		return
	}
	if b, err := json.Marshal(res); err == nil {
		os.WriteFile(proVidsFile(champ), b, 0o644)
	}
	reply(res)
}
