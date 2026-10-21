// Package appdata knows where the program keeps its own files.
//
// settings.json, queue.json, Recurbate.log and the data of the program's
// window (a browser profile) all live in one "data folder", which can be any
// folder. Because settings.json can not say where settings.json is, the data
// folder is kept in a small file next to the program (Recurbate.paths.json).
// A blank data folder means "next to the program"; a first start uses the
// folder named DefaultData next to the program instead (see EnsureDefault).
package appdata

import (
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// FileName is the small pointer file kept next to the program.
const FileName = "Recurbate.paths.json"

// ProfileName is the folder (inside the data folder) holding the window's
// browser profile.
const ProfileName = "browser-data"

// DefaultData is the data folder of a fresh install, next to the program.
const DefaultData = "Recu-Settings"

// EnsureDefault makes a fresh install keep its settings, queue and log in the
// DefaultData folder. It writes the pointer file only when there is none and
// no settings.json or queue.json already sits next to the program, so someone
// who already has files there (or who chose "next to the program" in Settings,
// which moves them there) keeps working with them.
func EnsureDefault(base string) error {
	if _, err := os.Stat(filepath.Join(base, FileName)); err == nil || !errors.Is(err, fs.ErrNotExist) {
		return nil // there is a pointer file already (or it can not be checked)
	}
	for _, f := range []string{"settings.json", "queue.json"} {
		if _, err := os.Stat(filepath.Join(base, f)); err == nil {
			return nil
		}
	}
	if err := os.MkdirAll(base, 0755); err != nil {
		return err
	}
	return Save(base, Locations{Data: DefaultData})
}

// Locations is what the pointer file holds.
type Locations struct {
	// Data is the data folder as typed by the user. Blank = next to the
	// program; a name without a drive or full path is relative to the
	// program's folder.
	Data string `json:"data,omitempty"`
	// MoveProfileFrom: the data folder was changed while the window was open,
	// so its browser profile (which is in use) is still in this old data
	// folder and is moved at the next start.
	MoveProfileFrom string `json:"moveProfileFrom,omitempty"`
}

// Load reads the pointer file. A missing or unreadable file means defaults.
func Load(base string) Locations {
	var l Locations
	data, err := os.ReadFile(filepath.Join(base, FileName))
	if err != nil || json.Unmarshal(data, &l) != nil {
		return Locations{}
	}
	l.Data = strings.TrimSpace(l.Data)
	l.MoveProfileFrom = strings.TrimSpace(l.MoveProfileFrom)
	return l
}

// Save writes the pointer file, or removes it when there is nothing to remember.
func Save(base string, l Locations) error {
	l.Data = strings.TrimSpace(l.Data)
	path := filepath.Join(base, FileName)
	if l == (Locations{}) {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return nil
	}
	data, err := json.MarshalIndent(l, "", "\t")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// DataDir is the full path of the data folder.
func (l Locations) DataDir(base string) string { return Resolve(base, l.Data) }

// ProfileDir is where the window's browser profile belongs.
func (l Locations) ProfileDir(base string) string {
	return filepath.Join(l.DataDir(base), ProfileName)
}

// ActiveProfileDir is where the window's browser profile is right now: the
// old place while a move is still pending, otherwise ProfileDir.
func (l Locations) ActiveProfileDir(base string) string {
	if l.MoveProfileFrom != "" {
		return filepath.Join(l.MoveProfileFrom, ProfileName)
	}
	return l.ProfileDir(base)
}

// Resolve turns a folder setting into a full path.
func Resolve(base, name string) string {
	name = strings.TrimSpace(name)
	switch {
	case name == "":
		return base
	case filepath.IsAbs(name):
		return filepath.Clean(name)
	default:
		return filepath.Join(base, name)
	}
}

// Same reports whether two folder settings point at the same place.
func Same(base, a, b string) bool {
	pa, pb := Resolve(base, a), Resolve(base, b)
	if strings.EqualFold(pa, pb) && filepath.Separator == '\\' || pa == pb {
		return true
	}
	ia, err1 := os.Stat(pa)
	ib, err2 := os.Stat(pb)
	return err1 == nil && err2 == nil && os.SameFile(ia, ib)
}

// CheckWritable creates the folder if needed and proves files can be written there.
func CheckWritable(dir string) error {
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".rp-write-test-*")
	if err != nil {
		return err
	}
	name := f.Name()
	f.Close()
	return os.Remove(name)
}

// MoveFile moves a file, copying when the folders are on different drives.
func MoveFile(src, dst string, perm os.FileMode) error {
	if err := os.Rename(src, dst); err == nil {
		return nil
	}
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	if err := os.WriteFile(dst, data, perm); err != nil {
		return err
	}
	return os.Remove(src)
}

// MoveDir moves a folder, copying it when the folders are on different drives.
func MoveDir(src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
		return err
	}
	if err := os.Rename(src, dst); err == nil {
		return nil
	}
	err := filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0755)
		}
		return copyFile(p, target)
	})
	if err != nil {
		os.RemoveAll(dst) // do not leave half a profile
		return err
	}
	return os.RemoveAll(src)
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// FinishProfileMove carries out a browser-profile move left over from a
// change of the data folder. Call it at start, before the window opens. A
// profile that can not be moved yet (still in use) stays pending.
func FinishProfileMove(base string) error {
	l := Load(base)
	if l.MoveProfileFrom == "" {
		return nil
	}
	from := filepath.Join(l.MoveProfileFrom, ProfileName)
	to := l.ProfileDir(base)
	if Same(base, from, to) {
		l.MoveProfileFrom = ""
		return Save(base, l)
	}
	if _, err := os.Stat(from); err == nil {
		if _, err := os.Stat(to); err == nil {
			// the new place already has a profile: keep it, leave the old one alone
		} else if err := MoveDir(from, to); err != nil {
			return err
		}
	}
	l.MoveProfileFrom = ""
	return Save(base, l)
}
