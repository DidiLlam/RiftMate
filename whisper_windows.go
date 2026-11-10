//go:build windows

package main

import (
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

const whisperSupported = true

var (
	wsMu  sync.Mutex
	wsCmd *exec.Cmd
)

// whisperServe starts whisper.cpp's own little server with the model loaded once (each phrase then takes a
// fraction of a second). Low priority (the game goes first), 4 threads for a short burst per phrase.
func whisperServe() error {
	cl := &http.Client{Timeout: 2 * time.Second}
	alive := func() bool {
		r, err := cl.Get(fmt.Sprintf("http://127.0.0.1:%d/", whisperPort))
		if err != nil {
			return false
		}
		r.Body.Close()
		return true
	}
	dir := whisperDir()
	// -nc: each phrase on its own. Without it the server carries what it wrote before into the next phrase, and after
	// a laugh or a silence it got stuck writing "[RÍE]" for everything (which the app cleans to nothing).
	// -sns: no sound tags like [RÍE] or (música).
	args := []string{"-m", filepath.Join(dir, whisperModel), "--host", "127.0.0.1", "--port", strconv.Itoa(whisperPort), "-t", "4", "-l", "es", "-nc", "-sns"}
	sig := strings.Join(args, " ")
	wsMu.Lock()
	running := wsCmd != nil
	wsMu.Unlock()
	if running {
		return nil
	}
	if alive() { // one a previous run of the app left going (after an update): kept only if started the same way
		if b, _ := os.ReadFile(filepath.Join(dir, "servidor.txt")); string(b) == sig {
			return nil
		}
		k := exec.Command("taskkill", "/IM", "whisper-server.exe", "/F")
		k.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000}
		k.Run()
		logf("Whisper: reinicio el servidor (era d'una versió anterior)")
		time.Sleep(time.Second)
	}
	os.WriteFile(filepath.Join(dir, "servidor.txt"), []byte(sig), 0o644)
	cmd := exec.Command(filepath.Join(dir, "whisper-server.exe"), args...)
	cmd.Dir = dir
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000 | 0x00004000} // no window, below normal priority
	if err := cmd.Start(); err != nil {
		return err
	}
	exited := make(chan struct{})
	wsMu.Lock()
	wsCmd = cmd
	wsMu.Unlock()
	go func() {
		err := cmd.Wait()
		close(exited)
		wsMu.Lock()
		if wsCmd == cmd {
			wsCmd = nil
		}
		wsMu.Unlock()
		whisperSet("error", fmt.Sprint("aturat: ", err))
		logf("Whisper: el servidor s'ha aturat (%v)", err)
	}()
	for i := 0; i < 60; i++ { // loading the model takes a second or two
		select {
		case <-exited:
			return fmt.Errorf("el servidor s'ha tancat en començar (potser falta el Visual C++ de Microsoft)")
		case <-time.After(500 * time.Millisecond):
		}
		if alive() {
			return nil
		}
	}
	return fmt.Errorf("el servidor no respon")
}

func whisperStop() {
	wsMu.Lock()
	c := wsCmd
	wsCmd = nil
	wsMu.Unlock()
	if c != nil && c.Process != nil {
		c.Process.Kill()
	}
}
