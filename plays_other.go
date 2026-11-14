//go:build !windows

package main

import (
	"errors"
	"time"
)

const playsSupported = false

func captureStart()        {}
func captureStop()         {}
func captureProbe() string { return "" }
func captureCut(from, to time.Time, out string) error {
	return errors.New("només a Windows")
}

func captureCleanup() {}

func rankPreview(src, dst string) error { return errors.New("només a Windows") }
