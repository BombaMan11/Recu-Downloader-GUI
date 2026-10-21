package manager

import (
	"strconv"
	"time"

	"Recurbate/tools"
)

// ItemView is one table row as the user interface sees it.
type ItemView struct {
	ID      int64   `json:"id"`
	URL     string  `json:"url"`
	Name    string  `json:"name"`
	Status  string  `json:"status"`
	Hint    string  `json:"hint"` // "Next", "#3", "Inactive", ...
	Detail  string  `json:"detail"`
	Active  bool    `json:"active"`
	Forced  bool    `json:"forced"`
	Next    bool    `json:"next"`
	Percent float64 `json:"percent"`
	Speed   float64 `json:"speed"` // bytes / second
	EtaSec  float64 `json:"etaSec"`
	Bytes   int64   `json:"bytes"`
	// FullSize is the size of the finished file: estimated while the download
	// is unfinished (0 = not known yet), exact once it is complete.
	FullSize int64  `json:"fullSize"`
	Server   string `json:"server"`
	// StartSec, EndSec and DurSec are the part of the video being downloaded,
	// in seconds (-1 = not known yet).
	StartSec float64 `json:"startSec"`
	EndSec   float64 `json:"endSec"`
	DurSec   float64 `json:"durSec"`
	// Category is the category id ("" = none), CategoryName what the table shows.
	Category     string `json:"category"`
	CategoryName string `json:"categoryName"`
	Error        string `json:"error"`
	M3U8         bool   `json:"m3u8"`
	Done         string `json:"donePath"`
}

// CategoryView is a category as the add box and the menus list it.
type CategoryView struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type Totals struct {
	Speed       float64 `json:"speed"`
	Total       int     `json:"total"`
	Downloading int     `json:"downloading"`
	Queued      int     `json:"queued"`
	Paused      int     `json:"paused"`
	Complete    int     `json:"complete"`
	Error       int     `json:"error"`
}

type Snapshot struct {
	Version     uint64         `json:"version"`
	Mode        string         `json:"mode"`
	MaxParallel int            `json:"maxParallel"`
	NeedCreds   bool           `json:"needCreds"`
	Theme       string         `json:"theme"`
	Items       []ItemView     `json:"items"`
	Categories  []CategoryView `json:"categories"`
	Columns     map[string]int `json:"columns,omitempty"` // table column widths, nil = default
	Listener    ListenerView   `json:"listener"`
	Totals      Totals         `json:"totals"`
	Notices     []Notice       `json:"notices"`
	App         string         `json:"app"`
}

// Version changes whenever anything visible changes.
func (m *Manager) Version() uint64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.version
}

func (m *Manager) Snapshot() Snapshot {
	m.mu.Lock()
	defer m.mu.Unlock()
	snap := Snapshot{
		Version:     m.version,
		Mode:        m.settings.Mode,
		MaxParallel: m.settings.MaxParallel,
		NeedCreds:   !m.settings.credsOK(),
		Theme:       m.settings.Theme,
		Items:       make([]ItemView, 0, len(m.items)),
		Categories:  make([]CategoryView, 0, len(m.settings.Categories)),
		Notices:     append([]Notice(nil), m.notices...),
		App:         m.tag,
		Columns:     m.settings.ColumnWidths,
		Listener:    ListenerView{Enabled: m.settings.Listen.Enabled, Added: m.listenAdded, Error: m.listenErr, Links: append([]ListenedLink(nil), m.listened...)},
	}
	for _, c := range m.settings.Categories {
		snap.Categories = append(snap.Categories, CategoryView{ID: c.ID, Name: c.Name})
	}
	hints := m.hintsLocked()
	for _, it := range m.items {
		v := ItemView{
			ID: it.ID, URL: it.URL, Name: displayName(it), Status: string(it.Status),
			Hint: hints[it.ID], Active: it.Active, Forced: it.Forced,
			Percent: float64(it.Percent), Bytes: it.FileSize, FullSize: it.FullSize, Server: it.Server,
			Error: it.Error, M3U8: it.M3U8Saved, Done: it.DonePath,
		}
		if it.FileBase == "" && it.res == nil && it.Status != StatusComplete {
			v.Name = it.URL
		}
		v.Next = v.Hint == "Next"
		if c := m.settings.category(it.Category); c != nil {
			v.Category, v.CategoryName = c.ID, c.Name
		}
		v.StartSec, v.EndSec, v.DurSec = sectionSeconds(it)
		switch it.Status {
		case StatusResolving:
			v.Detail = it.stage
		case StatusDownloading:
			v.Speed = float64(it.speed)
			v.EtaSec = float64(it.etaMin) * 60
			v.Detail = it.note
		case StatusQueued:
			if it.preResolving {
				v.Detail = "Checking server"
			} else if time.Now().Before(it.preWait) {
				v.Detail = "Site refused, will try again"
			}
		case StatusComplete:
			v.Percent = 100
			if it.FileSize > 0 {
				v.FullSize = it.FileSize
			}
		}
		snap.Totals.Total++
		switch it.Status {
		case StatusDownloading, StatusResolving:
			snap.Totals.Downloading++
			snap.Totals.Speed += v.Speed
		case StatusQueued:
			snap.Totals.Queued++
		case StatusPaused:
			snap.Totals.Paused++
		case StatusComplete:
			snap.Totals.Complete++
		case StatusError:
			snap.Totals.Error++
		}
		snap.Items = append(snap.Items, v)
	}
	return snap
}

// hintsLocked tells each waiting row where it stands in the line: "Next" for
// the one that starts as soon as a slot frees up, "#n" for the ones behind it
// (per server in Hybrid, overall otherwise).
func (m *Manager) hintsLocked() map[int64]string {
	hints := map[int64]string{}
	mode := Mode(m.settings.Mode)
	overall := 0
	perServer := map[string]int{}
	for _, it := range m.items {
		if it.Status != StatusQueued || it.Forced {
			continue
		}
		if !it.Active {
			hints[it.ID] = "Inactive"
			continue
		}
		if mode == ModeNormal && m.settings.MaxParallel == 0 {
			continue // everything starts at once, nothing waits
		}
		var idx int
		if mode == ModeHybrid {
			if it.Server == "" {
				hints[it.ID] = "Waiting"
				continue
			}
			perServer[it.Server]++
			idx = perServer[it.Server]
		} else {
			overall++
			idx = overall
		}
		if idx == 1 {
			hints[it.ID] = "Next"
		} else {
			hints[it.ID] = "#" + strconv.Itoa(idx)
		}
	}
	return hints
}

// sectionSeconds is the start, end and length of the part of the video a row
// downloads. A whole video starts at 0 and ends where the video does, which is
// known once its playlist has been read (-1 until then).
func sectionSeconds(it *Item) (start, end, dur float64) {
	if len(it.SectionRef) >= 2 {
		if s, e, err := tools.ClockRange(it.SectionRef[0], it.SectionRef[1]); err == nil {
			return float64(s), float64(e), float64(e - s)
		}
	}
	if it.VideoSecs > 0 {
		return 0, it.VideoSecs, it.VideoSecs
	}
	return 0, -1, -1
}
