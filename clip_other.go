//go:build !windows

package main

import "os"

var lastClipboard string

// setClipboard: only Windows has the game; elsewhere (tests) the text is kept, and written to LT_CLIP_FILE if set
func setClipboard(text string) error {
	lastClipboard = text
	if f := os.Getenv("LT_CLIP_FILE"); f != "" {
		return os.WriteFile(f, []byte(text), 0o644)
	}
	return nil
}
