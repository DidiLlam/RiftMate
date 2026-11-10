//go:build !windows

package main

const whisperSupported = false

func whisperServe() error { return nil }
func whisperStop()        {}
