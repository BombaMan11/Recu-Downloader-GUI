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
	UserAgent          string  `json:"userAgent"`
	Cookie             string  `json:"cookie"`
	Theme              string  `json:"theme"` // system | light | dark
	Mode               string  `json:"mode"`
	DoneDir            string  `json:"doneDir"`
	IncompleteDir      string  `json:"incompleteDir"`
	M3U8Dir            string  `json:"m3u8Dir"`
	MaxResolution      int     `json:"maxResolution"`      // video height, 0 = no limit
	MaxParallel        int     `json:"maxParallel"`        // Normal mode, 0 = unlimited
	AutoStart          bool    `json:"autoStart"`          // start new rows as soon as they are added
	AutoResume         bool    `json:"autoResume"`         // resume unfinished rows on launch
	FirstPieces        bool    `json:"firstPieces"`        // a new row first downloads its first pieces, then pauses or starts as usual
	UniqueServer       bool    `json:"uniqueServer"`       // experimental: a row may not use the server of the row above it
	ServerRetries      int     `json:"serverRetries"`      // Unique Server Find: how often a row asks for another server
	ServerWait         float64 `json:"serverWait"`         // seconds between those tries
	DeepSearch         bool    `json:"deepSearch"`         // also avoid the servers of the rows further up (needs UniqueServer)
	DeepPositions      int     `json:"deepPositions"`      // how many rows above Deep Search compares
	LongLasting        bool    `json:"longLasting"`        // experimental: rows are read when started, not when their turn comes
	SkipRefused        bool    `json:"skipRefused"`        // experimental: a row the site refuses is skipped at once, not retried later
	FFmpegDir          string  `json:"ffmpegDir"`          // folder with ffmpeg(.exe); blank = the one in PATH
	FolderRedirect     string  `json:"folderRedirect"`     // category whose Finished folder the main menu folder button opens; "" = the normal one
	ForceNameSort      string  `json:"forceNameSort"`      // "az" / "za": the queue is kept in name order (right-click the Name heading); "" = off
	ConvertMP4         bool    `json:"convertMp4"`         // experimental: finished .ts videos are converted to .mp4 with ffmpeg
	DeleteAfterConvert bool    `json:"deleteAfterConvert"` // with ConvertMP4: the .ts is deleted once the .mp4 is made
	SilentReplace      bool    `json:"silentReplace"`      // offers Silent Replace: a new cookie without pausing the list
	BottomFirst        bool    `json:"addBottomFirst"`     // links added together go in bottom to top: the bottom link becomes the first row of the batch

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
		Theme:         "system",
		FirstPieces:   true,
		LongLasting:   true,
		ServerRetries: 10, ServerWait: 1, DeepPositions: 2,
		Mode: string(ModeHybrid),
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
	s.FFmpegDir = strings.TrimSpace(s.FFmpegDir)
	if s.ForceNameSort != "az" && s.ForceNameSort != "za" {
		s.ForceNameSort = ""
	}
	s.normalizeCategories()
	s.Listen.normalize()
	if s.MaxResolution < 0 {
		s.MaxResolution = 0
	}
	if s.MaxParallel < 0 {
		s.MaxParallel = 0
	}
	s.ServerRetries = clampInt(s.ServerRetries, 1, 100)
	if s.ServerWait < 0.1 || s.ServerWait != s.ServerWait {
		s.ServerWait = 0.1
	}
	if s.ServerWait > 60 {
		s.ServerWait = 60
	}
	s.DeepPositions = clampInt(s.DeepPositions, 2, 20)
}

func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
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
