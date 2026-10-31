package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Automatic updates: the program reads latest.json from the "dist" branch of DidiLlam/RiftMate on GitHub
// (version, download address, size, SHA-256, notes); the page offers the update and, when accepted, the new
// RiftMate.exe is downloaded, checked (size, Windows executable, SHA-256), swapped in place of the running
// one and started. A GitHub release in the same repository is understood too.
const updateRepo = "DidiLlam/RiftMate"

type relAsset struct {
	Name   string `json:"name"`
	URL    string `json:"browser_download_url"`
	Size   int64  `json:"size"`
	Digest string `json:"digest"`
}
type relInfo struct {
	Tag    string     `json:"tag_name"`
	Name   string     `json:"name"`
	Body   string     `json:"body"`
	Draft  bool       `json:"draft"`
	Pre    bool       `json:"prerelease"`
	Assets []relAsset `json:"assets"`
}

var (
	updMu   sync.Mutex
	updLast *relInfo
	updAt   time.Time
	updBusy bool
	updHTTP = &http.Client{Timeout: 5 * time.Minute}
)

func updateBase() string {
	if b := os.Getenv("LT_UPDATE_BASE"); b != "" { // tests
		return b
	}
	return "https://raw.githubusercontent.com/" + updateRepo + "/dist/latest.json"
}

// newerVersion: is a (like "2.0.1" or "v2.0.1") newer than b?
func newerVersion(a, b string) bool {
	pa, pb := verParts(a), verParts(b)
	for i := 0; i < 3; i++ {
		if pa[i] != pb[i] {
			return pa[i] > pb[i]
		}
	}
	return false
}

func verParts(v string) [3]int {
	var out [3]int
	for i, p := range strings.SplitN(strings.TrimPrefix(strings.TrimSpace(v), "v"), ".", 3) {
		n, _ := strconv.Atoi(strings.TrimFunc(p, func(r rune) bool { return r < '0' || r > '9' }))
		out[i] = n
	}
	return out
}

func latestRelease(force bool) (*relInfo, error) {
	updMu.Lock()
	defer updMu.Unlock()
	if !force && updLast != nil && time.Since(updAt) < 10*time.Minute {
		return updLast, nil
	}
	req, _ := http.NewRequest("GET", updateBase(), nil)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", appName+"/"+version)
	r, err := (&http.Client{Timeout: 15 * time.Second}).Do(req)
	if err != nil {
		return nil, err
	}
	defer r.Body.Close()
	if r.StatusCode != 200 {
		return nil, fmt.Errorf("HTTP %d", r.StatusCode)
	}
	var rel relInfo
	b, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	var man struct {
		Version string `json:"version"`
		URL     string `json:"url"`
		Size    int64  `json:"size"`
		SHA256  string `json:"sha256"`
		Notes   string `json:"notes"`
	}
	if json.Unmarshal(b, &man) == nil && man.Version != "" { // latest.json of the dist branch
		rel = relInfo{Tag: man.Version, Body: man.Notes, Assets: []relAsset{{Name: appName + ".exe", URL: man.URL, Size: man.Size, Digest: "sha256:" + man.SHA256}}}
	} else if err := json.Unmarshal(b, &rel); err != nil {
		return nil, err
	}
	updLast, updAt = &rel, time.Now()
	return &rel, nil
}

func exeAsset(rel *relInfo) *relAsset {
	for i := range rel.Assets {
		if strings.EqualFold(rel.Assets[i].Name, appName+".exe") {
			return &rel.Assets[i]
		}
	}
	return nil
}

func updateJSON(force bool) []byte {
	out := map[string]any{"current": version}
	rel, err := latestRelease(force)
	if err != nil {
		out["error"] = err.Error()
	} else {
		latest := strings.TrimPrefix(rel.Tag, "v")
		out["latest"] = latest
		out["notes"] = rel.Body
		out["available"] = !rel.Draft && !rel.Pre && exeAsset(rel) != nil && newerVersion(latest, version)
	}
	updMu.Lock()
	out["busy"] = updBusy
	updMu.Unlock()
	b, _ := json.Marshal(out)
	return b
}

// applyUpdate downloads and installs the latest release; the caller restarts the program.
func applyUpdate() (string, error) {
	rel, err := latestRelease(true)
	if err != nil {
		return "", err
	}
	latest := strings.TrimPrefix(rel.Tag, "v")
	a := exeAsset(rel)
	if a == nil || !newerVersion(latest, version) {
		return "", fmt.Errorf("no hi ha cap versió nova")
	}
	self, err := os.Executable()
	if err != nil {
		return "", err
	}
	self, _ = filepath.EvalSymlinks(self)
	tmp := self + ".new"
	logf("Actualització: descarregant %s %s (%d bytes)", appName, latest, a.Size)
	r, err := updHTTP.Get(a.URL)
	if err != nil {
		return "", err
	}
	defer r.Body.Close()
	if r.StatusCode != 200 {
		return "", fmt.Errorf("descàrrega: HTTP %d", r.StatusCode)
	}
	f, err := os.Create(tmp)
	if err != nil {
		return "", err
	}
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(f, h), r.Body)
	f.Close()
	fail := func(e error) (string, error) { os.Remove(tmp); return "", e }
	if err != nil {
		return fail(err)
	}
	if a.Size > 0 && n != a.Size {
		return fail(fmt.Errorf("descàrrega incompleta (%d de %d bytes)", n, a.Size))
	}
	if d := strings.TrimPrefix(a.Digest, "sha256:"); d != "" && !strings.EqualFold(d, hex.EncodeToString(h.Sum(nil))) {
		return fail(fmt.Errorf("el fitxer descarregat no coincideix amb el de GitHub"))
	}
	if runtime.GOOS == "windows" {
		head := make([]byte, 2)
		if fh, err := os.Open(tmp); err == nil {
			io.ReadFull(fh, head)
			fh.Close()
		}
		if string(head) != "MZ" {
			return fail(fmt.Errorf("el fitxer descarregat no és un programa de Windows"))
		}
	}
	os.Chmod(tmp, 0o755)
	old := self + ".old"
	os.Remove(old)
	if err := os.Rename(self, old); err != nil { // Windows lets a running exe be renamed, not overwritten
		return fail(err)
	}
	if err := os.Rename(tmp, self); err != nil {
		os.Rename(old, self)
		return fail(err)
	}
	logf("Actualització instal·lada: %s -> %s", version, latest)
	return self, nil
}

// cleanupOldExe removes the copy left by the last update (it may still be closing at first).
func cleanupOldExe() {
	self, err := os.Executable()
	if err != nil {
		return
	}
	self, _ = filepath.EvalSymlinks(self)
	for i := 0; i < 10; i++ {
		if _, err := os.Stat(self + ".old"); err != nil {
			return
		}
		if os.Remove(self+".old") == nil {
			return
		}
		time.Sleep(2 * time.Second)
	}
}

func updateHandlers(mux *http.ServeMux, send func(http.ResponseWriter, int, string, []byte), restart func(exe string)) {
	mux.HandleFunc("/api/update", func(w http.ResponseWriter, req *http.Request) {
		send(w, 200, "application/json", updateJSON(req.URL.Query().Get("force") == "1"))
	})
	mux.HandleFunc("/api/update/apply", func(w http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodPost {
			send(w, 405, "text/plain", []byte("POST only"))
			return
		}
		updMu.Lock()
		if updBusy {
			updMu.Unlock()
			send(w, 200, "application/json", []byte(`{"error":"ja s'està actualitzant"}`))
			return
		}
		updBusy = true
		updMu.Unlock()
		exe, err := applyUpdate()
		if err != nil {
			updMu.Lock()
			updBusy = false
			updMu.Unlock()
			logf("Actualització: ERROR %v", err)
			b, _ := json.Marshal(map[string]string{"error": err.Error()})
			send(w, 200, "application/json", b)
			return
		}
		send(w, 200, "application/json", []byte(`{"ok":true}`))
		go restart(exe)
	})
}

// startUpdated starts the new program, which waits for this one to free the port.
func startUpdated(exe string) error {
	cmd := exec.Command(exe, "--updated")
	cmd.Dir = filepath.Dir(exe)
	if f, err := os.Create(filepath.Join(filepath.Dir(dataDir()), "actualitzacio.txt")); err == nil {
		cmd.Stdout, cmd.Stderr = f, f // anything the new copy prints if it fails to start
	}
	detach(cmd)
	if err := cmd.Start(); err != nil {
		return err
	}
	logf("Actualització: versió nova oberta (procés %d)", cmd.Process.Pid)
	return cmd.Process.Release()
}
