//go:build windows

package main

import (
	"errors"
	"fmt"
	"os"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"
)

// The game's sound for the clips: Windows (10 version 2004 and later) can capture what one program plays, so only
// League's sound goes in: not Discord, not your duo, not the coach's voice. It is read with WASAPI (process
// loopback), paced to real time (silence when the game is quiet) and handed to ffmpeg through a named pipe as
// 48 kHz 16-bit stereo. If anything here fails, the clips are recorded without sound, as before.

var (
	ole32            = syscall.NewLazyDLL("ole32.dll")
	pCoInitializeEx  = ole32.NewProc("CoInitializeEx")
	pCoUninitialize  = ole32.NewProc("CoUninitialize")
	mmdevapi         = syscall.NewLazyDLL("mmdevapi.dll")
	pActivateAsync   = mmdevapi.NewProc("ActivateAudioInterfaceAsync")
	k32a             = syscall.NewLazyDLL("kernel32.dll")
	pCreateNamedPipe = k32a.NewProc("CreateNamedPipeW")
	pConnectPipe     = k32a.NewProc("ConnectNamedPipe")
	pDisconnectPipe  = k32a.NewProc("DisconnectNamedPipe")
	pToolhelp        = k32a.NewProc("CreateToolhelp32Snapshot")
	pProc32First     = k32a.NewProc("Process32FirstW")
	pProc32Next      = k32a.NewProc("Process32NextW")
	pCreateEvent     = k32a.NewProc("CreateEventW")
)

type guid struct {
	D1     uint32
	D2, D3 uint16
	D4     [8]byte
}

var (
	iidIUnknown         = guid{0x00000000, 0x0000, 0x0000, [8]byte{0xC0, 0, 0, 0, 0, 0, 0, 0x46}}
	iidAgile            = guid{0x94ea2b94, 0xe9cc, 0x49e0, [8]byte{0xc0, 0xff, 0xee, 0x64, 0xca, 0x8f, 0x5b, 0x90}}
	iidCompletion       = guid{0x41D949AB, 0x9862, 0x444A, [8]byte{0x80, 0xF6, 0xC2, 0x61, 0x33, 0x4D, 0xA5, 0xEB}}
	iidAudioClient      = guid{0x1CB9AD4C, 0xDBFA, 0x4c32, [8]byte{0xB1, 0x78, 0xC2, 0xF5, 0x68, 0xA7, 0x03, 0xB2}}
	iidAudioCaptureClnt = guid{0xC8ADBD64, 0xE71E, 0x48a0, [8]byte{0xA4, 0xDE, 0x18, 0x5C, 0x39, 0x5C, 0xD3, 0x17}}
)

const (
	audRate      = 48000
	audChannels  = 2
	audBlock     = 4 // bytes per frame (16-bit stereo)
	audPipe      = `\\.\pipe\riftmate-so`
	audBytesPerS = audRate * audBlock
)

// a COM object Windows calls when the capture client is ready (IActivateAudioInterfaceCompletionHandler)
type comHandler struct {
	vtbl *[4]uintptr
	done chan struct{}
}

var (
	handlerMu   sync.Mutex
	handlerObjs = map[uintptr]*comHandler{}
	handlerVtbl = [4]uintptr{
		syscall.NewCallback(func(this, riid, ppv uintptr) uintptr { // QueryInterface
			g := (*guid)(unsafe.Pointer(riid))
			if *g == iidIUnknown || *g == iidAgile || *g == iidCompletion {
				*(*uintptr)(unsafe.Pointer(ppv)) = this
				return 0
			}
			*(*uintptr)(unsafe.Pointer(ppv)) = 0
			return 0x80004002 // E_NOINTERFACE
		}),
		syscall.NewCallback(func(this uintptr) uintptr { return 1 }), // AddRef (kept alive by Go)
		syscall.NewCallback(func(this uintptr) uintptr { return 1 }), // Release
		syscall.NewCallback(func(this, op uintptr) uintptr { // ActivateCompleted
			handlerMu.Lock()
			h := handlerObjs[this]
			handlerMu.Unlock()
			if h != nil {
				select {
				case <-h.done:
				default:
					close(h.done)
				}
			}
			return 0
		}),
	}
)

func comCall(obj uintptr, idx int, args ...uintptr) uintptr {
	vtbl := *(*uintptr)(unsafe.Pointer(obj))
	fn := *(*uintptr)(unsafe.Pointer(vtbl + uintptr(idx)*unsafe.Sizeof(uintptr(0))))
	r, _, _ := syscall.SyscallN(fn, append([]uintptr{obj}, args...)...)
	return r
}

func hr(r uintptr) error {
	if int32(r) < 0 {
		return fmt.Errorf("0x%08X", uint32(r))
	}
	return nil
}

// leaguePID: the game's process (League of Legends.exe)
func leaguePID() uint32 {
	snap, _, _ := pToolhelp.Call(0x2, 0) // TH32CS_SNAPPROCESS
	if snap == uintptr(syscall.InvalidHandle) {
		return 0
	}
	defer syscall.CloseHandle(syscall.Handle(snap))
	type pe32 struct {
		Size, Usage, PID          uint32
		HeapID                    uintptr
		ModuleID, Threads, Parent uint32
		PriBase                   int32
		Flags                     uint32
		Exe                       [260]uint16
	}
	var e pe32
	e.Size = uint32(unsafe.Sizeof(e))
	for ok, _, _ := pProc32First.Call(snap, uintptr(unsafe.Pointer(&e))); ok != 0; ok, _, _ = pProc32Next.Call(snap, uintptr(unsafe.Pointer(&e))) {
		if strings.EqualFold(syscall.UTF16ToString(e.Exe[:]), "League of Legends.exe") {
			return e.PID
		}
	}
	return 0
}

type gameAudio struct {
	pipe   syscall.Handle
	stop   chan struct{}
	once   sync.Once
	ready  chan error
	client uintptr
}

// audioPrepare: opens the pipe ffmpeg will read and starts capturing League's sound. Returns the ffmpeg input
// arguments, or nil if the sound can't be captured (then the clips go without it).
func audioPrepare() (*gameAudio, []string) {
	pid := leaguePID()
	if pid == 0 {
		logf("Jugades: no trobo el procés del joc; clips sense so")
		return nil, nil
	}
	name, _ := syscall.UTF16PtrFromString(audPipe)
	h, _, err := pCreateNamedPipe.Call(uintptr(unsafe.Pointer(name)), 2 /*PIPE_ACCESS_OUTBOUND*/, 0, 1, 1<<20, 1<<20, 0, 0)
	if syscall.Handle(h) == syscall.InvalidHandle {
		logf("Jugades: no puc obrir el canal del so (%v); clips sense so", err)
		return nil, nil
	}
	a := &gameAudio{pipe: syscall.Handle(h), stop: make(chan struct{}), ready: make(chan error, 1)}
	go a.run(pid)
	if err := <-a.ready; err != nil {
		syscall.CloseHandle(a.pipe)
		logf("Jugades: no puc capturar el so del joc (%v); clips sense so", err)
		return nil, nil
	}
	return a, []string{"-thread_queue_size", "1024", "-f", "s16le", "-ar", fmt.Sprint(audRate), "-ac", fmt.Sprint(audChannels), "-i", audPipe}
}

func (a *gameAudio) Stop() {
	a.once.Do(func() {
		close(a.stop)
		// if ffmpeg never opened the pipe, the capture is still waiting for it: open it ourselves so it can finish
		go func() {
			name, _ := syscall.UTF16PtrFromString(audPipe)
			if h, err := syscall.CreateFile(name, syscall.GENERIC_READ, 0, nil, syscall.OPEN_EXISTING, 0, 0); err == nil {
				time.Sleep(200 * time.Millisecond)
				syscall.CloseHandle(h)
			}
		}()
	})
}

// run: on its own thread (COM): activate the capture, then feed the pipe at real-time pace
func (a *gameAudio) run(pid uint32) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	pCoInitializeEx.Call(0, 0) // COINIT_MULTITHREADED
	defer pCoUninitialize.Call()
	client, capture, err := audioActivate(pid)
	if err != nil {
		a.ready <- err
		return
	}
	defer comCall(client, 2)
	defer comCall(capture, 2)
	a.client = client
	if err := hr(comCall(client, 10)); err != nil { // Start
		a.ready <- fmt.Errorf("Start %v", err)
		return
	}
	defer comCall(client, 11) // Stop
	a.ready <- nil
	// ffmpeg opens the pipe when it starts reading its inputs
	pConnectPipe.Call(uintptr(a.pipe), 0)
	defer func() {
		pDisconnectPipe.Call(uintptr(a.pipe))
		syscall.CloseHandle(a.pipe)
	}()
	start := time.Now()
	var written int64
	pending := make([]byte, 0, audBytesPerS)
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-a.stop:
			return
		case <-tick.C:
		}
		// everything the game played since the last time
		for {
			var n uint32
			if hr(comCall(capture, 5, uintptr(unsafe.Pointer(&n)))) != nil || n == 0 { // GetNextPacketSize
				break
			}
			var data uintptr
			var frames, flags uint32
			if hr(comCall(capture, 3, uintptr(unsafe.Pointer(&data)), uintptr(unsafe.Pointer(&frames)), uintptr(unsafe.Pointer(&flags)), 0, 0)) != nil {
				break
			}
			nb := int(frames) * audBlock
			if flags&0x2 != 0 || data == 0 { // AUDCLNT_BUFFERFLAGS_SILENT
				pending = append(pending, make([]byte, nb)...)
			} else {
				pending = append(pending, unsafe.Slice((*byte)(unsafe.Pointer(data)), nb)...)
			}
			comCall(capture, 4, uintptr(frames)) // ReleaseBuffer
		}
		// keep to real time: what's due is written (silence where the game made none), a little behind the clock
		due := int64(time.Since(start).Seconds()*audBytesPerS) - audBytesPerS/10
		due -= due % audBlock
		if due <= written {
			continue
		}
		need := int(due - written)
		out := pending
		if len(out) > need {
			out = out[:need]
		}
		if len(pending) > need+audBytesPerS/2 { // the capture ran ahead of the clock: drop the excess
			pending = pending[len(pending)-need:]
			out = pending
		}
		chunk := make([]byte, need)
		copy(chunk, out)
		pending = pending[len(out):]
		var w uint32
		if err := syscall.WriteFile(a.pipe, chunk, &w, nil); err != nil {
			return // ffmpeg stopped
		}
		written += int64(w)
	}
}

// audioActivate: an audio client that captures only that process (and its children)
func audioActivate(pid uint32) (client, capture uintptr, err error) {
	if pActivateAsync.Find() != nil {
		return 0, 0, errors.New("aquest Windows no ho permet (cal Windows 10 2004 o més nou)")
	}
	type actParams struct {
		Type uint32 // AUDIOCLIENT_ACTIVATION_TYPE_PROCESS_LOOPBACK = 1
		PID  uint32
		Mode uint32 // PROCESS_LOOPBACK_MODE_INCLUDE_TARGET_PROCESS_TREE = 0
	}
	params := &actParams{Type: 1, PID: pid, Mode: 0}
	type propVariant struct {
		VT       uint16
		r1, r2   uint16
		r3       uint16
		CB       uint32
		_        uint32
		BlobData uintptr
	}
	pv := &propVariant{VT: 65 /*VT_BLOB*/, CB: uint32(unsafe.Sizeof(*params)), BlobData: uintptr(unsafe.Pointer(params))}
	h := &comHandler{vtbl: &handlerVtbl, done: make(chan struct{})}
	hp := uintptr(unsafe.Pointer(h))
	handlerMu.Lock()
	handlerObjs[hp] = h
	handlerMu.Unlock()
	defer func() { handlerMu.Lock(); delete(handlerObjs, hp); handlerMu.Unlock() }()
	path, _ := syscall.UTF16PtrFromString("VAD\\Process_Loopback")
	var op uintptr
	r, _, _ := pActivateAsync.Call(uintptr(unsafe.Pointer(path)), uintptr(unsafe.Pointer(&iidAudioClient)), uintptr(unsafe.Pointer(pv)), hp, uintptr(unsafe.Pointer(&op)))
	runtime.KeepAlive(params)
	if err := hr(r); err != nil {
		return 0, 0, fmt.Errorf("ActivateAudioInterfaceAsync %v", err)
	}
	defer comCall(op, 2)
	select {
	case <-h.done:
	case <-time.After(5 * time.Second):
		return 0, 0, errors.New("Windows no respon")
	}
	var actHr, unk uintptr
	if err := hr(comCall(op, 3, uintptr(unsafe.Pointer(&actHr)), uintptr(unsafe.Pointer(&unk)))); err != nil { // GetActivateResult
		return 0, 0, fmt.Errorf("GetActivateResult %v", err)
	}
	if err := hr(actHr); err != nil || unk == 0 {
		return 0, 0, fmt.Errorf("activació %v", err)
	}
	client = unk
	type waveFormat struct {
		Tag, Channels       uint16
		Rate, ByteRate      uint32
		Align, Bits, CbSize uint16
	}
	wf := &waveFormat{Tag: 1, Channels: audChannels, Rate: audRate, ByteRate: audBytesPerS, Align: audBlock, Bits: 16}
	// as Microsoft's own example does (loopback + event + automatic conversion to the format asked)
	const loopback, eventcb, autoconv, srcq = 0x00020000, 0x00040000, 0x80000000, 0x08000000
	if err := hr(comCall(client, 3, 0 /*SHARED*/, loopback|eventcb|autoconv|srcq, 2000000, 0, uintptr(unsafe.Pointer(wf)), 0)); err != nil { // Initialize
		comCall(client, 2)
		return 0, 0, fmt.Errorf("Initialize %v", err)
	}
	ev, _, _ := pCreateEvent.Call(0, 0, 0, 0)           // the capture also gets polled every 10 ms; the event just has to exist
	if err := hr(comCall(client, 13, ev)); err != nil { // SetEventHandle
		comCall(client, 2)
		return 0, 0, fmt.Errorf("SetEventHandle %v", err)
	}
	if err := hr(comCall(client, 14, uintptr(unsafe.Pointer(&iidAudioCaptureClnt)), uintptr(unsafe.Pointer(&capture)))); err != nil || capture == 0 { // GetService
		comCall(client, 2)
		return 0, 0, fmt.Errorf("GetService %v", err)
	}
	return client, capture, nil
}

var _ = os.Getpid
