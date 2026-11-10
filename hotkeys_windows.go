//go:build windows

package main

import (
	"runtime"
	"syscall"
	"time"
	"unsafe"
)

// Global keyboard shortcuts for the summoner spell timers, without the voice: Ctrl+Shift+1..5 the Flash of the
// enemy top, jungle, mid, ADC and support; Ctrl+Alt+1..5 their other spell. Registered with Windows
// (RegisterHotKey), outside the game: nothing touches the game's process.
func startHotkeys() {
	go pttWatch()
	go func() {
		runtime.LockOSThread()
		user32 := syscall.NewLazyDLL("user32.dll")
		reg, getMsg := user32.NewProc("RegisterHotKey"), user32.NewProc("GetMessageW")
		const modAlt, modCtrl, modShift, noRepeat = 1, 2, 4, 0x4000
		ok := 0
		for i := 0; i < 5; i++ {
			if r, _, _ := reg.Call(0, uintptr(100+i), modCtrl|modShift|noRepeat, uintptr(0x31+i)); r != 0 {
				ok++
			}
			if r, _, _ := reg.Call(0, uintptr(200+i), modCtrl|modAlt|noRepeat, uintptr(0x31+i)); r != 0 {
				ok++
			}
		}
		logf("Dreceres de teclat: %d de 10 registrades", ok)
		var msg struct {
			Hwnd    uintptr
			Message uint32
			WParam  uintptr
			LParam  uintptr
			Time    uint32
			Pt      [2]int32
		}
		for {
			r, _, _ := getMsg.Call(uintptr(unsafe.Pointer(&msg)), 0, 0, 0)
			if int32(r) <= 0 {
				return
			}
			if msg.Message == 0x0312 { // WM_HOTKEY
				id := int(msg.WParam)
				switch {
				case id >= 100 && id < 105:
					hotkeyPush(id-100, "flash")
				case id >= 200 && id < 205:
					hotkeyPush(id-200, "other")
				}
			}
		}
	}()
}

// pttWatch: is the push-to-talk key held? (checked 40 times a second, only while push to talk is on)
var pGetAsyncKeyStatePTT = user32.NewProc("GetAsyncKeyState")

func pttWatch() {
	for {
		c := pttGet()
		if !c.On {
			time.Sleep(500 * time.Millisecond)
			continue
		}
		if k, _, _ := pGetAsyncKeyStatePTT.Call(uintptr(c.Key)); k&0x8000 != 0 {
			pttSeen()
		}
		time.Sleep(25 * time.Millisecond)
	}
}
