//go:build windows

package main

import (
	"errors"
	"syscall"
	"time"
	"unicode/utf16"
	"unsafe"
)

var (
	user32c         = syscall.NewLazyDLL("user32.dll")
	kernel32c       = syscall.NewLazyDLL("kernel32.dll")
	pOpenClipboard  = user32c.NewProc("OpenClipboard")
	pCloseClipboard = user32c.NewProc("CloseClipboard")
	pEmptyClipboard = user32c.NewProc("EmptyClipboard")
	pSetClipboard   = user32c.NewProc("SetClipboardData")
	pGlobalAlloc    = kernel32c.NewProc("GlobalAlloc")
	pGlobalLock     = kernel32c.NewProc("GlobalLock")
	pGlobalUnlock   = kernel32c.NewProc("GlobalUnlock")
	pRtlMoveMemory  = kernel32c.NewProc("RtlMoveMemory")
)

// setClipboard puts text on the Windows clipboard (to paste it in the game chat with Ctrl+V)
func setClipboard(text string) error {
	u := utf16.Encode([]rune(text + "\x00"))
	var ok uintptr
	for i := 0; i < 10; i++ { // another program may have it open for a moment
		if ok, _, _ = pOpenClipboard.Call(0); ok != 0 {
			break
		}
		time.Sleep(30 * time.Millisecond)
	}
	if ok == 0 {
		return errors.New("clipboard_busy")
	}
	defer pCloseClipboard.Call()
	pEmptyClipboard.Call()
	h, _, _ := pGlobalAlloc.Call(0x0002, uintptr(len(u)*2)) // GMEM_MOVEABLE
	if h == 0 {
		return errors.New("alloc")
	}
	p, _, _ := pGlobalLock.Call(h)
	if p == 0 {
		return errors.New("lock")
	}
	pRtlMoveMemory.Call(p, uintptr(unsafe.Pointer(&u[0])), uintptr(len(u)*2))
	pGlobalUnlock.Call(h)
	if r, _, _ := pSetClipboard.Call(13, h); r == 0 { // CF_UNICODETEXT
		return errors.New("set")
	}
	return nil
}
