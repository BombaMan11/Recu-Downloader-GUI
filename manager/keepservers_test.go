package manager

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"Recurbate/internal/mocksite"
)

// restartPartWay pauses id part-way, closes the program, has the site hand out
// the other server from now on and opens the program again.
func restartPartWay(e *testEnv, id int64) (oldServer string) {
	e.t.Helper()
	e.waitFor("some progress", func() bool { return e.item(id).Percent > 20 })
	e.m.Pause([]int64{id})
	e.waitFor("worker to stop", func() bool { return !e.busy(id) })
	oldServer = e.item(id).Server
	e.m.Shutdown()
	e.site.MoveVideos() // a page read now would hand out the other server
	e.m = e.openManager()
	return oldServer
}

// A row's playlist is kept as its .m3u8: after a restart the row opens paused
// with its server and the file. Start then reads a new playlist and carries on
// from the same piece.
func TestPlaylistsSurviveARestart(t *testing.T) {
	e := newTestEnv(t, "series", func(s *mocksite.Site) { s.Lines = 62; s.Delay = 12 * time.Millisecond })
	id := e.addVideos(2)[0]
	old := restartPartWay(e, id)
	if it := e.item(id); it.Server != old || it.Status != "paused" {
		t.Fatalf("after restart: server %q status %s, want %q and paused", it.Server, it.Status, old)
	}
	if !pathExists(e.path("", e.site.Filename(2)+".m3u8")) {
		t.Fatal("no .m3u8 for the unfinished row")
	}
	before := e.site.Resolves()
	e.m.Start([]int64{id})
	e.waitComplete(id)
	if n := e.site.Resolves() - before; n != 1 {
		t.Errorf("read the page %d times after the restart, want 1 (Start)", n)
	}
	if want := e.site.Expected(2, "720p", 0, 60); !bytes.Equal(e.readDone(2), want) {
		t.Fatal("file is not whole after the restart")
	}
	e.waitFor("the finished row's .m3u8 deleted", func() bool { return !pathExists(e.path("", e.site.Filename(2)+".m3u8")) })
}

// The .m3u8 is written within a second, so a crash (no normal close) loses nothing.
func TestPlaylistsSurviveACrash(t *testing.T) {
	e := newTestEnv(t, "series", func(s *mocksite.Site) { s.Lines = 62; s.Delay = 12 * time.Millisecond })
	e.setSettings(func(s *Settings) { s.AutoStart = false })
	id := e.addVideos(2)[0]
	e.waitFor("links read", func() bool { return e.item(id).Server != "" })
	old := e.item(id).Server
	e.waitFor("playlist saved", func() bool { return pathExists(e.path("", e.site.Filename(2)+".m3u8")) })
	time.Sleep(1100 * time.Millisecond) // the queue (with the playlist's place) is saved every second too
	m2, err := New(e.dir, "")           // the next start after a crash: only what is on disk
	if err != nil {
		t.Fatal(err)
	}
	for _, it := range m2.Snapshot().Items {
		if it.ID == id && it.Server != old {
			t.Fatalf("server after the crash: %q, want %q", it.Server, old)
		}
	}
}

// Paused rows (added with Start new rows off) get their playlist straight
// away (their server shows); starting them reads a new one.
func TestPausedRowsAreReadAhead(t *testing.T) {
	e := newTestEnv(t, "series", func(s *mocksite.Site) { s.Lines = 62; s.Delay = 12 * time.Millisecond })
	e.setSettings(func(s *Settings) { s.AutoStart = false })
	ids := e.addVideos(2, 3)
	e.waitFor("both paused rows read ahead", func() bool {
		return e.item(ids[0]).Server != "" && e.item(ids[1]).Server != "" && !e.busy(ids[0]) && !e.busy(ids[1])
	})
	if st := e.item(ids[0]).Status; st != "paused" {
		t.Fatalf("status %s, want still paused", st)
	}
	before := e.site.Resolves()
	e.m.Start(ids)
	e.waitComplete(ids...)
	if n := e.site.Resolves() - before; n != 2 {
		t.Errorf("starting read %d pages, want 2", n)
	}
}

// Removing a row deletes its .m3u8; Get playlist reads the page again even
// when the row has links, and the file is written again.
func TestPlaylistFileRemoveAndGetPlaylist(t *testing.T) {
	e := newTestEnv(t, "series", func(s *mocksite.Site) { s.Lines = 62; s.Delay = 12 * time.Millisecond })
	e.setSettings(func(s *Settings) { s.AutoStart = false })
	ids := e.addVideos(2, 3)
	a, b := e.path("", e.site.Filename(2)+".m3u8"), e.path("", e.site.Filename(3)+".m3u8")
	e.waitFor("playlists saved", func() bool { return pathExists(a) && pathExists(b) })

	before := e.site.Resolves()
	e.m.SaveM3U8(ids[:1])
	e.waitFor("a fresh read", func() bool { return e.site.Resolves() == before+1 })
	e.site.MoveVideos()
	e.m.SaveM3U8(ids[:1])
	e.waitFor("the file rewritten with the new server", func() bool {
		data, _ := os.ReadFile(a)
		return e.item(ids[0]).Server == e.site.ServerOf(2) && bytes.Contains(data, []byte(e.site.ServerOf(2)))
	})

	e.m.Remove(ids[1:], false)
	e.waitFor("the removed row's playlist deleted", func() bool { return !pathExists(b) })
}

// Saved links that have expired do no harm: Start reads a new playlist first.
func TestStartReplacesExpiredSavedLinks(t *testing.T) {
	e := newTestEnv(t, "series", func(s *mocksite.Site) {
		s.Lines = 62
		s.Delay = 12 * time.Millisecond
		s.SetExpiredStatus(403)
	})
	e.setSettings(func(s *Settings) { s.AutoStart = false })
	id := e.addVideos(2)[0]
	e.waitFor("links read", func() bool { return e.item(id).Server != "" && !e.busy(id) })
	e.site.ExpireAll()
	before := e.site.Resolves()
	e.m.Start([]int64{id})
	e.waitComplete(id)
	if n := e.site.Resolves() - before; n != 1 {
		t.Errorf("read the page %d times, want 1", n)
	}
	if want := e.site.Expected(2, "720p", 0, 60); !bytes.Equal(e.readDone(2), want) {
		t.Fatal("content differs")
	}
}

// Links saved by v3.4.8 to v3.5.00 (the links folder) are turned into .m3u8
// files on the first start, and the old folder is deleted.
func TestOldSavedLinksAreConverted(t *testing.T) {
	e := newTestEnv(t, "series", func(s *mocksite.Site) { s.Lines = 62; s.Delay = 12 * time.Millisecond })
	e.setSettings(func(s *Settings) { s.AutoStart = false })
	id := e.addVideos(2)[0]
	e.waitFor("links read", func() bool { return e.item(id).Server != "" && !e.busy(id) })
	old := e.item(id).Server
	e.m.mu.Lock()
	r := e.m.items[0].res
	stored, _ := json.Marshal(storedLinks{URL: e.m.items[0].URL, PlaylistURL: r.PlaylistURL, M3U8: string(r.Playlist.M3u8)})
	e.m.mu.Unlock()
	e.m.Shutdown()
	m3u8 := e.path("", e.site.Filename(2)+".m3u8")
	os.Remove(m3u8) // as v3.5.00 left it: no .m3u8, a file in the links folder
	dir := filepath.Join(e.dir, "links")
	os.MkdirAll(dir, 0700)
	os.WriteFile(filepath.Join(dir, fmt.Sprintf("%d.json", id)), stored, 0600)
	e.site.MoveVideos()

	e.m = e.openManager()
	if got := e.item(id).Server; got != old {
		t.Fatalf("server from the old links: %q, want %q", got, old)
	}
	e.waitFor("the .m3u8 written", func() bool { return pathExists(m3u8) })
	if pathExists(dir) {
		t.Error("the old links folder was left behind")
	}
}
