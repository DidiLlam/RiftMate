package main

import (
	"encoding/json"
	"net/http"
	"os"
	"strconv"
)

type overlayCfg struct {
	On      bool   `json:"on"`
	Pos     string `json:"pos"`     // tr, tl, br, bl
	Alpha   int    `json:"alpha"`   // 80..255
	Through bool   `json:"through"` // clicks go through to the game (timers by voice only); off = the spells can be clicked
	Always  bool   `json:"always"`  // also outside games (to place it)
}

func overlayFile() string { return profileFile("overlay.json") }

func overlayLoad() overlayCfg {
	c := overlayCfg{Pos: "tr", Alpha: 225, Through: true}
	if b, err := os.ReadFile(overlayFile()); err == nil {
		json.Unmarshal(b, &c)
	}
	return c
}

// /api/overlay: GET its settings; POST ?on=&pos=&alpha=&through=&always= to change them (opens or closes the window)
func overlayHandler(send func(http.ResponseWriter, int, string, []byte)) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		c := overlayLoad()
		if req.Method == http.MethodPost {
			q := req.URL.Query()
			was := c.On
			if v := q.Get("on"); v != "" {
				c.On = v == "1"
			}
			if v := q.Get("pos"); v == "tr" || v == "tl" || v == "br" || v == "bl" {
				c.Pos = v
			}
			if v, err := strconv.Atoi(q.Get("alpha")); err == nil && v >= 80 && v <= 255 {
				c.Alpha = v
			}
			if v := q.Get("through"); v != "" {
				c.Through = v == "1"
			}
			if v := q.Get("always"); v != "" {
				c.Always = v == "1"
			}
			if b, err := json.Marshal(c); err == nil {
				os.WriteFile(overlayFile(), b, 0o644)
			}
			switch {
			case c.On && !was:
				go overlayOpenWindow()
			case !c.On && was:
				overlayCloseWindow()
			case c.On:
				if len(overlayWindowsAny()) == 0 {
					go overlayOpenWindow()
				} else {
					overlayApply(c, false)
				}
			}
			logf("Overlay: %+v", c)
		}
		b, _ := json.Marshal(c)
		send(w, 200, "application/json", b)
	}
}

// overlayAuto: at start, if it was on, open it again (it hides itself outside games)
func overlayAuto() {
	go overlayKeep()
	if overlayLoad().On {
		overlayOpenWindow()
	}
}

func overlayPageHandler(w http.ResponseWriter, req *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Write([]byte(overlayPage))
}
