//go:build !windows

package main

import "errors"

const voiceSupported = false

func voiceStart(champs []string, lanes bool) error { return errors.New("unsupported") }
func voiceStop()                                   {}
func voiceRunning() bool                           { return false }
