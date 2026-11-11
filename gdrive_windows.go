package main

import (
	"os"
	"path/filepath"
	"sync"
	"time"
)

var (
	gdMu   sync.Mutex
	gdAt   time.Time
	gdPath string
)

// gdriveFolder: the "RiftMate clips" folder inside Google Drive for desktop's "My Drive", or "" if it isn't installed.
// Drive mounts as a letter (G: by default) with "My Drive" named in the account's language; in "mirror" mode it's
// a folder in the user's profile.
func gdriveFolder() string {
	gdMu.Lock()
	defer gdMu.Unlock()
	if time.Since(gdAt) < 2*time.Minute {
		return gdPath
	}
	gdAt, gdPath = time.Now(), gdriveFind()
	return gdPath
}

func gdriveFind() string {
	names := []string{"My Drive", "La meva unitat", "Mi unidad", "Mon Drive", "Meine Ablage", "Il mio Drive", "O meu disco"}
	roots := []string{}
	for l := 'D'; l <= 'Z'; l++ {
		roots = append(roots, string(l)+`:\`)
	}
	if h, err := os.UserHomeDir(); err == nil {
		roots = append(roots, h)
	}
	for _, r := range roots {
		for _, n := range names {
			d := filepath.Join(r, n)
			if fi, err := os.Stat(d); err == nil && fi.IsDir() {
				return filepath.Join(d, "RiftMate clips")
			}
		}
	}
	return ""
}
