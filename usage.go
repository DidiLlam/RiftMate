package main

import (
	"runtime"
	"time"
)

// usageLog: what RiftMate itself used during the game (processor and memory), so the log shows if it costs the game
var usageLast struct {
	cpu time.Duration
	at  time.Time
}

func usageLog(gameSec float64) {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	cpu := procCPU()
	now := time.Now()
	span := now.Sub(usageLast.at)
	if usageLast.at.IsZero() || span > time.Duration(gameSec*float64(time.Second))+5*time.Minute {
		span = time.Duration(gameSec * float64(time.Second))
		usageLast.cpu = 0
	}
	used := cpu - usageLast.cpu
	usageLast.cpu, usageLast.at = cpu, now
	pct := 0.0
	if span > 0 && cpu > 0 {
		pct = 100 * used.Seconds() / span.Seconds()
	}
	logf("Consum de RiftMate (programa): %.0f s de processador (%.1f%% d'un nucli de mitjana), %d MB de memòria", used.Seconds(), pct, m.Sys>>20)
}
