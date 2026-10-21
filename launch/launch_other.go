//go:build !windows

package launch

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
)

func browserCandidates() []string {
	var out []string
	for _, name := range []string{"chromium", "chromium-browser", "google-chrome", "google-chrome-stable", "microsoft-edge", "brave-browser"} {
		if p, err := exec.LookPath(name); err == nil {
			out = append(out, p)
		}
	}
	return out
}

func opener() string {
	if runtime.GOOS == "darwin" {
		return "open"
	}
	return "xdg-open"
}

// OpenFolder shows a folder in the file manager.
func OpenFolder(path string) error {
	return exec.Command(opener(), path).Start()
}

// RevealFile shows the file in the file manager.
func RevealFile(path string) error {
	if runtime.GOOS == "darwin" {
		return exec.Command("open", "-R", path).Start()
	}
	return exec.Command(opener(), filepath.Dir(path)).Start()
}

func openDefault(url string) error {
	return exec.Command(opener(), url).Start()
}

// ShowError prints the problem (there is no message box off Windows).
func ShowError(title, text string) {
	fmt.Fprintf(os.Stderr, "%s: %s\n", title, text)
}

// FocusWindow is only implemented on Windows; the caller opens a window instead.
func FocusWindow(title string) bool { return false }

// PickFolder is only implemented on Windows; type the folder instead.
func PickFolder(start string) (string, error) {
	return "", fmt.Errorf("choosing a folder is only available on Windows, type the path instead")
}

// ReadClipboard is only implemented on Windows; the page falls back to the browser's own read.
func ReadClipboard() (string, error) {
	return "", fmt.Errorf("reading the clipboard is only available on Windows")
}

// ClipboardSeq / ClipboardIsPrivate: the link listener is only implemented on Windows.
func ClipboardSeq() uint32     { return 0 }
func ClipboardIsPrivate() bool { return false }
