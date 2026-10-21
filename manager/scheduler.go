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
		return it.Active && it.Status == StatusQueued && !it.Forced && !it.busy
	}
	switch Mode(m.settings.Mode) {
	case ModeSeries:
		if running == 0 {
			for _, it := range m.items {
				if it.Active && it.Status == StatusQueued && !it.Forced {
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
			if !it.Active || it.Status != StatusQueued || it.Forced {
				continue
			}
			if it.Server == "" {
				if !it.busy && pre < 2 && !time.Now().Before(it.preWait) {
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
	// find the length of the videos that do not start yet, a couple at a time
	for _, it := range m.items {
		if pre >= 2 {
			break
		}
		if it.needsLength() {
			m.lookupLengthLocked(it)
			pre++
		}
	}
}

func (m *Manager) launchLocked(it *Item) {
	ctx, cancel := context.WithCancel(m.rootCtx)
	it.cancel = cancel
	it.busy = true
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
		var res *engine.Resolved
		var err error
		if m.acquireResolve(ctx) {
			res, err = engine.Resolve(env, it.URL)
			m.releaseResolve()
		} else {
			err = ctx.Err()
		}
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
	for {
		env := m.envFor(ctx, it, true) // a cookie replaced while waiting is used from now on
		m.mu.Lock()
		res := it.res
		m.mu.Unlock()
		if res == nil || res.Stale(30*time.Second) {
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

// uniqueBaseLocked picks a file name that no other row or file uses
// (name, name(1), name(2), ...).
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
	if !taken(name) {
		return name
	}
	for i := 1; ; i++ {
		cand := fmt.Sprintf("%s(%d)", name, i)
		if !taken(cand) {
			return cand
		}
	}
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func uniquePath(dir, base, ext string) string {
	p := filepath.Join(dir, base+ext)
	for i := 1; exists(p); i++ {
		p = filepath.Join(dir, fmt.Sprintf("%s(%d)%s", base, i, ext))
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
		var res *engine.Resolved
		var err error
		if m.acquireResolve(ctx) {
			res, err = engine.Resolve(env, it.URL)
			m.releaseResolve()
		} else {
			err = ctx.Err()
		}
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
	return it.SectionRef == nil && it.VideoSecs <= 0 && !it.lenTried && !it.busy &&
		(it.Status == StatusPaused || it.Status == StatusQueued)
}
