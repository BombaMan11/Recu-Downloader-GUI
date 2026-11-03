package manager

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"Recurbate/engine"
	"Recurbate/tools"
)

func (m *Manager) schedulerLoop() {
	defer m.wg.Done()
	t := time.NewTicker(time.Second)
	defer t.Stop()
	for {
		select {
		case <-m.rootCtx.Done():
			return
		case <-m.kickCh:
		case <-t.C:
		}
		m.mu.Lock()
		m.scheduleLocked()
		m.mu.Unlock()
	}
}

func (m *Manager) saverLoop() {
	defer m.wg.Done()
	t := time.NewTicker(time.Second)
	defer t.Stop()
	for {
		select {
		case <-m.rootCtx.Done():
			return
		case <-t.C:
		}
		m.mu.Lock()
		if m.dirty {
			m.dirty = false
			if err := m.saveQueueLocked(); err != nil {
				m.notifyLocked("error", "Could not save queue.json: "+err.Error())
			}
		}
		m.mu.Unlock()
	}
}

// estimateFullSize guesses how big the finished file will be from what has
// been downloaded so far: the average size of the segments done, times the
// number of segments in the part of the video that is being downloaded.
// It needs a few segments before the average means anything.
func estimateFullSize(p engine.Progress) int64 {
	done, total := p.Next-p.Start, p.End-p.Start
	if done < 3 || total <= 0 || p.Bytes <= 0 {
		return 0
	}
	if done >= total {
		return p.Bytes
	}
	return int64(float64(p.Bytes) / float64(done) * float64(total))
}

func isCancel(err error) bool { return errors.Is(err, context.Canceled) }

func setServer(it *Item, server string) {
	if server == "" {
		server = "unknown"
	}
	it.Server = server
	it.fresh = true
}

func (m *Manager) acquireResolve(ctx context.Context) bool {
	select {
	case m.resolveSem <- struct{}{}:
		return true
	case <-ctx.Done():
		return false
	}
}

func (m *Manager) releaseResolve() { <-m.resolveSem }

// scheduleLocked starts whatever the current mode allows, top to bottom.
// Force-started rows ignore the mode and are not counted against its limits.
func (m *Manager) scheduleLocked() {
	if m.shutdown || !m.settings.credsOK() {
		return
	}
	for _, it := range m.items {
		if it.Active && it.Forced && it.Status == StatusQueued && !it.busy {
			m.launchLocked(it)
		}
	}
	m.primeNextLocked()
	running, pre := 0, 0
	busyServers := map[string]bool{}
	for _, it := range m.items {
		if (it.Status == StatusResolving || it.Status == StatusDownloading) && !it.Forced {
			running++
			if it.Server != "" {
				busyServers[it.Server] = true
			}
		}
		if it.preResolving || it.lookingUp {
			pre++
		}
	}
	waiting := func(it *Item) bool {
		return it.Active && it.Status == StatusQueued && !it.Forced && !it.busy && !it.Prime
	}
	switch Mode(m.settings.Mode) {
	case ModeSeries:
		if running == 0 {
			for _, it := range m.items {
				if it.Active && it.Status == StatusQueued && !it.Forced && !it.Prime {
					if !it.busy { // a busy row is about to be free: nothing may jump ahead of it
						m.launchLocked(it)
					}
					break
				}
			}
		}
	case ModeHybrid:
		// One download per server, top to bottom. A row's server is only known
		// once its playlist has been read, so playlists are checked ahead of
		// time (in table order). Nothing may jump ahead of a row whose server
		// is still unknown: it might be the same server and that row is above.
		blocked := false
		for _, it := range m.items {
			if !it.Active || it.Status != StatusQueued || it.Forced || it.Prime {
				continue
			}
			if it.Server == "" {
				if !it.busy && pre < m.lookupLimit() && !time.Now().Before(it.preWait) {
					m.preResolveLocked(it)
					pre++
				}
				blocked = true
				continue
			}
			if it.busy {
				busyServers[it.Server] = true // about to be free: the rows below it on this server wait
				continue
			}
			if blocked || busyServers[it.Server] {
				continue
			}
			m.launchLocked(it)
			busyServers[it.Server] = true
		}
	default: // normal
		for _, it := range m.items {
			if it.Active && it.Status == StatusQueued && !it.Forced && it.busy && m.settings.MaxParallel > 0 {
				running++ // about to be free, but it keeps its place in the line
				continue
			}
			if !waiting(it) {
				continue
			}
			if limit := m.settings.MaxParallel; limit > 0 && running >= limit {
				break
			}
			m.launchLocked(it)
			running++
		}
	}
	// Long Lasting Site: read the rows that have to wait, now, while the site
	// still lets us in, instead of when their turn comes
	if m.settings.LongLasting {
		for _, it := range m.items {
			if pre >= m.lookupLimit() {
				break
			}
			if waiting(it) && it.res == nil && !time.Now().Before(it.preWait) {
				m.preResolveLocked(it)
				pre++
			}
		}
	}
	// find the length of the videos that do not start yet, a couple at a time
	for _, it := range m.items {
		if pre >= m.lookupLimit() {
			break
		}
		if it.needsLength() {
			m.lookupLengthLocked(it)
			pre++
		}
	}
}

func (m *Manager) launchLocked(it *Item) {
	if !it.priming {
		it.Prime = false // started for real: no separate first pieces
	}
	ctx, cancel := context.WithCancel(m.rootCtx)
	it.cancel = cancel
	it.busy, it.settled = true, false
	it.Status = StatusResolving
	it.Error = ""
	it.stage = "Starting"
	it.note = ""
	m.changedLocked(true)
	m.wg.Add(1)
	go m.run(it, ctx)
}

// preResolveLocked reads the playlist of a waiting row only to learn which
// server it will download from (Hybrid needs that before it can decide).
func (m *Manager) preResolveLocked(it *Item) {
	ctx, cancel := context.WithCancel(m.rootCtx)
	it.cancel = cancel
	it.busy = true
	it.preResolving = true
	m.changedLocked(false)
	m.wg.Add(1)
	go func() {
		defer m.wg.Done()
		env := m.envFor(ctx, it, false)
		res, err := m.lookupResolve(ctx, it, env)
		m.mu.Lock()
		defer m.mu.Unlock()
		it.busy, it.preResolving, it.cancel = false, false, nil
		switch {
		case it.recat != nil && !it.removed:
			m.applyRecategoryLocked(it, *it.recat)
		case it.removed:
			m.cleanupRemovedLocked(it)
		case err == nil:
			if nerr := m.noteLengthLocked(it, res); nerr != nil {
				err = nerr
				if it.Status == StatusQueued {
					it.Status = StatusError
					it.Error = err.Error()
					m.notifyLocked("error", fmt.Sprintf("%s: %v", displayName(it), err))
				}
				break
			}
			it.res = res
			it.preBlocks = 0
			setServer(it, res.Server)
		case isCancel(err):
			it.lenTried = false
		case engine.Temporary(err) && it.preBlocks < len(engine.BlockedWaits) && it.Status == StatusQueued:
			// the site refused (or was not reachable): stay in line and ask
			// again later instead of failing a row that never got to start
			it.preWait = time.Now().Add(engine.BlockedWaits[it.preBlocks])
			it.preBlocks++
		default:
			it.preBlocks = 0
			if it.Status == StatusQueued {
				err = userError(err)
				it.Status = StatusError
				it.Error = err.Error()
				m.notifyLocked("error", fmt.Sprintf("%s: %v", displayName(it), err))
			}
		}
		m.changedLocked(true)
		m.kick()
	}()
}

func (m *Manager) envFor(ctx context.Context, it *Item, report bool) *engine.Env {
	m.mu.Lock()
	hdr := m.settings.headers()
	maxRes := m.settings.MaxResolution
	m.mu.Unlock()
	env := &engine.Env{Ctx: ctx, Header: hdr, MaxRes: maxRes}
	if report {
		env.Log = func(s string) {
			m.mu.Lock()
			if ctx.Err() == nil {
				it.stage = s
				m.changedLocked(false)
			}
			m.mu.Unlock()
		}
		env.Warn = func(s string) {
			m.mu.Lock()
			if ctx.Err() == nil {
				it.note = s
				m.changedLocked(false)
			}
			m.mu.Unlock()
		}
	}
	return env
}

func (m *Manager) setStatus(it *Item, ctx context.Context, st Status) {
	m.mu.Lock()
	if ctx.Err() == nil {
		it.Status = st
		m.changedLocked(true)
	}
	m.mu.Unlock()
}

// run drives one row from "resolving" to "complete" / "error" / stopped.
func (m *Manager) run(it *Item, ctx context.Context) {
	defer m.wg.Done()
	err := m.runItem(it, ctx)
	m.mu.Lock()
	defer m.mu.Unlock()
	it.busy, it.cancel = false, nil
	it.speed, it.etaMin, it.note = 0, 0, ""
	it.priming, it.settled = false, false
	if !m.shutdown {
		it.Prime = false // however the run ended, the first pieces are done with
	}
	switch {
	case it.recat != nil && !it.removed:
		m.applyRecategoryLocked(it, *it.recat)
	case it.removed:
		m.cleanupRemovedLocked(it)
	case err == nil:
		it.Status = StatusComplete
		it.Percent = 100
		it.FullSize = it.FileSize // now exact
		it.Error = ""
		it.Forced = false
		m.notifyLocked("info", "Completed: "+displayName(it))
	case isCancel(err):
		// whoever cancelled already set the status (paused / inactive);
		// on shutdown the row goes back to waiting so it resumes next launch
		if it.Status == StatusResolving || it.Status == StatusDownloading {
			it.Status = StatusQueued
		}
	default:
		it.Status = StatusError
		it.Error = err.Error()
		it.Forced = false
		m.notifyLocked("error", fmt.Sprintf("%s: %v", displayName(it), err))
	}
	m.changedLocked(true)
	m.kick()
}

func (m *Manager) runItem(it *Item, ctx context.Context) error {
	refreshes, waits := 0, 0 // since the last segment that came through
	tries := 0               // Unique Server Find
	seen := map[string]bool{}
	for {
		env := m.envFor(ctx, it, true) // a cookie replaced while waiting is used from now on
		m.mu.Lock()
		res := it.res
		m.mu.Unlock()
		if m.stale(res) {
			m.setStatus(it, ctx, StatusResolving)
			if !m.acquireResolve(ctx) {
				return ctx.Err()
			}
			r, err := engine.Resolve(env, it.URL)
			m.releaseResolve()
			if err != nil {
				if m.waitOut(ctx, it, &waits, err) {
					continue
				}
				return finalError(ctx, err)
			}
			res = r
			m.mu.Lock()
			nerr := m.noteLengthLocked(it, r)
			it.res = r
			setServer(it, r.Server)
			m.mu.Unlock()
			if nerr != nil {
				return nerr
			}
		}
		m.waitForRowsAbove(ctx, it)
		if m.serverBusy(it, res.Server, tries, seen) {
			tries++
			m.mu.Lock()
			it.res = nil
			it.stage = "Looking for another server"
			if tries <= m.settings.ServerRetries {
				it.stage = fmt.Sprintf("Looking for another server (%d/%d)", tries, m.settings.ServerRetries)
			}
			wait := time.Duration(m.settings.ServerWait * float64(time.Second))
			m.changedLocked(false)
			m.mu.Unlock()
			tools.Sleep(ctx, wait)
			if err := ctx.Err(); err != nil {
				return err
			}
			continue
		}
		m.mu.Lock()
		if it.FileBase == "" {
			it.FileBase = m.uniqueBaseLocked(res.Playlist.Filename, it)
		}
		if it.TempPath == "" {
			it.TempPath = filepath.Join(m.incDirLocked(it), it.FileBase+".ts")
		}
		incDir := filepath.Dir(it.TempPath)
		if len(it.SectionRef) == 2 {
			m.mu.Unlock()
			return fmt.Errorf("the length of this video could not be found, so the part from %s to %s can not be worked out. Add the length as a third time", it.SectionRef[0], it.SectionRef[1])
		}
		startOffset := it.Offset
		job := engine.MuxJob{
			Playlist:   &res.Playlist,
			Section:    it.Section,
			Offset:     it.Offset,
			Imported:   it.Offset > 0,
			ResumeSize: -1,
			Path:       it.TempPath,
		}
		// refresh the links shortly before they stop working, unless they
		// were handed out with hardly any time left (then wait for a refusal)
		if !res.Expires.IsZero() && time.Until(res.Expires) > 2*engine.ExpiryLead {
			job.Expires = res.Expires
		}
		if it.Offset > 0 && it.FileSize > 0 {
			job.ResumeSize = it.FileSize
		}
		m.mu.Unlock()
		if err := os.MkdirAll(incDir, 0755); err != nil {
			return fmt.Errorf("can not create folder %s: %v", incDir, err)
		}
		m.setStatus(it, ctx, StatusDownloading)
		err := engine.Mux(env, job, func(p engine.Progress) {
			m.mu.Lock()
			defer m.mu.Unlock()
			if ctx.Err() != nil {
				return
			}
			it.Status = StatusDownloading
			it.Offset = p.Next
			it.FileSize = p.Bytes
			it.Percent = p.Percent
			it.FullSize = estimateFullSize(p)
			it.speed = p.Speed
			it.etaMin = p.EtaMin
			it.note = ""
			if it.priming && p.Next-p.Start >= primeSegments {
				// the first pieces are in: stop here and queue or pause the row
				it.Prime = false
				if it.cancel != nil {
					it.cancel()
				}
				it.Status = StatusPaused
				if it.primeWant {
					it.Status = StatusQueued
				}
				it.speed, it.etaMin = 0, 0
			}
			m.changedLocked(true)
		})
		if err == nil {
			return m.finish(it, job.Path)
		}
		m.mu.Lock()
		if it.Offset > startOffset {
			refreshes, waits = 0, 0
		}
		m.mu.Unlock()
		if errors.Is(err, engine.ErrExpired) || errors.Is(err, engine.ErrDenied) {
			// The links expired (410), ran out, or the CDN refuses them
			// (403, which is also what Cloudflare answers): read the playlist
			// again for new links and carry on from the saved segment.
			m.mu.Lock()
			it.res = nil
			m.mu.Unlock()
			if refreshes < 2 {
				refreshes++
				env.Warn("links expired, refreshing playlist")
				continue
			}
			// new links did not help either: the site is blocking us for now
			if m.waitOut(ctx, it, &waits, err) {
				continue
			}
		}
		return finalError(ctx, err)
	}
}

// userError adds the advice for a block that did not go away.
func userError(err error) error {
	if engine.Blocked(err) {
		return fmt.Errorf("%v. %s", err, engine.BlockedHint)
	}
	return err
}

// finalError is what a row shows when its download gave up. A cancelled
// download keeps the context error, which is how pausing is told apart.
func finalError(ctx context.Context, err error) error {
	if cerr := ctx.Err(); cerr != nil {
		return cerr
	}
	return userError(err)
}

// headerSig identifies the cookie and user agent in use.
func (m *Manager) headerSig() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.settings.Cookie + "\x00" + m.settings.UserAgent
}

func waitText(d time.Duration) string {
	s := int(d.Seconds() + 0.5)
	if s < 60 {
		return fmt.Sprintf("%ds", s)
	}
	return fmt.Sprintf("%dm %02ds", s/60, s%60)
}

// waitOut waits before the download asks the site again after it refused
// (or could not be reached): 20 s, 1 min, 3 min, 10 min, then it gives up.
// The count starts over whenever a segment comes through. A cookie replaced
// in the settings ends the wait at once. It reports whether to try again.
func (m *Manager) waitOut(ctx context.Context, it *Item, waits *int, err error) bool {
	if ctx.Err() != nil || !engine.Temporary(err) || *waits >= len(engine.BlockedWaits) {
		return false
	}
	d := engine.BlockedWaits[*waits]
	*waits++
	m.setStatus(it, ctx, StatusResolving)
	sig := m.headerSig()
	why := tools.ShortenString(err, 60)
	end := time.Now().Add(d)
	for {
		left := time.Until(end)
		if left <= 0 {
			return true
		}
		m.mu.Lock()
		if ctx.Err() == nil {
			it.stage = fmt.Sprintf("%s. Trying again in %s", why, waitText(left))
			m.changedLocked(false)
		}
		m.mu.Unlock()
		step := time.Second
		if left < step {
			step = left
		}
		if !tools.Sleep(ctx, step) {
			return false
		}
		if m.headerSig() != sig {
			return true
		}
	}
}

// finish moves the completed video from Incomplete to Done.
func (m *Manager) finish(it *Item, src string) error {
	m.mu.Lock()
	doneDir := m.doneDirLocked(it)
	base := it.FileBase
	m.mu.Unlock()
	if err := os.MkdirAll(doneDir, 0755); err != nil {
		return fmt.Errorf("can not create folder %s: %v", doneDir, err)
	}
	dst := uniquePath(doneDir, base, ".ts")
	if filepath.Clean(src) == filepath.Join(doneDir, base+".ts") {
		dst = src // unfinished and finished videos share a folder: already in place
	} else if err := moveFile(src, dst); err != nil {
		return fmt.Errorf("can not move the video to %s: %v", doneDir, err)
	}
	m.mu.Lock()
	it.DonePath = dst
	it.TempPath = ""
	it.FileBase = strings.TrimSuffix(filepath.Base(dst), ".ts")
	m.mu.Unlock()
	return nil
}

// serverBusy tells whether Unique Server Find wants the row to ask the site
// for another server: the server is the one of the row directly above it, or
// with Deep Search of any of the rows within the chosen number of positions
// above it (finished rows and rows with no known server are not counted).
// After the retries are used up only the row directly above still counts, and
// only if the row has been given two different servers; a site that only ever
// gives one is left alone. seen collects the servers the row has been given.
func (m *Manager) serverBusy(it *Item, server string, tries int, seen map[string]bool) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	it.settled = true // unless it is sent away below
	if !m.settings.UniqueServer {
		return false
	}
	seen[server] = true
	n := 1
	if m.settings.DeepSearch && tries < m.settings.ServerRetries {
		n = m.settings.DeepPositions
	} else if tries >= m.settings.ServerRetries && len(seen) < 2 {
		return false
	}
	at := -1
	for i, o := range m.items {
		if o == it {
			at = i
		}
	}
	for i := at - 1; i >= 0 && n > 0; i-- {
		o := m.items[i]
		if o.Status == StatusComplete || o.Server == "" || o.Server == "unknown" || !o.fresh {
			continue
		}
		if o.Server == server {
			it.settled = false
			return true
		}
		n--
	}
	return false
}

// stale tells whether the links read earlier have to be read again. With Long
// Lasting Site on, links that state no expiry are kept until a download is
// refused (they are otherwise read again after 10 minutes).
func (m *Manager) stale(res *engine.Resolved) bool {
	if res == nil {
		return true
	}
	m.mu.Lock()
	keep := m.settings.LongLasting && res.Expires.IsZero()
	m.mu.Unlock()
	return !keep && res.Stale(30*time.Second)
}

// lookupLimit is how many playlists are read ahead at once: one at a time with
// Unique Server Find, so each row can be compared with the one above it.
func (m *Manager) lookupLimit() int {
	if m.settings.UniqueServer {
		return 1
	}
	return 2
}

// lookupResolve reads the playlist of a row that is not starting yet. With
// Unique Server Find on it asks again while the server is the one the row
// above has, like a start does, so the servers shown in the list already differ.
func (m *Manager) lookupResolve(ctx context.Context, it *Item, env *engine.Env) (*engine.Resolved, error) {
	seen := map[string]bool{}
	for tries := 0; ; tries++ {
		if !m.acquireResolve(ctx) {
			return nil, ctx.Err()
		}
		res, err := engine.Resolve(env, it.URL)
		m.releaseResolve()
		if err != nil {
			return res, err
		}
		m.waitForRowsAbove(ctx, it)
		if !m.serverBusy(it, res.Server, tries, seen) {
			return res, nil
		}
		m.mu.Lock()
		wait := time.Duration(m.settings.ServerWait * float64(time.Second))
		m.mu.Unlock()
		tools.Sleep(ctx, wait)
		if err := ctx.Err(); err != nil {
			return nil, err
		}
	}
}

// waitForRowsAbove holds a row back (up to 30 seconds) while a row above it is
// still looking for its server, so that it is compared with the server that
// row really gets and not with the one it had before.
func (m *Manager) waitForRowsAbove(ctx context.Context, it *Item) {
	for deadline := time.Now().Add(30 * time.Second); time.Now().Before(deadline); {
		m.mu.Lock()
		undecided := false
		if m.settings.UniqueServer {
			for _, o := range m.items {
				if o == it {
					break
				}
				if o.Active && o.Status == StatusResolving && !o.settled {
					undecided = true
					break
				}
			}
		}
		m.mu.Unlock()
		if !undecided || ctx.Err() != nil {
			return
		}
		tools.Sleep(ctx, 50*time.Millisecond)
	}
}

// primeSegments is how many pieces of a new row are downloaded before it
// pauses or goes back in line (see primeNextLocked).
const primeSegments = 2

// primeNextLocked downloads the first pieces of the new rows, one row at a
// time and top to bottom, whatever the mode is (like Force start). That reads
// the playlist, so the start, end and length are fixed, and the unfinished file
// shows up in its folder. The row then goes back in line if it was waiting to
// start, or stays paused. Rows the person started or paused meanwhile are left
// alone.
func (m *Manager) primeNextLocked() {
	for _, it := range m.items {
		if it.priming {
			return
		}
	}
	for _, it := range m.items {
		if it.Prime && it.Active && !it.Forced && !it.busy && (it.Status == StatusPaused || it.Status == StatusQueued) {
			it.priming = true
			it.primeWant = it.Status == StatusQueued
			m.launchLocked(it)
			return
		}
	}
}

// uniqueBaseLocked picks a file name that no other row or file uses
// (name, name_01, name_02, ... name_99, name_100).
//
// Rows of the same video (the same link with different start and end times)
// all get the same name, so they are numbered in the order they have in the
// list: the first is name, the next name_01, and so on. That way the numbers
// do not depend on which row happened to start first (in Normal mode they all
// start at once and whichever the site answers first used to take name).
func (m *Manager) uniqueBaseLocked(name string, self *Item) string {
	inc := m.incDirLocked(self)
	done := m.doneDirLocked(self)
	taken := func(b string) bool {
		if exists(filepath.Join(inc, b+".ts")) || exists(filepath.Join(done, b+".ts")) {
			return true
		}
		for _, o := range m.items {
			if o != self && o.FileBase == b {
				return true
			}
		}
		return false
	}
	first := 0 // rows of the same link above this one in the list
	for _, o := range m.items {
		if o == self {
			break
		}
		if o.URL == self.URL {
			first++
		}
	}
	for i := first; ; i++ {
		cand := numberedName(name, i)
		if !taken(cand) {
			return cand
		}
	}
}

// numberedName is the name of the i-th file with the same name: i = 0 is name
// itself, then name_01, name_02 ... name_99 (two digits, a leading zero below
// 10) and name_100 after that.
func numberedName(name string, i int) string {
	if i <= 0 {
		return name
	}
	return fmt.Sprintf("%s_%02d", name, i)
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func uniquePath(dir, base, ext string) string {
	p := filepath.Join(dir, base+ext)
	for i := 1; exists(p); i++ {
		p = filepath.Join(dir, numberedName(base, i)+ext)
	}
	return p
}

func moveFile(src, dst string) error {
	if err := os.Rename(src, dst); err == nil {
		return nil
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0666)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		os.Remove(dst)
		return err
	}
	if err := out.Close(); err != nil {
		os.Remove(dst)
		return err
	}
	in.Close()
	return os.Remove(src)
}

func displayName(it *Item) string {
	if it.FileBase != "" {
		return it.FileBase
	}
	if it.res != nil && it.res.Playlist.Filename != "" {
		return it.res.Playlist.Filename
	}
	return tools.ShortenString(strings.TrimPrefix(strings.TrimPrefix(it.URL, "https://"), "http://"), 60)
}

// noteLengthLocked records how long the video is, as stated by its playlist.
// A row given only a start and an end ("[url, 55:00, 1:10:00]") can work out
// which part of the video that is only now, so it also gets its total length
// (stored like a typed one, so it stays the same on later visits).
func (m *Manager) noteLengthLocked(it *Item, res *engine.Resolved) error {
	secs := int(res.Duration + 0.5)
	switch {
	case len(it.SectionRef) == 2:
		if secs <= 0 {
			return fmt.Errorf("the length of this video could not be found, so the part from %s to %s can not be worked out. Add the length as a third time", it.SectionRef[0], it.SectionRef[1])
		}
		s, e, err := tools.ClockRange(it.SectionRef[0], it.SectionRef[1])
		if err != nil {
			return err
		}
		sec, err := tools.PercentFromSeconds(s, e, secs)
		if err != nil {
			return fmt.Errorf("the video is only %s long: %v", tools.FormatClock(secs), err)
		}
		it.Section = sec
		it.SectionRef = []string{it.SectionRef[0], it.SectionRef[1], tools.FormatClock(secs)}
		it.VideoSecs = float64(secs)
	case it.SectionRef == nil && secs > 0:
		it.VideoSecs = float64(secs)
	}
	m.changedLocked(true)
	return nil
}

// lookupLengthLocked reads the playlist of a row that is not about to start,
// only to learn how long the video is (for the End and Duration columns). A
// row that is waiting keeps what it learns, as with a Hybrid pre-check.
func (m *Manager) lookupLengthLocked(it *Item) {
	ctx, cancel := context.WithCancel(m.rootCtx)
	it.cancel = cancel
	it.busy = true
	it.lenTried, it.lookingUp = true, true
	m.changedLocked(false)
	m.wg.Add(1)
	go func() {
		defer m.wg.Done()
		env := m.envFor(ctx, it, false)
		res, err := m.lookupResolve(ctx, it, env)
		m.mu.Lock()
		defer m.mu.Unlock()
		it.busy, it.lookingUp, it.cancel = false, false, nil
		switch {
		case it.recat != nil && !it.removed:
			m.applyRecategoryLocked(it, *it.recat)
		case it.removed:
			m.cleanupRemovedLocked(it)
		case err == nil:
			m.noteLengthLocked(it, res)
			if it.Status == StatusQueued {
				it.res = res
				setServer(it, res.Server)
			}
		case isCancel(err):
			it.lenTried = false // paused or stopped meanwhile: try again later
		}
		m.changedLocked(true)
		m.kick()
	}()
}

// needsLength: a whole-video row whose length is not known yet and that is
// not being worked on (finished and failed rows are left alone).
func (it *Item) needsLength() bool {
	return it.SectionRef == nil && it.VideoSecs <= 0 && !it.lenTried && !it.busy && !it.Prime &&
		(it.Status == StatusPaused || it.Status == StatusQueued)
}
