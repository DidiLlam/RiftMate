package main

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

// Whisper: a modern speech recogniser (OpenAI's open model, run by whisper.cpp) on this PC, free and offline.
// Windows' recogniser only knows the command phrases, so whatever it hears it squeezes into the nearest one
// ("kouch", "Shen barrier" out of a chat with your duo). Now each phrase the microphone picks up is also written
// out by Whisper, and only what Whisper heard counts. The first time it downloads ~65 MB (program + model).

const (
	whisperVer   = "v1.8.2"
	whisperZip   = "https://github.com/ggml-org/whisper.cpp/releases/download/" + whisperVer + "/whisper-bin-x64.zip"
	whisperModel = "ggml-base-q5_1.bin"
	whisperURL   = "https://huggingface.co/ggerganov/whisper.cpp/resolve/main/" + whisperModel
	whisperPort  = 8767
)

var (
	wMu      sync.Mutex
	wState   = "off" // off, downloading, starting, ready, error
	wErr     string
	wBusy    bool
	wQueue   = make(chan wClip, 4)
	wStarted bool
	wHTTP    = &http.Client{Timeout: 10 * time.Minute}
)

// one phrase the Windows recogniser caught, with its audio
type wClip struct {
	Path, Grammar, Kind string
	Conf                float64
	At                  time.Time
	Fast                int     // the page already acted on Windows' version (id): Whisper only confirms or undoes it
	DB                  float64 // how loud (0: not measured)
}

func whisperDir() string { return filepath.Join(filepath.Dir(dataDir()), "whisper") }

func whisperStatus() (string, string) {
	wMu.Lock()
	defer wMu.Unlock()
	return wState, wErr
}

func whisperSet(st, e string) {
	wMu.Lock()
	wState, wErr = st, e
	wMu.Unlock()
}

// whisperOn: the setting (on unless you turned it off)
func whisperOn() bool {
	b, err := os.ReadFile(profileFile("whisper.json"))
	if err != nil {
		return true
	}
	var c struct{ Off bool }
	json.Unmarshal(b, &c)
	return !c.Off
}

// whisperEnsure downloads (once) and starts the local server, in the background
func whisperEnsure() {
	if !whisperOn() || !whisperSupported {
		return
	}
	wMu.Lock()
	if wBusy || wState == "ready" {
		wMu.Unlock()
		return
	}
	wBusy = true
	wMu.Unlock()
	go func() {
		defer func() { wMu.Lock(); wBusy = false; wMu.Unlock() }()
		if err := whisperDownload(); err != nil {
			whisperSet("error", err.Error())
			logf("Whisper: no l'he pogut descarregar: %v", err)
			return
		}
		whisperSet("starting", "")
		if err := whisperServe(); err != nil {
			whisperSet("error", err.Error())
			logf("Whisper: no s'ha pogut engegar: %v", err)
			return
		}
		whisperWarm()
		whisperSet("ready", "")
		logf("Whisper: llest")
		wMu.Lock()
		if !wStarted {
			wStarted = true
			go whisperWorker()
		}
		wMu.Unlock()
	}()
}

func fileSize(p string) int64 {
	if st, err := os.Stat(p); err == nil {
		return st.Size()
	}
	return -1
}

func whisperDownload() error {
	dir := whisperDir()
	os.MkdirAll(dir, 0o755)
	need := []string{"whisper-server.exe", "whisper.dll", "ggml.dll", "ggml-base.dll", "ggml-cpu.dll"}
	have := true
	for _, n := range need {
		if fileSize(filepath.Join(dir, n)) <= 0 {
			have = false
		}
	}
	if !have {
		whisperSet("downloading", "programa")
		logf("Whisper: descarregant el programa (%s)", whisperVer)
		b, err := wGet(whisperZip, 50<<20)
		if err != nil {
			return err
		}
		zr, err := zip.NewReader(bytes.NewReader(b), int64(len(b)))
		if err != nil {
			return err
		}
		for _, f := range zr.File {
			base := filepath.Base(f.Name)
			ok := false
			for _, n := range need {
				ok = ok || strings.EqualFold(base, n)
			}
			if !ok || f.FileInfo().IsDir() {
				continue
			}
			rc, err := f.Open()
			if err != nil {
				return err
			}
			data, err := io.ReadAll(io.LimitReader(rc, 20<<20))
			rc.Close()
			if err != nil {
				return err
			}
			if err := os.WriteFile(filepath.Join(dir, base), data, 0o755); err != nil {
				return err
			}
		}
		for _, n := range need {
			if fileSize(filepath.Join(dir, n)) <= 0 {
				return fmt.Errorf("al paquet de whisper.cpp no hi ha %s", n)
			}
		}
	}
	mp := filepath.Join(dir, whisperModel)
	if fileSize(mp) < 30<<20 {
		whisperSet("downloading", "model")
		logf("Whisper: descarregant el model (%s, uns 60 MB)", whisperModel)
		r, err := wHTTP.Get(whisperURL)
		if err != nil {
			return err
		}
		defer r.Body.Close()
		if r.StatusCode != 200 {
			return fmt.Errorf("model: HTTP %d", r.StatusCode)
		}
		tmp := mp + ".part"
		f, err := os.Create(tmp)
		if err != nil {
			return err
		}
		n, err := io.Copy(f, io.LimitReader(r.Body, 400<<20))
		f.Close()
		if err != nil || n < 30<<20 {
			os.Remove(tmp)
			return fmt.Errorf("model incomplet (%d bytes) %v", n, err)
		}
		if err := os.Rename(tmp, mp); err != nil {
			return err
		}
		logf("Whisper: model descarregat (%d MB)", n>>20)
	}
	return nil
}

func wGet(u string, max int64) ([]byte, error) {
	r, err := wHTTP.Get(u)
	if err != nil {
		return nil, err
	}
	defer r.Body.Close()
	if r.StatusCode != 200 {
		return nil, fmt.Errorf("%s: HTTP %d", u, r.StatusCode)
	}
	return io.ReadAll(io.LimitReader(r.Body, max))
}

// whisperQueue: a phrase to write out. Full queue (a burst of noise): the oldest waits, this one is dropped.
func whisperQueue(c wClip) bool {
	if st, _ := whisperStatus(); st != "ready" {
		return false
	}
	select {
	case wQueue <- c:
		return true
	default:
		os.Remove(c.Path)
		return true
	}
}

func whisperWorker() {
	for c := range wQueue {
		text, dur, err := whisperTranscribe(c.Path)
		os.Remove(c.Path)
		if time.Since(c.At) > 8*time.Second { // too late to be useful
			continue
		}
		if err != nil && c.Kind == "Q" {
			logf("Whisper: error %v amb la pregunta", err)
			continue
		}
		if err != nil {
			logf("Whisper: error %v; faig servir el que ha entès Windows", err)
			if c.Kind == "HEARD" && c.Fast == 0 {
				voicePush("heard", c.Grammar)
			}
			continue
		}
		if c.Kind == "Q" {
			logf("Veu (Whisper, pregunta, %.1f s): \"%s\"", dur.Seconds(), text)
			questionAppend(text)
			continue
		}
		vStat.add(c, text, dur)
		logf("Veu (Whisper, %.2f s, %.0f dB): \"%s\"  [Windows: \"%s\" %.2f %s]", dur.Seconds(), c.DB, text, c.Grammar, c.Conf, c.Kind)
		b, _ := json.Marshal(map[string]any{"w": text, "g": c.Grammar, "c": c.Conf, "id": c.Fast})
		voicePush("heardw", string(b))
	}
}

var (
	wPromptMu sync.Mutex
	wPrompt   string
	wLang     = "es"
)

// whisperSetGame: the champions of this game, so Whisper writes their names right
func whisperSetGame(champs []string) {
	wPromptMu.Lock()
	defer wPromptMu.Unlock()
	names := strings.Join(champs, ", ")
	// only this game's names (an example name here made it write that name instead of the one said)
	wPrompt = "Flash, ignite, ulti, zhonya, barrera, heal, exhaust, teleport, ghost, cleanse, smite, coach. " + names + "."
}

func whisperSetLang(l string) {
	wPromptMu.Lock()
	defer wPromptMu.Unlock()
	if l = strings.ToLower(strings.SplitN(l, "-", 2)[0]); l == "es" || l == "ca" || l == "en" {
		wLang = l
	}
}

func whisperTranscribe(path string) (string, time.Duration, error) {
	t0 := time.Now()
	wav, err := os.ReadFile(path)
	if err != nil {
		return "", 0, err
	}
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, _ := mw.CreateFormFile("file", "clip.wav")
	fw.Write(wav)
	wPromptMu.Lock()
	mw.WriteField("prompt", wPrompt)
	mw.WriteField("language", wLang)
	wPromptMu.Unlock()
	mw.WriteField("temperature", "0.0")
	// (a shorter audio window, tried in 2.0.85 to make it faster, left Whisper deaf: it answered nothing)
	mw.WriteField("response_format", "json")
	mw.Close()
	req, _ := http.NewRequest("POST", fmt.Sprintf("http://127.0.0.1:%d/inference", whisperPort), &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	cl := &http.Client{Timeout: 8 * time.Second}
	r, err := cl.Do(req)
	if err != nil {
		return "", 0, err
	}
	defer r.Body.Close()
	var out struct {
		Text  string `json:"text"`
		Error string `json:"error"`
	}
	b, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err := json.Unmarshal(b, &out); err != nil {
		return "", 0, fmt.Errorf("resposta estranya: %.80s", b)
	}
	if out.Error != "" {
		return "", 0, fmt.Errorf("%s", out.Error)
	}
	return whisperClean(out.Text), time.Since(t0), nil
}

// wavSeconds: the length of a .wav (from its header)
func wavSeconds(b []byte) float64 {
	if len(b) < 44 || string(b[0:4]) != "RIFF" || string(b[8:12]) != "WAVE" {
		return 0
	}
	byteRate := 0.0
	for i := 12; i+8 <= len(b); {
		id, n := string(b[i:i+4]), int(uint32(b[i+4])|uint32(b[i+5])<<8|uint32(b[i+6])<<16|uint32(b[i+7])<<24)
		if id == "fmt " && i+16 <= len(b) {
			byteRate = float64(uint32(b[i+16]) | uint32(b[i+17])<<8 | uint32(b[i+18])<<16 | uint32(b[i+19])<<24)
		}
		if id == "data" {
			if byteRate <= 0 {
				return 0
			}
			if n > len(b)-i-8 || n <= 0 {
				n = len(b) - i - 8
			}
			return float64(n) / byteRate
		}
		i += 8 + n + n%2
	}
	return 0
}

var wJunk = regexp.MustCompile(`[\[\(][^\]\)]*[\]\)]`)

// whisperClean: one line, without what Whisper writes for silence or noise ("[RÍE]", "(música)")
func whisperClean(s string) string {
	s = wJunk.ReplaceAllString(s, " ")
	s = strings.Join(strings.Fields(s), " ")
	low := strings.ToLower(s)
	for _, junk := range []string{"[blank_audio]", "[música]", "[music]", "(música)", "[ruido]", "[silencio]", "subtítulos", "gracias por ver", "suscríbete"} {
		if strings.Contains(low, junk) {
			return ""
		}
	}
	return strings.Trim(s, " .")
}

func whisperHandler(send func(http.ResponseWriter, int, string, []byte)) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		if req.Method == http.MethodPost {
			on := req.URL.Query().Get("on") == "1"
			b, _ := json.Marshal(map[string]bool{"Off": !on})
			os.WriteFile(profileFile("whisper.json"), b, 0o644)
			logf("Whisper: %s", map[bool]string{true: "activat", false: "desactivat"}[on])
			if on {
				whisperEnsure()
			} else {
				whisperStop()
				whisperSet("off", "")
			}
		}
		st, e := whisperStatus()
		b, _ := json.Marshal(map[string]any{"on": whisperOn(), "supported": whisperSupported, "state": st, "error": e})
		send(w, 200, "application/json", b)
	}
}

// The question after "coach": Windows cuts a long sentence into pieces; Whisper writes each one and they are
// put back together. 1.5 s with nothing new: the question is complete.
var (
	qMu    sync.Mutex
	qParts []string
	qTimer *time.Timer
)

func questionFlag() string { return filepath.Join(os.TempDir(), "riftmate-pregunta.flag") }

// questionStart: from now on, what the microphone hears is the question
func questionStart() bool {
	if st, _ := whisperStatus(); st != "ready" || !voiceRunning() {
		return false
	}
	qMu.Lock()
	qParts = nil
	qMu.Unlock()
	return os.WriteFile(questionFlag(), []byte("1"), 0o644) == nil
}

func questionAppend(t string) {
	if t == "" { // the beep, a breath: not the question yet
		return
	}
	qMu.Lock()
	defer qMu.Unlock()
	qParts = append(qParts, t)
	if qTimer != nil {
		qTimer.Stop()
	}
	qTimer = time.AfterFunc(1500*time.Millisecond, func() {
		qMu.Lock()
		q := strings.TrimSpace(strings.Join(qParts, " "))
		qParts = nil
		qMu.Unlock()
		os.Remove(questionFlag())
		if q != "" {
			voicePush("question", q)
		}
	})
}

// whisperWarm: one second of silence through it, so the first real phrase isn't the slow one
func whisperWarm() {
	f := filepath.Join(os.TempDir(), "riftmate-escalfa.wav")
	n := 16000 * 2
	b := make([]byte, 44+n)
	copy(b, "RIFF")
	le32 := func(o int, v uint32) { b[o], b[o+1], b[o+2], b[o+3] = byte(v), byte(v>>8), byte(v>>16), byte(v>>24) }
	le32(4, uint32(36+n))
	copy(b[8:], "WAVEfmt ")
	le32(16, 16)
	b[20], b[22] = 1, 1 // PCM, mono
	le32(24, 16000)
	le32(28, 32000)
	b[32], b[34] = 2, 16
	copy(b[36:], "data")
	le32(40, uint32(n))
	if os.WriteFile(f, b, 0o644) == nil {
		_, d, err := whisperTranscribe(f)
		os.Remove(f)
		logf("Whisper: escalfat (%.2f s, %v)", d.Seconds(), err)
	}
}

// vStat: how the voice did in this game, for the log (to tune it with real numbers)
type voiceStats struct {
	mu                             sync.Mutex
	n, cmds, fast, fastOK, fastFix int
	total, max                     time.Duration
	dbs                            []float64
}

var vStat voiceStats

func (v *voiceStats) add(c wClip, text string, d time.Duration) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.n++
	v.total += d
	if d > v.max {
		v.max = d
	}
	if c.DB != 0 {
		v.dbs = append(v.dbs, c.DB)
	}
	if c.Fast > 0 {
		v.fast++
		if strings.EqualFold(strings.Join(strings.Fields(text), " "), "") {
			v.fastFix++
		}
	}
}

// voiceStatsFlush: one line at the end of the game, then start again
func voiceStatsFlush() {
	v := &vStat
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.n == 0 {
		return
	}
	logf("Veu (resum de la partida): %d frases a Whisper, %.2f s de mitjana, %.2f s la més lenta; %d marcades a l'instant per Windows, %d sense text a Whisper",
		v.n, (v.total / time.Duration(v.n)).Seconds(), v.max.Seconds(), v.fast, v.fastFix)
	// reset the numbers only (overwriting the whole struct would also wipe its lock while it's held: a crash)
	v.n, v.cmds, v.fast, v.fastOK, v.fastFix, v.total, v.max, v.dbs = 0, 0, 0, 0, 0, 0, 0, nil
}
