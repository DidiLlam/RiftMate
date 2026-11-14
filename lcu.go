package main

import (
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

// The League client (LCU) serves a local API while it is open. Champion select is only available there.
type lcuCreds struct {
	Port, Token string
}

var (
	lcuMu     sync.Mutex
	lcuCached *lcuCreds
	lcuTried  time.Time
)

var lcuHTTP = &http.Client{Timeout: 2 * time.Second, Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}}

// parseLockfile reads "name:pid:port:password:protocol".
func parseLockfile(path string) (*lcuCreds, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	f := strings.Split(strings.TrimSpace(string(b)), ":")
	if len(f) < 5 {
		return nil, errors.New("lockfile")
	}
	return &lcuCreds{Port: f[2], Token: f[3]}, nil
}

func lcuGet(path string, out any) (int, error) { return lcuDo("GET", path, nil, out) }

// lcuBytes: a file the client serves (its images), raw
func lcuBytes(path string) ([]byte, error) {
	lcuMu.Lock()
	c := lcuCached
	if c == nil && time.Since(lcuTried) > 5*time.Second {
		lcuTried = time.Now()
		c = findLCU()
		lcuCached = c
	}
	lcuMu.Unlock()
	if c == nil {
		return nil, errors.New("no_client")
	}
	req, _ := http.NewRequest("GET", "https://127.0.0.1:"+c.Port+path, nil)
	req.Header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte("riot:"+c.Token)))
	r, err := lcuHTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer r.Body.Close()
	if r.StatusCode != 200 {
		return nil, fmt.Errorf("client HTTP %d", r.StatusCode)
	}
	return io.ReadAll(io.LimitReader(r.Body, 8<<20))
}

// lcuDo: any method; body is sent as JSON
func lcuDo(method, path string, in, out any) (int, error) {
	lcuMu.Lock()
	c := lcuCached
	if c == nil && time.Since(lcuTried) > 5*time.Second {
		lcuTried = time.Now()
		c = findLCU() // platform specific
		lcuCached = c
	}
	lcuMu.Unlock()
	if c == nil {
		return 0, errors.New("no_client")
	}
	var rd io.Reader
	if in != nil {
		raw, _ := json.Marshal(in)
		rd = strings.NewReader(string(raw))
	}
	req, _ := http.NewRequest(method, "https://127.0.0.1:"+c.Port+path, rd)
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte("riot:"+c.Token)))
	r, err := lcuHTTP.Do(req)
	if err != nil {
		lcuMu.Lock()
		lcuCached = nil // client closed or restarted
		lcuMu.Unlock()
		return 0, err
	}
	defer r.Body.Close()
	body, _ := io.ReadAll(r.Body)
	if r.StatusCode >= 200 && r.StatusCode < 300 && out != nil && len(body) > 0 {
		return r.StatusCode, json.Unmarshal(body, out)
	}
	setPart("client", true, "")
	if r.StatusCode >= 400 && method != "GET" {
		logf("Client: %s %s -> %d %s", method, path, r.StatusCode, strings.TrimSpace(string(body)))
	}
	return r.StatusCode, nil
}

// ---- champion ids -> names (Data Dragon) ----
type ddChamp struct{ ID, Name string }

var (
	ddChampMu sync.Mutex
	ddChamps  map[string]ddChamp // numeric key -> champion
)

func championByKey(key int) ddChamp {
	ddChampMu.Lock()
	defer ddChampMu.Unlock()
	if ddChamps == nil {
		var data struct {
			Data map[string]struct {
				ID   string `json:"id"`
				Key  string `json:"key"`
				Name string `json:"name"`
			} `json:"data"`
		}
		var body io.Reader
		if f := os.Getenv("LT_CHAMPS_JSON"); f != "" { // tests
			if fh, err := os.Open(f); err == nil {
				defer fh.Close()
				body = fh
			}
		} else if r, err := riotHTTP.Get("https://ddragon.leagueoflegends.com/cdn/" + dataDragonVersion() + "/data/en_US/champion.json"); err == nil {
			defer r.Body.Close()
			body = r.Body
		}
		if body != nil {
			if json.NewDecoder(body).Decode(&data) == nil {
				ddChamps = map[string]ddChamp{}
				for _, c := range data.Data {
					ddChamps[c.Key] = ddChamp{c.ID, c.Name}
				}
			}
		}
		if ddChamps == nil {
			return ddChamp{}
		}
	}
	return ddChamps[itoa(key)]
}

func itoa(n int) string {
	b, _ := json.Marshal(n)
	return string(b)
}

var spellByID = map[int]string{4: "Flash", 14: "Ignite", 12: "Teleport", 7: "Heal", 3: "Exhaust", 21: "Barrier", 6: "Ghost", 1: "Cleanse", 11: "Smite"}

type csPlayer struct {
	Champ    string `json:"champ"` // Data Dragon id
	Name     string `json:"name"`  // display name
	Position string `json:"position,omitempty"`
	Me       bool   `json:"me,omitempty"`
	Locked   bool   `json:"locked,omitempty"`
	Spell1   string `json:"spell1,omitempty"`
	Spell2   string `json:"spell2,omitempty"`
}

// champSelect returns the current champion select (names of other players are never read or shown).
func champSelect() map[string]any {
	var s struct {
		LocalPlayerCellID int `json:"localPlayerCellId"`
		MyTeam            []struct {
			CellID             int    `json:"cellId"`
			ChampionID         int    `json:"championId"`
			ChampionPickIntent int    `json:"championPickIntent"`
			AssignedPosition   string `json:"assignedPosition"`
			Spell1ID           int    `json:"spell1Id"`
			Spell2ID           int    `json:"spell2Id"`
		} `json:"myTeam"`
		TheirTeam []struct {
			ChampionID int `json:"championId"`
		} `json:"theirTeam"`
		Timer struct {
			Phase string `json:"phase"`
		} `json:"timer"`
	}
	code, err := lcuGet("/lol-champ-select/v1/session", &s)
	if err != nil {
		return map[string]any{"client": false}
	}
	if code != 200 {
		return map[string]any{"client": true, "active": false}
	}
	mine, theirs := []csPlayer{}, []csPlayer{}
	myPos := ""
	for _, p := range s.MyTeam {
		id := p.ChampionID
		locked := id != 0
		if id == 0 {
			id = p.ChampionPickIntent
		}
		c := championByKey(id)
		pl := csPlayer{Champ: c.ID, Name: c.Name, Position: strings.ToUpper(p.AssignedPosition), Me: p.CellID == s.LocalPlayerCellID, Locked: locked,
			Spell1: spellByID[p.Spell1ID], Spell2: spellByID[p.Spell2ID]}
		if pl.Me {
			myPos = pl.Position
		}
		mine = append(mine, pl)
	}
	for _, p := range s.TheirTeam {
		if p.ChampionID == 0 {
			continue
		}
		c := championByKey(p.ChampionID)
		theirs = append(theirs, csPlayer{Champ: c.ID, Name: c.Name})
	}
	return map[string]any{"client": true, "active": true, "phase": s.Timer.Phase, "my_position": myPos, "my_team": mine, "their_team": theirs}
}
