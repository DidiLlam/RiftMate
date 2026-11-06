package main

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"sync"
)

// Replays: the game data the page received, every 4 seconds of game time, kept for the last 3 games. Sent to the
// developer, a replay lets the coach be run again over a real game to find wrong or repeated advice.
var (
	rpMu     sync.Mutex
	rpFrames []json.RawMessage
	rpLastT  = -1.0
)

func replayDir() string { return filepath.Join(filepath.Dir(dataDir()), "repeticions") }

func replayNote(raw []byte) {
	var g struct {
		GameData struct {
			GameTime *float64 `json:"gameTime"`
		} `json:"gameData"`
	}
	if json.Unmarshal(raw, &g) != nil || g.GameData.GameTime == nil {
		return
	}
	t := *g.GameData.GameTime
	rpMu.Lock()
	defer rpMu.Unlock()
	if t < rpLastT-10 { // a new game
		rpFrames, rpLastT = nil, -1
	}
	if rpLastT < 0 || t-rpLastT >= 4 {
		rpFrames = append(rpFrames, append(json.RawMessage{}, raw...))
		rpLastT = t
	}
}

// replaySave writes the game that just ended (stamp: the report's name) and keeps the 3 newest
func replaySave(stamp string) {
	rpMu.Lock()
	frames := rpFrames
	rpFrames, rpLastT = nil, -1
	rpMu.Unlock()
	if len(frames) < 20 {
		return
	}
	os.MkdirAll(replayDir(), 0o755)
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	json.NewEncoder(zw).Encode(frames)
	zw.Close()
	p := filepath.Join(replayDir(), stamp+".json.gz")
	if err := os.WriteFile(p, buf.Bytes(), 0o644); err != nil {
		logf("Repetició: no l'he pogut guardar: %v", err)
		return
	}
	logf("Repetició guardada: %s (%d moments, %d KB)", filepath.Base(p), len(frames), buf.Len()/1024)
	files, _ := filepath.Glob(filepath.Join(replayDir(), "*.json.gz"))
	sort.Strings(files)
	for len(files) > 3 {
		os.Remove(files[0])
		files = files[1:]
	}
}
