package main

import (
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"time"
)

// The player's summoner name, level and profile icon, for the header of the page.
// From the League client when it is open (always current), otherwise from Riot's API with the saved Riot ID;
// the last answer is kept in the config so the header still shows something with neither available.
type meInfo struct {
	Name    string `json:"name"`
	Tag     string `json:"tag"`
	Level   int    `json:"level"`
	Icon    int    `json:"icon"`
	Version string `json:"version,omitempty"`
	Owner   bool   `json:"owner"`
	Ask     bool   `json:"ask,omitempty"` // an account the app doesn't know yet: is it the owner's or someone else's?
	Main    string `json:"main,omitempty"`
	Viewing bool   `json:"viewing,omitempty"` // another of your accounts, not the one in use
	Playing string `json:"playing,omitempty"` // then: the one in use
}

// the header of another of your accounts (Riot's API), kept 10 minutes
var meOther = map[string]struct {
	m  meInfo
	at time.Time
}{}

func meFor(id string) *meInfo {
	if v, ok := meOther[strings.ToLower(id)]; ok && time.Since(v.at) < 10*time.Minute {
		return &v.m
	}
	c := loadConfig()
	p := strings.SplitN(id, "#", 2)
	m := meInfo{Name: p[0], Tag: p[1]}
	if puuid, err := puuidFor(&c, id); err == nil && c.RiotKey != "" {
		var sm struct {
			ProfileIconID int `json:"profileIconId"`
			SummonerLevel int `json:"summonerLevel"`
		}
		if riotGetHostQ(c, platformHost(c), "/lol/summoner/v4/summoners/by-puuid/"+puuid, &sm, true) == nil {
			m.Level, m.Icon = sm.SummonerLevel, sm.ProfileIconID
		}
	}
	meOther[strings.ToLower(id)] = struct {
		m  meInfo
		at time.Time
	}{m, time.Now()}
	return &m
}

var (
	meMu   sync.Mutex
	meLast *meInfo
	meAt   time.Time
)

func meJSON() []byte {
	meMu.Lock()
	defer meMu.Unlock()
	if meLast == nil || time.Since(meAt) > 3*time.Minute {
		if m := meFetch(); m != nil {
			meLast, meAt = m, time.Now()
			c := loadConfig()
			if c.Me == nil || *c.Me != *m {
				c.Me = m
				saveConfig(c)
			}
		} else if c := loadConfig(); c.Me != nil {
			meLast, meAt = c.Me, time.Now().Add(-2*time.Minute) // try again in a minute
		}
	}
	out := meInfo{}
	if meLast != nil {
		out = *meLast
	}
	out.Version = version
	if v := viewOrCurrent(); out.Name != "" && strings.Contains(v, "#") && !sameID(v, out.Name+"#"+out.Tag) {
		playing := out.Name + "#" + out.Tag
		out = *meFor(v)
		out.Version, out.Viewing, out.Playing = version, true, playing
	}
	out.Owner = out.Name == "" || isOwner(out.Name+"#"+out.Tag)
	if !out.Owner {
		c := loadConfig()
		out.Ask = !contains(c.NotMine, strings.ToLower(out.Name+"#"+out.Tag))
		out.Main = c.Owner
	}
	b, _ := json.Marshal(out)
	return b
}

func meFetch() *meInfo {
	var s struct {
		GameName      string `json:"gameName"`
		TagLine       string `json:"tagLine"`
		SummonerLevel int    `json:"summonerLevel"`
		ProfileIconID int    `json:"profileIconId"`
	}
	if code, err := lcuGet("/lol-summoner/v1/current-summoner", &s); err == nil && code == 200 && s.GameName != "" {
		if s.TagLine != "" {
			setMyRiotID(s.GameName + "#" + s.TagLine)
		}
		return &meInfo{Name: s.GameName, Tag: s.TagLine, Level: s.SummonerLevel, Icon: s.ProfileIconID}
	}
	c := loadConfig()
	id := c.MyRiotID
	if c.RiotKey == "" || !strings.Contains(id, "#") {
		return nil
	}
	puuid, err := puuidFor(&c, id)
	if err != nil {
		return nil
	}
	var sm struct {
		ProfileIconID int `json:"profileIconId"`
		SummonerLevel int `json:"summonerLevel"`
	}
	if err := riotGetHostQ(c, platformHost(c), "/lol/summoner/v4/summoners/by-puuid/"+puuid, &sm, true); err != nil {
		return nil
	}
	p := strings.SplitN(id, "#", 2)
	return &meInfo{Name: p[0], Tag: p[1], Level: sm.SummonerLevel, Icon: sm.ProfileIconID}
}

func meHandler(send func(http.ResponseWriter, int, string, []byte)) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) { send(w, 200, "application/json", meJSON()) }
}
