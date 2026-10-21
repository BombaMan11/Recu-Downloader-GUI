package manager

import (
	"strconv"
	"strings"
)

// A blank folder setting means the folder the program is in (no subfolders).
const defaultFolder = "."

// Folder names an older version used as its defaults; a setting that still
// holds one is treated as blank.
const (
	legacyDone       = "Done"
	legacyIncomplete = "Incomplete"
	legacyM3U8       = "M3U8"
)

// Category is a named set of folders, like a qBittorrent category. A blank
// folder uses the normal folder from Settings.
type Category struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	DoneDir       string `json:"doneDir"`
	IncompleteDir string `json:"incompleteDir"`
	M3U8Dir       string `json:"m3u8Dir"`
}

const (
	maxCategories   = 100
	maxCategoryName = 40
)

// Settings are stored in settings.json.
//
// DataDir (where settings.json, queue.json, the log and the window's browser
// data live) is not stored in settings.json, which could not say where it is
// itself; it lives in the pointer file described in package appdata and is
// only carried here so the settings page can edit it.
type Settings struct {
	UserAgent     string `json:"userAgent"`
	Cookie        string `json:"cookie"`
	Theme         string `json:"theme"` // system | light | dark
	Mode          string `json:"mode"`
	DoneDir       string `json:"doneDir"`
	IncompleteDir string `json:"incompleteDir"`
	M3U8Dir       string `json:"m3u8Dir"`
	MaxResolution int    `json:"maxResolution"` // video height, 0 = no limit
	MaxParallel   int    `json:"maxParallel"`   // Normal mode, 0 = unlimited
	AutoStart     bool   `json:"autoStart"`     // start new rows as soon as they are added
	AutoResume    bool   `json:"autoResume"`    // resume unfinished rows on launch

	DataDir string `json:"dataDir,omitempty"`

	Categories []Category `json:"categories"`

	// Listen is the clipboard link listener (see listener.go).
	Listen ListenSettings `json:"listen"`

	// ColumnWidths are the table's column widths in pixels, by column key
	// (empty = the default layout). Changed by dragging the column borders.
	ColumnWidths map[string]int `json:"columnWidths,omitempty"`
}

func (s Settings) doneName() string       { return orDefault(s.DoneDir, defaultFolder) }
func (s Settings) incompleteName() string { return orDefault(s.IncompleteDir, defaultFolder) }
func (s Settings) m3u8Name() string       { return orDefault(s.M3U8Dir, defaultFolder) }

func orDefault(v, def string) string {
	if v == "" {
		return def
	}
	return v
}

func DefaultSettings() Settings {
	return Settings{
		Theme: "system",
		Mode:  string(ModeHybrid),
		// AutoStart and AutoResume are off: nothing starts until the person says so.
		Listen: ListenSettings{
			Enabled:        true,
			Patterns:       append([]string(nil), DefaultListenPatterns...),
			SkipDuplicates: true,
			AllowPrivate:   true,
			NoArchive:      true, // no text file and no backups until they are ticked
			NoBackups:      true,
		},
	}
}

var headerCleaner = strings.NewReplacer("\r", "", "\n", "")

// ColumnKeys are the table's columns, left to right.
var ColumnKeys = []string{"act", "num", "name", "status", "progress", "speed", "eta", "size", "full", "server", "category", "start", "end", "duration"}

const (
	minColumnWidth = 30
	maxColumnWidth = 2000
)

// cleanColumnWidths keeps only known columns with sane widths; nil when the
// result is not a complete layout (a half-filled layout would look broken).
func cleanColumnWidths(in map[string]int) map[string]int {
	out := make(map[string]int, len(ColumnKeys))
	for _, k := range ColumnKeys {
		w, ok := in[k]
		if !ok {
			return nil
		}
		if w < minColumnWidth {
			w = minColumnWidth
		}
		if w > maxColumnWidth {
			w = maxColumnWidth
		}
		out[k] = w
	}
	return out
}

func (s *Settings) normalize() {
	s.ColumnWidths = cleanColumnWidths(s.ColumnWidths)
	s.UserAgent = strings.TrimSpace(headerCleaner.Replace(s.UserAgent))
	s.Cookie = strings.TrimSpace(headerCleaner.Replace(s.Cookie))
	switch s.Theme {
	case "light", "dark", "system":
	default:
		s.Theme = "system"
	}
	if !validMode(s.Mode) {
		s.Mode = string(ModeHybrid)
	}
	s.DoneDir = strings.TrimSpace(s.DoneDir)
	s.IncompleteDir = strings.TrimSpace(s.IncompleteDir)
	s.M3U8Dir = strings.TrimSpace(s.M3U8Dir)
	// blank means the default folder next to the program; an older version
	// stored the default names, which are the same thing
	if s.DoneDir == legacyDone {
		s.DoneDir = ""
	}
	if s.IncompleteDir == legacyIncomplete {
		s.IncompleteDir = ""
	}
	if s.M3U8Dir == legacyM3U8 {
		s.M3U8Dir = ""
	}
	s.DataDir = strings.TrimSpace(s.DataDir)
	s.normalizeCategories()
	s.Listen.normalize()
	if s.MaxResolution < 0 {
		s.MaxResolution = 0
	}
	if s.MaxParallel < 0 {
		s.MaxParallel = 0
	}
}

// credsOK: the original refused to run without a cookie and a user agent.
func (s Settings) credsOK() bool {
	return s.Cookie != "" && s.UserAgent != ""
}

// headers builds the request header map (the "header" object of the old config.json).
func (s Settings) headers() map[string]string {
	h := map[string]string{}
	h["Cookie"] = s.Cookie
	h["User-Agent"] = s.UserAgent
	return h
}

// normalizeCategories trims names and folders, drops empty and repeated names
// and gives every category an id that stays the same when it is renamed.
func (s *Settings) normalizeCategories() {
	seenName := map[string]bool{}
	seenID := map[string]bool{}
	var out []Category
	for _, c := range s.Categories {
		c.Name = strings.TrimSpace(headerCleaner.Replace(c.Name))
		if r := []rune(c.Name); len(r) > maxCategoryName {
			c.Name = string(r[:maxCategoryName])
		}
		key := strings.ToLower(c.Name)
		if c.Name == "" || seenName[key] || len(out) >= maxCategories {
			continue
		}
		seenName[key] = true
		c.ID = strings.TrimSpace(c.ID)
		if c.ID == "" || seenID[c.ID] {
			c.ID = ""
		} else {
			seenID[c.ID] = true
		}
		c.DoneDir = strings.TrimSpace(c.DoneDir)
		c.IncompleteDir = strings.TrimSpace(c.IncompleteDir)
		c.M3U8Dir = strings.TrimSpace(c.M3U8Dir)
		out = append(out, c)
	}
	next := 1
	for i := range out {
		if out[i].ID != "" {
			continue
		}
		for {
			id := "c" + strconv.Itoa(next)
			next++
			if !seenID[id] {
				out[i].ID = id
				seenID[id] = true
				break
			}
		}
	}
	s.Categories = out
}

func (s Settings) category(id string) *Category {
	if id == "" {
		return nil
	}
	for i := range s.Categories {
		if s.Categories[i].ID == id {
			return &s.Categories[i]
		}
	}
	return nil
}
