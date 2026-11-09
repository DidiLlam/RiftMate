//go:build windows

package main

import (
	"strings"
	"sync"
	"syscall"
	"unsafe"
)

// bringToFront puts the RiftMate window above every other window without taking the keyboard focus
// (so typing in the League client's chat isn't interrupted): restored if minimised, made topmost for a
// moment and then normal again.
var (
	procEnumWindows     = user32.NewProc("EnumWindows")
	procGetWindowText   = user32.NewProc("GetWindowTextW")
	procGetClassName    = user32.NewProc("GetClassNameW")
	procIsWindowVisible = user32.NewProc("IsWindowVisible")
	procIsIconic        = user32.NewProc("IsIconic")
	procShowWindow      = user32.NewProc("ShowWindow")
	procSetWindowPos    = user32.NewProc("SetWindowPos")
)

var (
	frontFound []uintptr
	frontMu    sync.Mutex
	frontEnum  = syscall.NewCallback(func(h uintptr, _ uintptr) uintptr {
		if v, _, _ := procIsWindowVisible.Call(h); v == 0 {
			if ic, _, _ := procIsIconic.Call(h); ic == 0 {
				return 1
			}
		}
		buf := make([]uint16, 256)
		procGetWindowText.Call(h, uintptr(unsafe.Pointer(&buf[0])), 256)
		title := syscall.UTF16ToString(buf)
		cls := make([]uint16, 128)
		procGetClassName.Call(h, uintptr(unsafe.Pointer(&cls[0])), 128)
		if strings.HasPrefix(title, appName) && strings.HasPrefix(syscall.UTF16ToString(cls), "Chrome_WidgetWin") {
			frontFound = append(frontFound, h)
		}
		return 1
	})
)

func bringToFront() bool {
	frontMu.Lock()
	frontFound = nil
	procEnumWindows.Call(frontEnum, 0)
	found := append([]uintptr{}, frontFound...)
	frontMu.Unlock()
	const (
		swShowNoActivate = 4
		swpNoSize        = 0x1
		swpNoMove        = 0x2
		swpNoActivate    = 0x10
		swpShowWindow    = 0x40
	)
	topmost, notopmost := ^uintptr(0), ^uintptr(1) // HWND_TOPMOST (-1), HWND_NOTOPMOST (-2)
	for _, h := range found {
		if ic, _, _ := procIsIconic.Call(h); ic != 0 {
			procShowWindow.Call(h, swShowNoActivate)
		}
		procSetWindowPos.Call(h, topmost, 0, 0, 0, 0, swpNoMove|swpNoSize|swpNoActivate|swpShowWindow)
		procSetWindowPos.Call(h, notopmost, 0, 0, 0, 0, swpNoMove|swpNoSize|swpNoActivate)
	}
	return len(found) > 0
}
