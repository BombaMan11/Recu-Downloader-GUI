//go:build windows

package launch

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"
)

func browserCandidates() []string {
	var out []string
	for _, env := range []string{"ProgramFiles(x86)", "ProgramFiles", "LocalAppData"} {
		root := os.Getenv(env)
		if root == "" {
			continue
		}
		out = append(out,
			filepath.Join(root, `Microsoft\Edge\Application\msedge.exe`),
			filepath.Join(root, `Google\Chrome\Application\chrome.exe`),
		)
	}
	for _, name := range []string{"msedge", "chrome"} {
		if p, err := exec.LookPath(name); err == nil {
			out = append(out, p)
		}
	}
	return out
}

// OpenFolder shows a folder in Explorer.
func OpenFolder(path string) error {
	// explorer reports "failure" exit codes even when it works, so only the start matters
	return exec.Command("explorer", filepath.Clean(path)).Start()
}

// RevealFile opens the file's folder in Explorer with the file selected.
func RevealFile(path string) error {
	cmd := exec.Command("explorer.exe")
	// Explorer wants exactly /select,"full path" (Go would quote the whole argument)
	cmd.SysProcAttr = &syscall.SysProcAttr{CmdLine: `explorer.exe /select,"` + filepath.Clean(path) + `"`}
	return cmd.Start()
}

func openDefault(url string) error {
	return exec.Command("rundll32", "url.dll,FileProtocolHandler", url).Start()
}

// ShowError tells the user something went wrong when there is no console.
func ShowError(title, text string) {
	t, _ := syscall.UTF16PtrFromString(title)
	m, _ := syscall.UTF16PtrFromString(text)
	syscall.NewLazyDLL("user32.dll").NewProc("MessageBoxW").Call(0, uintptr(unsafe.Pointer(m)), uintptr(unsafe.Pointer(t)), 0x10)
}

var (
	user32               = syscall.NewLazyDLL("user32.dll")
	procEnumWindows      = user32.NewProc("EnumWindows")
	procIsWindowVisible  = user32.NewProc("IsWindowVisible")
	procGetClassName     = user32.NewProc("GetClassNameW")
	procGetWindowText    = user32.NewProc("GetWindowTextW")
	procIsIconic         = user32.NewProc("IsIconic")
	procShowWindow       = user32.NewProc("ShowWindow")
	procBringWindowToTop = user32.NewProc("BringWindowToTop")
	procSetForeground    = user32.NewProc("SetForegroundWindow")
	procKeybdEvent       = user32.NewProc("keybd_event")
)

const (
	swRestore      = 9
	vkMenu         = 0x12
	keyeventfKeyUp = 0x2
)

// FocusWindow brings the already open program window (title starting with
// title) to the front, restoring it if it is minimised. It reports whether a
// window was found.
func FocusWindow(title string) bool {
	found := findWindow(title)
	if found == 0 {
		return false
	}
	if iconic, _, _ := procIsIconic.Call(found); iconic != 0 {
		procShowWindow.Call(found, swRestore)
	}
	// Windows only lets the foreground process take focus; a tap on Alt
	// counts as input and lifts that restriction for this call.
	procKeybdEvent.Call(vkMenu, 0, 0, 0)
	procKeybdEvent.Call(vkMenu, 0, keyeventfKeyUp, 0)
	procBringWindowToTop.Call(found)
	procSetForeground.Call(found)
	return true
}

// findWindow returns the visible Edge/Chrome window whose title starts with title.
func findWindow(title string) uintptr {
	var found uintptr
	cb := syscall.NewCallback(func(hwnd, _ uintptr) uintptr {
		if v, _, _ := procIsWindowVisible.Call(hwnd); v == 0 {
			return 1
		}
		class := make([]uint16, 64)
		procGetClassName.Call(hwnd, uintptr(unsafe.Pointer(&class[0])), uintptr(len(class)))
		// Edge and Chrome windows (including --app windows) use this class;
		// it keeps a File Explorer folder called "Recurbate" from matching
		if syscall.UTF16ToString(class) != "Chrome_WidgetWin_1" {
			return 1
		}
		text := make([]uint16, 256)
		n, _, _ := procGetWindowText.Call(hwnd, uintptr(unsafe.Pointer(&text[0])), uintptr(len(text)))
		if n == 0 {
			return 1
		}
		t := syscall.UTF16ToString(text)
		if len(t) >= len(title) && t[:len(title)] == title {
			found = hwnd
			return 0 // stop
		}
		return 1
	})
	procEnumWindows.Call(cb, 0)
	return found
}

// PickFolder shows the Windows "Browse for folder" dialog and returns the
// chosen folder, or "" if the person cancelled. It runs the dialog through
// PowerShell, which every Windows install has, so the program needs no
// extra libraries.
func PickFolder(start string) (string, error) {
	const script = `
[Console]::OutputEncoding = [System.Text.Encoding]::UTF8
Add-Type -AssemblyName System.Windows.Forms
$owner = New-Object System.Windows.Forms.Form
$owner.TopMost = $true
$owner.ShowInTaskbar = $false
$owner.Opacity = 0
$owner.StartPosition = 'CenterScreen'
$owner.Show()
$d = New-Object System.Windows.Forms.FolderBrowserDialog
$d.Description = 'Choose a folder'
$d.ShowNewFolderButton = $true
if ($env:RP_START -and (Test-Path -LiteralPath $env:RP_START)) { $d.SelectedPath = $env:RP_START }
if ($d.ShowDialog($owner) -eq [System.Windows.Forms.DialogResult]::OK) { [Console]::Out.Write($d.SelectedPath) }
$owner.Close()
`
	cmd := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-STA", "-ExecutionPolicy", "Bypass", "-Command", script)
	cmd.Env = append(os.Environ(), "RP_START="+start)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000} // no console window
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

var (
	kernel32               = syscall.NewLazyDLL("kernel32.dll")
	procOpenClipboard      = user32.NewProc("OpenClipboard")
	procCloseClipboard     = user32.NewProc("CloseClipboard")
	procGetClipboardData   = user32.NewProc("GetClipboardData")
	procClipboardAvailable = user32.NewProc("IsClipboardFormatAvailable")
	procGlobalLock         = kernel32.NewProc("GlobalLock")
	procGlobalUnlock       = kernel32.NewProc("GlobalUnlock")
	procGlobalSize         = kernel32.NewProc("GlobalSize")
	procRtlMoveMemory      = kernel32.NewProc("RtlMoveMemory")
)

const cfUnicodeText = 13

// ReadClipboard returns the text on the Windows clipboard. It reads it directly
// from Windows, so unlike the browser's own clipboard read it never asks the
// person for permission.
func ReadClipboard() (string, error) {
	opened := false
	for i := 0; i < 10; i++ { // another program may be holding the clipboard for a moment
		if r, _, _ := procOpenClipboard.Call(0); r != 0 {
			opened = true
			break
		}
		time.Sleep(25 * time.Millisecond)
	}
	if !opened {
		return "", fmt.Errorf("the clipboard is in use by another program, try again")
	}
	defer procCloseClipboard.Call()
	if r, _, _ := procClipboardAvailable.Call(cfUnicodeText); r == 0 {
		return "", fmt.Errorf("there is no text on the clipboard")
	}
	h, _, _ := procGetClipboardData.Call(cfUnicodeText)
	if h == 0 {
		return "", fmt.Errorf("could not read the clipboard")
	}
	size, _, _ := procGlobalSize.Call(h)
	p, _, _ := procGlobalLock.Call(h)
	if p == 0 || size < 2 {
		return "", fmt.Errorf("could not read the clipboard")
	}
	defer procGlobalUnlock.Call(h)
	buf := make([]uint16, size/2)
	procRtlMoveMemory.Call(uintptr(unsafe.Pointer(&buf[0])), p, size/2*2)
	for i, c := range buf { // the text ends at the first zero
		if c == 0 {
			buf = buf[:i]
			break
		}
	}
	return syscall.UTF16ToString(append(buf, 0)), nil
}

var (
	procClipboardSeq   = user32.NewProc("GetClipboardSequenceNumber")
	procRegisterFormat = user32.NewProc("RegisterClipboardFormatW")
	excludeFormatOnce  sync.Once
	excludeFormat      uintptr
)

// ClipboardSeq changes every time something new is copied. It is cheap, so the
// link listener asks it a few times a second and only reads the clipboard
// when the number has changed.
func ClipboardSeq() uint32 {
	n, _, _ := procClipboardSeq.Call()
	return uint32(n)
}

// ClipboardIsPrivate reports whether the program that copied asked for the
// clipboard not to be monitored (password managers do). The listener skips those.
func ClipboardIsPrivate() bool {
	excludeFormatOnce.Do(func() {
		name, _ := syscall.UTF16PtrFromString("ExcludeClipboardContentFromMonitorProcessing")
		excludeFormat, _, _ = procRegisterFormat.Call(uintptr(unsafe.Pointer(name)))
	})
	if excludeFormat == 0 {
		return false
	}
	r, _, _ := procClipboardAvailable.Call(excludeFormat)
	return r != 0
}
