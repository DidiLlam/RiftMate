//go:build windows

package main

import (
	"bufio"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode/utf16"
)

const voiceSupported = true

var (
	vMu  sync.Mutex
	vCmd *exec.Cmd
)

// The word lists go to a JSON file (too long for a command line). Every champion is listened for; this game's
// champions get a grammar of their own with more weight, so they win when two names sound alike.
// Each grammar is a plain sequence of two lists of words (no optional parts, no shared objects): the nested
// grammar of earlier versions failed on some PCs with "Referencia de regla ” no definida en esta gramatica".
// If even this fails, the flat list of this game's phrases is loaded instead.
const voiceScript = `
$ErrorActionPreference = 'Stop'
$ProgressPreference = 'SilentlyContinue'
try {
  Add-Type -AssemblyName System.Speech
  # how loud each phrase was (the loudest 20 ms, in dB): you speak right into the microphone, your duo, Discord and
  # the game reach it much quieter. The app learns your level and ignores what is clearly quieter.
  $lvlOK = $false
  try { Add-Type -TypeDefinition 'public static class RMLvl { public static double Peak(byte[] b) { double best = -120, acc = 0; int n = 0; for (int i = 0; i + 1 < b.Length; i += 2) { double v = (short)(b[i] | (b[i + 1] << 8)); acc += v * v; n++; if (n == 320) { double r = System.Math.Sqrt(acc / n) / 32768.0; double db = r > 0 ? 20 * System.Math.Log10(r) : -120; if (db > best) best = db; acc = 0; n = 0; } } return best; } }'; $lvlOK = $true } catch {}
  function Lvl($r) {
    if (-not $lvlOK -or -not $r.Audio) { return '' }
    try { if ($r.Audio.Format.BitsPerSample -ne 16) { return '' }; $ms = New-Object System.IO.MemoryStream; $r.Audio.WriteToAudioStream($ms); return [string][math]::Round([RMLvl]::Peak($ms.ToArray()), 1) } catch { return '' }
  }
  $all = [System.Speech.Recognition.SpeechRecognitionEngine]::InstalledRecognizers()
  if (-not $all -or $all.Count -eq 0) { [Console]::Out.WriteLine('ERROR no_recognizer'); exit 2 }
  [Console]::Out.WriteLine('INFO recognizers: ' + (($all | ForEach-Object { $_.Culture.Name }) -join ', '))
  $ri = $null
  foreach ($p in @('es-ES','ca-ES','es-MX','es-US','en-US','en-GB')) { $ri = $all | Where-Object { $_.Culture.Name -eq $p } | Select-Object -First 1; if ($ri) { break } }
  if (-not $ri) { $ri = $all[0] }
  $cul = $ri.Culture
  $eng = New-Object System.Speech.Recognition.SpeechRecognitionEngine($ri)
  $d = Get-Content -LiteralPath '__FILE__' -Raw -Encoding UTF8 | ConvertFrom-Json
  function CH($xs) { $c = New-Object System.Speech.Recognition.Choices; $c.Add([string[]]@($xs)); return $c }
  function G($a, $b, $name, $weight, $prio) {
    $gb = New-Object System.Speech.Recognition.GrammarBuilder
    $gb.Culture = $cul
    $gb.Append((CH $a)); $gb.Append((CH $b))
    $g = New-Object System.Speech.Recognition.Grammar($gb)
    $g.Name = $name; $g.Weight = $weight; $g.Priority = $prio
    $eng.LoadGrammar($g)
  }
  try {
    if (@($d.game).Count -gt 0) {
      G $d.prefixes $d.game 'partida' 1.0 10
      G $d.game $d.spells 'partida2' 1.0 10
    }
    G $d.prefixes $d.all 'tots' 0.6 0
    G $d.all $d.spells 'tots2' 0.6 0
    if (@($d.game).Count -eq 0 -or $d.lanesToo) { G $d.lanes $d.names 'linies' 0.8 5 }   # 'top Darius' only when the rivals weren't detected
    $gw = New-Object System.Speech.Recognition.GrammarBuilder
    $gw.Culture = $cul
    $gw.Append((CH $d.wake))
    $g = New-Object System.Speech.Recognition.Grammar($gw); $g.Name = 'coach'; $eng.LoadGrammar($g)
    [Console]::Out.WriteLine('INFO gramatica: tots els campions (' + @($d.all).Count + ' noms), ' + @($d.game).Count + ' de la partida amb prioritat')
  } catch {
    [Console]::Out.WriteLine('INFO gramatica completa no disponible (' + $_.Exception.Message + '): faig servir les frases de la partida')
    $eng.UnloadAllGrammars()
    $gb = New-Object System.Speech.Recognition.GrammarBuilder
    $gb.Culture = $cul
    $gb.Append((CH $d.flat))
    $eng.LoadGrammar((New-Object System.Speech.Recognition.Grammar($gb)))
  }
  $eng.SetInputToDefaultAudioDevice()
  # end a phrase soon after you stop talking, and never give up on a long one
  try { $eng.EndSilenceTimeout = [TimeSpan]::FromMilliseconds(400) } catch {}
  try { $eng.EndSilenceTimeoutAmbiguous = [TimeSpan]::FromMilliseconds(700) } catch {}
  try { $eng.BabbleTimeout = [TimeSpan]::Zero } catch {}
  try { $eng.InitialSilenceTimeout = [TimeSpan]::Zero } catch {}
  # the phrase's audio to a temporary .wav, for Whisper to write out what was really said
  # right after "coach": everything heard is the question (Whisper writes it out), whatever Windows made of it
  $qflag = Join-Path $env:TEMP 'riftmate-pregunta.flag'
  function QMode { try { if (Test-Path -LiteralPath $qflag) { return (((Get-Date) - (Get-Item -LiteralPath $qflag).LastWriteTime).TotalSeconds -lt 12) } } catch {}; return $false }
  function Clip($r, $text, $conf, $lv, $kind) {
    try {
      $max = 6; if ($kind -eq 'Q') { $max = 15 }
      if (-not $r.Audio -or $r.Audio.Duration.TotalSeconds -gt $max -or $r.Audio.Duration.TotalSeconds -lt 0.3) { return $false }
      $p = Join-Path $env:TEMP ('riftmate-' + [guid]::NewGuid().ToString('N') + '.wav')
      $fs = [System.IO.File]::Create($p); $r.Audio.WriteToWaveStream($fs); $fs.Close()
      $t = [string][char]9
      [Console]::Out.WriteLine('CLIP ' + $p + $t + $text + $t + [math]::Round($conf, 2) + $t + $lv + $t + $kind)
      return $true
    } catch { return $false }
  }
  function Judge($r) {
    # the wake word is a single short word the Spanish recognizer scores low: accept it from 0.3
    $thr = 0.6; $gn = ''; if ($r.Grammar) { $gn = $r.Grammar.Name }
    if ($gn -eq 'coach') { $thr = 0.3 }
    # "top jil" / "heal top" is what game noise gets mistaken for: that one needs real certainty
    elseif ($r.Text -match '\btop\b' -and $r.Text -match '\b(jil|heal|curar)\b' -and $gn -ne 'linies') { $thr = 0.8 }
    elseif ($r.Text -match '\b(top|jungla|jungle|mid|adc|bot|suport|support|soporte)\b' -and $gn -ne 'linies') { $thr = 0.62 }
    # a champion of this game: the page still ignores a spell that champion doesn't carry
    elseif ($gn -like 'partida*') { $thr = 0.5 }
    $lv = Lvl $r
    if ($d.clips) {
      $k = 'LOW'; if ($r.Confidence -ge $thr) { $k = 'HEARD' }
      if (QMode) { $k = 'Q' }
      if (Clip $r $r.Text $r.Confidence $lv $k) { return }
    }
    if ($r.Confidence -ge $thr) { [Console]::Out.WriteLine('HEARD ' + $r.Text + ' |' + [math]::Round($r.Confidence, 2) + ' |' + $lv) }
    else { [Console]::Out.WriteLine('LOW ' + [math]::Round($r.Confidence, 2) + ' ' + $r.Text + ' (' + $lv + ' dB)') }
  }
  [Console]::Out.WriteLine('READY ' + $cul.Name)
  # continuous listening: the microphone is never closed between phrases, so the start of "flash Darius"
  # isn't lost while the previous phrase (or game noise) is being processed
  $async = $true
  try {
    $null = Register-ObjectEvent -InputObject $eng -EventName SpeechRecognized -SourceIdentifier 'vrec'
    $null = Register-ObjectEvent -InputObject $eng -EventName SpeechRecognitionRejected -SourceIdentifier 'vrej'
    $eng.RecognizeAsync([System.Speech.Recognition.RecognizeMode]::Multiple)
    [Console]::Out.WriteLine('INFO escolta continua')
  } catch { $async = $false; [Console]::Out.WriteLine('INFO escolta frase a frase (' + $_.Exception.Message + ')') }
  if ($async) {
    while ($true) {
      $e = Wait-Event -Timeout 5
      if (-not $e) { continue }
      Remove-Event -EventIdentifier $e.EventIdentifier -ErrorAction SilentlyContinue
      $r = $e.SourceEventArgs.Result
      if (-not $r) { continue }
      if ($e.SourceIdentifier -eq 'vrej') {
        # nothing matched well enough: the best guess goes to the log, to see what was almost understood
        $a = $null; if ($r.Alternates -and $r.Alternates.Count -gt 0) { $a = $r.Alternates[0] }
        if ($d.clips -and (QMode)) { $at = ''; if ($a) { $at = $a.Text }; if (Clip $r $at 0 (Lvl $r) 'Q') { continue } }
        if ($a -and $a.Text -and $a.Confidence -ge 0.4) {
          if ($d.clips -and (Clip $r $a.Text $a.Confidence (Lvl $r) 'REJ')) { continue }
          [Console]::Out.WriteLine('LOW ' + [math]::Round($a.Confidence, 2) + ' ' + $a.Text + ' (rebutjat)')
        }
        continue
      }
      Judge $r
    }
  } else {
    while ($true) { $r = $eng.Recognize(); if ($r) { Judge $r } }
  }
} catch {
  [Console]::Out.WriteLine('ERROR ' + $_.Exception.Message)
  exit 3
}
`

func voiceRunning() bool {
	vMu.Lock()
	defer vMu.Unlock()
	return vCmd != nil
}

func voiceStart(champs []string, lanes bool) error {
	vMu.Lock()
	defer vMu.Unlock()
	if vCmd != nil {
		return nil
	}
	gd := voiceGrammar(champs)
	gd.LanesToo = lanes
	gd.Clips = whisperOn()
	whisperSetGame(champs)
	whisperEnsure()
	file := filepath.Join(filepath.Dir(dataDir()), "veu-gramatica.json")
	js, _ := json.Marshal(gd)
	if err := os.WriteFile(file, js, 0o644); err != nil {
		logf("Veu de Windows: no puc escriure la gramatica: %v", err)
		return err
	}
	logf("Veu de Windows: %d noms en total, %d de la partida (%s)", len(gd.All), len(gd.Game), strings.Join(champs, ", "))
	script := strings.ReplaceAll(voiceScript, "__FILE__", strings.ReplaceAll(file, "'", "''"))
	u := utf16.Encode([]rune(script))
	b := make([]byte, len(u)*2)
	for i, v := range u {
		b[2*i], b[2*i+1] = byte(v), byte(v>>8)
	}
	cmd := exec.Command("powershell", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-EncodedCommand", base64.StdEncoding.EncodeToString(b))
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000}
	out, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	cmd.Stderr = cmd.Stdout
	if err := cmd.Start(); err != nil {
		logf("Veu de Windows: no s'ha pogut iniciar PowerShell: %v", err)
		return err
	}
	vCmd = cmd
	logf("Veu de Windows: iniciant reconeixement")
	go func() {
		sc := bufio.NewScanner(out)
		for sc.Scan() {
			line := strings.TrimSpace(sc.Text())
			switch {
			case strings.HasPrefix(line, "HEARD "):
				parts := strings.Split(line[6:], " |")
				text, conf, lv := parts[0], "", ""
				if len(parts) > 1 {
					conf = parts[1]
				}
				if len(parts) > 2 {
					lv = parts[2]
				}
				if db, err := strconv.ParseFloat(lv, 64); err == nil {
					ok, ref := voiceLoudEnough(db, true)
					if !ok {
						logf("Veu de Windows: \"%s\" (%s, %.0f dB) ignorat: massa fluix, no sembla la teva veu (tu: uns %.0f dB)", text, conf, db, ref)
						continue
					}
					lv = fmt.Sprintf(", %.0f dB", db)
				}
				voicePush("heard", text)
				logf("Veu de Windows: \"%s\" (%s%s)", text, conf, lv)
			case strings.HasPrefix(line, "CLIP "):
				voiceClip(line[5:])
			case strings.HasPrefix(line, "READY "):
				voicePush("ready", line[6:])
				logf("Veu de Windows: llest (idioma %s)", line[6:])
			case strings.HasPrefix(line, "ERROR "):
				voicePush("error", line[6:])
				logf("Veu de Windows: ERROR %s", line[6:])
			case strings.HasPrefix(line, "LOW "), strings.HasPrefix(line, "INFO "):
				logf("Veu de Windows: %s", line)
			case line != "":
				logf("Veu de Windows (sortida): %s", line)
			}
		}
		err := cmd.Wait()
		vMu.Lock()
		if vCmd == cmd {
			vCmd = nil
		}
		vMu.Unlock()
		voicePush("stopped", "")
		logf("Veu de Windows: aturat (%v)", err)
	}()
	return nil
}

func voiceStop() {
	vMu.Lock()
	cmd := vCmd
	vCmd = nil
	vMu.Unlock()
	if cmd != nil && cmd.Process != nil {
		cmd.Process.Kill()
	}
}

var _ = errors.New

var fastSeq int

// voiceClip: a phrase with its audio. Too quiet to be you: dropped. Whisper ready: it decides what was said.
// Otherwise, as before: what Windows understood, if it was sure enough.
func voiceClip(line string) {
	f := strings.Split(line, "\t")
	if len(f) < 5 {
		return
	}
	path, text, kind := f[0], f[1], f[4]
	conf, _ := strconv.ParseFloat(f[2], 64)
	lvs := ""
	if db, err := strconv.ParseFloat(f[3], 64); err == nil {
		ok, ref := voiceLoudEnough(db, kind == "HEARD" && conf >= 0.65)
		if !ok {
			os.Remove(path)
			logf("Veu de Windows: \"%s\" (%.2f, %.0f dB) ignorat: massa fluix, no sembla la teva veu (tu: uns %.0f dB)", text, conf, db, ref)
			return
		}
		lvs = fmt.Sprintf(", %.0f dB", db)
	}
	// Windows sure enough: the page acts on it now (no waiting), and Whisper confirms it or undoes it a moment later
	fast := 0
	if kind == "HEARD" && conf >= 0.62 {
		if st, _ := whisperStatus(); st == "ready" {
			fastSeq++
			fast = fastSeq
			b, _ := json.Marshal(map[string]any{"id": fast, "t": text})
			voicePush("heardf", string(b))
			logf("Veu de Windows: \"%s\" (%.2f%s), Whisper ho comprova", text, conf, lvs)
		}
	}
	if whisperQueue(wClip{Path: path, Grammar: text, Kind: kind, Conf: conf, At: time.Now(), Fast: fast}) {
		return
	}
	os.Remove(path)
	if kind == "HEARD" {
		voicePush("heard", text)
		logf("Veu de Windows: \"%s\" (%.2f%s)", text, conf, lvs)
	} else {
		logf("Veu de Windows: LOW %.2f %s (%s%s)", conf, text, kind, lvs)
	}
}
