package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sync"
	"time"
)

var logMu sync.Mutex

func logPath() string { return filepath.Join(filepath.Dir(dataDir()), "registre.txt") }

// Never write the Riot key to the log.
var keyRe = regexp.MustCompile(`RGAPI-[A-Za-z0-9-]+`)
var aiKeyRe = regexp.MustCompile(`sk-ant-[A-Za-z0-9_-]+|AIza[A-Za-z0-9_-]{20,}|AQ\.[A-Za-z0-9_-]{20,}`)

// logf appends one timestamped line to registre.txt (rotated at 1 MB).
func logf(format string, args ...any) {
	line := keyRe.ReplaceAllString(fmt.Sprintf(format, args...), "RGAPI-***")
	line = aiKeyRe.ReplaceAllString(line, "***")
	logMu.Lock()
	defer logMu.Unlock()
	p := logPath()
	os.MkdirAll(filepath.Dir(p), 0o755)
	if st, err := os.Stat(p); err == nil && st.Size() > 1<<20 {
		os.Rename(p, filepath.Join(filepath.Dir(p), "registre-anterior.txt"))
	}
	f, err := os.OpenFile(p, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	fmt.Fprintf(f, "%s  %s\r\n", time.Now().Format("2006-01-02 15:04:05"), line)
}
