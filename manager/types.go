// Package manager owns the download table: the items, their order, the
// Normal / Hybrid / Series scheduler, force start, persistence and settings.
package manager

import (
	"context"
	"time"

	"Recurbate/engine"
)

// Mode decides how queued items are started.
//
//	normal: every active item downloads at the same time (optionally capped)
//	hybrid: one download per server, different servers in parallel
//	series: one download at a time
type Mode string

const (
	ModeNormal Mode = "normal"
	ModeHybrid Mode = "hybrid"
	ModeSeries Mode = "series"
)

func validMode(m string) bool {
	return m == string(ModeNormal) || m == string(ModeHybrid) || m == string(ModeSeries)
}

type Status string

const (
	StatusPaused      Status = "paused"      // stopped by the user, never scheduled
	StatusQueued      Status = "queued"      // wants to download, waiting for its turn
	StatusResolving   Status = "resolving"   // fetching the playlist
	StatusDownloading Status = "downloading" // writing segments
	StatusComplete    Status = "complete"
	StatusError       Status = "error"
)

// Item is one row of the table. All fields are guarded by Manager.mu.
type Item struct {
	ID         int64
	URL        string
	SectionRef []string // ["55:00","1:10:00","1:30:00"], ["55:00","1:10:00"] (length not known yet) or nil for the whole video
	VideoSecs  float64  // length of the whole video in seconds, 0 = not known yet
	Section    [2]float32
	Active     bool
	Status     Status
	Error      string
	Offset     int   // next segment (absolute) to download when resuming
	FileSize   int64 // size of the .ts file at Offset
	FileBase   string
	Server     string
	Percent    float32
	FullSize   int64 // estimated size of the finished file (exact once complete)
	Forced     bool  // force-started: ignores the mode's limits
	M3U8Saved  bool
	M3U8Path   string // the playlist saved for this row
	DonePath   string
	TempPath   string // the unfinished .ts file, wherever it was started
	Category   string // category id, "" = none

	// runtime only
	res          *engine.Resolved
	busy         bool // a goroutine is working on this item (it may still be winding down)
	preResolving bool
	lookingUp    bool      // its length is being looked up
	lenTried     bool      // the video length was already looked up since the program started
	preWait      time.Time // the site refused the server check: do not ask again before this
	preBlocks    int       // how many times in a row
	cancel       context.CancelFunc
	removed      bool
	deleteFiles  bool
	speed        float32
	etaMin       float32
	stage        string
	note         string
	recat        *string // category to move to once the running work has stopped
}

// Notice is a message for the user interface (shown as a toast).
type Notice struct {
	ID    int64  `json:"id"`
	Level string `json:"level"` // info | warn | error
	Text  string `json:"text"`
}
