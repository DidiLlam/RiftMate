//go:build windows

package main

import (
	"archive/zip"
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"
)

const playsSupported = true

// ffmpeg (the open video tool), downloaded the first time: it captures the screen with the graphics card
// (ddagrab) and encodes with the card's own video encoder (NVIDIA, AMD or Intel), into a ring of 4-second pieces
// that always holds the last minute. Cutting a play = joining the pieces of those seconds, without re-encoding.
const ffmpegZip = "https://github.com/BtbN/FFmpeg-Builds/releases/download/latest/ffmpeg-master-latest-win64-lgpl-shared.zip"

var (
	capMu  sync.Mutex
	capCmd *exec.Cmd
	capIn  io.WriteCloser
	capT0  time.Time
	capDL  sync.Mutex
)

func ffDir() string   { return filepath.Join(filepath.Dir(dataDir()), "ffmpeg") }
func ffExe() string   { return filepath.Join(ffDir(), "ffmpeg.exe") }
func capRing() string { return filepath.Join(os.TempDir(), "riftmate-gravacio") }
func hideCmd(c *exec.Cmd) {
	c.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000 | 0x00004000}
}

func ffEnsure() error {
	capDL.Lock()
	defer capDL.Unlock()
	if fileSize(ffExe()) > 0 {
		return nil
	}
	playsSet("downloading", "")
	logf("Jugades: descarregant ffmpeg (uns 80 MB)")
	b, err := wGet(ffmpegZip, 300<<20)
	if err != nil {
		return err
	}
	zr, err := zip.NewReader(bytes.NewReader(b), int64(len(b)))
	if err != nil {
		return err
	}
	os.MkdirAll(ffDir(), 0o755)
	for _, f := range zr.File {
		base := filepath.Base(f.Name)
		if !strings.Contains(f.Name, "/bin/") || f.FileInfo().IsDir() || !(base == "ffmpeg.exe" || strings.HasSuffix(base, ".dll")) {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return err
		}
		data, err := io.ReadAll(io.LimitReader(rc, 200<<20))
		rc.Close()
		if err != nil {
			return err
		}
		tmp := filepath.Join(ffDir(), base+".part")
		if err := os.WriteFile(tmp, data, 0o755); err != nil {
			return err
		}
		os.Rename(tmp, filepath.Join(ffDir(), base))
	}
	if fileSize(ffExe()) <= 0 {
		return fmt.Errorf("al paquet no hi ha ffmpeg.exe")
	}
	logf("Jugades: ffmpeg descarregat")
	return nil
}

// encoder options per card; the first that works on this PC is kept
var capEncoders = []struct {
	name, label string
	args        []string
}{
	{"h264_nvenc", "NVIDIA", []string{"-c:v", "h264_nvenc", "-preset", "p3", "-rc", "vbr", "-b:v", "8M", "-maxrate", "12M"}},
	{"h264_amf", "AMD", []string{"-c:v", "h264_amf", "-quality", "speed", "-rc", "vbr_peak", "-b:v", "8M", "-maxrate", "12M"}},
	{"h264_qsv", "Intel", []string{"-vf", "hwmap=derive_device=qsv,format=qsv", "-c:v", "h264_qsv", "-preset", "veryfast", "-b:v", "8M"}},
}

func capInput() []string {
	return []string{"-hide_banner", "-loglevel", "error", "-f", "lavfi", "-i", "ddagrab=output_idx=0:framerate=30:draw_mouse=0"}
}

// captureProbe: try each encoder for one second
func captureProbe() string {
	if err := ffEnsure(); err != nil {
		playsSet("error", err.Error())
		logf("Jugades: no he pogut descarregar ffmpeg: %v", err)
		return ""
	}
	playsSet("probing", "")
	for _, e := range capEncoders {
		args := append(capInput(), append(append([]string{"-t", "1"}, e.args...), "-f", "null", "-")...)
		cmd := exec.Command(ffExe(), args...)
		cmd.Dir = ffDir()
		hideCmd(cmd)
		var errb bytes.Buffer
		cmd.Stderr = &errb
		if err := cmd.Run(); err == nil {
			c := playsLoadCfg()
			c.Encoder = e.name
			playsSaveCfg(c)
			playsSet("ready", "")
			logf("Jugades: gravaré amb la targeta (%s, %s)", e.label, e.name)
			return e.name
		} else {
			logf("Jugades: %s no va (%v: %.160s)", e.name, err, strings.TrimSpace(errb.String()))
		}
	}
	playsSet("error", "cap targeta gràfica no pot gravar (NVIDIA, AMD o Intel)")
	return ""
}

func captureStart() {
	c := playsLoadCfg()
	enc := c.Encoder
	if enc == "" {
		if enc = captureProbe(); enc == "" {
			return
		}
	}
	var eargs []string
	for _, e := range capEncoders {
		if e.name == enc {
			eargs = e.args
		}
	}
	capMu.Lock()
	defer capMu.Unlock()
	if capCmd != nil {
		return
	}
	os.RemoveAll(capRing())
	os.MkdirAll(capRing(), 0o755)
	args := append(capInput(), eargs...)
	// -t: never more than 90 minutes, even if the app were closed without stopping it
	args = append(args, "-t", "5400", "-g", "60", "-f", "segment", "-segment_time", "4", "-segment_wrap", "16", "-reset_timestamps", "1",
		"-segment_format", "mpegts", filepath.Join(capRing(), "t%02d.ts"))
	cmd := exec.Command(ffExe(), args...)
	cmd.Dir = ffDir()
	hideCmd(cmd)
	in, _ := cmd.StdinPipe()
	var errb bytes.Buffer
	cmd.Stderr = &errb
	if err := cmd.Start(); err != nil {
		playsSet("error", err.Error())
		logf("Jugades: no puc començar a gravar: %v", err)
		return
	}
	capCmd, capIn, capT0 = cmd, in, time.Now()
	os.WriteFile(capPidFile(), []byte(fmt.Sprint(cmd.Process.Pid)), 0o644)
	playsSet("recording", "")
	logf("Jugades: gravant (%s)", enc)
	go func() {
		err := cmd.Wait()
		os.Remove(capPidFile())
		capMu.Lock()
		mine := capCmd == cmd
		if mine {
			capCmd, capIn = nil, nil
		}
		capMu.Unlock()
		cpu := time.Duration(0)
		if cmd.ProcessState != nil {
			cpu = cmd.ProcessState.UserTime() + cmd.ProcessState.SystemTime()
		}
		logf("Jugades: gravació aturada (%v; ffmpeg ha fet servir %.0f s de processador en %.0f min)%s", err, cpu.Seconds(), time.Since(capT0).Minutes(),
			map[bool]string{true: ": " + strings.TrimSpace(errb.String()), false: ""}[errb.Len() > 0 && err != nil])
		if mine {
			playsSet("ready", "")
		}
	}()
}

func captureStop() {
	capMu.Lock()
	cmd, in := capCmd, capIn
	capMu.Unlock()
	if cmd == nil {
		return
	}
	if in != nil {
		in.Write([]byte("q")) // ffmpeg closes its files cleanly
		in.Close()
	}
	go func() {
		time.Sleep(5 * time.Second)
		capMu.Lock()
		still := capCmd == cmd
		capMu.Unlock()
		if still && cmd.Process != nil {
			cmd.Process.Kill()
		}
	}()
}

// captureCut: the pieces that cover [from, to] joined into one video
func captureCut(from, to time.Time, out string) error {
	files, _ := filepath.Glob(filepath.Join(capRing(), "t*.ts"))
	type seg struct {
		p   string
		end time.Time
	}
	var segs []seg
	for _, f := range files {
		if st, err := os.Stat(f); err == nil && st.Size() > 0 {
			segs = append(segs, seg{f, st.ModTime()})
		}
	}
	sort.Slice(segs, func(a, b int) bool { return segs[a].end.Before(segs[b].end) })
	var pick []string
	for _, s := range segs {
		// a piece covers about [end-4s, end]
		if s.end.After(from) && s.end.Add(-4*time.Second).Before(to) {
			pick = append(pick, s.p)
		}
	}
	if len(pick) == 0 {
		return fmt.Errorf("no hi ha gravació d'aquells segons")
	}
	tmp := out + ".parts"
	os.MkdirAll(tmp, 0o755)
	defer os.RemoveAll(tmp)
	var list strings.Builder
	for i, p := range pick { // copied first: the ring keeps overwriting them
		b, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		q := filepath.Join(tmp, fmt.Sprintf("%02d.ts", i))
		os.WriteFile(q, b, 0o644)
		fmt.Fprintf(&list, "file '%s'\n", strings.ReplaceAll(q, "'", "'\\''"))
	}
	lp := filepath.Join(tmp, "list.txt")
	os.WriteFile(lp, []byte(list.String()), 0o644)
	cmd := exec.Command(ffExe(), "-hide_banner", "-loglevel", "error", "-y", "-f", "concat", "-safe", "0", "-i", lp, "-c", "copy", "-movflags", "+faststart", out)
	cmd.Dir = ffDir()
	hideCmd(cmd)
	if b, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("%v: %.200s", err, b)
	}
	return nil
}

func capPidFile() string { return filepath.Join(os.TempDir(), "riftmate-gravacio.pid") }

// captureCleanup: at start, a recording a previous run left going (closed or updated mid-game) is stopped
func captureCleanup() {
	b, err := os.ReadFile(capPidFile())
	if err != nil {
		return
	}
	os.Remove(capPidFile())
	pid := strings.TrimSpace(string(b))
	tl := exec.Command("tasklist", "/FI", "PID eq "+pid, "/FO", "CSV", "/NH")
	hideCmd(tl)
	out, _ := tl.Output()
	if strings.Contains(strings.ToLower(string(out)), "ffmpeg.exe") {
		k := exec.Command("taskkill", "/PID", pid, "/F")
		hideCmd(k)
		k.Run()
		logf("Jugades: aturada una gravació que havia quedat oberta")
	}
}
