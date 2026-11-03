package manager

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"Recurbate/appdata"
	"Recurbate/tools"
)

type storedItem struct {
	ID       int64    `json:"id"`
	URL      string   `json:"url"`
	Section  []string `json:"section,omitempty"`
	Active   bool     `json:"active"`
	Status   string   `json:"status"`
	Error    string   `json:"error,omitempty"`
	Offset   int      `json:"offset,omitempty"`
	FileSize int64    `json:"fileSize,omitempty"`
	FileBase string   `json:"fileBase,omitempty"`
	Server   string   `json:"server,omitempty"`
	Percent  float32  `json:"percent,omitempty"`
	FullSize int64    `json:"fullSize,omitempty"`
	M3U8     bool     `json:"m3u8,omitempty"`
	DonePath string   `json:"donePath,omitempty"`
	TempPath string   `json:"tempPath,omitempty"`
	M3U8Path string   `json:"m3u8Path,omitempty"`
	Category string   `json:"category,omitempty"`
	Prime    bool     `json:"prime,omitempty"`

	VideoSecs float64 `json:"videoSecs,omitempty"`
}

type storedQueue struct {
	Version int          `json:"version"`
	NextID  int64        `json:"nextId"`
	Items   []storedItem `json:"items"`
}

// backupEvery is how often the copy kept beside queue.json and settings.json
// (<name>.bak) is refreshed. It is only ever made from a file that was read
// back and is valid, so a damaged file can not replace a good copy.
const backupEvery = 30 * time.Second

// writeFileAtomic saves path crash-safely (see appdata.WriteFileAtomic) and
// keeps <path>.bak, a copy of the previous good version at most backupEvery old,
// for loadJSONFile to fall back on.
func writeFileAtomic(path string, data []byte, perm os.FileMode) error {
	refreshBackup(path, perm)
	return appdata.WriteFileAtomic(path, data, perm)
}

// refreshBackup copies the current path to path+".bak" when that copy is
// missing or older than backupEvery and path holds valid JSON. Best effort.
func refreshBackup(path string, perm os.FileMode) {
	bak := path + ".bak"
	if st, err := os.Stat(bak); err == nil && time.Since(st.ModTime()) < backupEvery {
		if cur, err := os.ReadFile(bak); err == nil && json.Valid(cur) {
			return // fresh enough
		}
	}
	cur, err := os.ReadFile(path)
	if err != nil || len(bytes.TrimSpace(cur)) == 0 || !json.Valid(cur) {
		return
	}
	appdata.WriteFileAtomic(bak, cur, perm)
}

// loadJSONFile reads path and hands its text to decode, which must only keep
// the result when it returns nil. found is false when there is nothing to use:
// the file does not exist, or it is empty or damaged and there is no usable
// backup either.
//
// An empty or damaged file (what a crash or power cut can leave behind) does
// not stop the program: it is moved aside as <name>.damaged-<time> (an empty
// one is just removed), <name>.bak is used when it is valid and put back as the
// file, and what happened is returned as problem so it can be shown.
func loadJSONFile(path string, perm os.FileMode, decode func([]byte) error) (found bool, problem string, err error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, "", nil
		}
		return false, "", err
	}
	if len(bytes.TrimSpace(data)) > 0 {
		if derr := decode(data); derr == nil {
			return true, "", nil
		}
	}
	name := filepath.Base(path)
	why := "is damaged"
	if len(bytes.TrimSpace(data)) == 0 {
		why = "is empty"
	}
	kept := ""
	if len(data) > 0 {
		aside := path + ".damaged-" + time.Now().Format("20060102-150405")
		if os.Rename(path, aside) == nil {
			kept = " The damaged file was kept as " + filepath.Base(aside) + "."
		}
	} else {
		os.Remove(path)
	}
	bak := path + ".bak"
	if b, berr := os.ReadFile(bak); berr == nil && len(bytes.TrimSpace(b)) > 0 {
		if derr := decode(b); derr == nil {
			appdata.WriteFileAtomic(path, b, perm) // best effort: the next save writes it anyway
			when := ""
			if st, serr := os.Stat(bak); serr == nil {
				when = " (saved " + st.ModTime().Format("2 Jan 15:04") + ")"
			}
			return true, fmt.Sprintf("%s %s, which can happen when the computer crashes or loses power. It was restored from %s.bak%s; anything changed after that is lost.%s", name, why, name, when, kept), nil
		}
	}
	return false, fmt.Sprintf("%s %s, which can happen when the computer crashes or loses power, and there is no usable backup. Recurbate started with an empty %s.%s", name, why, map[string]string{"queue.json": "list", "settings.json": "set of settings"}[name], kept), nil
}

func (m *Manager) settingsPath() string {
	return filepath.Join(m.loc.DataDir(m.baseDir), "settings.json")
}
func (m *Manager) queuePath() string {
	return filepath.Join(m.loc.DataDir(m.baseDir), "queue.json")
}

func (m *Manager) saveSettingsLocked() error {
	stored := m.settings
	// the data folder lives in the pointer file, not in settings.json
	stored.DataDir = ""
	data, err := json.MarshalIndent(stored, "", "\t")
	if err != nil {
		return err
	}
	// holds the cookie, keep it private
	return writeFileAtomic(m.settingsPath(), data, 0600)
}

func (m *Manager) withLocations(s Settings) Settings {
	s.DataDir = m.loc.Data
	return s
}

func (m *Manager) loadSettings() error {
	var loaded Settings
	found, problem, err := loadJSONFile(m.settingsPath(), 0600, func(data []byte) error {
		s := DefaultSettings()
		if err := json.Unmarshal(data, &s); err != nil {
			return err
		}
		loaded = s
		return nil
	})
	if err != nil {
		return err
	}
	if problem != "" {
		m.recoveryLocked(problem)
	}
	if !found {
		m.settings = m.withLocations(DefaultSettings())
		return m.saveSettingsLocked()
	}
	loaded.normalize()
	m.settings = m.withLocations(loaded)
	return nil
}

func (m *Manager) saveQueueLocked() error {
	q := storedQueue{Version: 1, NextID: m.nextID}
	for _, it := range m.items {
		q.Items = append(q.Items, storedItem{
			ID: it.ID, URL: it.URL, Section: it.SectionRef, Active: it.Active,
			Status: string(it.Status), Error: it.Error, Offset: it.Offset,
			FileSize: it.FileSize, FileBase: it.FileBase, Server: it.Server,
			Percent: it.Percent, FullSize: it.FullSize, M3U8: it.M3U8Saved, DonePath: it.DonePath,
			TempPath: it.TempPath, M3U8Path: it.M3U8Path, Category: it.Category, VideoSecs: it.VideoSecs,
			Prime: it.Prime,
		})
	}
	data, err := json.MarshalIndent(q, "", "\t")
	if err != nil {
		return err
	}
	return writeFileAtomic(m.queuePath(), data, 0600)
}

func (m *Manager) loadQueue() error {
	var q storedQueue
	found, problem, err := loadJSONFile(m.queuePath(), 0600, func(data []byte) error {
		var tmp storedQueue
		if err := json.Unmarshal(data, &tmp); err != nil {
			return err
		}
		q = tmp
		return nil
	})
	if err != nil {
		return err
	}
	if problem != "" {
		m.recoveryLocked(problem)
	}
	if !found {
		return nil
	}
	m.nextID = q.NextID
	for _, s := range q.Items {
		it := &Item{
			ID: s.ID, URL: s.URL, SectionRef: s.Section, Active: s.Active,
			Status: Status(s.Status), Error: s.Error, Offset: s.Offset,
			FileSize: s.FileSize, FileBase: s.FileBase, Server: s.Server,
			Percent: s.Percent, FullSize: s.FullSize, M3U8Saved: s.M3U8, DonePath: s.DonePath,
			TempPath: s.TempPath, M3U8Path: s.M3U8Path, Category: s.Category, VideoSecs: s.VideoSecs, Prime: s.Prime,
			Section: [2]float32{0, 100},
		}
		switch len(s.Section) {
		case 3:
			if sec, err := tools.PercentParse(s.Section); err == nil {
				it.Section = sec
				if total, terr := tools.ParseClock(s.Section[2]); terr == nil && it.VideoSecs <= 0 {
					it.VideoSecs = float64(total)
				}
			} else {
				it.SectionRef = nil
			}
		case 2: // start and end, the video length is still to be found
			if _, _, err := tools.ClockRange(s.Section[0], s.Section[1]); err != nil {
				it.SectionRef = nil
			}
		default:
			it.SectionRef = nil
		}
		switch it.Status {
		case StatusComplete, StatusError, StatusPaused:
		default:
			// was running or waiting when the program closed
			if m.settings.AutoResume {
				it.Status = StatusQueued
			} else {
				it.Status = StatusPaused
			}
		}
		if m.settings.category(it.Category) == nil {
			it.Category = "" // the category was removed
		}
		if it.Status != StatusComplete {
			// the site may use another server this time; it is looked up again
			it.Server = ""
			if it.TempPath == "" && it.FileBase != "" {
				// saved by an earlier version: the partial file is in the unfinished folder
				it.TempPath = filepath.Join(m.incDirLocked(it), it.FileBase+".ts")
			}
		}
		if it.ID >= m.nextID {
			m.nextID = it.ID + 1
		}
		m.items = append(m.items, it)
	}
	return nil
}
