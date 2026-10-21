package manager

import (
	"encoding/json"
	"os"
	"path/filepath"

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

	VideoSecs float64 `json:"videoSecs,omitempty"`
}

type storedQueue struct {
	Version int          `json:"version"`
	NextID  int64        `json:"nextId"`
	Items   []storedItem `json:"items"`
}

func writeFileAtomic(path string, data []byte, perm os.FileMode) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, perm); err != nil {
		return err
	}
	return os.Rename(tmp, path)
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
	data, err := os.ReadFile(m.settingsPath())
	if err != nil {
		if os.IsNotExist(err) {
			m.settings = m.withLocations(DefaultSettings())
			return m.saveSettingsLocked()
		}
		return err
	}
	s := DefaultSettings()
	if err := json.Unmarshal(data, &s); err != nil {
		return err
	}
	s.normalize()
	m.settings = m.withLocations(s)
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
		})
	}
	data, err := json.MarshalIndent(q, "", "\t")
	if err != nil {
		return err
	}
	return writeFileAtomic(m.queuePath(), data, 0600)
}

func (m *Manager) loadQueue() error {
	data, err := os.ReadFile(m.queuePath())
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	var q storedQueue
	if err := json.Unmarshal(data, &q); err != nil {
		return err
	}
	m.nextID = q.NextID
	for _, s := range q.Items {
		it := &Item{
			ID: s.ID, URL: s.URL, SectionRef: s.Section, Active: s.Active,
			Status: Status(s.Status), Error: s.Error, Offset: s.Offset,
			FileSize: s.FileSize, FileBase: s.FileBase, Server: s.Server,
			Percent: s.Percent, FullSize: s.FullSize, M3U8Saved: s.M3U8, DonePath: s.DonePath,
			TempPath: s.TempPath, M3U8Path: s.M3U8Path, Category: s.Category, VideoSecs: s.VideoSecs,
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
