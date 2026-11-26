package manager

import (
	"testing"
	"time"

	"Recurbate/internal/mocksite"
)

// waitSettled waits until no row is waiting for its playlist or being read.
func (e *testEnv) waitSettled() {
	e.t.Helper()
	e.waitFor("the rows to be read", func() bool {
		e.m.mu.Lock()
		defer e.m.mu.Unlock()
		for _, it := range e.m.items {
			if it.held || it.preResolving || it.lookingUp {
				return false
			}
		}
		return !e.m.uniquePass
	})
}

func (e *testEnv) waitUniquePass() {
	e.t.Helper()
	e.waitFor("the unique servers pass to end", func() bool {
		e.m.mu.Lock()
		defer e.m.mu.Unlock()
		return !e.m.uniquePass
	})
}

// The button reads the page of a row on the same server as the row above it
// (only that row) until it gets another server. Starting afterwards reads no page.
func TestUniqueServersResetsOnlyTheRepeatingRow(t *testing.T) {
	e := newTestEnv(t, "hybrid", slowSite)
	e.setSettings(func(s *Settings) { s.AutoStart, s.FirstPieces = false, false })
	ids := e.addVideos(2, 4) // the mock site gives videos 2 and 4 the same server
	e.waitFor("both servers", func() bool {
		return e.item(ids[1]).Server != "" && !e.busy(ids[0]) && !e.busy(ids[1])
	})
	e.waitSettled()
	top := e.item(ids[0]).Server
	if e.item(ids[1]).Server != top {
		t.Fatal("setup: the rows should share a server")
	}
	e.site.MoveVideos() // a page read now hands out the other server
	before := e.site.Resolves()
	if !e.m.ResetRepeatingServers() {
		t.Fatal("the pass did not start")
	}
	e.waitUniquePass()
	if n := e.site.Resolves() - before; n != 1 {
		t.Errorf("read %d pages, want 1", n)
	}
	if e.item(ids[0]).Server != top {
		t.Error("the top row was reset")
	}
	if e.item(ids[1]).Server == top {
		t.Error("the repeating row kept the server of the row above")
	}
}

// In the tests a second of Time Between Tries lasts a few milliseconds, or a
// repeating row would hold up the tests that add links on one server.
func init() { uniqueSecond = 20 * time.Millisecond }

// A site that keeps giving the same server is asked 10 times, then left alone,
// with the wait before every try (asked again at once, the site gave the same
// server every time).
func TestUniqueServersTriesTenTimesApart(t *testing.T) {
	e := newTestEnv(t, "hybrid", slowSite)
	e.setSettings(func(s *Settings) { s.AutoStart, s.FirstPieces = false, false })
	ids := e.addVideos(2, 4)
	e.waitFor("both servers", func() bool {
		return e.item(ids[1]).Server != "" && !e.busy(ids[0]) && !e.busy(ids[1])
	})
	e.waitSettled()
	uniqueSecond = 100 * time.Millisecond
	defer func() { uniqueSecond = 20 * time.Millisecond }()
	before, start := e.site.Resolves(), time.Now()
	e.m.ResetRepeatingServers()
	e.waitUniquePass()
	if n := e.site.Resolves() - before; n != uniqueTries {
		t.Errorf("read %d pages, want %d", n, uniqueTries)
	}
	if took := time.Since(start); took < uniqueTries*uniqueSecond {
		t.Errorf("%d tries took %v, want at least %v", uniqueTries, took, uniqueTries*uniqueSecond)
	}
}

// A refusal ends the pass at once: no more pages are read.
func TestUniqueServersStopsAtARefusal(t *testing.T) {
	e := newTestEnv(t, "hybrid", slowSite)
	e.setSettings(func(s *Settings) { s.AutoStart, s.FirstPieces = false, false })
	ids := e.addVideos(2, 4, 6, 8) // all on the same server
	e.waitFor("all servers", func() bool {
		for _, id := range ids {
			if e.item(id).Server == "" || e.busy(id) {
				return false
			}
		}
		return true
	})
	e.waitSettled()
	before := e.site.Resolves()
	e.site.BlockPages(5) // one page read (5 tries); the reads after it would get through
	e.m.ResetRepeatingServers()
	e.waitUniquePass()
	if n := e.site.Resolves() - before; n != 0 {
		t.Errorf("%d pages were read after the refusal", n)
	}
	for _, id := range ids {
		if e.item(id).Status == "error" {
			t.Error("a refusal in the pass failed a row")
		}
	}
}

// With Group Split Videos on, a part is compared only with the part above it in
// its group, and a single row only with the single rows above it. The mock site
// gives every one of these rows the same server, so each row that is checked
// reads its page 10 times. With it off, every row is compared with the row above.
func TestUniqueServersChecksGroupsOnTheirOwn(t *testing.T) {
	for _, c := range []struct {
		group bool
		reads int
	}{{true, 2 * uniqueTries}, {false, 3 * uniqueTries}} {
		e := newTestEnv(t, "hybrid", slowSite)
		e.setSettings(func(s *Settings) { s.AutoStart, s.FirstPieces, s.GroupSplit = false, false, c.group })
		u := e.site.VideoURL(2)
		ids := e.add(e.site.VideoURL(4), `["`+u+`","0:00","0:20","2:00"]`, `["`+u+`","1:00","1:20","2:00"]`, e.site.VideoURL(6))
		e.waitFor("all servers", func() bool {
			for _, id := range ids {
				if e.item(id).Server == "" || e.busy(id) {
					return false
				}
			}
			return true
		})
		e.waitSettled()
		before := e.site.Resolves()
		e.m.ResetRepeatingServers()
		e.waitUniquePass()
		if n := e.site.Resolves() - before; n != c.reads {
			t.Errorf("Group Split Videos %v: read %d pages, want %d", c.group, n, c.reads)
		}
	}
}

// Newly added links are checked as their playlists come in: a row on the
// server of the row above reads again, the rows already there are not read
// again. (The mock site gives videos 2, 4 and 6 the same server.)
func TestNewLinksAreCheckedAsTheyLoad(t *testing.T) {
	e := newTestEnv(t, "hybrid", slowSite)
	e.setSettings(func(s *Settings) { s.AutoStart, s.FirstPieces = false, false })
	e.addVideos(2, 4)
	e.waitSettled()
	if n := e.site.Resolves(); n != 2+uniqueTries {
		t.Errorf("read %d pages, want %d", n, 2+uniqueTries)
	}
	e.addVideos(6)
	e.waitSettled()
	if n := e.site.Resolves(); n != 3+2*uniqueTries {
		t.Errorf("read %d pages, want %d (the old rows were read again)", n, 3+2*uniqueTries)
	}
}

// Rows that are downloading are never read again by the button.
func TestUniqueServersNeverTouchesDownloads(t *testing.T) {
	e := newTestEnv(t, "normal", slowSite)
	e.setSettings(func(s *Settings) { s.AutoStart, s.FirstPieces = true, false })
	ids := e.addVideos(2, 4) // one server, both download at once
	e.waitFor("both downloading", func() bool {
		return e.item(ids[0]).Status == "downloading" && e.item(ids[1]).Status == "downloading"
	})
	before := e.site.Resolves()
	e.m.ResetRepeatingServers()
	e.waitUniquePass()
	if n := e.site.Resolves() - before; n != 0 {
		t.Errorf("read %d pages of downloading rows, want 0", n)
	}
}

// Start all reads a new playlist for every unfinished row that is not
// downloading, like the original read every page on each run. Each row is
// checked as its playlist comes in and starts once its own check is done: a
// row never downloads before that, and the top row does not wait for the
// check of the rows below it.
func TestStartAllChecksEachRowBeforeItStarts(t *testing.T) {
	// slow enough that the top row is still downloading during the check below it
	e := newTestEnv(t, "normal", func(s *mocksite.Site) { slowSite(s); s.Delay = 40 * time.Millisecond })
	e.setSettings(func(s *Settings) { s.AutoStart, s.FirstPieces = false, false })
	ids := e.addVideos(2, 4, 3) // 2 and 4 share a server
	e.waitSettled()
	before := e.site.Resolves()
	uniqueSecond = 100 * time.Millisecond
	defer func() { uniqueSecond = 20 * time.Millisecond }()
	e.m.StartAll()
	early := false
	for e.item(ids[0]).Status != "complete" || e.item(ids[1]).Status != "complete" || e.item(ids[2]).Status != "complete" {
		e.m.mu.Lock()
		for i, it := range e.m.items {
			if it.held && it.Status == StatusDownloading {
				t.Errorf("row %d downloads before its check", i)
			}
		}
		if e.m.items[0].Status == StatusDownloading && e.m.items[1].held {
			early = true
		}
		e.m.mu.Unlock()
		time.Sleep(10 * time.Millisecond)
	}
	if !early {
		t.Error("the top row waited for the check of the row below")
	}
	// 3 new playlists, and row 4 asked 10 times for another server
	if n := e.site.Resolves() - before; n != 3+uniqueTries {
		t.Errorf("read %d pages, want %d", n, 3+uniqueTries)
	}
}

// A row that is downloading when Start all is pressed keeps its playlist.
func TestStartAllLeavesDownloadsAlone(t *testing.T) {
	e := newTestEnv(t, "normal", slowSite)
	id := e.addVideos(2)[0]
	e.waitFor("downloading", func() bool { return e.item(id).Status == "downloading" })
	e.m.StartAll()
	e.waitComplete(id)
	if n := e.site.Resolves(); n != 1 {
		t.Errorf("read %d pages, want 1", n)
	}
}

// With Unique Servers unticked (Settings → Experimental) a new row reads its
// playlist once and keeps the server it gets. It is ticked by default.
func TestUniqueServersCanBeTurnedOff(t *testing.T) {
	if !DefaultSettings().UniqueServers {
		t.Fatal("Unique Servers must be on by default")
	}
	e := newTestEnv(t, "hybrid", slowSite)
	e.setSettings(func(s *Settings) { s.AutoStart, s.FirstPieces, s.UniqueServers = false, false, false })
	e.addVideos(2, 4) // the mock site gives both the same server
	e.waitSettled()
	if n := e.site.Resolves(); n != 2 {
		t.Errorf("read %d pages, want 2", n)
	}
}

// Time Between Tries is the wait before every Unique Servers try, kept
// between 0.1 and 60 seconds.
func TestUniqueServersWaitIsTheSetting(t *testing.T) {
	if DefaultSettings().UniqueWait != 1 {
		t.Error("the default wait must be 1 second")
	}
	for _, c := range [][2]float64{{0, 0.1}, {0.05, 0.1}, {2.5, 2.5}, {500, 60}} {
		s := Settings{UniqueWait: c[0]}
		s.normalize()
		if s.UniqueWait != c[1] {
			t.Errorf("%v became %v, want %v", c[0], s.UniqueWait, c[1])
		}
	}
	e := newTestEnv(t, "hybrid", slowSite)
	e.setSettings(func(s *Settings) { s.AutoStart, s.FirstPieces, s.UniqueWait = false, false, 5 })
	e.addVideos(2, 4) // the same server: row 4 asks 10 times, 5 "seconds" apart
	start := time.Now()
	e.waitSettled()
	if took := time.Since(start); took < uniqueTries*5*uniqueSecond {
		t.Errorf("10 tries took %v, want at least %v", took, uniqueTries*5*uniqueSecond)
	}
}
