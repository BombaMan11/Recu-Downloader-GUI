package manager

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"Recurbate/appdata"
	"Recurbate/applog"
	"Recurbate/engine"
	"Recurbate/tools"
)

// Manager is the download table and everything that acts on it.
type Manager struct {
	mu       sync.Mutex
	baseDir  string
	loc      appdata.Locations // where settings.json, queue.json and the log live
	settings Settings
	items    []*Item
	nextID   int64
	notices  []Notice
	noticeID int64
	tag      string

	rootCtx    context.Context
	rootCancel context.CancelFunc
	wg         sync.WaitGroup
	kickCh     chan struct{}
	version    uint64
	dirty      bool
	resolveSem chan struct{}
	shutdown   bool

	clip        Clipboard // what the link listener watches
	listenEvery time.Duration
	listenAdded int    // links the listener added since the program started
	listenErr   string // the listener's last problem, "" = none
	listened    []ListenedLink
	listenSeq   int64
}

// New loads settings.json and queue.json (creating default settings on the
// first run) and makes sure the Done / Incomplete / M3U8 folders exist.
// baseDir is the program's folder: the default home of every file, and where
// the pointer file with custom locations is kept (see package appdata).
func New(baseDir, tag string) (*Manager, error) {
	m := &Manager{
		baseDir:     baseDir,
		loc:         appdata.Load(baseDir),
		tag:         tag,
		kickCh:      make(chan struct{}, 1),
		resolveSem:  make(chan struct{}, 2),
		version:     1,
		listenEvery: listenEvery,
	}
	m.rootCtx, m.rootCancel = context.WithCancel(context.Background())
	if err := os.MkdirAll(baseDir, 0755); err != nil {
		return nil, err
	}
	if dir := m.loc.DataDir(baseDir); os.MkdirAll(dir, 0755) != nil {
		return nil, fmt.Errorf("can not use the folder %s\n\nTo go back to the default, delete %s",
			dir, filepath.Join(baseDir, appdata.FileName))
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.loadSettings(); err != nil {
		return nil, fmt.Errorf("settings.json: %v", err)
	}
	if err := m.loadQueue(); err != nil {
		return nil, fmt.Errorf("queue.json: %v", err)
	}
	if err := m.ensureDirsLocked(); err != nil {
		m.notifyLocked("error", err.Error())
	}
	return m, nil
}

// Run launches the scheduler and the background saver.
func (m *Manager) Run() {
	m.wg.Add(3)
	go m.schedulerLoop()
	go m.saverLoop()
	go m.listenLoop()
	m.kick()
}

// Shutdown stops every download (their progress is kept so they resume on
// the next launch), waits for them to wind down and saves the queue.
func (m *Manager) Shutdown() {
	m.mu.Lock()
	if m.shutdown {
		m.mu.Unlock()
		return
	}
	m.shutdown = true
	for _, it := range m.items {
		if it.Status == StatusResolving || it.Status == StatusDownloading {
			it.Status = StatusQueued
		}
		it.Forced = false
	}
	m.mu.Unlock()
	m.rootCancel()
	m.wg.Wait()
	m.mu.Lock()
	defer m.mu.Unlock()
	m.saveQueueLocked()
}

// ---- small helpers (callers hold mu where the name ends in Locked) ----

func (m *Manager) changedLocked(persist bool) {
	m.version++
	if persist {
		m.dirty = true
	}
}

func (m *Manager) kick() {
	select {
	case m.kickCh <- struct{}{}:
	default:
	}
}

func (m *Manager) notifyLocked(level, text string) {
	m.noticeID++
	m.notices = append(m.notices, Notice{ID: m.noticeID, Level: level, Text: text})
	if len(m.notices) > 30 {
		m.notices = m.notices[len(m.notices)-30:]
	}
	m.changedLocked(false)
}

func (m *Manager) notify(level, text string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.notifyLocked(level, text)
}

func (m *Manager) dirLocked(name string) string {
	if filepath.IsAbs(name) {
		return name
	}
	return filepath.Join(m.baseDir, name)
}

func (m *Manager) ensureDirsLocked() error {
	names := []string{m.settings.doneName(), m.settings.incompleteName(), m.settings.m3u8Name()}
	for _, c := range m.settings.Categories {
		for _, n := range []string{c.DoneDir, c.IncompleteDir, c.M3U8Dir} {
			if n != "" {
				names = append(names, n)
			}
		}
	}
	for _, name := range names {
		if err := os.MkdirAll(m.dirLocked(name), 0755); err != nil {
			return fmt.Errorf("can not create folder %s: %v", m.dirLocked(name), err)
		}
	}
	return nil
}

// A row's folders: its category's folder when that has one, otherwise the
// normal folder from Settings.
func (m *Manager) folderLocked(it *Item, pick func(*Category) string, normal string) string {
	if c := m.settings.category(it.Category); c != nil {
		if v := pick(c); v != "" {
			return m.dirLocked(v)
		}
	}
	return m.dirLocked(normal)
}

func (m *Manager) incDirLocked(it *Item) string {
	return m.folderLocked(it, func(c *Category) string { return c.IncompleteDir }, m.settings.incompleteName())
}
func (m *Manager) doneDirLocked(it *Item) string {
	return m.folderLocked(it, func(c *Category) string { return c.DoneDir }, m.settings.doneName())
}
func (m *Manager) m3u8DirLocked(it *Item) string {
	return m.folderLocked(it, func(c *Category) string { return c.M3U8Dir }, m.settings.m3u8Name())
}

// DirFor is a folder as it applies to one row (its category's, or the normal one).
func (m *Manager) DirFor(id int64, which string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, it := range m.items {
		if it.ID != id {
			continue
		}
		var dir string
		switch which {
		case "done":
			dir = m.doneDirLocked(it)
		case "incomplete":
			dir = m.incDirLocked(it)
		case "m3u8":
			dir = m.m3u8DirLocked(it)
		default:
			return "", fmt.Errorf("unknown folder %q", which)
		}
		return m.ownDirLocked(dir)
	}
	return "", fmt.Errorf("That row is not in the list any more.")
}

func (m *Manager) ownDirLocked(dir string) (string, error) {
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", err
	}
	return dir, nil
}

// Dir returns the absolute path of the "done", "incomplete", "m3u8",
// or "data" folder.
func (m *Manager) Dir(which string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var name string
	switch which {
	case "done":
		name = m.settings.doneName()
	case "incomplete":
		name = m.settings.incompleteName()
	case "m3u8":
		name = m.settings.m3u8Name()
	case "data":
		return m.ownDirLocked(m.loc.DataDir(m.baseDir))
	default:
		return "", fmt.Errorf("unknown folder %q", which)
	}
	dir := m.dirLocked(name)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", err
	}
	return dir, nil
}

func (m *Manager) selectedLocked(ids []int64) []*Item {
	set := make(map[int64]bool, len(ids))
	for _, id := range ids {
		set[id] = true
	}
	var out []*Item
	for _, it := range m.items {
		if set[it.ID] {
			out = append(out, it)
		}
	}
	return out
}

func (m *Manager) allIDsLocked(keep func(*Item) bool) []int64 {
	var ids []int64
	for _, it := range m.items {
		if keep(it) {
			ids = append(ids, it.ID)
		}
	}
	return ids
}

// ---- settings ----

func (m *Manager) Settings() Settings {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.settings
}

// SetSettings stores new settings. The mode is changed separately (SetMode).
//
// When the data folder changes, settings.json, queue.json and the log are
// moved there. Nothing is changed if that can not be done (for example the
// new folder is read-only or already holds a settings.json / queue.json).
func (m *Manager) SetSettings(s Settings) error {
	_, err := m.setSettings(s)
	return err
}

// setSettings is SetSettings; it also reports how many downloads were paused
// because the cookie changed. They are not started again: the user does that.
func (m *Manager) setSettings(s Settings) (int, error) {
	s.normalize()
	if err := s.Listen.validate(); err != nil {
		return 0, err
	}
	// A new cookie is never swapped in under downloads that are running: they are
	// paused first, like Pause all. The user starts them again.
	paused := m.pauseForNewCookie(s.Cookie)
	m.mu.Lock()
	defer m.mu.Unlock()
	s.Mode = m.settings.Mode
	s.ColumnWidths = m.settings.ColumnWidths // only /api/columns changes these

	if err := m.relocateLocked(s.DataDir, s); err != nil {
		return 0, err
	}
	s = m.withLocations(s) // what is really in effect (and in the pointer file)
	m.settings = s
	for _, it := range m.items {
		if s.category(it.Category) == nil {
			it.Category = "" // its category was removed; its files stay where they are
		}
		if it.recat != nil && s.category(*it.recat) == nil {
			it.recat = nil
		}
	}
	err := m.saveSettingsLocked()
	if derr := m.ensureDirsLocked(); derr != nil && err == nil {
		err = derr
	}
	m.changedLocked(false)
	m.kick()
	return paused, err
}

// maxCookie is the longest cookie that is accepted (a real one is a few KB).
const maxCookie = 64 << 10

// SetCookie replaces the saved cookie and nothing else. Downloads that are
// running or waiting are paused first, as if you had pressed Pause all, so none
// of them carries on with the old cookie. They stay paused until the user
// starts them. It returns how many were paused.
func (m *Manager) SetCookie(cookie string) (int, error) {
	cookie = strings.TrimSpace(headerCleaner.Replace(cookie))
	if cookie == "" {
		return 0, errors.New("the cookie is empty")
	}
	if len(cookie) > maxCookie {
		return 0, errors.New("that is too long to be a cookie")
	}
	s := m.Settings()
	s.Cookie = cookie
	return m.setSettings(s)
}

// cookieStopWait is the longest a cookie change waits for paused downloads to wind down.
const cookieStopWait = 15 * time.Second

// pauseForNewCookie is called before settings with a different cookie are
// saved. The rows that are running, resolving or waiting for their turn are
// paused and the call returns once their workers have stopped. It returns how
// many rows were paused (0 when the cookie is the same, is the first one, or nothing was going).
func (m *Manager) pauseForNewCookie(newCookie string) int {
	m.mu.Lock()
	if newCookie == m.settings.Cookie || strings.TrimSpace(m.settings.Cookie) == "" {
		m.mu.Unlock()
		return 0 // same cookie, or the first one: nothing is using an old cookie
	}
	var live []int64
	for _, it := range m.items {
		switch it.Status {
		case StatusQueued, StatusResolving, StatusDownloading:
			live = append(live, it.ID)
			m.pauseLocked(it)
		}
	}
	if len(live) == 0 {
		m.mu.Unlock()
		return 0
	}
	m.changedLocked(true)
	m.kick()
	m.mu.Unlock()

	deadline := time.Now().Add(cookieStopWait)
	for time.Now().Before(deadline) && m.anyBusy(live) {
		time.Sleep(20 * time.Millisecond)
	}
	return len(live)
}

// anyBusy reports whether a worker is still winding down on one of the rows.
func (m *Manager) anyBusy(ids []int64) bool {
	set := make(map[int64]bool, len(ids))
	for _, id := range ids {
		set[id] = true
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, it := range m.items {
		if set[it.ID] && it.busy {
			return true
		}
	}
	return false
}

// relocateLocked moves settings.json, queue.json and the log into a new data
// folder and stores the new location. On error nothing has changed.
//
// The window's browser profile is in use, so it can not move now; the pointer
// file records where it is and it is moved the next time the program starts.
func (m *Manager) relocateLocked(newData string, s Settings) error {
	base, old := m.baseDir, m.loc
	if appdata.Same(base, old.Data, newData) {
		return nil // at most the spelling changed
	}
	newLoc := appdata.Locations{Data: newData, MoveProfileFrom: old.MoveProfileFrom}
	if newLoc.MoveProfileFrom == "" {
		if _, err := os.Stat(old.ProfileDir(base)); err == nil {
			newLoc.MoveProfileFrom = old.DataDir(base)
		}
	}
	if newLoc.MoveProfileFrom != "" && appdata.Same(base, newLoc.MoveProfileFrom, newData) {
		newLoc.MoveProfileFrom = "" // back where the profile already is
	}

	dir := newLoc.DataDir(base)
	if err := appdata.CheckWritable(dir); err != nil {
		return fmt.Errorf("can not use the folder %s: %v", dir, err)
	}
	for _, name := range []string{"settings.json", "queue.json"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err == nil {
			return fmt.Errorf("%s already has a %s. Choose an empty folder, or move that file away first", dir, name)
		}
	}

	oldSettings, oldQueue := m.settingsPath(), m.queuePath()
	prevSettings := m.settings
	m.loc = newLoc
	m.settings = s
	undo := func() {
		os.Remove(m.settingsPath())
		os.Remove(m.queuePath())
		m.loc, m.settings = old, prevSettings
	}
	if err := m.saveSettingsLocked(); err != nil {
		undo()
		return fmt.Errorf("could not write settings.json to %s: %v", dir, err)
	}
	if err := m.saveQueueLocked(); err != nil {
		undo()
		return fmt.Errorf("could not write queue.json to %s: %v", dir, err)
	}
	if err := appdata.Save(base, newLoc); err != nil {
		undo()
		return fmt.Errorf("could not save %s next to the program: %v", appdata.FileName, err)
	}
	// committed: remove the old copies
	os.Remove(oldSettings)
	os.Remove(oldQueue)
	if err := applog.Move(dir); err != nil {
		m.notifyLocked("error", "Could not move the log: "+err.Error())
	}
	if newLoc.MoveProfileFrom != "" {
		m.notifyLocked("info", "The window's own data moves to the new folder the next time Recurbate starts.")
	}
	return nil
}

// SetColumnWidths remembers the table's column widths (a complete layout, in
// pixels by column key). Nil or incomplete goes back to the default layout.
func (m *Manager) SetColumnWidths(w map[string]int) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.settings.ColumnWidths = cleanColumnWidths(w)
	m.changedLocked(false)
	return m.saveSettingsLocked()
}

func (m *Manager) Mode() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.settings.Mode
}

// SetMode switches between normal / hybrid / series. Downloads that are
// already running keep going; the new rules apply to what starts next.
func (m *Manager) SetMode(mode string) error {
	if !validMode(mode) {
		return fmt.Errorf("unknown mode %q", mode)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.settings.Mode = mode
	err := m.saveSettingsLocked()
	m.changedLocked(false)
	m.kick()
	return err
}

// ---- adding ----

// Add parses the input box text and appends the rows to the table.
func (m *Manager) Add(text string) (added int, problems []string) {
	return m.AddTo(text, "")
}

// AddTo is Add with a category (an id from Settings; unknown or "" = none).
func (m *Manager) AddTo(text, category string) (added int, problems []string) {
	entries, problems := ParseInput(text)
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.settings.category(category) == nil {
		category = ""
	}
	for _, e := range entries {
		if m.hasLocked(e) {
			problems = append(problems, "already in the list: "+tools.ShortenString(e.URL, 70))
			continue
		}
		it := &Item{ID: m.nextID, URL: e.URL, SectionRef: e.Section, Section: [2]float32{0, 100}, Active: true, Category: category}
		m.nextID++
		if len(e.Section) == 3 {
			it.Section, _ = tools.PercentParse(e.Section)
			if total, err := tools.ParseClock(e.Section[2]); err == nil {
				it.VideoSecs = float64(total)
			}
		}
		if m.settings.AutoStart {
			it.Status = StatusQueued
		} else {
			it.Status = StatusPaused
		}
		m.items = append(m.items, it)
		added++
	}
	if added > 0 {
		m.changedLocked(true)
		m.kick()
	}
	return
}

// sectionKey identifies a part of a video by its start and end in seconds, so
// "55:00" and "0:55:00" (or a section with and without the video length)
// count as the same part.
func sectionKey(ref []string) string {
	if len(ref) < 2 {
		return ""
	}
	s, e, err := tools.ClockRange(ref[0], ref[1])
	if err != nil {
		return strings.Join(ref, "|")
	}
	return fmt.Sprintf("%d-%d", s, e)
}

func (m *Manager) hasLocked(e Entry) bool {
	for _, it := range m.items {
		if it.URL == e.URL && sectionKey(it.SectionRef) == sectionKey(e.Section) {
			return true
		}
	}
	return false
}

// ScanPage lists the video links found on a page (the old "parse" command).
func (m *Manager) ScanPage(pageURL string) ([]string, error) {
	m.mu.Lock()
	hdr := m.settings.headers()
	m.mu.Unlock()
	return engine.ScanPage(&engine.Env{Ctx: m.rootCtx, Header: hdr}, pageURL)
}

// ---- start / pause ----

// Start makes the rows eligible: the scheduler starts them according to the mode.
// Each row that is not running first forgets its server and has its playlist
// read again (only its own: the site can hand out a different server on a
// later visit), also when it is simply resumed after a pause.
func (m *Manager) Start(ids []int64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, it := range m.selectedLocked(ids) {
		m.forgetServerLocked(it) // starting a row looks its server up again
		stopLookupLocked(it)
		if it.Status == StatusPaused || it.Status == StatusError {
			it.Status = StatusQueued
			it.Error = ""
		}
	}
	m.changedLocked(true)
	m.kick()
}

// stopLookupLocked ends a length lookup that is still running, so the row can
// start right away.
func stopLookupLocked(it *Item) {
	if it.lookingUp && it.cancel != nil {
		it.cancel()
	}
}

func (m *Manager) pauseLocked(it *Item) {
	switch it.Status {
	case StatusComplete:
		return
	}
	it.Forced = false
	if it.cancel != nil {
		it.cancel()
	}
	it.Status = StatusPaused
	it.speed, it.etaMin, it.note = 0, 0, ""
}

// Pause stops the rows (the partial file and position are kept).
func (m *Manager) Pause(ids []int64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, it := range m.selectedLocked(ids) {
		m.pauseLocked(it)
	}
	m.changedLocked(true)
	m.kick()
}

// ForceStart starts the rows right now, regardless of the mode. Inactive rows
// are never started; their number is returned.
func (m *Manager) ForceStart(ids []int64) (skippedInactive int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, it := range m.selectedLocked(ids) {
		if it.Status == StatusComplete {
			continue
		}
		if !it.Active {
			skippedInactive++
			continue
		}
		it.Forced = true
		m.forgetServerLocked(it)
		stopLookupLocked(it)
		if it.Status == StatusPaused || it.Status == StatusError {
			it.Status = StatusQueued
			it.Error = ""
		}
	}
	m.changedLocked(true)
	m.kick()
	return
}

func (m *Manager) ForceStartAll() (skippedInactive int) {
	m.mu.Lock()
	ids := m.allIDsLocked(func(it *Item) bool { return it.Status != StatusComplete })
	m.mu.Unlock()
	return m.ForceStart(ids)
}

// StartAll starts every unfinished row. First it forgets which server each
// waiting or paused row was last seen on and re-reads its playlist, because the
// site can hand out a different server on a later visit (for example after the
// program was closed and opened again). Only the playlists are read, in table
// order and a couple at a time; nothing is downloaded until that is known, and
// then rows start in the normal order for the current mode.
func (m *Manager) StartAll() {
	m.mu.Lock()
	ids := m.allIDsLocked(func(it *Item) bool { return it.Status != StatusComplete })
	m.mu.Unlock()
	m.Start(ids) // Start looks the servers up again
}

// forgetServerLocked drops what is known about a row's server so the next
// start looks it up again. Rows that are running keep their server.
func (m *Manager) forgetServerLocked(it *Item) {
	if it.busy || it.Status == StatusResolving || it.Status == StatusDownloading || it.Status == StatusComplete {
		return
	}
	it.res = nil
	if Mode(m.settings.Mode) == ModeHybrid {
		it.Server = ""
	}
}

func (m *Manager) PauseAll() {
	m.mu.Lock()
	ids := m.allIDsLocked(func(it *Item) bool { return it.Status != StatusComplete })
	m.mu.Unlock()
	m.Pause(ids)
}

// SetActive enables or disables rows. Inactive rows are never started
// (not even by Force Start); a running row that becomes inactive is stopped
// and goes back to waiting.
func (m *Manager) SetActive(ids []int64, active bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, it := range m.selectedLocked(ids) {
		if it.Status == StatusComplete || it.Active == active {
			continue
		}
		it.Active = active
		if !active {
			if it.Status == StatusResolving || it.Status == StatusDownloading {
				if it.cancel != nil {
					it.cancel()
				}
				it.Status = StatusQueued
				it.speed, it.etaMin, it.note = 0, 0, ""
			}
			it.Forced = false
		}
	}
	m.changedLocked(true)
	m.kick()
}

// ---- ordering ----

// Move reorders the rows: top, up, down or bottom. The order is the queue
// order the scheduler works through, top to bottom.
func (m *Manager) Move(ids []int64, how string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	sel := make(map[int64]bool, len(ids))
	for _, id := range ids {
		sel[id] = true
	}
	items := m.items
	n := len(items)
	isSel := func(i int) bool { return sel[items[i].ID] }
	switch how {
	case "up":
		for i := 1; i < n; i++ {
			if isSel(i) && !isSel(i-1) {
				items[i], items[i-1] = items[i-1], items[i]
			}
		}
	case "down":
		for i := n - 2; i >= 0; i-- {
			if isSel(i) && !isSel(i+1) {
				items[i], items[i+1] = items[i+1], items[i]
			}
		}
	case "top", "bottom":
		var picked, rest []*Item
		for i := range items {
			if isSel(i) {
				picked = append(picked, items[i])
			} else {
				rest = append(rest, items[i])
			}
		}
		if how == "top" {
			m.items = append(picked, rest...)
		} else {
			m.items = append(rest, picked...)
		}
	}
	m.changedLocked(true)
	m.kick()
}

// Arrange puts the rows at the top (or the bottom) of the queue in exactly the
// order of ids; the other rows keep their order. Unknown or repeated ids are
// ignored. The window uses it to sort the queue by a column.
func (m *Manager) Arrange(ids []int64, bottom bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	byID := make(map[int64]*Item, len(m.items))
	for _, it := range m.items {
		byID[it.ID] = it
	}
	var picked, rest []*Item
	taken := make(map[int64]bool, len(ids))
	for _, id := range ids {
		if it := byID[id]; it != nil && !taken[id] {
			taken[id] = true
			picked = append(picked, it)
		}
	}
	for _, it := range m.items {
		if !taken[it.ID] {
			rest = append(rest, it)
		}
	}
	if bottom {
		m.items = append(rest, picked...)
	} else {
		m.items = append(picked, rest...)
	}
	m.changedLocked(true)
	m.kick()
}

// ---- removing ----

// Remove deletes rows from the table. With deleteFiles the partial / finished
// video file is deleted too.
func (m *Manager) Remove(ids []int64, deleteFiles bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	gone := m.selectedLocked(ids)
	if len(gone) == 0 {
		return
	}
	set := make(map[*Item]bool, len(gone))
	for _, it := range gone {
		set[it] = true
		it.removed = true
		it.deleteFiles = deleteFiles
		it.Forced = false
		if it.cancel != nil {
			it.cancel()
		}
		if !it.busy {
			m.cleanupRemovedLocked(it)
		}
	}
	kept := m.items[:0:0]
	for _, it := range m.items {
		if !set[it] {
			kept = append(kept, it)
		}
	}
	m.items = kept
	m.changedLocked(true)
	m.kick()
}

// ClearCompleted removes the finished rows (the videos stay in Done).
func (m *Manager) ClearCompleted() {
	m.mu.Lock()
	ids := m.allIDsLocked(func(it *Item) bool { return it.Status == StatusComplete })
	m.mu.Unlock()
	m.Remove(ids, false)
}

// FilePath is the file of a row where it is right now: in the finished folder
// once it is complete, otherwise in the unfinished folder.
func (m *Manager) FilePath(id int64) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, it := range m.items {
		if it.ID != id {
			continue
		}
		if it.DonePath != "" {
			if _, err := os.Stat(it.DonePath); err == nil {
				return it.DonePath, nil
			}
		}
		if it.TempPath != "" {
			if _, err := os.Stat(it.TempPath); err == nil {
				return it.TempPath, nil
			}
		}
		if it.Status == StatusComplete {
			return "", fmt.Errorf("The finished file is not there any more. It was moved, renamed or deleted.")
		}
		return "", fmt.Errorf("There is no file yet. This video has not started downloading.")
	}
	return "", fmt.Errorf("That row is not in the list any more.")
}

func (m *Manager) cleanupRemovedLocked(it *Item) {
	if !it.deleteFiles {
		return
	}
	if it.DonePath != "" {
		os.Remove(it.DonePath)
	}
	if it.TempPath != "" {
		os.Remove(it.TempPath)
	}
}

// ---- playlist only ----

// SaveM3U8 downloads only the .m3u8 playlist of the rows into the M3U8 folder
// (the old "playlist" mode), for use with an external tool.
func (m *Manager) SaveM3U8(ids []int64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.shutdown {
		return
	}
	for _, it := range m.selectedLocked(ids) {
		m.wg.Add(1)
		go m.saveM3U8(it)
	}
}

func (m *Manager) saveM3U8(it *Item) {
	defer m.wg.Done()
	ctx := m.rootCtx
	m.mu.Lock()
	res := it.res
	m.mu.Unlock()
	if res == nil || res.Stale(30*time.Second) {
		env := m.envFor(ctx, it, false)
		if !m.acquireResolve(ctx) {
			return
		}
		r, err := engine.Resolve(env, it.URL)
		m.releaseResolve()
		if err != nil {
			if ctx.Err() == nil {
				m.notify("error", fmt.Sprintf("Playlist for %s: %v", tools.ShortenString(it.URL, 60), err))
			}
			return
		}
		res = r
		m.mu.Lock()
		it.res = r
		setServer(it, r.Server)
		m.mu.Unlock()
	}
	m.mu.Lock()
	dir := m.m3u8DirLocked(it)
	m.mu.Unlock()
	if err := os.MkdirAll(dir, 0755); err != nil {
		m.notify("error", fmt.Sprintf("Failed to write playlist data: %v", err))
		return
	}
	name := res.Playlist.Filename + ".m3u8"
	if err := os.WriteFile(filepath.Join(dir, name), res.Playlist.M3u8, 0666); err != nil {
		m.notify("error", fmt.Sprintf("Failed to write playlist data: %v", err))
		return
	}
	m.mu.Lock()
	it.M3U8Saved = true
	it.M3U8Path = filepath.Join(dir, name)
	m.notifyLocked("info", "Saved playlist: "+name)
	m.changedLocked(true)
	m.mu.Unlock()
}

// SetCategory moves rows to another category ("" = none). It is destructive:
// each row's files (unfinished, finished and saved playlist) are deleted from
// the old category's folders and the row starts again from the beginning in
// the new category's folders. Rows that are running are stopped first. It
// returns how many rows changed.
func (m *Manager) SetCategory(ids []int64, category string) (changed int, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.settings.category(category) == nil {
		if category != "" {
			return 0, fmt.Errorf("That category does not exist any more.")
		}
		category = ""
	}
	for _, it := range m.selectedLocked(ids) {
		if it.Category == category || it.removed {
			continue
		}
		changed++
		if it.busy {
			c := category
			it.recat = &c
			if it.cancel != nil {
				it.cancel()
			}
			continue
		}
		m.applyRecategoryLocked(it, category)
	}
	if changed > 0 {
		m.changedLocked(true)
		m.kick()
	}
	return changed, nil
}

// applyRecategoryLocked does the move once nothing is writing to the row's files.
func (m *Manager) applyRecategoryLocked(it *Item, category string) {
	it.recat = nil
	var stuck []string
	del := func(p string) {
		if p == "" {
			return
		}
		if err := os.Remove(p); err != nil && !errors.Is(err, fs.ErrNotExist) {
			stuck = append(stuck, p)
		}
	}
	del(it.TempPath)
	del(it.DonePath)
	del(it.M3U8Path)
	if len(stuck) > 0 {
		m.notifyLocked("error", "Could not delete "+strings.Join(stuck, ", ")+". Delete it yourself.")
	}
	it.Category = category
	it.Offset, it.FileSize, it.FullSize = 0, 0, 0
	it.Percent = 0
	it.FileBase, it.TempPath, it.DonePath = "", "", ""
	it.M3U8Saved, it.M3U8Path = false, ""
	it.Error = ""
	it.Forced = false
	it.Status = StatusQueued
	it.res = nil
	if Mode(m.settings.Mode) == ModeHybrid {
		it.Server = ""
	}
}
