//go:build windows

package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	"unsafe"
)

var (
	user32         = syscall.NewLazyDLL("user32.dll")
	procMessageBox = user32.NewProc("MessageBoxW")
)

const (
	mbOK            = 0x0
	mbYesNo         = 0x4
	mbIconInfo      = 0x40
	mbIconQuestion  = 0x20
	idYes           = 6
	uninstallKey    = `HKCU\Software\Microsoft\Windows\CurrentVersion\Uninstall\RiftMate`
	oldUninstallKey = `HKCU\Software\Microsoft\Windows\CurrentVersion\Uninstall\LoLTemporitzador`
)

func msgBox(text string, flags uintptr) int {
	t, _ := syscall.UTF16PtrFromString(text)
	c, _ := syscall.UTF16PtrFromString(appName)
	r, _, _ := procMessageBox.Call(0, uintptr(unsafe.Pointer(t)), uintptr(unsafe.Pointer(c)), flags)
	return int(r)
}

func hidden(name string, args ...string) *exec.Cmd {
	c := exec.Command(name, args...)
	c.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000}
	return c
}

// removeOldVersion cleans up an install made with the old name (shortcuts, uninstall entry, folder).
func removeOldVersion() {
	oldDir := filepath.Join(os.Getenv("LOCALAPPDATA"), "Programs", oldName)
	if _, err := os.Stat(oldDir); err != nil {
		return
	}
	logf("Eliminant la versio antiga a %s", oldDir)
	desktop := filepath.Join(os.Getenv("USERPROFILE"), "Desktop", oldName+".lnk")
	menu := filepath.Join(os.Getenv("APPDATA"), `Microsoft\Windows\Start Menu\Programs`, oldName+".lnk")
	os.Remove(desktop)
	os.Remove(menu)
	hidden("reg", "delete", oldUninstallKey, "/f").Run()
	os.RemoveAll(oldDir)
}

func installDir() string {
	return filepath.Join(os.Getenv("LOCALAPPDATA"), "Programs", appName)
}

func shortcutPaths() []string {
	desktop := filepath.Join(os.Getenv("USERPROFILE"), "Desktop", appName+".lnk")
	menu := filepath.Join(os.Getenv("APPDATA"), `Microsoft\Windows\Start Menu\Programs`, appName+".lnk")
	return []string{desktop, menu}
}

func psQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }

func makeShortcut(lnk, target string) error {
	script := fmt.Sprintf(`$s=(New-Object -ComObject WScript.Shell).CreateShortcut(%s);$s.TargetPath=%s;$s.WorkingDirectory=%s;$s.Description=%s;$s.Save()`,
		psQuote(lnk), psQuote(target), psQuote(filepath.Dir(target)), psQuote("Temporitzador, coach i informes de partida"))
	return hidden("powershell", "-NoProfile", "-NonInteractive", "-Command", script).Run()
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	if _, err = io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

func stopRunning() {
	http.Post(fmt.Sprintf("http://127.0.0.1:%d/api/quit", port), "text/plain", nil)
	time.Sleep(800 * time.Millisecond)
}

func install(self string) error {
	dir := installDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	stopRunning() // an older copy may be running
	target := filepath.Join(dir, "RiftMate.exe")
	removeOldVersion()
	if err := copyFile(self, target); err != nil {
		return err
	}
	for _, l := range shortcutPaths() {
		os.MkdirAll(filepath.Dir(l), 0o755)
		if err := makeShortcut(l, target); err != nil {
			logf("No s'ha pogut crear l'acces directe %s: %v", l, err)
		}
	}
	vals := [][2]string{
		{"DisplayName", appName},
		{"DisplayVersion", version},
		{"Publisher", "Dídac"},
		{"DisplayIcon", target},
		{"InstallLocation", dir},
		{"UninstallString", `"` + target + `" --uninstall`},
	}
	for _, v := range vals {
		hidden("reg", "add", uninstallKey, "/v", v[0], "/d", v[1], "/f").Run()
	}
	hidden("reg", "add", uninstallKey, "/v", "NoModify", "/t", "REG_DWORD", "/d", "1", "/f").Run()
	hidden("reg", "add", uninstallKey, "/v", "NoRepair", "/t", "REG_DWORD", "/d", "1", "/f").Run()
	return exec.Command(target).Start()
}

func uninstall() {
	if msgBox("Vols desinstal·lar "+appName+"?\n\nEls informes de les partides es conservaran.", mbYesNo|mbIconQuestion) != idYes {
		return
	}
	logf("Desinstal.lant")
	stopRunning()
	for _, l := range shortcutPaths() {
		os.Remove(l)
	}
	hidden("reg", "delete", uninstallKey, "/f").Run()
	// the running exe can't delete itself: remove the folder once we have exited
	hidden("cmd", "/c", "ping 127.0.0.1 -n 3 > nul & rmdir /s /q \""+installDir()+"\"").Start()
	msgBox(appName+" s'ha desinstal·lat.", mbOK|mbIconInfo)
}

// handleInstallFlags returns true when the process should exit afterwards.
func handleInstallFlags(args []string) bool {
	for _, a := range args {
		if a == "--uninstall" {
			uninstall()
			return true
		}
		if a == "--portable" || a == "--updated" {
			return false
		}
	}
	self, err := os.Executable()
	if err != nil {
		return false
	}
	self, _ = filepath.EvalSymlinks(self)
	inst := installDir()
	if strings.HasPrefix(strings.ToLower(self), strings.ToLower(inst)) {
		return false // already the installed copy
	}
	verb := "instal·lar"
	if _, err := os.Stat(filepath.Join(inst, "RiftMate.exe")); err == nil {
		verb = "actualitzar"
	}
	q := fmt.Sprintf("Vols %s %s en aquest ordinador?\n\nEs crearan accessos directes a l'escriptori i al menú Inici, i es podrà desinstal·lar des de Configuració > Aplicacions.\n\nSi dius que no, s'obrirà sense instal·lar.", verb, appName)
	if msgBox(q, mbYesNo|mbIconQuestion) != idYes {
		return false
	}
	logf("Instal.lant des de %s", self)
	if err := install(self); err != nil {
		logf("ERROR d'instal.lacio: %v", err)
		msgBox("No s'ha pogut instal·lar: "+err.Error(), mbOK|mbIconInfo)
		return false
	}
	msgBox(appName+" s'ha instal·lat. A partir d'ara obre'l des de l'accés directe de l'escriptori.", mbOK|mbIconInfo)
	return true
}

func openFolder(p string) {
	if _, err := os.Stat(p); err == nil {
		exec.Command("explorer", "/select,", p).Start()
		return
	}
	exec.Command("explorer", filepath.Dir(p)).Start()
}

// openApp opens the page in its own window (Chrome or Edge app mode), so the mic works.
func openApp(url string) {
	// Edge first: it comes with Windows and gives the page Microsoft's natural voices (Catalan and Spanish
	// included), which sound much better than the voices Chrome can use.
	cands := []string{
		edgeFromRegistry(),
		filepath.Join(os.Getenv("PROGRAMFILES(X86)"), `Microsoft\Edge\Application\msedge.exe`),
		filepath.Join(os.Getenv("PROGRAMFILES"), `Microsoft\Edge\Application\msedge.exe`),
		filepath.Join(os.Getenv("LOCALAPPDATA"), `Microsoft\Edge\Application\msedge.exe`),
		filepath.Join(os.Getenv("PROGRAMFILES"), `Google\Chrome\Application\chrome.exe`),
		filepath.Join(os.Getenv("PROGRAMFILES(X86)"), `Google\Chrome\Application\chrome.exe`),
		filepath.Join(os.Getenv("LOCALAPPDATA"), `Google\Chrome\Application\chrome.exe`),
	}
	edge := false
	for _, c := range cands[:4] {
		if _, err := os.Stat(c); c != "" && err == nil {
			edge = true
		}
	}
	if !edge {
		logf("No trobo Microsoft Edge en aquest ordinador: obro Chrome (les veus naturals en català són d'Edge)")
	}
	for _, c := range cands {
		if c == "" {
			continue
		}
		if _, err := os.Stat(c); err == nil {
			// own profile: its own Chrome process, so the flags apply. The mic permission for our page is written
			// into the profile beforehand (a command-line flag for it made Chrome show an "unsupported flag" bar).
			profile := filepath.Join(filepath.Dir(dataDir()), "navegador")
			if strings.Contains(strings.ToLower(c), "msedge") {
				migrateChromePrefs(cands[4:], profile, url)
				profile += "-edge" // a Chrome profile folder must not be opened by Edge
			}
			allowMic(profile, url)
			err := exec.Command(c, "--app="+url, "--window-size=720,900", "--user-data-dir="+profile,
				"--autoplay-policy=no-user-gesture-required", "--no-first-run", "--no-default-browser-check").Start()
			if err == nil {
				logf("Finestra oberta amb %s", filepath.Base(c))
				return
			}
			logf("No s'ha pogut obrir %s: %v", c, err)
		}
	}
	logf("No hi ha Chrome ni Edge: s'obre el navegador per defecte")
	hidden("rundll32", "url.dll,FileProtocolHandler", url).Start()
}

// findLCU locates the League client: its lockfile in the usual folders, or the running client's command line.
func findLCU() *lcuCreds {
	for _, p := range []string{os.Getenv("LT_LCU_LOCKFILE"), `C:\Riot Games\League of Legends\lockfile`, `D:\Riot Games\League of Legends\lockfile`, `E:\Riot Games\League of Legends\lockfile`} {
		if p == "" {
			continue
		}
		if c, err := parseLockfile(p); err == nil {
			return c
		}
	}
	out, err := hidden("powershell", "-NoProfile", "-NonInteractive", "-Command",
		"Get-CimInstance Win32_Process -Filter \"name='LeagueClientUx.exe'\" | Select-Object -ExpandProperty CommandLine").Output()
	if err != nil {
		return nil
	}
	cl := string(out)
	port := argValue(cl, "--app-port=")
	token := argValue(cl, "--remoting-auth-token=")
	if port == "" || token == "" {
		return nil
	}
	logf("Client del LoL trobat (port %s)", port)
	return &lcuCreds{Port: port, Token: token}
}

func argValue(cl, key string) string {
	i := strings.Index(cl, key)
	if i < 0 {
		return ""
	}
	v := cl[i+len(key):]
	v = strings.TrimLeft(v, "\"")
	if j := strings.IndexAny(v, "\" "); j >= 0 {
		v = v[:j]
	}
	return v
}

// migrateChromePrefs: the first time the window opens in Edge, the settings saved by the earlier Chrome window
// (its localStorage) are copied over: an invisible Chrome opens the page with ?migrate=1, the page sends its
// settings to the program, and Edge loads them from there.
func migrateChromePrefs(chromes []string, chromeProfile, url string) {
	if _, err := os.Stat(prefsFile()); err == nil {
		return // settings already mirrored
	}
	if _, err := os.Stat(chromeProfile); err != nil {
		return // never used Chrome
	}
	for _, c := range chromes {
		if _, err := os.Stat(c); err != nil {
			continue
		}
		cmd := exec.Command(c, "--headless=new", "--disable-gpu", "--no-first-run", "--user-data-dir="+chromeProfile, url+"?migrate=1")
		if err := cmd.Start(); err != nil {
			logf("No he pogut copiar els ajustos de Chrome: %v", err)
			return
		}
		ok := waitForFile(prefsFile(), 15*time.Second)
		cmd.Process.Kill()
		if !ok {
			logf("No he pogut copiar els ajustos de Chrome (no han arribat)")
		}
		return
	}
}

// edgeFromRegistry: where Windows says Edge is installed ("App Paths"), or "" if it isn't.
func edgeFromRegistry() string {
	for _, root := range []string{`HKLM`, `HKCU`} {
		out, err := hidden("reg", "query", root+`\SOFTWARE\Microsoft\Windows\CurrentVersion\App Paths\msedge.exe`, "/ve").Output()
		if err != nil {
			continue
		}
		for _, l := range strings.Split(string(out), "\n") {
			if i := strings.Index(l, "REG_SZ"); i >= 0 {
				if p := strings.Trim(strings.TrimSpace(l[i+6:]), `"`); strings.HasSuffix(strings.ToLower(p), "msedge.exe") {
					return p
				}
			}
		}
	}
	return ""
}

// allowMic writes "microphone allowed" for our page into the browser profile's settings, so it never asks.
func allowMic(profile, url string) {
	origin := strings.TrimSuffix(url, "/") // http://localhost:8765
	file := filepath.Join(profile, "Default", "Preferences")
	prefs := map[string]any{}
	if b, err := os.ReadFile(file); err == nil {
		if json.Unmarshal(b, &prefs) != nil {
			return // unreadable: leave the browser's file alone
		}
	}
	node := func(m map[string]any, k string) map[string]any {
		if v, ok := m[k].(map[string]any); ok {
			return v
		}
		v := map[string]any{}
		m[k] = v
		return v
	}
	ex := node(node(node(prefs, "profile"), "content_settings"), "exceptions")
	mic := node(ex, "media_stream_mic")
	key := origin + ",*"
	if e, ok := mic[key].(map[string]any); ok && e["setting"] == float64(1) {
		return // already allowed
	}
	mic[key] = map[string]any{"setting": 1}
	os.MkdirAll(filepath.Dir(file), 0o755)
	if b, err := json.Marshal(prefs); err == nil {
		os.WriteFile(file, b, 0o644)
		logf("Permis del micro desat al perfil del navegador")
	}
}

// detach: the new copy started after an update must outlive this one (own process group, no console).
func detach(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x00000008 | 0x00000200}
}

// killReplay closes the game window of a replay the app opened (only while the client says a replay is on)
func killReplay() {
	if gameflowPhase() != "WatchInProgress" {
		phaseMu.Lock()
		phaseAt = time.Time{}
		phaseMu.Unlock()
		if gameflowPhase() != "WatchInProgress" {
			return
		}
	}
	hidden("taskkill", "/IM", "League of Legends.exe", "/F").Run()
	logf("Repetició tancada")
}
