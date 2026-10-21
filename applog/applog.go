// Package applog writes the program's log to Recurbate.log in a folder that
// can be changed while the program runs.
package applog

import (
	"errors"
	"log"
	"os"
	"path/filepath"
	"sync"

	"Recurbate/appdata"
)

const FileName = "Recurbate.log"

var (
	mu   sync.Mutex
	file *os.File
	path string
)

type writer struct{}

func (writer) Write(p []byte) (int, error) {
	mu.Lock()
	defer mu.Unlock()
	os.Stderr.Write(p) // harmless when there is no console
	if file != nil {
		file.Write(p)
	}
	return len(p), nil
}

func openFile(dir string) (*os.File, string, error) {
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, "", err
	}
	p := filepath.Join(dir, FileName)
	f, err := os.OpenFile(p, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	return f, p, err
}

// Open starts logging into dir. If the folder can not be used the log only
// goes to the console.
func Open(dir string) error {
	mu.Lock()
	defer mu.Unlock()
	f, p, err := openFile(dir)
	if err == nil {
		file, path = f, p
	}
	log.SetOutput(writer{})
	return err
}

// Move continues the log in dir. The existing log file goes along, unless
// the new folder already has one (then both stay as they are).
func Move(dir string) error {
	mu.Lock()
	defer mu.Unlock()
	newPath := filepath.Join(dir, FileName)
	if path != "" && newPath == path {
		return nil
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	old, oldPath := file, path
	if old != nil {
		old.Close()
		file = nil
	}
	if oldPath != "" {
		if _, err := os.Stat(newPath); errors.Is(err, os.ErrNotExist) {
			appdata.MoveFile(oldPath, newPath, 0600) // best effort
		}
	}
	f, p, err := openFile(dir)
	if err != nil {
		// keep logging where we were
		if oldPath != "" {
			if f2, p2, err2 := openFile(filepath.Dir(oldPath)); err2 == nil {
				file, path = f2, p2
			}
		}
		return err
	}
	file, path = f, p
	return nil
}

// Path is the log file currently in use.
func Path() string {
	mu.Lock()
	defer mu.Unlock()
	return path
}
