//go:build windows

package main

import (
	"fmt"
	"net/http"
	"os/exec"
	"path/filepath"
	"strconv"
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
	wsMu.Lock()
	running := wsCmd != nil
	wsMu.Unlock()
	if running || alive() { // ours, or the one a previous run of the app left (after an update)
		return nil
	}
	dir := whisperDir()
	cmd := exec.Command(filepath.Join(dir, "whisper-server.exe"), "-m", filepath.Join(dir, whisperModel),
		"--host", "127.0.0.1", "--port", strconv.Itoa(whisperPort), "-t", "4", "-nf", "-l", "es")
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
