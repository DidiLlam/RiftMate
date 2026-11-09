//go:build windows

package main

import (
	"encoding/json"
	"fmt"
	"math"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"
)

// The overlay: RiftMate's own little window above the game, drawn here with plain Windows drawing (no browser
// window behind it, so it costs the game next to nothing). Nothing is drawn into the game or read from it.
// The background is fully see-through (colour key); only the plates show, with the chosen opacity.
// Dragging a plate moves it; clicking a spell starts or cancels its timer; "lock" lets every click go to the game.
// It shows above the game in borderless mode and, on most Windows 10/11 setups, in fullscreen too.

var (
	gdi32               = syscall.NewLazyDLL("gdi32.dll")
	kernel32ov          = syscall.NewLazyDLL("kernel32.dll")
	pGetModuleHandle    = kernel32ov.NewProc("GetModuleHandleW")
	pRegisterClassEx    = user32.NewProc("RegisterClassExW")
	pCreateWindowEx     = user32.NewProc("CreateWindowExW")
	pDefWindowProc      = user32.NewProc("DefWindowProcW")
	pDestroyWindow      = user32.NewProc("DestroyWindow")
	pGetMessage         = user32.NewProc("GetMessageW")
	pTranslateMessage   = user32.NewProc("TranslateMessage")
	pDispatchMessage    = user32.NewProc("DispatchMessageW")
	pPostQuitMessage    = user32.NewProc("PostQuitMessage")
	pPostMessage        = user32.NewProc("PostMessageW")
	pSetTimer           = user32.NewProc("SetTimer")
	pInvalidateRect     = user32.NewProc("InvalidateRect")
	pBeginPaint         = user32.NewProc("BeginPaint")
	pEndPaint           = user32.NewProc("EndPaint")
	pFillRect           = user32.NewProc("FillRect")
	pDrawText           = user32.NewProc("DrawTextW")
	pSetCapture         = user32.NewProc("SetCapture")
	pReleaseCapture     = user32.NewProc("ReleaseCapture")
	pGetCursorPos       = user32.NewProc("GetCursorPos")
	pGetWindowRect      = user32.NewProc("GetWindowRect")
	pLoadCursor         = user32.NewProc("LoadCursorW")
	pSetCursor          = user32.NewProc("SetCursor")
	pScreenToClient     = user32.NewProc("ScreenToClient")
	pGetWindowLongPtr   = user32.NewProc("GetWindowLongPtrW")
	pSetWindowLongPtr   = user32.NewProc("SetWindowLongPtrW")
	pSetLayeredAttr     = user32.NewProc("SetLayeredWindowAttributes")
	pGetSystemMetrics   = user32.NewProc("GetSystemMetrics")
	pGetDpiForWindow    = user32.NewProc("GetDpiForWindow")
	pSetThreadDpiAware  = user32.NewProc("SetThreadDpiAwarenessContext")
	pCreateCompatibleDC = gdi32.NewProc("CreateCompatibleDC")
	pCreateCompatBitmap = gdi32.NewProc("CreateCompatibleBitmap")
	pSelectObject       = gdi32.NewProc("SelectObject")
	pDeleteObject       = gdi32.NewProc("DeleteObject")
	pDeleteDC           = gdi32.NewProc("DeleteDC")
	pBitBlt             = gdi32.NewProc("BitBlt")
	pCreateSolidBrush   = gdi32.NewProc("CreateSolidBrush")
	pCreateFont         = gdi32.NewProc("CreateFontW")
	pSetTextColor       = gdi32.NewProc("SetTextColor")
	pSetBkMode          = gdi32.NewProc("SetBkMode")
	pRoundRect          = gdi32.NewProc("RoundRect")
	pGetStockObject     = gdi32.NewProc("GetStockObject")
)

const (
	overlayTitle = "RM·Overlay"
	wmApply      = 0x8001 // WM_APP+1: settings changed
	ovKey        = 0x010101
)

type ovRect struct{ L, T, R, B int32 }
type ovPoint struct{ X, Y int32 }
type ovMsg struct {
	Hwnd    uintptr
	Message uint32
	WParam  uintptr
	LParam  uintptr
	Time    uint32
	Pt      ovPoint
	Private uint32
}
type ovPaint struct {
	Hdc       uintptr
	Erase     int32
	Rc        ovRect
	Restore   int32
	IncUpdate int32
	Reserved  [32]byte
}
type ovClass struct {
	Size       uint32
	Style      uint32
	WndProc    uintptr
	ClsExtra   int32
	WndExtra   int32
	Instance   uintptr
	Icon       uintptr
	Cursor     uintptr
	Background uintptr
	MenuName   *uint16
	ClassName  *uint16
	IconSm     uintptr
}

// what the page sends (the same state the phone gets)
type ovTimer struct {
	Left float64 `json:"left"`
	Has  bool    `json:"has"`
	Icon string  `json:"icon"`
}
type ovState struct {
	Time *float64 `json:"time"`
	Rows []struct {
		Row    int    `json:"row"`
		Name   string `json:"name"`
		Icon   string `json:"icon"`
		Spells []struct {
			Spell string  `json:"spell"`
			Kind  string  `json:"kind"`
			Left  float64 `json:"left"`
			Icon  string  `json:"icon"`
		} `json:"spells"`
		Ult *ovTimer `json:"ult"`
		Zh  *ovTimer `json:"zh"`
	} `json:"rows"`
	Obj []struct {
		Name string  `json:"name"`
		Left float64 `json:"left"`
	} `json:"obj"`
}

type ovChip struct {
	r         ovRect
	row       int
	kind      string
	label     string
	left      float64
	fresh     bool
	clickable bool
	icon      string
}

type ovFace struct {
	r    ovRect
	name string
	icon string
}

var (
	ovMu      sync.Mutex
	ovHwnd    uintptr
	ovRunning bool
	ovProcCB  = syscall.NewCallback(ovProc)
	ovChips   []ovChip // last layout, for clicks (window thread only)
	ovSig     string   // what was last drawn
	ovSince   = map[string]time.Time{}
	ovWas     = map[string]bool{}
	ovH       int32 // current height
	ovDrag    struct {
		on, moved bool
		sx, sy    int32 // cursor at press
		wx, wy    int32 // window at press
		chip      int
	}
	ovTick int
)

var gwlExStyle = int32(-20)

func rgb(r, g, b int) uintptr { return uintptr(r | g<<8 | b<<16) }

func u16(s string) *uint16 { p, _ := syscall.UTF16PtrFromString(s); return p }

func ovScale(h uintptr) float64 {
	if pGetDpiForWindow.Find() == nil {
		if d, _, _ := pGetDpiForWindow.Call(h); d > 0 {
			return float64(d) / 96
		}
	}
	return 1
}

func overlayOpenWindow() {
	ovMu.Lock()
	if ovRunning {
		h := ovHwnd
		ovMu.Unlock()
		if h != 0 {
			pPostMessage.Call(h, wmApply, 0, 0)
		}
		return
	}
	ovRunning = true
	ovMu.Unlock()
	go ovThread()
}

func ovThread() {
	runtime.LockOSThread()
	defer func() {
		ovMu.Lock()
		ovRunning, ovHwnd = false, 0
		ovMu.Unlock()
		if r := recover(); r != nil {
			logf("Overlay: error %v", r)
		}
	}()
	if pSetThreadDpiAware.Find() == nil {
		pSetThreadDpiAware.Call(^uintptr(3)) // DPI_AWARENESS_CONTEXT_PER_MONITOR_AWARE_V2 (-4): sharp text
	}
	inst, _, _ := pGetModuleHandle.Call(0)
	arrow, _, _ := pLoadCursor.Call(0, 32512)
	cls := u16("RiftMateOverlay")
	wc := ovClass{WndProc: ovProcCB, Instance: inst, Cursor: arrow, ClassName: cls}
	wc.Size = uint32(unsafe.Sizeof(wc))
	pRegisterClassEx.Call(uintptr(unsafe.Pointer(&wc))) // fails harmlessly if already registered
	const exLayered, exTool, exTop, exNoActivate = 0x80000, 0x80, 0x8, 0x08000000
	const wsPopup = 0x80000000
	h, _, err := pCreateWindowEx.Call(exLayered|exTool|exTop|exNoActivate, uintptr(unsafe.Pointer(cls)), uintptr(unsafe.Pointer(u16(overlayTitle))),
		wsPopup, 0, 0, 240, 60, 0, 0, inst, 0)
	if h == 0 {
		logf("Overlay: no s'ha pogut crear la finestra: %v", err)
		return
	}
	ovMu.Lock()
	ovHwnd = h
	ovMu.Unlock()
	ovSig, ovH = "", 0
	ovApplyCfg(h)
	pSetTimer.Call(h, 1, 500, 0)
	logf("Overlay: obert")
	var m ovMsg
	for {
		r, _, _ := pGetMessage.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0)
		if int32(r) <= 0 {
			break
		}
		pTranslateMessage.Call(uintptr(unsafe.Pointer(&m)))
		pDispatchMessage.Call(uintptr(unsafe.Pointer(&m)))
	}
	logf("Overlay: tancat")
}

// ovApplyCfg: opacity, lock and place, from the saved settings
func ovApplyCfg(h uintptr) {
	c := overlayLoad()
	a := c.Alpha
	if a < 40 || a > 255 {
		a = 210
	}
	pSetLayeredAttr.Call(h, ovKey, uintptr(a), 1|2) // LWA_COLORKEY | LWA_ALPHA
	ge := int32(-20)
	ex, _, _ := pGetWindowLongPtr.Call(h, uintptr(ge))
	if c.Lock {
		ex |= 0x20 // WS_EX_TRANSPARENT: every click goes to the game
	} else {
		ex &^= 0x20
	}
	pSetWindowLongPtr.Call(h, uintptr(ge), ex)
	s := ovScale(h)
	w := int32(math.Round(ovW * s))
	x, y := int32(c.X), int32(c.Y)
	if !c.Placed {
		sw, _, _ := pGetSystemMetrics.Call(0)
		sh, _, _ := pGetSystemMetrics.Call(1)
		x, y = int32(sw)-w-int32(16*s), int32(110*s)
		switch c.Pos {
		case "tl":
			x = int32(16 * s)
		case "bl":
			x, y = int32(16*s), int32(sh)-int32(420*s)
		case "br":
			y = int32(sh) - int32(420*s)
		}
	}
	hh := ovH
	if hh == 0 {
		hh = int32(40 * s)
	}
	procSetWindowPos.Call(h, ^uintptr(0), uintptr(x), uintptr(y), uintptr(w), uintptr(hh), 0x10) // HWND_TOPMOST, SWP_NOACTIVATE
	ovSig = ""
	pInvalidateRect.Call(h, 0, 0)
}

func overlayApply(c overlayCfg, frame bool) {
	ovMu.Lock()
	h := ovHwnd
	ovMu.Unlock()
	if h != 0 {
		pPostMessage.Call(h, wmApply, 0, 0)
	}
}

func overlayCloseWindow() {
	ovMu.Lock()
	h := ovHwnd
	ovMu.Unlock()
	if h != 0 {
		pPostMessage.Call(h, 0x10, 0, 0) // WM_CLOSE
	}
}

func overlayKeep() {}

func overlayWindowsAny() []uintptr {
	ovMu.Lock()
	defer ovMu.Unlock()
	if ovRunning {
		return []uintptr{1}
	}
	return nil
}

// the state to show, and whether it is fresh (the page sends it every second while a game runs)
func ovRead() (ovState, float64, bool) {
	var st ovState
	mobMu.Lock()
	b, at := mobState, mobStateAt
	mobMu.Unlock()
	json.Unmarshal(b, &st)
	age := time.Since(at).Seconds()
	return st, age, age < 6 && len(st.Rows) > 0
}

var ovShort = map[string]string{"Flash": "FLASH", "Ignite": "IGNITE", "Teleport": "TP", "Heal": "HEAL", "Exhaust": "EXH", "Barrier": "BARR",
	"Ghost": "GHOST", "Cleanse": "CLNS", "Smite": "SMITE", "R": "ULTI", "Zhonya": "ZHONYA"}

func ovFmt(s float64) string {
	n := int(math.Ceil(s))
	return fmt.Sprintf("%d:%02d", n/60, n%60)
}

const ovW, ovPortrait, ovChipSz, ovGap = 210.0, 36.0, 34.0, 3.0

// ovLayout builds the plates, portraits and chips, and a signature of what would be drawn
func ovLayout(h uintptr) (sig string, plates []ovRect, header string, faces []ovFace, chips []ovChip, height int32) {
	c := overlayLoad()
	st, age, live := ovRead()
	s := ovScale(h)
	sc := func(v float64) int32 { return int32(math.Round(v * s)) }
	w := sc(ovW)
	if !live {
		if !c.Always {
			return "hidden", nil, "", nil, nil, 0
		}
		return "wait", []ovRect{{0, 0, w, sc(24)}}, "RiftMate · esperant la partida", nil, nil, sc(24)
	}
	head := ""
	if st.Time != nil {
		head = ovFmt(*st.Time + age)
	}
	for _, o := range st.Obj {
		if l := o.Left - age; l > -45 && l <= 90 {
			if l > 0 {
				head += "  ·  " + o.Name + " " + ovFmt(l)
			} else {
				head += "  ·  " + o.Name + " ja"
			}
		}
	}
	plates = append(plates, ovRect{0, 0, w, sc(20)})
	y := sc(23)
	now := map[string]bool{}
	cw, gap, pad := sc(ovChipSz), sc(ovGap), sc(3)
	for _, r := range st.Rows {
		pl := ovRect{0, y, w, y + sc(ovPortrait) + 2*pad}
		plates = append(plates, pl)
		faces = append(faces, ovFace{r: ovRect{pad, y + pad, pad + sc(ovPortrait), y + pad + sc(ovPortrait)}, name: r.Name, icon: r.Icon})
		var list []ovChip
		for _, sp := range r.Spells {
			list = append(list, ovChip{row: r.Row, kind: sp.Kind, label: sp.Spell, left: sp.Left - age, icon: sp.Icon})
		}
		if r.Ult != nil {
			list = append(list, ovChip{row: r.Row, kind: "R", label: "R", left: r.Ult.Left - age, icon: r.Ult.Icon})
		}
		if r.Zh != nil && (r.Zh.Has || r.Zh.Left > 0) {
			list = append(list, ovChip{row: r.Row, kind: "Zhonya", label: "Zhonya", left: r.Zh.Left - age, icon: r.Zh.Icon})
		}
		x := pad + sc(ovPortrait) + sc(9)
		top := y + pad + (sc(ovPortrait)-cw)/2
		for _, cp := range list {
			cp.r = ovRect{x, top, x + cw, top + cw}
			cp.clickable = !c.Lock
			k := fmt.Sprint(cp.row, cp.kind)
			run := cp.left > 0
			now[k] = run
			if run && !ovWas[k] {
				ovSince[k] = time.Now()
			}
			cp.fresh = run && time.Since(ovSince[k]) < 2500*time.Millisecond
			chips = append(chips, cp)
			x += cw + gap
		}
		y = pl.B + sc(3)
	}
	ovWas = now
	var b strings.Builder
	b.WriteString(head)
	for _, f := range faces {
		fmt.Fprint(&b, "|", f.name, f.r.T, ovImg(f.icon) != nil)
	}
	for _, cp := range chips {
		v := "ok"
		if cp.left > 0 {
			v = ovFmt(cp.left)
		}
		fmt.Fprint(&b, "|", cp.kind, v, cp.fresh, cp.r.L, cp.r.T, ovImg(cp.icon) != nil)
	}
	return b.String(), plates, head, faces, chips, y
}

func ovPaintAll(h uintptr) {
	_, plates, header, faces, chips, _ := ovLayout(h)
	ovChips = chips
	var ps ovPaint
	hdc, _, _ := pBeginPaint.Call(h, uintptr(unsafe.Pointer(&ps)))
	defer pEndPaint.Call(h, uintptr(unsafe.Pointer(&ps)))
	var wr ovRect
	pGetWindowRect.Call(h, uintptr(unsafe.Pointer(&wr)))
	W, H := wr.R-wr.L, wr.B-wr.T
	mdc, _, _ := pCreateCompatibleDC.Call(hdc)
	bmp, _, _ := pCreateCompatBitmap.Call(hdc, uintptr(W), uintptr(H))
	oldBmp, _, _ := pSelectObject.Call(mdc, bmp)
	defer func() {
		pBitBlt.Call(hdc, 0, 0, uintptr(W), uintptr(H), mdc, 0, 0, 0x00CC0020) // SRCCOPY
		pSelectObject.Call(mdc, oldBmp)
		pDeleteObject.Call(bmp)
		pDeleteDC.Call(mdc)
	}()
	key, _, _ := pCreateSolidBrush.Call(ovKey)
	full := ovRect{0, 0, W, H}
	pFillRect.Call(mdc, uintptr(unsafe.Pointer(&full)), key)
	pDeleteObject.Call(key)
	if len(plates) == 0 {
		return
	}
	s := ovScale(h)
	sc := func(v float64) int32 { return int32(math.Round(v * s)) }
	font := func(px float64, weight int) uintptr {
		f, _, _ := pCreateFont.Call(uintptr(int32(-sc(px))), 0, 0, 0, uintptr(weight), 0, 0, 0, 1, 0, 0, 5, 0, uintptr(unsafe.Pointer(u16("Segoe UI"))))
		return f
	}
	fHead, fSmall, fBig := font(11, 600), font(9.5, 700), font(13.5, 800)
	defer func() { pDeleteObject.Call(fHead); pDeleteObject.Call(fSmall); pDeleteObject.Call(fBig) }()
	nullPen, _, _ := pGetStockObject.Call(8)
	oldPen, _, _ := pSelectObject.Call(mdc, nullPen)
	defer pSelectObject.Call(mdc, oldPen)
	pSetBkMode.Call(mdc, 1) // TRANSPARENT
	box := func(r ovRect, col uintptr, rad int32) {
		br, _, _ := pCreateSolidBrush.Call(col)
		old, _, _ := pSelectObject.Call(mdc, br)
		pRoundRect.Call(mdc, uintptr(r.L), uintptr(r.T), uintptr(r.R+1), uintptr(r.B+1), uintptr(rad), uintptr(rad))
		pSelectObject.Call(mdc, old)
		pDeleteObject.Call(br)
	}
	text := func(t string, r ovRect, f, col uintptr, flags uintptr) {
		pSelectObject.Call(mdc, f)
		pSetTextColor.Call(mdc, col)
		p, _ := syscall.UTF16FromString(t)
		pDrawText.Call(mdc, uintptr(unsafe.Pointer(&p[0])), uintptr(len(p)-1), uintptr(unsafe.Pointer(&r)), flags)
	}
	// text with a dark outline, to read on top of an icon
	outlined := func(t string, r ovRect, f, col uintptr, flags uintptr) {
		for _, d := range [][2]int32{{-1, 0}, {1, 0}, {0, -1}, {0, 1}, {1, 1}} {
			text(t, ovRect{r.L + d[0], r.T + d[1], r.R + d[0], r.B + d[1]}, f, rgb(8, 8, 8), flags)
		}
		text(t, r, f, col, flags)
	}
	const dtCenter, dtVCenter, dtSingle, dtEllipsis, dtNoPrefix = 0x1, 0x4, 0x20, 0x8000, 0x800
	plate := rgb(15, 24, 21)
	for i, p := range plates {
		box(p, plate, sc(10))
		if i == 0 {
			text(header, ovRect{p.L + sc(8), p.T, p.R - sc(6), p.B}, fHead, rgb(205, 220, 214), dtVCenter|dtSingle|dtEllipsis|dtNoPrefix)
		}
	}
	for _, f := range faces {
		n := f.r.R - f.r.L
		if b := ovBitmap(f.icon, n, false); b != 0 {
			ovBlit(mdc, b, f.r.L, f.r.T, n)
		} else { // no picture (yet): the first letters of the name
			box(f.r, rgb(30, 44, 39), sc(6))
			nm := []rune(f.name)
			if len(nm) > 3 {
				nm = nm[:3]
			}
			text(string(nm), f.r, fSmall, rgb(238, 244, 241), dtCenter|dtVCenter|dtSingle|dtNoPrefix)
		}
	}
	for _, cp := range chips {
		n := cp.r.R - cp.r.L
		run := cp.left > 0
		if cp.fresh { // just marked: an orange frame for a moment
			box(ovRect{cp.r.L - sc(2), cp.r.T - sc(2), cp.r.R + sc(2), cp.r.B + sc(2)}, rgb(255, 154, 74), sc(5))
		}
		if b := ovBitmap(cp.icon, n, run); b != 0 {
			ovBlit(mdc, b, cp.r.L, cp.r.T, n)
		} else {
			lab := ovShort[cp.label]
			if lab == "" {
				lab = strings.ToUpper(cp.label)
			}
			col := rgb(26, 38, 34)
			if run {
				col = rgb(48, 32, 20)
			}
			box(cp.r, col, sc(5))
			if !run {
				text(lab, cp.r, fSmall, rgb(51, 201, 159), dtCenter|dtVCenter|dtSingle|dtNoPrefix)
			}
		}
		if run {
			t := ovFmt(cp.left)
			if cp.left < 60 {
				t = fmt.Sprint(int(math.Ceil(cp.left)))
			}
			outlined(t, cp.r, fBig, rgb(255, 176, 105), dtCenter|dtVCenter|dtSingle|dtNoPrefix)
		}
	}
}

func ovChipAt(x, y int32) int {
	for i, c := range ovChips {
		if c.clickable && x >= c.r.L && x < c.r.R && y >= c.r.T && y < c.r.B {
			return i
		}
	}
	return -1
}

func ovProc(h uintptr, msg uint32, wp, lp uintptr) uintptr {
	switch msg {
	case 0x0113: // WM_TIMER: redraw only when what it shows changed; resize to the content; keep it on top
		sig, _, _, _, _, height := ovLayout(h)
		vis, _, _ := procIsWindowVisible.Call(h)
		if sig == "hidden" {
			if vis != 0 {
				procShowWindow.Call(h, 0)
			}
			ovSig = sig
			return 0
		}
		if height != ovH && height > 0 {
			ovH = height
			var wr ovRect
			pGetWindowRect.Call(h, uintptr(unsafe.Pointer(&wr)))
			procSetWindowPos.Call(h, 0, 0, 0, uintptr(wr.R-wr.L), uintptr(height), 0x2|0x4|0x10) // NOMOVE NOZORDER NOACTIVATE
			ovSig = ""
		}
		if vis == 0 {
			procShowWindow.Call(h, 4) // SW_SHOWNOACTIVATE
			procSetWindowPos.Call(h, ^uintptr(0), 0, 0, 0, 0, 0x1|0x2|0x10)
		}
		if sig != ovSig {
			ovSig = sig
			pInvalidateRect.Call(h, 0, 0)
		}
		if ovTick++; ovTick%6 == 0 { // every 3 s: back on top if something took its place (only then: it makes Windows recompose)
			if ex, _, _ := pGetWindowLongPtr.Call(h, uintptr(gwlExStyle)); ex&0x8 == 0 {
				procSetWindowPos.Call(h, ^uintptr(0), 0, 0, 0, 0, 0x1|0x2|0x10)
			}
		}
		return 0
	case 0x000F: // WM_PAINT
		ovPaintAll(h)
		return 0
	case 0x0014: // WM_ERASEBKGND: all painted in WM_PAINT
		return 1
	case 0x0021: // WM_MOUSEACTIVATE: never take the focus from the game
		return 3 // MA_NOACTIVATE
	case 0x0020: // WM_SETCURSOR: a hand over the spells
		var p ovPoint
		pGetCursorPos.Call(uintptr(unsafe.Pointer(&p)))
		pScreenToClient.Call(h, uintptr(unsafe.Pointer(&p)))
		id := uintptr(32646) // IDC_SIZEALL: drag
		if ovChipAt(p.X, p.Y) >= 0 {
			id = 32649 // IDC_HAND
		}
		cur, _, _ := pLoadCursor.Call(0, id)
		pSetCursor.Call(cur)
		return 1
	case 0x0201: // WM_LBUTTONDOWN
		var p, wr = ovPoint{}, ovRect{}
		pGetCursorPos.Call(uintptr(unsafe.Pointer(&p)))
		pGetWindowRect.Call(h, uintptr(unsafe.Pointer(&wr)))
		x, y := int32(int16(lp&0xffff)), int32(int16(lp>>16&0xffff))
		ovDrag.on, ovDrag.moved, ovDrag.sx, ovDrag.sy, ovDrag.wx, ovDrag.wy, ovDrag.chip = true, false, p.X, p.Y, wr.L, wr.T, ovChipAt(x, y)
		pSetCapture.Call(h)
		return 0
	case 0x0215: // WM_CAPTURECHANGED: the drag ends however it ends
		ovDrag.on = false
		return 0
	case 0x0200: // WM_MOUSEMOVE
		if ovDrag.on && wp&1 == 0 { // the button was let go somewhere we didn't see
			ovDrag.on = false
			pReleaseCapture.Call()
			return 0
		}
		if ovDrag.on {
			var p ovPoint
			pGetCursorPos.Call(uintptr(unsafe.Pointer(&p)))
			dx, dy := p.X-ovDrag.sx, p.Y-ovDrag.sy
			if !ovDrag.moved && dx*dx+dy*dy < 25 {
				return 0
			}
			ovDrag.moved = true
			procSetWindowPos.Call(h, 0, uintptr(ovDrag.wx+dx), uintptr(ovDrag.wy+dy), 0, 0, 0x1|0x4|0x10)
		}
		return 0
	case 0x0202: // WM_LBUTTONUP: a click on a spell starts/cancels it; a drag saves the new place
		if !ovDrag.on {
			return 0
		}
		ovDrag.on = false
		moved, chip := ovDrag.moved, ovDrag.chip
		pReleaseCapture.Call()
		ovDrag.moved, ovDrag.chip = moved, chip
		if ovDrag.moved {
			var wr ovRect
			pGetWindowRect.Call(h, uintptr(unsafe.Pointer(&wr)))
			overlaySavePos(int(wr.L), int(wr.T))
		} else if ovDrag.chip >= 0 && ovDrag.chip < len(ovChips) {
			c := ovChips[ovDrag.chip]
			hotkeyPushFrom(c.row, c.kind, "overlay")
		}
		return 0
	case wmApply:
		ovApplyCfg(h)
		return 0
	case 0x0010: // WM_CLOSE
		pDestroyWindow.Call(h)
		return 0
	case 0x0002: // WM_DESTROY
		pPostQuitMessage.Call(0)
		return 0
	}
	r, _, _ := pDefWindowProc.Call(h, uintptr(msg), wp, lp)
	return r
}
