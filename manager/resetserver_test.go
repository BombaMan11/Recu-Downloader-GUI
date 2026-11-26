package manager

import (
	"testing"
	"time"

	"Recurbate/internal/mocksite"
)

// Reset server makes the selected waiting rows forget their server and read
// their page again; downloading rows and rows not selected keep theirs.
func TestResetServer(t *testing.T) {
	e := newTestEnv(t, "hybrid", func(s *mocksite.Site) { s.Lines = 122; s.Delay = 15 * time.Millisecond })
	ids := e.addVideos(2, 4, 6) // all on the same server: the first downloads, the others wait
	e.waitFor("waiting rows to show their server", func() bool {
		return e.item(ids[0]).Status == "downloading" && e.item(ids[1]).Server != "" && e.item(ids[2]).Server != "" && !e.busy(ids[1]) && !e.busy(ids[2])
	})
	old := e.item(ids[0]).Server
	e.site.MoveVideos() // a new read now hands out the other server

	if n := e.m.ResetServer([]int64{ids[0], ids[2]}); n != 1 {
		t.Fatalf("reset %d rows, want 1 (the downloading row is left alone)", n)
	}
	e.waitFor("the reset row to read its new server", func() bool { s := e.item(ids[2]).Server; return s != "" && s != old })
	if e.item(ids[0]).Server != old || e.item(ids[1]).Server != old {
		t.Errorf("rows that were not reset changed server: %s %s", e.item(ids[0]).Server, e.item(ids[1]).Server)
	}
}

// A paused row forgets its server and links, and is read again straight away
// (paused rows are read ahead): it gets the new server and stays paused.
func TestResetServerOnAPausedRow(t *testing.T) {
	e := newTestEnv(t, "series", func(s *mocksite.Site) { s.Lines = 122; s.Delay = 15 * time.Millisecond })
	id := e.addVideos(2)[0]
	e.waitFor("downloading", func() bool { return e.item(id).Status == "downloading" })
	old := e.item(id).Server
	e.m.Pause([]int64{id})
	e.waitFor("worker to stop", func() bool { return !e.busy(id) })
	e.site.MoveVideos()
	if n := e.m.ResetServer([]int64{id}); n != 1 {
		t.Fatalf("reset %d rows, want 1", n)
	}
	e.waitFor("the new server", func() bool { s := e.item(id).Server; return s != "" && s != old && !e.busy(id) })
	if st := e.item(id).Status; st != "paused" {
		t.Errorf("status %s, want still paused", st)
	}
}
