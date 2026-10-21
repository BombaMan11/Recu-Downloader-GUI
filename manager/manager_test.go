package manager

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"Recurbate/internal/mocksite"
)

func TestFullDownload(t *testing.T) {
	e := newTestEnv(t, "series", nil)
	id := e.addVideos(2)[0]
	e.waitComplete(id)

	got := e.readDone(2)
	if want := e.site.Expected(2, "720p", 0, 20); !bytes.Equal(got, want) {
		t.Fatalf("downloaded file differs: got %d bytes, want %d", len(got), len(want))
	}
	it := e.item(id)
	if it.Server != e.site.ServerOf(2) {
		t.Fatalf("server = %q, want %q", it.Server, e.site.ServerOf(2))
	}
	if it.Name != e.site.Filename(2) || it.Percent != 100 {
		t.Fatalf("name/percent = %q / %v", it.Name, it.Percent)
	}
	// unfinished and finished videos share the program folder: only the finished file is left
	if matches, _ := filepath.Glob(e.path("", "*.ts")); len(matches) != 1 {
		t.Fatalf("expected exactly the finished video, found %v", matches)
	}
	e.noSiteProblems()
}

func TestDefaultFolderIsTheProgramFolderWithoutSubfolders(t *testing.T) {
	e := newTestEnv(t, "series", nil)
	ids := e.addVideos(2)
	e.waitComplete(ids...)
	e.m.SaveM3U8(ids[:1])
	e.waitFor("playlist saved", func() bool { return e.item(ids[0]).M3U8 })
	if !pathExists(e.path("", e.site.Filename(2)+".ts")) || !pathExists(e.path("", e.site.Filename(2)+".m3u8")) {
		t.Fatal("video and playlist should sit directly in the program folder")
	}
	for _, d := range []string{"Done", "Incomplete", "M3U8"} {
		if pathExists(e.path(d, "")) {
			t.Fatalf("unexpected subfolder %s", d)
		}
	}
}

func TestMaxResolution(t *testing.T) {
	for _, tc := range []struct {
		max  int
		want string
	}{{360, "360p"}, {0, "720p"}, {1080, "720p"}, {200, "360p"}} {
		tc := tc
		t.Run(tc.want, func(t *testing.T) {
			e := newTestEnv(t, "series", nil)
			e.setSettings(func(s *Settings) { s.MaxResolution = tc.max })
			id := e.addVideos(2)[0]
			e.waitComplete(id)
			if want := e.site.Expected(2, tc.want, 0, 20); !bytes.Equal(e.readDone(2), want) {
				t.Fatalf("max=%d did not pick %s", tc.max, tc.want)
			}
		})
	}
}

func TestSectionOnly(t *testing.T) {
	e := newTestEnv(t, "series", nil)
	// 0:30 - 1:00 of a 2:00 video = 25% - 50% of the 20 segments = entries 5..9
	id := e.add(`["` + e.site.VideoURL(2) + `","0:30","1:00","2:00"]`)[0]
	e.waitComplete(id)
	if want := e.site.Expected(2, "720p", 5, 10); !bytes.Equal(e.readDone(2), want) {
		t.Fatal("section download has the wrong content")
	}
	if it := e.item(id); it.StartSec != 30 || it.EndSec != 60 || it.DurSec != 30 {
		t.Fatalf("start/end/duration = %v / %v / %v", it.StartSec, it.EndSec, it.DurSec)
	}
}

// Start and end without the video length: the length is read from the
// playlist, which is 20 segments of 4 s = 80 s, so 0:20-0:40 is entries 5..9.
func TestSectionWithoutTotalLength(t *testing.T) {
	e := newTestEnv(t, "series", nil)
	id := e.add(`["` + e.site.VideoURL(2) + `","0:20","0:40"]`)[0]
	e.waitComplete(id)
	if want := e.site.Expected(2, "720p", 5, 10); !bytes.Equal(e.readDone(2), want) {
		t.Fatal("section download has the wrong content")
	}
	if it := e.item(id); it.StartSec != 20 || it.EndSec != 40 || it.DurSec != 20 {
		t.Fatalf("start/end/duration = %v / %v / %v", it.StartSec, it.EndSec, it.DurSec)
	}
	e.m.mu.Lock()
	ref := e.m.items[0].SectionRef
	e.m.mu.Unlock()
	if len(ref) != 3 || ref[2] != "00:01:20" {
		t.Fatalf("the found length was not remembered: %v", ref)
	}
}

func TestSectionPastTheEndOfTheVideoIsAnError(t *testing.T) {
	e := newTestEnv(t, "series", nil)
	id := e.add(`["` + e.site.VideoURL(2) + `","5:00","6:00"]`)[0]
	e.waitFor("an error", func() bool { return e.item(id).Status == "error" })
	if msg := e.item(id).Error; !strings.Contains(msg, "00:01:20") {
		t.Fatalf("error should name the real length: %q", msg)
	}
}

// A whole video starts at 00:00:00 and its end is looked up without starting
// the download, also after a restart.
func TestWholeVideoLengthIsLookedUp(t *testing.T) {
	e := newTestEnv(t, "series", nil)
	e.setSettings(func(s *Settings) { s.AutoStart = false })
	id := e.add(e.site.VideoURL(2))[0]
	e.waitFor("the length", func() bool { return e.item(id).DurSec == 80 })
	it := e.item(id)
	if it.StartSec != 0 || it.EndSec != 80 || it.Status != "paused" {
		t.Fatalf("start/end/status = %v / %v / %s", it.StartSec, it.EndSec, it.Status)
	}
	e.m.Shutdown()
	m2, err := New(e.dir, "")
	if err != nil {
		t.Fatal(err)
	}
	defer m2.Shutdown()
	if got := m2.Snapshot().Items[0].DurSec; got != 80 {
		t.Fatalf("length not kept after a restart: %v", got)
	}
}

func TestSameSectionWrittenDifferentlyIsADuplicate(t *testing.T) {
	e := newTestEnv(t, "series", nil)
	e.setSettings(func(s *Settings) { s.AutoStart = false })
	u := e.site.VideoURL(2)
	e.add(`["` + u + `","0:55","1:10","1:30"]`)
	added, problems := e.m.Add(`["` + u + `","00:00:55","00:01:10"]`)
	if added != 0 || len(problems) != 1 {
		t.Fatalf("added %d, problems %v", added, problems)
	}
	if added, _ := e.m.Add(`["` + u + `","0:55","1:11"]`); added != 1 {
		t.Fatal("a different end is a different part")
	}
}

func TestSeriesRunsOneAtATimeTopDown(t *testing.T) {
	e := newTestEnv(t, "series", func(s *mocksite.Site) { s.Delay = 15 * time.Millisecond })
	e.setSettings(func(s *Settings) { s.AutoStart = false })
	ids := e.addVideos(1, 2, 3)
	// table order: 1, 2, 3 -> move 3 to the top: 3, 1, 2
	e.m.Move([]int64{ids[2]}, "top")
	smp := e.sample()
	e.m.StartAll()
	e.waitComplete(ids...)
	hist := smp.finish()
	if n := maxWorking(hist); n != 1 {
		t.Fatalf("series mode ran %d downloads at once", n)
	}
	if got, want := startOrder(hist), []int64{ids[2], ids[0], ids[1]}; !reflect.DeepEqual(got, want) {
		t.Fatalf("start order %v, want %v (top to bottom)", got, want)
	}
	e.noSiteProblems()
}

func TestNormalRunsEverythingAtOnce(t *testing.T) {
	e := newTestEnv(t, "normal", func(s *mocksite.Site) { s.Delay = 25 * time.Millisecond })
	smp := e.sample()
	ids := e.addVideos(1, 2, 3)
	e.waitComplete(ids...)
	hist := smp.finish()
	if !together(hist, ids...) {
		t.Fatalf("normal mode never had all three downloading together (max %d)", maxWorking(hist))
	}
}

func TestNormalModeMaxSimultaneous(t *testing.T) {
	e := newTestEnv(t, "normal", func(s *mocksite.Site) { s.Delay = 15 * time.Millisecond })
	e.setSettings(func(s *Settings) { s.MaxParallel = 2 })
	smp := e.sample()
	ids := e.addVideos(1, 2, 3, 4)
	e.waitComplete(ids...)
	if n := maxWorking(smp.finish()); n != 2 {
		t.Fatalf("max simultaneous = %d, want 2", n)
	}
}

func TestHybridOnePerServer(t *testing.T) {
	e := newTestEnv(t, "hybrid", func(s *mocksite.Site) { s.Delay = 20 * time.Millisecond })
	// videos 2 and 4 share cdn 0, video 1 is on cdn 1
	smp := e.sample()
	ids := e.addVideos(2, 4, 1)
	e.waitComplete(ids...)
	hist := smp.finish()
	if together(hist, ids[0], ids[1]) {
		t.Fatal("two downloads from the same server ran at the same time")
	}
	if !together(hist, ids[0], ids[2]) {
		t.Fatal("downloads from different servers did not run in parallel")
	}
	// the one above on the same server goes first
	order := startOrder(hist)
	pos := map[int64]int{}
	for i, id := range order {
		pos[id] = i
	}
	if pos[ids[0]] > pos[ids[1]] {
		t.Fatalf("row 4 started before the row above it on the same server: %v", order)
	}
	for _, v := range []int{2, 4, 1} {
		if want := e.site.Expected(v, "720p", 0, 20); !bytes.Equal(e.readDone(v), want) {
			t.Fatalf("video %d content differs", v)
		}
	}
	e.noSiteProblems()
}

func TestHybridShowsServerAndNextBeforeDownloading(t *testing.T) {
	e := newTestEnv(t, "hybrid", func(s *mocksite.Site) { s.Lines = 62; s.Delay = 15 * time.Millisecond })
	ids := e.addVideos(2, 4, 6) // all on cdn 0
	e.waitFor("first row downloading", func() bool { return e.item(ids[0]).Status == "downloading" })
	e.waitFor("waiting rows to show their server and place in line", func() bool {
		b, c := e.item(ids[1]), e.item(ids[2])
		return b.Server == e.site.ServerOf(4) && b.Hint == "Next" && b.Next &&
			c.Server == e.site.ServerOf(6) && c.Hint == "#2" && c.Status == "queued"
	})
	if h := e.item(ids[0]).Hint; h != "" {
		t.Fatalf("running row has hint %q", h)
	}
}

func TestPauseAndResumeKeepsFileIntact(t *testing.T) {
	e := newTestEnv(t, "series", func(s *mocksite.Site) { s.Lines = 62; s.Delay = 12 * time.Millisecond })
	id := e.addVideos(2)[0]
	e.waitFor("20% done", func() bool { return e.item(id).Percent > 20 })
	e.m.Pause([]int64{id})
	e.waitFor("worker to stop", func() bool { return !e.busy(id) })
	if e.item(id).Status != "paused" {
		t.Fatalf("status %q after pause", e.item(id).Status)
	}
	off, size := e.offset(id)
	if off <= 0 || off >= 60 || size != int64(off)*int64(e.site.SegSize) {
		t.Fatalf("recorded offset=%d size=%d", off, size)
	}
	if st, err := os.Stat(e.path("", e.site.Filename(2)+".ts")); err != nil || st.Size() != size {
		t.Fatalf("partial file: %v (size %v, want %d)", err, st, size)
	}
	// stays paused, nothing restarts it
	time.Sleep(1200 * time.Millisecond)
	if e.item(id).Status != "paused" || e.busy(id) {
		t.Fatal("a paused row restarted on its own")
	}
	e.m.Start([]int64{id})
	e.waitComplete(id)
	if want := e.site.Expected(2, "720p", 0, 60); !bytes.Equal(e.readDone(2), want) {
		t.Fatal("file is not identical after pause + resume")
	}
	e.noSiteProblems()
}

func TestRestartResumesAndTrimsInterruptedWrite(t *testing.T) {
	e := newTestEnv(t, "series", func(s *mocksite.Site) { s.Lines = 62; s.Delay = 12 * time.Millisecond })
	id := e.addVideos(2)[0]
	e.waitFor("30% done", func() bool { return e.item(id).Percent > 30 })
	e.m.Shutdown() // what closing the window does

	partial := e.path("", e.site.Filename(2)+".ts")
	f, err := os.OpenFile(partial, os.O_APPEND|os.O_WRONLY, 0666)
	if err != nil {
		t.Fatal(err)
	}
	f.Write([]byte("junk from an interrupted write"))
	f.Close()

	e.m = e.openManager() // next launch
	if got := e.item(id).Status; got != "queued" && got != "resolving" && got != "downloading" {
		t.Fatalf("after restart the row is %q, want it to resume", got)
	}
	e.waitComplete(id)
	if want := e.site.Expected(2, "720p", 0, 60); !bytes.Equal(e.readDone(2), want) {
		t.Fatal("file differs after restart + resume")
	}
}

func TestRestartKeepsPausedRowsPausedAndOrderAndSettings(t *testing.T) {
	e := newTestEnv(t, "series", nil)
	e.setSettings(func(s *Settings) { s.AutoStart = false; s.Theme = "dark"; s.MaxResolution = 480 })
	ids := e.addVideos(1, 2, 3)
	e.m.Move([]int64{ids[2]}, "top")
	e.m.SetActive([]int64{ids[0]}, false)
	e.m.Shutdown()

	e.m = e.openManager()
	snap := e.m.Snapshot()
	var got []int64
	for _, it := range snap.Items {
		got = append(got, it.ID)
		if it.Status != "paused" {
			t.Fatalf("row %d is %q after restart, want paused", it.ID, it.Status)
		}
	}
	if want := []int64{ids[2], ids[0], ids[1]}; !reflect.DeepEqual(got, want) {
		t.Fatalf("order after restart %v, want %v", got, want)
	}
	if e.item(ids[0]).Active {
		t.Fatal("inactive flag was not saved")
	}
	s := e.m.Settings()
	if s.Theme != "dark" || s.MaxResolution != 480 || s.Cookie != e.site.Cookie || s.UserAgent != e.site.UA || s.Mode != "series" {
		t.Fatalf("settings were not persisted: %+v", s)
	}
}

func TestInactiveRowsNeverStart(t *testing.T) {
	e := newTestEnv(t, "series", func(s *mocksite.Site) { s.Delay = 10 * time.Millisecond })
	e.setSettings(func(s *Settings) { s.AutoStart = false })
	ids := e.addVideos(1, 2)
	e.m.SetActive([]int64{ids[1]}, false)

	// even Force Start refuses inactive rows
	if skipped := e.m.ForceStart([]int64{ids[1]}); skipped != 1 {
		t.Fatalf("force start skipped %d inactive rows, want 1", skipped)
	}
	e.m.StartAll()
	e.waitComplete(ids[0])
	time.Sleep(700 * time.Millisecond)
	if st := e.item(ids[1]).Status; st != "queued" || e.busy(ids[1]) {
		t.Fatalf("inactive row went to %q", st)
	}
	if e.item(ids[1]).Hint != "Inactive" {
		t.Fatalf("hint = %q", e.item(ids[1]).Hint)
	}
	e.m.SetActive([]int64{ids[1]}, true)
	e.waitComplete(ids[1])
}

func TestDeactivatingARunningRowStopsIt(t *testing.T) {
	e := newTestEnv(t, "series", func(s *mocksite.Site) { s.Lines = 62; s.Delay = 12 * time.Millisecond })
	id := e.addVideos(2)[0]
	e.waitFor("downloading", func() bool { return e.item(id).Percent > 5 })
	e.m.SetActive([]int64{id}, false)
	e.waitFor("worker to stop", func() bool { return !e.busy(id) })
	time.Sleep(300 * time.Millisecond)
	if st := e.item(id).Status; st != "queued" {
		t.Fatalf("status %q, want queued (inactive)", st)
	}
	p := e.item(id).Percent
	time.Sleep(300 * time.Millisecond)
	if e.item(id).Percent != p {
		t.Fatal("inactive row kept downloading")
	}
	e.m.SetActive([]int64{id}, true)
	e.waitComplete(id)
	if want := e.site.Expected(2, "720p", 0, 60); !bytes.Equal(e.readDone(2), want) {
		t.Fatal("content differs after deactivate / activate")
	}
}

func TestForceStartRunsAlongsideSeries(t *testing.T) {
	e := newTestEnv(t, "series", func(s *mocksite.Site) { s.Lines = 62; s.Delay = 12 * time.Millisecond })
	ids := e.addVideos(1, 2)
	e.waitFor("first row downloading", func() bool { return e.item(ids[0]).Status == "downloading" })
	if st := e.item(ids[1]).Status; st != "queued" {
		t.Fatalf("second row is %q in series mode", st)
	}
	if e.m.ForceStart([]int64{ids[1]}) != 0 {
		t.Fatal("unexpected skip")
	}
	e.waitFor("both downloading", func() bool {
		return e.item(ids[0]).Status == "downloading" && e.item(ids[1]).Status == "downloading"
	})
	if !e.item(ids[1]).Forced {
		t.Fatal("forced flag missing")
	}
	e.waitComplete(ids...)
	for _, v := range []int{1, 2} {
		if want := e.site.Expected(v, "720p", 0, 60); !bytes.Equal(e.readDone(v), want) {
			t.Fatalf("video %d differs", v)
		}
	}
}

func TestForceStartAllIgnoresMode(t *testing.T) {
	e := newTestEnv(t, "series", func(s *mocksite.Site) { s.Delay = 25 * time.Millisecond })
	e.setSettings(func(s *Settings) { s.AutoStart = false })
	ids := e.addVideos(1, 2, 3)
	smp := e.sample()
	e.m.ForceStartAll()
	e.waitComplete(ids...)
	if hist := smp.finish(); !together(hist, ids...) {
		t.Fatalf("force start all did not run everything together (max %d)", maxWorking(hist))
	}
}

func TestForcedRowsDoNotBlockTheModeQueue(t *testing.T) {
	e := newTestEnv(t, "series", func(s *mocksite.Site) { s.Lines = 62; s.Delay = 12 * time.Millisecond })
	e.setSettings(func(s *Settings) { s.AutoStart = false })
	ids := e.addVideos(1, 2)
	e.m.ForceStart([]int64{ids[0]}) // forced, runs although nothing was started
	e.waitFor("forced row downloading", func() bool { return e.item(ids[0]).Status == "downloading" })
	e.m.Start([]int64{ids[1]}) // normal start in series: forced one is not counted
	e.waitFor("second row also downloading", func() bool { return e.item(ids[1]).Status == "downloading" })
}

func TestPauseSelectedRowsOnly(t *testing.T) {
	e := newTestEnv(t, "normal", func(s *mocksite.Site) { s.Lines = 62; s.Delay = 12 * time.Millisecond })
	ids := e.addVideos(1, 2, 3)
	e.waitFor("all downloading", func() bool {
		for _, id := range ids {
			if e.item(id).Percent < 5 {
				return false
			}
		}
		return true
	})
	e.m.Pause([]int64{ids[0], ids[2]})
	e.waitFor("two workers stopped", func() bool { return !e.busy(ids[0]) && !e.busy(ids[2]) })
	if e.item(ids[0]).Status != "paused" || e.item(ids[2]).Status != "paused" {
		t.Fatal("selected rows are not paused")
	}
	e.waitComplete(ids[1])
	e.m.Start([]int64{ids[0], ids[2]})
	e.waitComplete(ids[0], ids[2])
	for _, v := range []int{1, 2, 3} {
		if want := e.site.Expected(v, "720p", 0, 60); !bytes.Equal(e.readDone(v), want) {
			t.Fatalf("video %d differs", v)
		}
	}
}

func TestMoveRows(t *testing.T) {
	e := newTestEnv(t, "series", nil)
	e.setSettings(func(s *Settings) { s.AutoStart = false })
	ids := e.addVideos(1, 2, 3, 4, 5) // a b c d e
	order := func() string {
		var out []string
		for _, it := range e.m.Snapshot().Items {
			for i, id := range ids {
				if id == it.ID {
					out = append(out, string(rune('a'+i)))
				}
			}
		}
		return strings.Join(out, "")
	}
	pick := func(letters string) []int64 {
		var out []int64
		for _, l := range letters {
			out = append(out, ids[l-'a'])
		}
		return out
	}
	steps := []struct {
		sel, how, want string
	}{
		{"c", "up", "acbde"},
		{"a", "up", "acbde"},    // already on top
		{"cd", "up", "cadbe"},   // every selected row moves up one place
		{"ce", "down", "acdbe"}, // e is already last
		{"b", "top", "bacde"},
		{"bd", "bottom", "acebd"},
		{"ca", "top", "acebd"}, // already the first two
	}
	for i, s := range steps {
		e.m.Move(pick(s.sel), s.how)
		if got := order(); got != s.want {
			t.Fatalf("step %d (%s %s): got %s, want %s", i, s.sel, s.how, got, s.want)
		}
	}
	e.m.Move(pick("ae"), "up")
	if got := order(); got != "aecbd" {
		t.Fatalf("selected a+e up: got %s, want aecbd", got)
	}
	e.m.Move(pick("ae"), "down")
	if got := order(); got != "caebd" {
		t.Fatalf("selected a+e down: got %s, want caebd", got)
	}
}

func TestNothingStartsWithoutCredentials(t *testing.T) {
	e := newTestEnv(t, "series", nil)
	e.setSettings(func(s *Settings) { s.Cookie = "" })
	id := e.addVideos(2)[0]
	time.Sleep(700 * time.Millisecond)
	if st := e.item(id).Status; st != "queued" {
		t.Fatalf("started without a cookie: %q", st)
	}
	if !e.m.Snapshot().NeedCreds {
		t.Fatal("snapshot does not ask for credentials")
	}
	e.setSettings(func(s *Settings) { s.Cookie = e.site.Cookie })
	e.waitComplete(id)
}

func TestLoginRequiredIsReported(t *testing.T) {
	e := newTestEnv(t, "series", nil)
	e.setSettings(func(s *Settings) { s.Cookie = "session=expired" })
	id := e.addVideos(2)[0]
	e.waitStatus(id, StatusError)
	if msg := e.item(id).Error; !strings.Contains(msg, "log in") {
		t.Fatalf("error = %q", msg)
	}
	// fixing the cookie and pressing start works
	e.setSettings(func(s *Settings) { s.Cookie = e.site.Cookie })
	e.m.Start([]int64{id})
	e.waitComplete(id)
}

func TestDailyViewLimitIsReported(t *testing.T) {
	e := newTestEnv(t, "series", func(s *mocksite.Site) { s.SetSubscribe(true) })
	id := e.addVideos(2)[0]
	e.waitStatus(id, StatusError)
	if msg := e.item(id).Error; !strings.Contains(msg, "Daily view limit") {
		t.Fatalf("error = %q", msg)
	}
}

func TestSegmentErrorsAreRetried(t *testing.T) {
	e := newTestEnv(t, "series", func(s *mocksite.Site) { s.FailOnce(4); s.FailOnce(9) })
	id := e.addVideos(2)[0]
	e.waitComplete(id)
	if want := e.site.Expected(2, "720p", 0, 20); !bytes.Equal(e.readDone(2), want) {
		t.Fatal("content differs after retries")
	}
}

func TestExpiredLinksAreRefreshedAutomatically(t *testing.T) {
	e := newTestEnv(t, "series", func(s *mocksite.Site) { s.Lines = 62; s.Delay = 12 * time.Millisecond })
	id := e.addVideos(2)[0]
	e.waitFor("some progress", func() bool { return e.item(id).Percent > 20 })
	e.site.ExpireAll()
	e.waitComplete(id)
	if want := e.site.Expected(2, "720p", 0, 60); !bytes.Equal(e.readDone(2), want) {
		t.Fatal("content differs after the links expired")
	}
	if n := e.site.Resolves(); n != 2 {
		t.Fatalf("video was resolved %d times, want 2 (initial + refresh)", n)
	}
}

func TestSameVideoTwiceGetsDistinctFileNames(t *testing.T) {
	e := newTestEnv(t, "series", nil)
	u := e.site.VideoURL(2)
	ids := e.add(`["`+u+`","0:00","0:20","2:00"]`, `["`+u+`","1:00","1:20","2:00"]`)
	e.waitComplete(ids...)
	a, err1 := os.ReadFile(e.path("", e.site.Filename(2)+".ts"))
	b, err2 := os.ReadFile(e.path("", e.site.Filename(2)+"(1).ts"))
	if err1 != nil || err2 != nil {
		t.Fatalf("expected %s.ts and %s(1).ts in Done: %v %v", e.site.Filename(2), e.site.Filename(2), err1, err2)
	}
	if bytes.Equal(a, b) {
		t.Fatal("both sections have identical content")
	}
}

func TestDuplicateRowsAreRejected(t *testing.T) {
	e := newTestEnv(t, "series", nil)
	e.setSettings(func(s *Settings) { s.AutoStart = false })
	u := e.site.VideoURL(2)
	e.add(u)
	n, problems := e.m.Add(u)
	if n != 0 || len(problems) != 1 || !strings.Contains(problems[0], "already") {
		t.Fatalf("n=%d problems=%v", n, problems)
	}
	// the same video with a section is a different row
	if n, _ := e.m.Add(`["` + u + `","0:10","0:20","1:00"]`); n != 1 {
		t.Fatal("section row was rejected")
	}
}

func TestRemoveRows(t *testing.T) {
	e := newTestEnv(t, "series", func(s *mocksite.Site) { s.Lines = 62; s.Delay = 12 * time.Millisecond })
	ids := e.addVideos(2, 4)
	e.waitFor("progress", func() bool { return e.item(ids[0]).Percent > 10 })
	partial := e.path("", e.site.Filename(2)+".ts")
	if _, err := os.Stat(partial); err != nil {
		t.Fatal(err)
	}
	e.m.Remove([]int64{ids[0]}, true) // running + delete files
	e.waitFor("partial file deleted", func() bool { _, err := os.Stat(partial); return err != nil })
	if len(e.m.Snapshot().Items) != 1 {
		t.Fatal("row not removed")
	}
	e.waitComplete(ids[1]) // the next one carries on
	done := e.path("", e.site.Filename(4)+".ts")
	e.m.Remove([]int64{ids[1]}, false) // keep the file
	if _, err := os.Stat(done); err != nil {
		t.Fatal("finished file was deleted although only the row was removed")
	}
	if len(e.m.Snapshot().Items) != 0 {
		t.Fatal("row not removed")
	}
}

func TestClearCompleted(t *testing.T) {
	e := newTestEnv(t, "normal", nil)
	e.setSettings(func(s *Settings) { s.AutoStart = false })
	ids := e.addVideos(1, 2)
	e.m.Start([]int64{ids[0]})
	e.waitComplete(ids[0])
	e.m.ClearCompleted()
	snap := e.m.Snapshot()
	if len(snap.Items) != 1 || snap.Items[0].ID != ids[1] {
		t.Fatalf("items after clear: %+v", snap.Items)
	}
	if _, err := os.Stat(e.path("", e.site.Filename(1)+".ts")); err != nil {
		t.Fatal("clearing the list deleted the video")
	}
}

func TestSaveM3U8GoesToItsFolder(t *testing.T) {
	e := newTestEnv(t, "series", nil)
	e.setSettings(func(s *Settings) { s.AutoStart = false })
	id := e.addVideos(2)[0]
	e.m.SaveM3U8([]int64{id})
	path := e.path("", e.site.Filename(2)+".m3u8")
	e.waitFor("m3u8 file", func() bool { _, err := os.Stat(path); return err == nil })
	data, _ := os.ReadFile(path)
	if !strings.Contains(string(data), e.site.CDNURL(0)+"/hls/user2/2024-01-02,10-30-45/720p/seg3.ts") {
		t.Fatalf("playlist does not contain absolute segment links:\n%s", data)
	}
	e.waitFor("flag set", func() bool { return e.item(id).M3U8 })
	if st := e.item(id).Status; st != "paused" {
		t.Fatalf("saving the playlist changed the row to %q", st)
	}
}

func TestCustomFolders(t *testing.T) {
	e := newTestEnv(t, "series", nil)
	custom := t.TempDir()
	e.setSettings(func(s *Settings) {
		s.DoneDir = filepath.Join(custom, "finished")
		s.IncompleteDir = "partial"
	})
	id := e.addVideos(2)[0]
	e.waitComplete(id)
	if _, err := os.Stat(filepath.Join(custom, "finished", e.site.Filename(2)+".ts")); err != nil {
		t.Fatalf("absolute Done folder not used: %v", err)
	}
	if st, err := os.Stat(filepath.Join(e.dir, "partial")); err != nil || !st.IsDir() {
		t.Fatal("relative Incomplete folder was not created next to the executable")
	}
}

func TestSwitchingModeKeepsRunningDownloads(t *testing.T) {
	e := newTestEnv(t, "normal", func(s *mocksite.Site) { s.Lines = 62; s.Delay = 12 * time.Millisecond })
	ids := e.addVideos(1, 2, 3)
	e.waitFor("all running", func() bool {
		for _, id := range ids {
			if e.item(id).Status != "downloading" {
				return false
			}
		}
		return true
	})
	if err := e.m.SetMode("series"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(200 * time.Millisecond)
	for _, id := range ids {
		if e.item(id).Status != "downloading" {
			t.Fatalf("switching to series interrupted row %d (%s)", id, e.item(id).Status)
		}
	}
	e.waitComplete(ids...)
	if err := e.m.SetMode("bogus"); err == nil {
		t.Fatal("bogus mode accepted")
	}
}

func TestSettingsHeadersAreCleaned(t *testing.T) {
	s := Settings{UserAgent: "UA\r\n", Cookie: " a=b\n"}
	s.normalize()
	h := s.headers()
	want := map[string]string{"User-Agent": "UA", "Cookie": "a=b"}
	if !reflect.DeepEqual(h, want) {
		t.Fatalf("headers = %v", h)
	}
}

func TestStartAllLooksUpServersAgain(t *testing.T) {
	e := newTestEnv(t, "hybrid", func(s *mocksite.Site) { s.Lines = 62; s.Delay = 15 * time.Millisecond })
	ids := e.addVideos(2, 3) // two different servers, both start
	e.waitFor("both downloading", func() bool {
		return e.item(ids[0]).Status == "downloading" && e.item(ids[1]).Status == "downloading"
	})
	oldA, oldB := e.item(ids[0]).Server, e.item(ids[1]).Server
	e.m.PauseAll()
	e.waitFor("workers to stop", func() bool { return !e.busy(ids[0]) && !e.busy(ids[1]) })

	e.site.MoveVideos() // the site now uses the other server for each video
	before := e.site.Resolves()
	e.m.StartAll()

	e.waitFor("both rows to show their new server", func() bool {
		return e.item(ids[0]).Server == e.site.ServerOf(2) && e.item(ids[1]).Server == e.site.ServerOf(3)
	})
	if e.item(ids[0]).Server == oldA || e.item(ids[1]).Server == oldB {
		t.Fatal("rows kept the old server")
	}
	if e.site.Resolves() < before+2 {
		t.Fatalf("playlists were not read again: %d -> %d", before, e.site.Resolves())
	}
	e.waitComplete(ids...)
	for _, v := range []int{2, 3} {
		if want := e.site.Expected(v, "720p", 0, 60); !bytes.Equal(e.readDone(v), want) {
			t.Fatalf("video %d is damaged after changing server", v)
		}
	}
	e.noSiteProblems()
}

func TestServersAreLookedUpAgainAfterRestart(t *testing.T) {
	e := newTestEnv(t, "hybrid", func(s *mocksite.Site) { s.Lines = 62; s.Delay = 15 * time.Millisecond })
	ids := e.addVideos(2, 4)
	e.waitFor("second row to show its server", func() bool { return e.item(ids[1]).Server != "" })
	e.m.Shutdown()
	e.site.MoveVideos()
	m2, err := New(e.dir, "") // not started: look at what was loaded
	if err != nil {
		t.Fatal(err)
	}
	m2.mu.Lock()
	for _, it := range m2.items {
		if it.Status != StatusComplete && it.Server != "" {
			m2.mu.Unlock()
			t.Fatalf("row %d tRecued its stored server %q after a restart", it.ID, it.Server)
		}
	}
	m2.mu.Unlock()
}

func TestFullSizeIsEstimatedThenExact(t *testing.T) {
	e := newTestEnv(t, "series", func(s *mocksite.Site) { s.Lines = 62; s.Delay = 15 * time.Millisecond })
	id := e.addVideos(2)[0]
	want := int64(60 * e.site.SegSize)
	e.waitFor("an estimate", func() bool { return e.item(id).FullSize > 0 })
	if got := e.item(id).FullSize; got != want {
		t.Fatalf("estimated full size %d, want %d (all segments are the same size here)", got, want)
	}
	e.m.Pause([]int64{id})
	e.waitFor("worker to stop", func() bool { return !e.busy(id) })
	p1 := e.item(id).Percent
	if e.item(id).FullSize != want {
		t.Fatalf("estimate lost on pause: %d", e.item(id).FullSize)
	}

	// resume: the bar carries on instead of starting again from 0
	e.m.Start([]int64{id})
	e.waitFor("progress after resume", func() bool { return e.item(id).Percent > p1 })
	if e.item(id).Percent < p1 {
		t.Fatal("progress went backwards after a resume")
	}
	e.waitComplete(id)
	if got := e.item(id).FullSize; got != want {
		t.Fatalf("finished full size %d, want %d", got, want)
	}
}

func TestFullSizeSurvivesRestart(t *testing.T) {
	e := newTestEnv(t, "series", func(s *mocksite.Site) { s.Lines = 62; s.Delay = 15 * time.Millisecond })
	id := e.addVideos(2)[0]
	e.waitFor("an estimate", func() bool { return e.item(id).FullSize > 0 })
	e.m.Shutdown()
	m2, err := New(e.dir, "")
	if err != nil {
		t.Fatal(err)
	}
	defer m2.Shutdown()
	var got int64
	for _, it := range m2.Snapshot().Items {
		got = it.FullSize
	}
	if got != int64(60*e.site.SegSize) {
		t.Fatalf("full size after restart: %d", got)
	}
}

// Start (and resuming a paused row) looks up the server of the selected rows
// only, not of the others.
func TestStartLooksUpServerOfSelectedRowsOnly(t *testing.T) {
	e := newTestEnv(t, "hybrid", func(s *mocksite.Site) { s.Lines = 62; s.Delay = 15 * time.Millisecond })
	ids := e.addVideos(2, 3)
	e.waitFor("both downloading", func() bool {
		return e.item(ids[0]).Status == "downloading" && e.item(ids[1]).Status == "downloading"
	})
	oldA, oldB := e.item(ids[0]).Server, e.item(ids[1]).Server
	e.m.PauseAll()
	e.waitFor("workers to stop", func() bool { return !e.busy(ids[0]) && !e.busy(ids[1]) })

	e.site.MoveVideos()
	before := e.site.Resolves()
	e.m.Start(ids[:1]) // only the first row

	e.waitFor("the started row to show its new server", func() bool { return e.item(ids[0]).Server == e.site.ServerOf(2) })
	if e.item(ids[0]).Server == oldA {
		t.Fatal("started row kept its old server")
	}
	e.waitFor("the started row to download", func() bool { return e.item(ids[0]).Bytes > 0 })
	if got := e.item(ids[1]); got.Status != "paused" || got.Server != oldB {
		t.Fatalf("the other row must be left alone, got %s on %q (was %q)", got.Status, got.Server, oldB)
	}
	if n := e.site.Resolves() - before; n != 1 {
		t.Fatalf("expected exactly one playlist to be read, got %d", n)
	}
}

func TestArrangeKeepsTheGivenOrder(t *testing.T) {
	e := newTestEnv(t, "series", nil)
	e.setSettings(func(s *Settings) { s.AutoStart = false })
	ids := e.addVideos(1, 2, 3, 4, 5)
	order := func() []int64 {
		var out []int64
		for _, it := range e.m.Snapshot().Items {
			out = append(out, it.ID)
		}
		return out
	}
	e.m.Arrange([]int64{ids[3], ids[1]}, false) // two rows to the top, in this order
	if got, want := order(), []int64{ids[3], ids[1], ids[0], ids[2], ids[4]}; !reflect.DeepEqual(got, want) {
		t.Fatalf("top: %v want %v", got, want)
	}
	e.m.Arrange([]int64{ids[2], ids[0], ids[2], 9999}, true) // to the bottom, repeats and unknown ids ignored
	if got, want := order(), []int64{ids[3], ids[1], ids[4], ids[2], ids[0]}; !reflect.DeepEqual(got, want) {
		t.Fatalf("bottom: %v want %v", got, want)
	}
	e.m.Arrange([]int64{ids[0], ids[1], ids[2], ids[3], ids[4]}, false) // the whole queue
	if got, want := order(), ids; !reflect.DeepEqual(got, want) {
		t.Fatalf("all: %v want %v", got, want)
	}
}
