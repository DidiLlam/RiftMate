package main

import (
	"encoding/json"
	"os"
	"sort"
	"sync"
)

// Your voice's level: the loudest moment of each phrase you say (dB below full scale). You speak right into the
// microphone; your duo in the room, Discord or the game reach it quieter. The last phrases that passed are kept
// (across games) and one that is more than 12 dB below your usual level is ignored. It needs 10 phrases to learn
// you; until then everything counts.
var (
	vlMu   sync.Mutex
	vlRead bool
	vlL    []float64
)

const vlMargin = 12.0

func vlFile() string { return profileFile("veu-volum.json") }

func voiceLoudEnough(db float64) (bool, float64) {
	vlMu.Lock()
	defer vlMu.Unlock()
	if !vlRead {
		vlRead = true
		if b, err := os.ReadFile(vlFile()); err == nil {
			json.Unmarshal(b, &vlL)
		}
	}
	if db <= -119 { // no level measured
		return true, 0
	}
	ref := 0.0
	ok := true
	if len(vlL) >= 10 {
		s := append([]float64{}, vlL...)
		sort.Float64s(s)
		ref = s[len(s)*7/10] // your usual level: a bit above the middle, so a few quiet ones don't pull it down
		ok = db >= ref-vlMargin
	}
	if ok {
		vlL = append(vlL, db)
		if len(vlL) > 60 {
			vlL = vlL[len(vlL)-60:]
		}
		if b, err := json.Marshal(vlL); err == nil {
			os.WriteFile(vlFile(), b, 0o644)
		}
	}
	return ok, ref
}
