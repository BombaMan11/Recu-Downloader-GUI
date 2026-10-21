// Package launch opens the program's window and the download folders.
package launch

import (
	"os"
	"os/exec"
)

// OpenWindow shows url in an application window (Edge or Chrome in --app mode,
// so there is no tab strip or address bar), falling back to the default
// browser. profileDir keeps that window's settings separate from the user's
// normal browsing profile.
func OpenWindow(url, profileDir string) error { return open(url, profileDir) }

func open(url, profileDir string) error {
	for _, exe := range browserCandidates() {
		if _, err := os.Stat(exe); err != nil {
			continue
		}
		args := []string{
			"--app=" + url,
			"--window-size=1420,800",
			"--no-first-run",
			"--no-default-browser-check",
			"--user-data-dir=" + profileDir,
		}
		cmd := exec.Command(exe, args...)
		if err := cmd.Start(); err == nil {
			go cmd.Wait()
			return nil
		}
	}
	return openDefault(url)
}
