package main

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Backup: one .zip with the app's data (reports, history, settings, keys), and restoring it. Left out: the
// replays, the 3D models, the video clips and the log (they are big and come back on their own).
func backupSkip(rel string) bool {
	top := strings.SplitN(filepath.ToSlash(rel), "/", 2)[0]
	switch top {
	case "repeticions", "models", "clips", "escuts", "WebView2", "chrome", "whisper":
		return true
	}
	low := strings.ToLower(rel)
	return strings.HasPrefix(low, "registre") || strings.HasSuffix(low, ".exe") || strings.HasSuffix(low, ".old") || strings.HasSuffix(low, ".tmp")
}

func backupHandler(w http.ResponseWriter, req *http.Request) {
	root := filepath.Dir(dataDir())
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	n := 0
	filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		rel, _ := filepath.Rel(root, p)
		if rel == "." {
			return nil
		}
		if backupSkip(rel) {
			if info.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if info.IsDir() || info.Size() > 50<<20 {
			return nil
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return nil
		}
		f, err := zw.Create(filepath.ToSlash(rel))
		if err == nil {
			f.Write(b)
			n++
		}
		return nil
	})
	zw.Close()
	logf("Còpia de seguretat: %d fitxers (%d KB)", n, buf.Len()/1024)
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="RiftMate-copia-%s.zip"`, time.Now().Format("2006-01-02")))
	w.Write(buf.Bytes())
}

func restoreHandler(send func(http.ResponseWriter, int, string, []byte)) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		fail := func(m string) {
			b, _ := json.Marshal(map[string]string{"error": m})
			send(w, 200, "application/json", b)
		}
		if req.Method != http.MethodPost {
			fail("post")
			return
		}
		data, err := io.ReadAll(io.LimitReader(req.Body, 500<<20))
		if err != nil {
			fail(err.Error())
			return
		}
		zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
		if err != nil {
			fail("no és un fitxer de còpia de RiftMate")
			return
		}
		root := filepath.Dir(dataDir())
		n := 0
		for _, f := range zr.File {
			name := filepath.FromSlash(f.Name)
			dst := filepath.Join(root, name)
			if f.FileInfo().IsDir() || backupSkip(name) || !strings.HasPrefix(dst, root+string(os.PathSeparator)) {
				continue // folders, skipped kinds, and any path that would leave the data folder
			}
			rc, err := f.Open()
			if err != nil {
				continue
			}
			b, err := io.ReadAll(io.LimitReader(rc, 60<<20))
			rc.Close()
			if err != nil {
				continue
			}
			os.MkdirAll(filepath.Dir(dst), 0o755)
			if os.WriteFile(dst, b, 0o644) == nil {
				n++
			}
		}
		logf("Còpia restaurada: %d fitxers", n)
		b, _ := json.Marshal(map[string]int{"files": n})
		send(w, 200, "application/json", b)
	}
}
