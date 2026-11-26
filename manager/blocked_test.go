package manager

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"Recurbate/internal/mocksite"
)

func slowSite(s *mocksite.Site) { s.Lines = 62; s.Delay = 12 * time.Millisecond }

// A short refusal of the pieces (less than the 5 tries, 1 s apart, of the
// original) does not stop the row and reads no page: the download carries on
// with the links it has. (Reading the page again overnight, with a cookie that
// had run out, is what showed Site refused.)
func TestShortRefusalIsRetriedWithoutReadingThePage(t *testing.T) {
	e := newTestEnv(t, "series", slowSite)
	id := e.addVideos(2)[0]
	e.waitFor("some progress", func() bool { return e.item(id).Percent > 20 })
	e.site.BlockSegments(true)
	time.Sleep(1500 * time.Millisecond)
	e.site.BlockSegments(false)
	e.waitComplete(id)
	if want := e.site.Expected(2, "720p", 0, 60); !bytes.Equal(e.readDone(2), want) {
		t.Fatal("content differs after the refusal")
	}
	if n := e.site.Resolves(); n != 1 {
		t.Fatalf("the page was read %d times, want 1", n)
	}
}

// Expired links (a CDN that signs them answers 403) stop the row like the
// original: an error, the position kept, no page read. Start reads a new
// playlist and carries on from the same piece.
func TestExpiredLinksStopTheRowAndStartReadsANewPlaylist(t *testing.T) {
	e := newTestEnv(t, "series", func(s *mocksite.Site) { slowSite(s); s.SetExpiredStatus(403) })
	id := e.addVideos(2)[0]
	e.waitFor("some progress", func() bool { return e.item(id).Percent > 20 })
	e.site.ExpireAll()
	e.waitStatus(id, StatusError)
	if n := e.site.Resolves(); n != 1 {
		t.Fatalf("the page was read %d times before Start, want 1", n)
	}
	e.waitFor("the refused playlist to be dropped", func() bool { return !e.item(id).M3U8 })
	e.m.Start([]int64{id})
	e.waitComplete(id)
	if want := e.site.Expected(2, "720p", 0, 60); !bytes.Equal(e.readDone(2), want) {
		t.Fatal("content differs after the links expired")
	}
	if n := e.site.Resolves(); n != 2 {
		t.Fatalf("video was resolved %d times, want 2", n)
	}
}

// A block that stays ends in a clear message (no HTML dump), and pressing
// Start later carries on from where it stopped.
func TestBlockThatStaysGivesAClearErrorAndResumes(t *testing.T) {
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

// A video page the site refuses fails that row only, like the original; the
// list moves on, and Start on the row reads it again.
func TestRefusedPageFailsOnlyThatRow(t *testing.T) {
	e := newTestEnv(t, "hybrid", func(s *mocksite.Site) { s.BlockPages(5) }) // one read (5 tries)
	ids := e.addVideos(2, 3)
	e.waitStatus(ids[0], StatusError)
	if !strings.Contains(e.item(ids[0]).Error, "Cloudflare") {
		t.Errorf("error %q", e.item(ids[0]).Error)
	}
	e.waitComplete(ids[1])
	e.m.Start(ids[:1])
	e.waitComplete(ids[0])
}

// 429 is asked again every 100 ms, like the original, and the download finishes.
func TestRateLimitedSegmentsAreAskedAgain(t *testing.T) {
	e := newTestEnv(t, "series", func(s *mocksite.Site) { s.Limit429(4) })
	id := e.addVideos(2)[0]
	e.waitComplete(id)
	if want := e.site.Expected(2, "720p", 0, 20); !bytes.Equal(e.readDone(2), want) {
		t.Fatal("content differs")
	}
}

// Replacing the cookie leaves the list alone, from the Replace cookie button or
// a Settings save: nothing is paused, the rows finish, and the next requests
// use the new cookie. An empty cookie is refused and changes nothing.
func TestReplacingTheCookieDoesNotPauseTheList(t *testing.T) {
	e := newTestEnv(t, "hybrid", func(s *mocksite.Site) { s.Lines = 62; s.Delay = 15 * time.Millisecond })
	ids := e.addVideos(2, 3, 4)
	e.waitFor("two downloading", func() bool {
		return e.item(ids[0]).Status == "downloading" && e.item(ids[1]).Status == "downloading"
	})

	e.site.AlsoAccept("session=fresh")
	if err := e.m.SetCookie("session=fresh"); err != nil {
		t.Fatal(err)
	}
	if got := e.m.Settings().Cookie; got != "session=fresh" {
		t.Fatalf("cookie %q", got)
	}
	e.site.AlsoAccept("session=third")
	s := e.m.Settings()
	s.Cookie = "session=third"
	if err := e.m.SetSettings(s); err != nil {
		t.Fatal(err)
	}
	for _, id := range ids {
		if st := e.item(id).Status; st == "paused" {
			t.Errorf("row %d was paused by a cookie change", id)
		}
	}
	e.waitComplete(ids...)
	if e.site.CookieUses("session=third") == 0 {
		t.Error("the new cookie was never used")
	}
	if err := e.m.SetCookie("   "); err == nil {
		t.Error("an empty cookie was accepted")
	}
	if e.m.Settings().Cookie != "session=third" {
		t.Error("a refused cookie changed the saved one")
	}
}
