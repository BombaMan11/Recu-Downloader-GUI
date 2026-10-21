package manager

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"Recurbate/engine"
	"Recurbate/internal/mocksite"
)

// shortWaits makes the waits after a refusal last milliseconds.
func shortWaits(t *testing.T, n int) {
	t.Helper()
	old := engine.BlockedWaits
	w := make([]time.Duration, n)
	for i := range w {
		w[i] = 80 * time.Millisecond
	}
	engine.BlockedWaits = w
	t.Cleanup(func() { engine.BlockedWaits = old })
}

func slowSite(s *mocksite.Site) { s.Lines = 62; s.Delay = 12 * time.Millisecond }

// A CDN that signs its links answers 403 (a Cloudflare page) when they have
// expired, not 410. The download must read the playlist again and carry on.
func TestExpiredLinksAnswering403AreRefreshed(t *testing.T) {
	e := newTestEnv(t, "series", func(s *mocksite.Site) { slowSite(s); s.SetExpiredStatus(403) })
	id := e.addVideos(2)[0]
	e.waitFor("some progress", func() bool { return e.item(id).Percent > 20 })
	e.site.ExpireAll()
	e.waitComplete(id)
	if want := e.site.Expected(2, "720p", 0, 60); !bytes.Equal(e.readDone(2), want) {
		t.Fatal("content differs after the links expired")
	}
	if n := e.site.Resolves(); n != 2 {
		t.Fatalf("video was resolved %d times, want 2", n)
	}
}

// Links that run out several times during one long download are refreshed
// every time, not just twice.
func TestLinksExpiringRepeatedlyKeepBeingRefreshed(t *testing.T) {
	e := newTestEnv(t, "series", func(s *mocksite.Site) { slowSite(s); s.Lines = 122; s.SetExpiredStatus(403) })
	id := e.addVideos(2)[0]
	for _, at := range []float64{15, 40, 65, 85} {
		at := at
		e.waitFor("progress", func() bool { return e.item(id).Percent > at })
		e.site.ExpireAll()
	}
	e.waitComplete(id)
	if want := e.site.Expected(2, "720p", 0, 120); !bytes.Equal(e.readDone(2), want) {
		t.Fatal("content differs")
	}
	if n := e.site.Resolves(); n < 4 {
		t.Fatalf("video was resolved %d times, want at least 4", n)
	}
}

// The playlist is read again shortly before the links expire, so the CDN
// never has to turn a request away.
func TestLinksAreRefreshedBeforeTheyExpire(t *testing.T) {
	old := engine.ExpiryLead
	engine.ExpiryLead = 2 * time.Second
	t.Cleanup(func() { engine.ExpiryLead = old })
	e := newTestEnv(t, "series", func(s *mocksite.Site) {
		s.Lines = 102
		s.Delay = 50 * time.Millisecond
		s.SetLinkLife(6 * time.Second) // refresh after about 4 s; the download takes about 5 s
		s.SetExpiredStatus(403)
	})
	id := e.addVideos(2)[0]
	e.waitComplete(id)
	if want := e.site.Expected(2, "720p", 0, 100); !bytes.Equal(e.readDone(2), want) {
		t.Fatal("content differs")
	}
	if n := e.site.Resolves(); n < 2 {
		t.Fatalf("video was resolved %d time(s): the links were never refreshed", n)
	}
	if n := e.site.Refusals(); n != 0 {
		t.Fatalf("%d requests were refused: the links were not refreshed in time", n)
	}
}

// When refreshing does not help the CDN is blocking us: wait, ask again, and
// carry on from the same segment once it lets us in.
func TestBlockedDownloadWaitsAndResumes(t *testing.T) {
	shortWaits(t, 6)
	e := newTestEnv(t, "series", slowSite)
	id := e.addVideos(2)[0]
	e.waitFor("some progress", func() bool { return e.item(id).Percent > 20 })
	e.site.BlockSegments(true)
	e.waitFor("waiting message", func() bool { return strings.Contains(e.item(id).Detail, "Trying again") })
	if st := e.item(id).Status; st == string(StatusError) {
		t.Fatalf("the row failed instead of waiting")
	}
	e.site.BlockSegments(false)
	e.waitComplete(id)
	if want := e.site.Expected(2, "720p", 0, 60); !bytes.Equal(e.readDone(2), want) {
		t.Fatal("content differs after the block")
	}
}

// A block that never goes away ends in a clear message (no HTML dump), and
// pressing Start later carries on from where it stopped.
func TestBlockThatStaysGivesAClearErrorAndResumes(t *testing.T) {
	shortWaits(t, 2)
	e := newTestEnv(t, "series", slowSite)
	id := e.addVideos(2)[0]
	e.waitFor("some progress", func() bool { return e.item(id).Percent > 20 })
	e.site.BlockSegments(true)
	e.waitStatus(id, StatusError)
	msg := e.item(id).Error
	for _, want := range []string{"403", "Cloudflare", "Replace cookie"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("error %q lacks %q", msg, want)
		}
	}
	if strings.Contains(msg, "<") {
		t.Fatalf("error shows page source: %q", msg)
	}
	off, _ := e.offset(id)
	if off == 0 {
		t.Fatal("the position was lost")
	}
	e.site.BlockSegments(false)
	e.m.Start([]int64{id})
	e.waitComplete(id)
	if want := e.site.Expected(2, "720p", 0, 60); !bytes.Equal(e.readDone(2), want) {
		t.Fatal("content differs after resuming")
	}
}

// Replacing the cookie while the download waits pauses it (like every other
// running download); the person presses Start and it carries on with the new
// cookie.
func TestReplacingTheCookieEndsTheWait(t *testing.T) {
	old := engine.BlockedWaits
	engine.BlockedWaits = []time.Duration{time.Hour}
	t.Cleanup(func() { engine.BlockedWaits = old })
	e := newTestEnv(t, "series", slowSite)
	id := e.addVideos(2)[0]
	e.waitFor("some progress", func() bool { return e.item(id).Percent > 20 })
	e.site.BlockSegments(true)
	e.waitFor("waiting message", func() bool { return strings.Contains(e.item(id).Detail, "Trying again") })
	e.site.BlockSegments(false)
	e.site.SetCookie("session=fresh") // the person signs in again and replaces the cookie
	n, err := e.m.SetCookie("session=fresh")
	if err != nil || n != 1 {
		t.Fatalf("paused %d, err %v", n, err)
	}
	if st := e.item(id).Status; st != "paused" {
		t.Fatalf("the waiting row is %q, want paused", st)
	}
	e.m.Start([]int64{id})
	e.waitComplete(id)
	if want := e.site.Expected(2, "720p", 0, 60); !bytes.Equal(e.readDone(2), want) {
		t.Fatal("content differs")
	}
}

// A video page that Cloudflare turns away is asked again later.
func TestBlockedPageIsAskedAgain(t *testing.T) {
	shortWaits(t, 4)
	e := newTestEnv(t, "series", func(s *mocksite.Site) { s.BlockPages(10) }) // two failed resolves
	id := e.addVideos(2)[0]
	e.waitFor("waiting message", func() bool {
		it := e.item(id)
		return strings.Contains(it.Detail, "Trying again") && strings.Contains(it.Detail, "Cloudflare")
	})
	e.waitComplete(id)
	if want := e.site.Expected(2, "720p", 0, 20); !bytes.Equal(e.readDone(2), want) {
		t.Fatal("content differs")
	}
}

// Hybrid checks the playlists of waiting rows ahead of time; a refusal there
// must not fail rows that never got to start.
func TestHybridServerCheckSurvivesABlock(t *testing.T) {
	shortWaits(t, 4)
	e := newTestEnv(t, "hybrid", func(s *mocksite.Site) { s.BlockPages(10) })
	ids := e.addVideos(2, 3)
	e.waitComplete(ids...)
	for _, id := range ids {
		if e.item(id).Error != "" {
			t.Fatalf("row %d: %s", id, e.item(id).Error)
		}
	}
}

// 429 is asked again with growing pauses, and the download still finishes.
func TestRateLimitedSegmentsBackOffAndFinish(t *testing.T) {
	e := newTestEnv(t, "series", func(s *mocksite.Site) { s.Limit429(4) })
	id := e.addVideos(2)[0]
	e.waitComplete(id)
	if want := e.site.Expected(2, "720p", 0, 20); !bytes.Equal(e.readDone(2), want) {
		t.Fatal("content differs")
	}
}

// Replacing the cookie never swaps it in under a download that is running:
// the running and waiting rows are paused and the cookie is replaced. They
// stay paused until the person starts them. Rows the person had paused stay
// paused too.
func TestReplacingTheCookiePausesRunningDownloadsAndLeavesThemPaused(t *testing.T) {
	e := newTestEnv(t, "hybrid", func(s *mocksite.Site) { s.Lines = 62; s.Delay = 15 * time.Millisecond })
	ids := e.addVideos(2, 3, 4)
	e.m.Pause([]int64{ids[2]}) // the person paused this one themselves
	e.waitFor("two downloading", func() bool {
		return e.item(ids[0]).Status == "downloading" && e.item(ids[1]).Status == "downloading"
	})
	e.waitFor("some progress", func() bool { return e.item(ids[0]).Percent > 5 && e.item(ids[1]).Percent > 5 })

	e.site.AlsoAccept("session=fresh")
	n, err := e.m.SetCookie("session=fresh")
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Errorf("paused %d rows, want the 2 that were running", n)
	}
	if got := e.m.Settings().Cookie; got != "session=fresh" {
		t.Fatalf("cookie %q", got)
	}
	for _, id := range ids {
		if st := e.item(id).Status; st != "paused" {
			t.Errorf("row %d is %q after the cookie was replaced, want paused", id, st)
		}
		if e.busy(id) {
			t.Errorf("row %d is still winding down after the call returned", id)
		}
	}
	// nothing starts them again by itself
	uses := e.site.CookieUses("session=good") + e.site.CookieUses("session=fresh")
	time.Sleep(300 * time.Millisecond)
	if e.site.CookieUses("session=good")+e.site.CookieUses("session=fresh") != uses {
		t.Error("a paused row kept downloading")
	}
	for _, id := range ids {
		if st := e.item(id).Status; st != "paused" {
			t.Errorf("row %d became %q on its own", id, st)
		}
	}

	// the person starts them: they use the new cookie only
	old := e.site.CookieUses("session=good")
	e.m.Start(ids[:2])
	e.waitComplete(ids[0])
	e.waitComplete(ids[1])
	if after := e.site.CookieUses("session=good"); after != old {
		t.Errorf("%d requests still used the old cookie", after-old)
	}
	if e.site.CookieUses("session=fresh") == 0 {
		t.Error("the new cookie was never used")
	}
	for _, vid := range []int{2, 3} {
		if want := e.site.Expected(vid, "720p", 0, 60); !bytes.Equal(e.readDone(vid), want) {
			t.Errorf("video %d: content differs after the restart", vid)
		}
	}

	// saving a different cookie from the Settings page pauses them the same way
	e.m.Start([]int64{ids[2]})
	e.waitFor("third video downloading", func() bool { return e.item(ids[2]).Status == "downloading" && e.item(ids[2]).Percent > 5 })
	e.site.AlsoAccept("session=third")
	s := e.m.Settings()
	s.Cookie = "session=third"
	if err := e.m.SetSettings(s); err != nil {
		t.Fatal(err)
	}
	if st := e.item(ids[2]).Status; st != "paused" {
		t.Errorf("settings save: the running row is %q, want paused", st)
	}
	if e.busy(ids[2]) {
		t.Error("settings save: the row is still winding down")
	}
}

// With nothing running, replacing the cookie only changes the cookie.
func TestReplacingTheCookieWithNothingRunningPausesNothing(t *testing.T) {
	e := newTestEnv(t, "hybrid", nil)
	ids := e.addVideos(2)
	e.m.Pause(ids)
	n, err := e.m.SetCookie("session=other")
	if err != nil || n != 0 {
		t.Fatalf("paused %d, err %v", n, err)
	}
	time.Sleep(150 * time.Millisecond)
	if st := e.item(ids[0]).Status; st != "paused" {
		t.Errorf("paused row became %q", st)
	}
	if _, err := e.m.SetCookie("   "); err == nil {
		t.Error("an empty cookie must be refused")
	}
	if e.m.Settings().Cookie != "session=other" {
		t.Error("a refused cookie changed the saved one")
	}
}
