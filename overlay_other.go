//go:build !windows

package main

func overlayOpenWindow()                    {}
func overlayApply(c overlayCfg, frame bool) {}
func overlayCloseWindow()                   {}
func overlayKeep()                          {}

func overlayWindowsAny() []uintptr { return nil }
