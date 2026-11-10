package manager

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// fakeFFmpeg puts a stand-in "ffmpeg" first in PATH. Every run is written to
// the returned log; the probe pass (output "-") reports a timeline problem when
// disc is set; a normal run writes "converted" to its output, or fails.
func fakeFFmpeg(t *testing.T, disc, fail bool) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("shell script stand-in")
	}
	dir := t.TempDir()
	log := filepath.Join(dir, "log.txt")
	script := `#!/bin/sh
for last; do :; done
echo "start $*" >> '` + log + `'
sleep 0.3
if [ "$last" = "-" ]; then
  [ "` + b2s(disc) + `" = 1 ] && echo "[x] timestamp discontinuity (stream id=256)" >&2
else
  [ "` + b2s(fail) + `" = 1 ] && { echo "boom: bad input" >&2; exit 1; }
  echo converted > "$last"
fi
echo "end" >> '` + log + `'
`
	if err := os.WriteFile(filepath.Join(dir, "ffmpeg"), []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return log
}

func b2s(b bool) string {
	if b {
		return "1"
	}
	return "0"
}

func readLog(t *testing.T, p string) []string {
	b, _ := os.ReadFile(p)
	return strings.Fields(strings.ReplaceAll(strings.TrimSpace(string(b)), "\n", " \n "))
}

func convertOn(e *testEnv, del bool) {
	e.setSettings(func(s *Settings) { s.ConvertMP4, s.DeleteAfterConvert = true, del })
}

// Videos that finish together are converted one after the other, never two at
// once.
func TestConvertsOneAtATime(t *testing.T) {
	log := fakeFFmpeg(t, false, false)
	e := newTestEnv(t, "hybrid", nil)
	convertOn(e, false)
	ids := e.addVideos(2, 3, 4)
	e.waitFor("three mp4 files", func() bool {
		n := 0
		for _, id := range ids {
			if strings.HasSuffix(e.item(id).Done, ".mp4") {
				n++
			}
		}
		return n == 3
	})
	depth, most := 0, 0
	for _, w := range readLog(t, log) {
		switch w {
		case "start":
			depth++
		case "end":
			depth--
		}
		if depth > most {
			most = depth
		}
	}
	if most != 1 {
		t.Errorf("up to %d conversions at once, want 1", most)
	}
}

func TestConvertKeepsOrDeletesTheOriginal(t *testing.T) {
	for _, del := range []bool{false, true} {
		fakeFFmpeg(t, false, false)
		e := newTestEnv(t, "series", nil)
		convertOn(e, del)
		id := e.addVideos(2)[0]
		e.waitFor("the mp4", func() bool { return strings.HasSuffix(e.item(id).Done, ".mp4") })
		mp4 := e.item(id).Done
		ts := strings.TrimSuffix(mp4, ".mp4") + ".ts"
		if _, err := os.Stat(mp4); err != nil {
			t.Errorf("delete=%v: %v", del, err)
		}
		e.waitFor("the original to be dealt with", func() bool {
			_, err := os.Stat(ts)
			return os.IsNotExist(err) == del
		})
	}
}

func TestConvertFailureKeepsTheTS(t *testing.T) {
	fakeFFmpeg(t, false, true)
	e := newTestEnv(t, "series", nil)
	convertOn(e, true)
	id := e.addVideos(2)[0]
	e.waitFor("the failure notice", func() bool {
		for _, n := range e.m.Snapshot().Notices {
			if n.Level == "error" && strings.Contains(n.Text, "boom: bad input") {
				return true
			}
		}
		return false
	})
	done := e.item(id).Done
	if !strings.HasSuffix(done, ".ts") {
		t.Errorf("done path %q, want the .ts", done)
	}
	if _, err := os.Stat(done); err != nil {
		t.Errorf("the .ts is gone: %v", err)
	}
	left, _ := filepath.Glob(filepath.Join(filepath.Dir(done), "*.mp4*"))
	if len(left) != 0 {
		t.Errorf("left over: %v", left)
	}
}

// The timeline check is the script's: a probe pass counts "timestamp
// discontinuity"; with any the audio is re-encoded and the timestamps are
// fixed, otherwise everything is copied.
func TestConvertTimelineCheck(t *testing.T) {
	for _, disc := range []bool{false, true} {
		log := fakeFFmpeg(t, disc, false)
		e := newTestEnv(t, "series", nil)
		convertOn(e, false)
		id := e.addVideos(2)[0]
		e.waitFor("the mp4", func() bool { return strings.HasSuffix(e.item(id).Done, ".mp4") })
		b, _ := os.ReadFile(log)
		fixed := strings.Contains(string(b), "-copyts -start_at_zero")
		if fixed != disc {
			t.Errorf("discontinuity %v: fix mode %v\n%s", disc, fixed, b)
		}
		if !disc && !strings.Contains(string(b), "-c:a copy") {
			t.Errorf("plain mode did not copy the audio\n%s", b)
		}
	}
}

// With the real ffmpeg: a normal video and one with a jump in its timeline both
// become playable mp4 files.
func TestConvertWithRealFFmpeg(t *testing.T) {
	ff, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("no ffmpeg")
	}
	dir := t.TempDir()
	part := func(name, offset string) string {
		p := filepath.Join(dir, name)
		args := []string{"-loglevel", "error", "-f", "lavfi", "-i", "testsrc=d=2:s=160x120:r=15", "-f", "lavfi", "-i", "sine=d=2",
			"-c:v", "libx264", "-preset", "ultrafast", "-c:a", "aac", "-output_ts_offset", offset, "-f", "mpegts", p}
		if out, err := exec.Command(ff, args...).CombinedOutput(); err != nil {
			t.Skipf("can not make a test video: %v %s", err, out)
		}
		return p
	}
	a, b := part("a.ts", "0"), part("b.ts", "5000")
	jump := filepath.Join(dir, "jump.ts")
	x, _ := os.ReadFile(a)
	y, _ := os.ReadFile(b)
	os.WriteFile(jump, append(x, y...), 0644)
	for _, src := range []string{a, jump} {
		dst, err := convertToMP4(testCtx(t), "", src)
		if err != nil {
			t.Fatalf("%s: %v", filepath.Base(src), err)
		}
		if out, err := exec.Command("ffprobe", "-v", "error", "-show_entries", "format=duration", "-of", "csv=p=0", dst).Output(); err != nil || strings.TrimSpace(string(out)) == "" {
			t.Errorf("%s: not playable: %v %s", filepath.Base(dst), err, out)
		}
		if _, err := os.Stat(dst + ".part"); err == nil {
			t.Errorf("%s: partial file left", dst)
		}
	}
}

func testCtx(t *testing.T) context.Context {
	c, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	t.Cleanup(cancel)
	return c
}

// The FFmpeg Location folder is used instead of PATH; a folder without ffmpeg
// is an error that names it (and does not quietly use the one in PATH).
func TestConvertUsesTheFFmpegFolder(t *testing.T) {
	fakeFFmpeg(t, false, false) // the one in PATH writes "converted"
	dir := t.TempDir()
	script := "#!/bin/sh\nfor last; do :; done\n[ \"$last\" = \"-\" ] || echo from-folder > \"$last\"\n"
	if err := os.WriteFile(filepath.Join(dir, "ffmpeg"), []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	e := newTestEnv(t, "series", nil)
	e.setSettings(func(s *Settings) { s.ConvertMP4, s.FFmpegDir = true, dir })
	id := e.addVideos(2)[0]
	e.waitFor("the mp4", func() bool { return strings.HasSuffix(e.item(id).Done, ".mp4") })
	if b, _ := os.ReadFile(e.item(id).Done); strings.TrimSpace(string(b)) != "from-folder" {
		t.Errorf("converted by %q, want the ffmpeg in the folder", b)
	}

	empty := t.TempDir()
	e.setSettings(func(s *Settings) { s.FFmpegDir = empty })
	id2 := e.addVideos(3)[0]
	e.waitFor("the not-found notice", func() bool {
		for _, n := range e.m.Snapshot().Notices {
			if n.Level == "error" && strings.Contains(n.Text, empty) {
				return true
			}
		}
		return false
	})
	if done := e.item(id2).Done; !strings.HasSuffix(done, ".ts") {
		t.Errorf("done path %q, want the .ts", done)
	}
}
