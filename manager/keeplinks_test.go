package manager

import (
	"bytes"
	"testing"
	"time"

	"Recurbate/internal/mocksite"
)

// Start, Start all and Force start read a new playlist for the rows (like the
// original read every page on each run) and carry on from the same piece.
func TestStartReadsNewPlaylists(t *testing.T) {
	for _, how := range []string{"start", "startall", "force"} {
		e := newTestEnv(t, "hybrid", func(s *mocksite.Site) { s.Lines = 62; s.Delay = 15 * time.Millisecond })
		ids := e.addVideos(2, 3) // two different servers, both start
		e.waitFor("both downloading", func() bool {
			return e.item(ids[0]).Status == "downloading" && e.item(ids[1]).Status == "downloading"
		})
		oldA, oldB := e.item(ids[0]).Server, e.item(ids[1]).Server
		e.m.PauseAll()
		e.waitFor("workers to stop", func() bool { return !e.busy(ids[0]) && !e.busy(ids[1]) })
		e.site.MoveVideos() // a page read now hands out the other server
		before := e.site.Resolves()
		switch how {
		case "start":
			e.m.Start(ids)
		case "startall":
			e.m.StartAll()
		case "force":
			e.m.ForceStart(ids)
		}
		e.waitComplete(ids...)
		if n := e.site.Resolves() - before; n != 2 {
			t.Errorf("%s: read %d pages, want 2", how, n)
		}
		if e.item(ids[0]).Server == oldA || e.item(ids[1]).Server == oldB {
			t.Errorf("%s: kept the old servers", how)
		}
		for _, v := range []int{2, 3} {
			if want := e.site.Expected(v, "720p", 0, 60); !bytes.Equal(e.readDone(v), want) {
				t.Fatalf("%s: video %d is damaged", how, v)
			}
		}
		e.noSiteProblems()
	}
}
