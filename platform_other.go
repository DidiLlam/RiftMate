//go:build !windows

package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
)

func handleInstallFlags(args []string) bool { return false }

func openFolder(p string) { exec.Command("xdg-open", filepath.Dir(p)).Start() }

func openApp(url string) {
	if exec.Command("xdg-open", url).Start() != nil {
		fmt.Println("Obre", url)
	}
}

// findLCU (tests): lockfile path from LT_LCU_LOCKFILE.
func findLCU() *lcuCreds {
	if p := os.Getenv("LT_LCU_LOCKFILE"); p != "" {
		c, _ := parseLockfile(p)
		return c
	}
	return nil
}

// detach: the new copy started after an update must outlive this one.
func detach(cmd *exec.Cmd) { cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true} }

func killReplay() {}
