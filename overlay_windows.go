//go:build windows

package main

import (
	"fmt"
	"strings"
	"syscall"
	"time"
	"unsafe"
)

// The overlay: a small window of the app's own browser (title "RM·Overlay") that Windows keeps above the game.
// It's a separate window: nothing is drawn into the game or read from it. Clicking it doesn't take the focus from
// the game (WS_EX_NOACTIVATE); with "let clicks through" on, clicks go to the game below. It only shows over the
// game when the game's display mode is borderless or windowed (in exclusive fullscreen nothing can be on top).
var (
	procGetWindowLongPtr = user32.NewProc("GetWindowLongPtrW")
	procSetWindowLongPtr = user32.NewProc("SetWindowLongPtrW")
	procSetLayeredAttr   = user32.NewProc("SetLayeredWindowAttributes")
	procGetSystemMetrics = user32.NewProc("GetSystemMetrics")
	procPostMessage      = user32.NewProc("PostMessageW")
)

const overlayTitle = "RM·Overlay"

func overlayWindows() []uintptr {
	found := []uintptr{}
	cb := syscall.NewCallback(func(h uintptr, _ uintptr) uintptr {
		buf := make([]uint16, 128)
		procGetWindowText.Call(h, uintptr(unsafe.Pointer(&buf[0])), 128)
		cls := make([]uint16, 64)
		procGetClassName.Call(h, uintptr(unsafe.Pointer(&cls[0])), 64)
		if strings.HasPrefix(syscall.UTF16ToString(buf), overlayTitle) && strings.HasPrefix(syscall.UTF16ToString(cls), "Chrome_WidgetWin") {
			found = append(found, h)
		}
		return 1
	})
	procEnumWindows.Call(cb, 0)
	return found
}

func overlayOpenWindow() {
	c := overlayLoad()
	if len(overlayWindows()) > 0 { // already open (e.g. after an update restart): just put it in place
		overlayApply(c, true)
		return
	}
	launchBrowser(fmt.Sprintf("http://127.0.0.1:%d/overlay", port), fmt.Sprintf("--window-size=%d,%d", 250, 300))
	for i := 0; i < 40 && len(overlayWindows()) == 0; i++ {
		time.Sleep(250 * time.Millisecond)
	}
	overlayApply(c, true)
}

// overlayApply sets the window's look and place: no frame, always on top, not in the taskbar, see-through.
func overlayApply(c overlayCfg, frame bool) {
	const (
		gwlStyle, gwlExStyle                  = -16, -20
		wsCaption, wsThickFrame, wsSysMenu    = 0x00C00000, 0x00040000, 0x00080000
		wsExLayered, wsExToolWindow, wsExTop  = 0x80000, 0x80, 0x8
		wsExTransparent, wsExNoActivate       = 0x20, 0x08000000
		wsExAppWindow                         = 0x40000
		swpNoActivate, swpFrameChanged, swpSh = 0x10, 0x20, 0x40
	)
	sw, _, _ := procGetSystemMetrics.Call(0)
	sh, _, _ := procGetSystemMetrics.Call(1)
	w, h := 250, 300
	x, y := int(sw)-w-16, 120
	switch c.Pos {
	case "tl":
		x, y = 16, 120
	case "bl":
		x, y = 16, int(sh)-h-200
	case "br":
		x, y = int(sw)-w-16, int(sh)-h-200
	}
	gs, ge := int32(gwlStyle), int32(gwlExStyle)
	for _, hw := range overlayWindows() {
		if frame {
			st, _, _ := procGetWindowLongPtr.Call(hw, uintptr(gs))
			procSetWindowLongPtr.Call(hw, uintptr(gs), st&^(wsCaption|wsThickFrame|wsSysMenu))
		}
		ex, _, _ := procGetWindowLongPtr.Call(hw, uintptr(ge))
		ex = (ex | wsExLayered | wsExToolWindow | wsExTop | wsExNoActivate) &^ wsExAppWindow
		if c.Through {
			ex |= wsExTransparent
		} else {
			ex &^= wsExTransparent
		}
		procSetWindowLongPtr.Call(hw, uintptr(ge), ex)
		a := c.Alpha
		if a < 80 || a > 255 {
			a = 225
		}
		procSetLayeredAttr.Call(hw, 0, uintptr(a), 2) // LWA_ALPHA
		procSetWindowPos.Call(hw, ^uintptr(0), uintptr(x), uintptr(y), uintptr(w), uintptr(h), swpNoActivate|swpFrameChanged|swpSh)
	}
}

func overlayCloseWindow() {
	for _, hw := range overlayWindows() {
		procPostMessage.Call(hw, 0x10, 0, 0) // WM_CLOSE
	}
}

// overlayKeep: while it's on, keep it on top (the game or the client may come above it) and hidden outside games
func overlayKeep() {
	for {
		time.Sleep(2 * time.Second)
		c := overlayLoad()
		if !c.On {
			continue
		}
		ws := overlayWindows()
		if len(ws) == 0 {
			continue
		}
		inGame := liveNow.Load()
		for _, hw := range ws {
			if inGame || c.Always {
				procShowWindow.Call(hw, 4) // SW_SHOWNOACTIVATE
				procSetWindowPos.Call(hw, ^uintptr(0), 0, 0, 0, 0, 0x1|0x2|0x10)
			} else {
				procShowWindow.Call(hw, 0) // SW_HIDE
			}
		}
	}
}

func overlayWindowsAny() []uintptr { return overlayWindows() }
