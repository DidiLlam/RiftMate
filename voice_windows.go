//go:build windows

package main

import (
	"bufio"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
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
  [Console]::Out.WriteLine('READY ' + $cul.Name)
  while ($true) {
    $r = $eng.Recognize()
    if ($r) {
      # the wake word is a single short word the Spanish recognizer scores low: accept it from 0.3
      $thr = 0.6; $gn = ''; if ($r.Grammar) { $gn = $r.Grammar.Name }
      if ($gn -eq 'coach') { $thr = 0.3 }
      # "top jil" / "heal top" is what game noise gets mistaken for: that one needs real certainty
      elseif ($r.Text -match '\btop\b' -and $r.Text -match '\b(jil|heal|curar)\b' -and $gn -ne 'linies') { $thr = 0.8 }
      elseif ($r.Text -match '\b(top|jungla|jungle|mid|adc|bot|suport|support|soporte)\b' -and $gn -ne 'linies') { $thr = 0.62 }
      # a champion of this game: the page still ignores a spell that champion doesn't carry
      elseif ($gn -like 'partida*') { $thr = 0.5 }
      if ($r.Confidence -ge $thr) { [Console]::Out.WriteLine('HEARD ' + $r.Text) }
      else { [Console]::Out.WriteLine('LOW ' + [math]::Round($r.Confidence, 2) + ' ' + $r.Text) }
    }
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
				voicePush("heard", line[6:])
				logf("Veu de Windows: \"%s\"", line[6:])
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
