package manager

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"
)

func defaultRes(t *testing.T) []*regexp.Regexp {
	t.Helper()
	res, err := compilePatterns(DefaultListenPatterns)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func TestExtractLinks(t *testing.T) {
	res := defaultRes(t)
	cases := []struct {
		in   string
		want []string
	}{
		{`["https://Recu.me/video/xxxxxxx/play","55:00","1:10:00","1:30:00"],`, []string{"https://Recu.me/video/xxxxxxx/play"}},
		{`https://Recu.me/video/xxxxxxy/play`, []string{"https://Recu.me/video/xxxxxxy/play"}},
		{"a https://Recu.me/video/1/play b\nhttps://Recu.me/video/2/play, https://Recu.me/video/1/play", []string{"https://Recu.me/video/1/play", "https://Recu.me/video/2/play"}},
		{`https://example.com/other/page`, nil},
		{"nothing here", nil},
	}
	for _, c := range cases {
		if got := ExtractLinks(c.in, res); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%q: got %v want %v", c.in, got, c.want)
		}
	}
}

func linksOf(f []Found) []string {
	var out []string
	for _, x := range f {
		out = append(out, x.Link)
	}
	return out
}

func TestExtractLinksCustomPatterns(t *testing.T) {
	found, err := TryPatterns([]string{
		`# comment lines and blanks are ignored`,
		``,
		`"(https://other\.me/v/\d+)"`, // a capture group picks the link out of its quotes
		`magnet:\?xt=\S+`,             // no group: the whole match
	}, `x "https://other.me/v/42" y magnet:?xt=urn:abc z https://Recu.me/video/1/play`)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"https://other.me/v/42", "magnet:?xt=urn:abc"}; !reflect.DeepEqual(linksOf(found), want) {
		t.Fatalf("got %v want %v", linksOf(found), want)
	}
	// only the custom patterns are used when some are given; none = the default
	found, _ = TryPatterns(nil, `https://Recu.me/video/1/play`)
	if len(found) != 1 {
		t.Fatalf("default pattern: %v", found)
	}
	if _, err := TryPatterns([]string{`(unclosed`}, "x"); err == nil || !strings.Contains(err.Error(), "pattern 1") {
		t.Fatalf("bad pattern error: %v", err)
	}
	// a pattern can not smuggle a line break into the file
	found, _ = TryPatterns([]string{`a[\s\S]+b`}, "a\nb")
	if len(found) != 0 {
		t.Fatalf("multi-line match must be dropped: %q", found)
	}
}

// What the copy button of the browser script puts on the clipboard: the link box
// keeps the whole entry (with its times), the text file only the plain link.
func TestExtractFoundKeepsTheWholeEntry(t *testing.T) {
	res := defaultRes(t)
	const u1, u2 = "https://Recu.me/video/xxxxxxx/play", "https://Recu.me/video/xxxxxxy/play"
	e1 := `["` + u1 + `","0:00:00","1:30:00","1:30:00"]`
	e2 := `["` + u1 + `","12:34","20:00","1:30:00"]`
	e3 := `["` + u2 + `","5:00","6:00"]`
	cases := []struct {
		name string
		in   string
		want []Found
	}{
		{"entry with its trailing comma", e1 + ", ", []Found{{u1, e1}}},
		{"plain link", u2, []Found{{u2, u2}}},
		{"three-part entry (length looked up later)", e3 + ",", []Found{{u2, e3}}},
		{"two clips of one video in one copy", e1 + ",\n" + e2 + ",", []Found{{u1, e1}, {u1, e2}}},
		{"entry and a plain link together", "see " + u2 + " and " + e2, []Found{{u2, u2}, {u1, e2}}},
		{"the same entry twice", e1 + ", " + e1 + ",", []Found{{u1, e1}}},
		{"link inside a bracket that is not an entry", `["` + u1 + `"]`, []Found{{u1, u1}}},
		{"entry spread over lines is not kept as one line", "[\"" + u1 + "\",\n\"0:00:00\",\"1:30:00\"]", []Found{{u1, u1}}},
	}
	for _, c := range cases {
		if got := ExtractFound(c.in, res); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s:\n got  %+v\n want %+v", c.name, got, c.want)
		}
	}
	if got := ExtractLinks(e1+", "+e2+", "+u2, res); !reflect.DeepEqual(got, []string{u1, u2}) {
		t.Errorf("plain links: %v", got)
	}
}

func read(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestPrependLinks(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "links.txt")

	if n, err := prependLinks(p, []string{"B"}, true); err != nil || n != 1 { // creates the file
		t.Fatal(n, err)
	}
	if n, err := prependLinks(p, []string{"A"}, true); err != nil || n != 1 {
		t.Fatal(n, err)
	}
	got := strings.ReplaceAll(read(t, p), "\r\n", "\n")
	if got != "A\nB\n" {
		t.Fatalf("new links go on top: %q", got)
	}
	if n, _ := prependLinks(p, []string{"B", "A"}, true); n != 0 {
		t.Fatalf("duplicates were added (%d)", n)
	}
	if n, _ := prependLinks(p, []string{"B"}, false); n != 1 {
		t.Fatal("duplicates must be allowed when skipping is off")
	}
	if left, _ := filepath.Glob(filepath.Join(dir, ".Recurbate-links-*")); len(left) != 0 {
		t.Fatalf("temp files left behind: %v", left)
	}

	// an existing file keeps its line endings and byte order mark, and its own content
	crlf := filepath.Join(dir, "crlf.txt")
	os.WriteFile(crlf, []byte("\xEF\xBB\xBFold1\r\nold2\r\n"), 0644)
	if _, err := prependLinks(crlf, []string{"n1", "n2"}, true); err != nil {
		t.Fatal(err)
	}
	if got := read(t, crlf); got != "\xEF\xBB\xBFn1\r\nn2\r\nold1\r\nold2\r\n" {
		t.Fatalf("%q", got)
	}
	lf := filepath.Join(dir, "lf.txt")
	os.WriteFile(lf, []byte("old"), 0644) // no final newline
	prependLinks(lf, []string{"n"}, true)
	if got := read(t, lf); got != "n\nold" {
		t.Fatalf("%q", got)
	}

	// refused: wrong kind of file, a binary file, a missing folder, a folder
	if _, err := prependLinks(filepath.Join(dir, "settings.json"), []string{"x"}, true); err == nil {
		t.Error("a .json file must be refused")
	}
	if _, err := prependLinks(filepath.Join(dir, "a.exe"), []string{"x"}, true); err == nil {
		t.Error("a program must be refused")
	}
	bin := filepath.Join(dir, "bin.txt")
	os.WriteFile(bin, []byte("abc\x00\x01\x02"), 0644)
	if _, err := prependLinks(bin, []string{"x"}, true); err == nil || read(t, bin) != "abc\x00\x01\x02" {
		t.Error("a binary file must be refused and left alone")
	}
	if _, err := prependLinks(filepath.Join(dir, "nope", "x.txt"), []string{"x"}, true); err == nil {
		t.Error("a missing folder must be refused")
	}
	os.Mkdir(filepath.Join(dir, "d.txt"), 0755)
	if _, err := prependLinks(filepath.Join(dir, "d.txt"), []string{"x"}, true); err == nil {
		t.Error("a folder must be refused")
	}
}

func TestListenSettingsAreChecked(t *testing.T) {
	e := newTestEnv(t, "series", nil)
	good := filepath.Join(e.dir, "links.txt")
	set := func(l ListenSettings) error {
		s := e.m.Settings()
		s.Listen = l
		return e.m.SetSettings(s)
	}
	if err := set(ListenSettings{Enabled: true}); err != nil {
		t.Errorf("switching on without a file is fine, the links go to the link box: %v", err)
	}
	if err := set(ListenSettings{File: filepath.Join(e.dir, "x.exe")}); err == nil {
		t.Error("a program file must be refused")
	}
	if err := set(ListenSettings{File: good, Patterns: []string{`a(`}}); err == nil || !strings.Contains(err.Error(), "pattern 1") {
		t.Errorf("bad regular expression: %v", err)
	}
	if err := set(ListenSettings{File: good, Patterns: nil}); err != nil {
		t.Fatal(err)
	}
	if got := e.m.Settings().Listen.Patterns; !reflect.DeepEqual(got, DefaultListenPatterns) {
		t.Errorf("no patterns = the default, got %v", got)
	}
	if err := e.m.SetListenEnabled(true); err != nil {
		t.Fatal(err)
	}
	if !e.m.Snapshot().Listener.Enabled || !e.m.Settings().Listen.Enabled {
		t.Error("not switched on")
	}
	if err := e.m.SetListenEnabled(false); err != nil || e.m.Snapshot().Listener.Enabled {
		t.Error("not switched off")
	}
	// a settings.json from before the listener existed still gets the defaults
	old := DefaultSettings()
	l := old.Listen
	if !l.Enabled || !l.SkipDuplicates || !l.AllowPrivate || len(l.Patterns) == 0 || l.File != "" || !l.NoArchive || !l.NoBackups || l.archivePath() != "" || l.backupsOn() {
		t.Errorf("defaults: %+v", l)
	}
	if old.AutoStart || old.AutoResume {
		t.Errorf("a new install must not start or resume anything by itself: %+v", old)
	}
}

// a fake clipboard
type clip struct {
	mu   sync.Mutex
	seq  uint32
	text string
}

func (c *clip) copy(s string) { c.mu.Lock(); c.seq++; c.text = s; c.mu.Unlock() }
func (c *clip) source() Clipboard {
	return Clipboard{
		Seq:  func() uint32 { c.mu.Lock(); defer c.mu.Unlock(); return c.seq },
		Read: func() (string, error) { c.mu.Lock(); defer c.mu.Unlock(); return c.text, nil },
	}
}

func TestListenerAddsCopiedLinksToTheTop(t *testing.T) {
	dir := t.TempDir()
	m, err := New(dir, "")
	if err != nil {
		t.Fatal(err)
	}
	m.listenEvery = 5 * time.Millisecond
	cb := &clip{}
	cb.copy("https://Recu.me/video/before/play") // on the clipboard before it is switched on
	m.SetClipboard(cb.source())
	m.Run()
	t.Cleanup(m.Shutdown)

	file := filepath.Join(dir, "links.txt")
	s := m.Settings()
	s.Listen = ListenSettings{File: file, SkipDuplicates: true}
	if err := m.SetSettings(s); err != nil {
		t.Fatal(err)
	}
	waitFile := func(want string) {
		t.Helper()
		deadline := time.Now().Add(3 * time.Second)
		var got string
		for time.Now().Before(deadline) {
			if b, err := os.ReadFile(file); err == nil {
				got = strings.ReplaceAll(string(b), "\r\n", "\n")
				if got == want {
					return
				}
			}
			time.Sleep(10 * time.Millisecond)
		}
		t.Fatalf("file is %q, want %q", got, want)
	}

	// off: copying does nothing
	cb.copy("https://Recu.me/video/off/play")
	time.Sleep(60 * time.Millisecond)
	if _, err := os.Stat(file); err == nil {
		t.Fatal("wrote while switched off")
	}

	if err := m.SetListenEnabled(true); err != nil {
		t.Fatal(err)
	}
	time.Sleep(60 * time.Millisecond) // it notes what is on the clipboard now, and does not add it
	if _, err := os.Stat(file); err == nil {
		t.Fatal("added what was already on the clipboard")
	}

	cb.copy(`["https://Recu.me/video/xxxxxxx/play","55:00","1:10:00","1:30:00"],`)
	waitFile("https://Recu.me/video/xxxxxxx/play\n")
	cb.copy("https://Recu.me/video/xxxxxxy/play")
	waitFile("https://Recu.me/video/xxxxxxy/play\nhttps://Recu.me/video/xxxxxxx/play\n")
	cb.copy("just some words, no link")
	time.Sleep(60 * time.Millisecond)
	cb.copy("https://Recu.me/video/xxxxxxx/play") // already in the file
	time.Sleep(100 * time.Millisecond)
	cb.copy("two: https://Recu.me/video/3/play and https://Recu.me/video/4/play")
	waitFile("https://Recu.me/video/3/play\nhttps://Recu.me/video/4/play\nhttps://Recu.me/video/xxxxxxy/play\nhttps://Recu.me/video/xxxxxxx/play\n")

	snap := m.Snapshot()
	if snap.Listener.Added != 4 || snap.Listener.Error != "" {
		t.Errorf("listener view: %+v", snap.Listener)
	}
	found := false
	for _, n := range snap.Notices {
		if strings.Contains(n.Text, "Listener: added 2 links to links.txt") {
			found = true
		}
	}
	if !found {
		t.Errorf("no notice: %+v", snap.Notices)
	}
	dup := false
	for _, n := range snap.Notices {
		if strings.Contains(n.Text, "1 link was already in links.txt, so not added again") {
			dup = true
		}
	}
	if !dup {
		t.Errorf("a link that was already in the file was skipped without saying so: %+v", snap.Notices)
	}

	// switched off again: nothing more is added
	if err := m.SetListenEnabled(false); err != nil {
		t.Fatal(err)
	}
	time.Sleep(30 * time.Millisecond)
	cb.copy("https://Recu.me/video/5/play")
	time.Sleep(60 * time.Millisecond)
	if strings.Contains(read(t, file), "/video/5/") {
		t.Fatal("added a link after it was switched off")
	}
}

func TestListenerKeepsLinksUntilTheFileCanBeWritten(t *testing.T) {
	dir := t.TempDir()
	m, err := New(dir, "")
	if err != nil {
		t.Fatal(err)
	}
	m.listenEvery = 5 * time.Millisecond
	cb := &clip{}
	m.SetClipboard(cb.source())
	m.Run()
	t.Cleanup(m.Shutdown)

	folder := filepath.Join(dir, "later")
	file := filepath.Join(folder, "links.txt") // the folder does not exist yet
	s := m.Settings()
	s.Listen = ListenSettings{File: file, SkipDuplicates: true, Enabled: true}
	if err := m.SetSettings(s); err != nil {
		t.Fatal(err)
	}
	time.Sleep(60 * time.Millisecond)
	cb.copy("https://Recu.me/video/9/play")
	deadline := time.Now().Add(3 * time.Second)
	for m.Snapshot().Listener.Error == "" && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if m.Snapshot().Listener.Error == "" {
		t.Fatal("no error shown for a missing folder")
	}
	os.Mkdir(folder, 0755)
	deadline = time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if b, err := os.ReadFile(file); err == nil && strings.Contains(string(b), "/video/9/play") {
			if m.Snapshot().Listener.Error != "" {
				t.Fatal("error not cleared")
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("the link was lost instead of being kept for the next try")
}

func TestListenerOffersLinksForTheLinkBox(t *testing.T) {
	dir := t.TempDir()
	m, err := New(dir, "")
	if err != nil {
		t.Fatal(err)
	}
	m.listenEvery = 5 * time.Millisecond
	cb := &clip{}
	m.SetClipboard(cb.source())
	m.Run()
	t.Cleanup(m.Shutdown)

	const entryA = `["https://Recu.me/video/a/play","0:10","0:20"]` // copied with its times: the box keeps them
	set := func(l ListenSettings) {
		t.Helper()
		s := m.Settings()
		s.Listen = l
		if err := m.SetSettings(s); err != nil {
			t.Fatal(err)
		}
	}
	links := func() []string {
		var out []string
		for _, l := range m.Snapshot().Listener.Links {
			out = append(out, l.Line)
		}
		return out
	}
	waitLinks := func(want ...string) {
		t.Helper()
		deadline := time.Now().Add(3 * time.Second)
		for time.Now().Before(deadline) {
			if reflect.DeepEqual(links(), want) {
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
		t.Fatalf("links %v, want %v", links(), want)
	}

	// no file is needed: the links always go to the box
	set(ListenSettings{Enabled: true, NoArchive: true, NoBackups: true})
	time.Sleep(60 * time.Millisecond)
	cb.copy(`["https://Recu.me/video/a/play","0:10","0:20"],`)
	waitLinks(entryA)
	cb.copy("https://Recu.me/video/b/play and https://Recu.me/video/c/play")
	waitLinks(entryA, "https://Recu.me/video/b/play", "https://Recu.me/video/c/play")
	ids := m.Snapshot().Listener.Links
	if !(ids[0].ID < ids[1].ID && ids[1].ID < ids[2].ID) {
		t.Errorf("ids must grow: %+v", ids)
	}
	if files, _ := filepath.Glob(filepath.Join(dir, "*.txt")); len(files) != 0 {
		t.Errorf("wrote a file without one being set: %v", files)
	}

	// file and box together
	file := filepath.Join(dir, "links.txt")
	set(ListenSettings{File: file, Enabled: true, SkipDuplicates: true})
	time.Sleep(60 * time.Millisecond)
	cb.copy("https://Recu.me/video/e/play")
	waitLinks(entryA, "https://Recu.me/video/b/play", "https://Recu.me/video/c/play", "https://Recu.me/video/e/play")
	if !strings.Contains(read(t, file), "/e/play") {
		t.Error("file did not get the link")
	}

	// a whole entry goes to the box as it is, the file keeps only the plain link
	cb.copy(`["https://Recu.me/video/f/play","0:00:00","1:30:00","1:30:00"], `)
	waitLinks(entryA, "https://Recu.me/video/b/play", "https://Recu.me/video/c/play", "https://Recu.me/video/e/play",
		`["https://Recu.me/video/f/play","0:00:00","1:30:00","1:30:00"]`)
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) && !strings.Contains(read(t, file), "/f/play") {
		time.Sleep(10 * time.Millisecond)
	}
	if got := strings.SplitN(strings.ReplaceAll(read(t, file), "\r\n", "\n"), "\n", 2)[0]; got != "https://Recu.me/video/f/play" {
		t.Errorf("file's top line is %q, want the plain link", got)
	}
	// another clip of the same video: a new entry for the box, no second line in the file
	cb.copy(`["https://Recu.me/video/f/play","12:34","20:00","1:30:00"], `)
	waitLinks(entryA, "https://Recu.me/video/b/play", "https://Recu.me/video/c/play", "https://Recu.me/video/e/play",
		`["https://Recu.me/video/f/play","0:00:00","1:30:00","1:30:00"]`, `["https://Recu.me/video/f/play","12:34","20:00","1:30:00"]`)
	time.Sleep(60 * time.Millisecond)
	if n := strings.Count(read(t, file), "/f/play"); n != 1 {
		t.Errorf("the file has the link %d times, want once", n)
	}
}

func TestListenerSkipsPrivateCopiesUnlessAllowed(t *testing.T) {
	dir := t.TempDir()
	m, err := New(dir, "")
	if err != nil {
		t.Fatal(err)
	}
	m.listenEvery = 5 * time.Millisecond
	cb := &clip{}
	var private bool
	var pmu sync.Mutex
	src := cb.source()
	src.Private = func() bool { pmu.Lock(); defer pmu.Unlock(); return private }
	setPrivate := func(v bool) { pmu.Lock(); private = v; pmu.Unlock() }
	m.SetClipboard(src)
	m.Run()
	t.Cleanup(m.Shutdown)

	set := func(allow bool) {
		t.Helper()
		s := m.Settings()
		s.Listen = ListenSettings{Enabled: true, NoArchive: true, AllowPrivate: allow}
		if err := m.SetSettings(s); err != nil {
			t.Fatal(err)
		}
		time.Sleep(60 * time.Millisecond)
	}
	set(false)
	setPrivate(true)
	cb.copy("https://Recu.me/video/p1/play")
	deadline := time.Now().Add(3 * time.Second)
	var notice bool
	for time.Now().Before(deadline) && !notice {
		for _, n := range m.Snapshot().Notices {
			if strings.Contains(n.Text, "marked it as private") {
				notice = true
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !notice {
		t.Fatal("no notice that a private copy was skipped")
	}
	if n := len(m.Snapshot().Listener.Links); n != 0 {
		t.Fatalf("a private copy was read (%d links)", n)
	}
	// a second private copy right away does not repeat the notice
	cb.copy("https://Recu.me/video/p2/play")
	time.Sleep(80 * time.Millisecond)
	count := 0
	for _, n := range m.Snapshot().Notices {
		if strings.Contains(n.Text, "marked it as private") {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("notice shown %d times", count)
	}

	// allowed: private copies are read
	set(true)
	cb.copy("https://Recu.me/video/p3/play")
	deadline = time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) && len(m.Snapshot().Listener.Links) == 0 {
		time.Sleep(10 * time.Millisecond)
	}
	if l := m.Snapshot().Listener.Links; len(l) != 1 || l[0].Line != "https://Recu.me/video/p3/play" {
		t.Fatalf("links %+v", l)
	}
}

// A copy that can not be read the moment it is made (the program that copied still holds
// the clipboard) is tried again instead of being lost.
func TestListenerRetriesAReadThatFailsAtFirst(t *testing.T) {
	dir := t.TempDir()
	m, err := New(dir, "")
	if err != nil {
		t.Fatal(err)
	}
	m.listenEvery = 5 * time.Millisecond
	cb := &clip{}
	var mu sync.Mutex
	fails := 6
	src := cb.source()
	real := src.Read
	src.Read = func() (string, error) {
		mu.Lock()
		defer mu.Unlock()
		if fails > 0 {
			fails--
			return "", errors.New("clipboard busy")
		}
		return real()
	}
	m.SetClipboard(src)
	m.Run()
	t.Cleanup(m.Shutdown)
	s := m.Settings()
	s.Listen = ListenSettings{Enabled: true, NoArchive: true, NoBackups: true}
	if err := m.SetSettings(s); err != nil {
		t.Fatal(err)
	}
	time.Sleep(60 * time.Millisecond)
	cb.copy("https://Recu.me/video/r/play")
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) && len(m.Snapshot().Listener.Links) == 0 {
		time.Sleep(10 * time.Millisecond)
	}
	if len(m.Snapshot().Listener.Links) != 1 {
		t.Fatal("the copy was lost after a failed first read")
	}
}

// ---- the text file is somebody's master record: it may only ever gain lines ----

func TestPrependKeepsEveryOldByteAndBacksUpOncePerDay(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "All-Stored-links.txt")
	orig := []byte("\xEF\xBB\xBFold1\r\n\xff-not-utf8-\r\nold3")
	os.WriteFile(p, orig, 0644)
	for _, l := range []string{"n1", "n2", "n3"} {
		if _, err := prependLinks(p, []string{l}, true); err != nil {
			t.Fatal(err)
		}
	}
	got, _ := os.ReadFile(p)
	if !bytes.HasSuffix(got, orig[3:]) || !bytes.HasPrefix(got, []byte("\xEF\xBB\xBFn3\r\nn2\r\nn1\r\n")) {
		t.Fatalf("old content changed: %q", got)
	}
	baks, _ := filepath.Glob(filepath.Join(p+".backups", "*.bak"))
	if len(baks) != 1 {
		t.Fatalf("want exactly one backup for the day, got %v", baks)
	}
	if b, _ := os.ReadFile(baks[0]); !bytes.Equal(b, orig) {
		t.Fatalf("backup is not the file as it was: %q", b)
	}
	log, _ := os.ReadFile(filepath.Join(p+".backups", "added.log"))
	for _, l := range []string{"\tn1\n", "\tn2\n", "\tn3\n"} {
		if !strings.Contains(string(log), l) {
			t.Fatalf("added.log lacks %q: %q", l, log)
		}
	}
	// a backup that exists is never overwritten
	os.WriteFile(baks[0], []byte("precious"), 0644)
	if _, err := prependLinks(p, []string{"n4"}, true); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(baks[0]); string(b) != "precious" {
		t.Fatalf("a backup was overwritten: %q", b)
	}
	if left, _ := filepath.Glob(filepath.Join(dir, ".Recurbate-links-*")); len(left) != 0 {
		t.Fatalf("temp files left: %v", left)
	}
	if _, err := os.Stat(p + ".lock"); err == nil {
		t.Fatal("lock file left behind")
	}
}

func TestPrependDoesNotReplaceAMissingFileThatHasBackups(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "master.txt")
	os.WriteFile(p, []byte("a\nb\n"), 0644)
	if _, err := prependLinks(p, []string{"c"}, true); err != nil {
		t.Fatal(err)
	}
	os.Remove(p) // the file goes missing (drive unplugged, sync trouble, ...)
	n, err := prependLinks(p, []string{"d"}, true)
	if err == nil || n != 0 {
		t.Fatalf("a new file was started in place of the missing one (n=%d err=%v)", n, err)
	}
	if _, serr := os.Stat(p); serr == nil {
		t.Fatal("the missing file was created again")
	}
	if !strings.Contains(err.Error(), "backups") {
		t.Fatalf("message does not explain: %v", err)
	}
	// moving the backups away is the way to start a new file on purpose
	os.Rename(p+".backups", filepath.Join(dir, "old-backups"))
	if n, err := prependLinks(p, []string{"d"}, true); err != nil || n != 1 {
		t.Fatal(n, err)
	}
}

// Another program adds a line at the worst moment (after the new file was
// written, before it is swapped in): its line must survive.
func TestPrependRereadsWhenAnotherProgramChangedTheFile(t *testing.T) {
	p := filepath.Join(t.TempDir(), "master.txt")
	os.WriteFile(p, []byte("old\n"), 0644)
	done := false
	testBeforeSwap = func() {
		if done {
			return
		}
		done = true
		cur, _ := os.ReadFile(p)
		os.WriteFile(p, append([]byte("OTHER\n"), cur...), 0644)
	}
	t.Cleanup(func() { testBeforeSwap = nil })
	if n, err := prependLinks(p, []string{"mine"}, true); err != nil || n != 1 {
		t.Fatal(n, err)
	}
	if got := read(t, p); got != "mine\nOTHER\nold\n" {
		t.Fatalf("lost the other program's line: %q", got)
	}
}

// A file that keeps changing is never overwritten with a stale copy.
func TestPrependGivesUpRatherThanOverwrite(t *testing.T) {
	p := filepath.Join(t.TempDir(), "master.txt")
	os.WriteFile(p, []byte("old\n"), 0644)
	n := 0
	testBeforeSwap = func() {
		n++
		cur, _ := os.ReadFile(p)
		os.WriteFile(p, append([]byte(fmt.Sprintf("OTHER%d\n", n)), cur...), 0644)
	}
	t.Cleanup(func() { testBeforeSwap = nil })
	if got, err := prependLinks(p, []string{"mine"}, true); err == nil || got != 0 {
		t.Fatalf("expected a refusal, got %d, %v", got, err)
	}
	got := read(t, p)
	if strings.Contains(got, "mine") || !strings.HasSuffix(got, "OTHER1\nold\n") || !strings.Contains(got, "OTHER5\n") {
		t.Fatalf("file was overwritten or damaged: %q", got)
	}
}

func TestPrependWaitsForALockAndBreaksAStaleOne(t *testing.T) {
	p := filepath.Join(t.TempDir(), "master.txt")
	os.WriteFile(p, []byte("old\n"), 0644)
	lock := p + ".lock"
	os.WriteFile(lock, nil, 0644)
	go func() { time.Sleep(200 * time.Millisecond); os.Remove(lock) }() // the other program finishes
	start := time.Now()
	if n, err := prependLinks(p, []string{"a"}, true); err != nil || n != 1 {
		t.Fatal(n, err)
	}
	if time.Since(start) < 150*time.Millisecond {
		t.Fatal("did not wait for the other program")
	}
	// left over from a crash
	os.WriteFile(lock, nil, 0644)
	old := time.Now().Add(-time.Minute)
	os.Chtimes(lock, old, old)
	if n, err := prependLinks(p, []string{"b"}, true); err != nil || n != 1 {
		t.Fatal(n, err)
	}
	if got := read(t, p); got != "b\na\nold\n" {
		t.Fatalf("%q", got)
	}
}

// ---- archive and backups switches ----

func TestBackupsAreOptionalButTheArchiveIsNot(t *testing.T) {
	dir := t.TempDir()
	// backups on (the default): the daily copy and the log appear
	on := filepath.Join(dir, "on.txt")
	os.WriteFile(on, []byte("old\n"), 0644)
	if n, err := prependLinksWith(on, []string{"new"}, true, true); err != nil || n != 1 {
		t.Fatal(n, err)
	}
	if !hasBackups(on) {
		t.Error("backups on, but no backup folder")
	}
	// backups off: the file gets the link, nothing else is made
	off := filepath.Join(dir, "off.txt")
	os.WriteFile(off, []byte("old\n"), 0644)
	if n, err := prependLinksWith(off, []string{"new"}, true, false); err != nil || n != 1 {
		t.Fatal(n, err)
	}
	if got := strings.ReplaceAll(read(t, off), "\r\n", "\n"); got != "new\nold\n" {
		t.Errorf("content %q", got)
	}
	if _, err := os.Stat(backupDir(off)); !os.IsNotExist(err) {
		t.Errorf("backups off, but %s was made (%v)", backupDir(off), err)
	}
	// a brand new file without backups gets no folder and no log either
	fresh := filepath.Join(dir, "fresh.txt")
	if n, err := prependLinksWith(fresh, []string{"a"}, true, false); err != nil || n != 1 {
		t.Fatal(n, err)
	}
	if _, err := os.Stat(backupDir(fresh)); !os.IsNotExist(err) {
		t.Errorf("fresh file: backup folder made although backups are off (%v)", err)
	}
	// the data safety does not depend on backups: nothing already in the file is ever lost
	if got := strings.ReplaceAll(read(t, off), "\r\n", "\n"); !strings.HasSuffix(got, "old\n") {
		t.Errorf("old content lost: %q", got)
	}
}

func TestListenSettingsArchiveAndBackupRules(t *testing.T) {
	good := ListenSettings{File: filepath.Join(t.TempDir(), "x.txt")}
	if !good.backupsOn() || good.archivePath() == "" {
		t.Error("settings from an older version (neither switch set) must keep the archive and its backups")
	}
	if l := (ListenSettings{File: "x.txt", NoBackups: true}); l.backupsOn() || l.archivePath() != "x.txt" {
		t.Error("archive without backups")
	}
	if l := (ListenSettings{File: "x.txt", NoArchive: true}); l.backupsOn() || l.archivePath() != "" {
		t.Error("backups without the archive must not exist")
	}
	if l := (ListenSettings{File: "x.txt", NoArchive: true, NoBackups: false}); l.backupsOn() {
		t.Error("NoArchive must win over backups being wanted")
	}
	// archive off: the links still go to the link box, so that is allowed
	if err := (ListenSettings{Enabled: true, NoArchive: true}).validate(); err != nil {
		t.Errorf("archive off: %v", err)
	}
	// archive on with no file yet: allowed too (the link box still gets the links)
	if err := (ListenSettings{Enabled: true}).validate(); err != nil {
		t.Errorf("archive on, no file: %v", err)
	}
	// a stale bad file name does not matter while the archive is off
	if err := (ListenSettings{File: "x.exe", NoArchive: true}).validate(); err != nil {
		t.Errorf("bad name with archive off: %v", err)
	}
	if err := (ListenSettings{File: "x.exe"}).validate(); err == nil {
		t.Error("bad name with archive on must be refused")
	}
	// the switches survive saving and loading
	dir := t.TempDir()
	m, err := New(dir, "")
	if err != nil {
		t.Fatal(err)
	}
	s := m.Settings()
	s.Listen = ListenSettings{File: filepath.Join(dir, "a.txt"), NoBackups: true}
	if err := m.SetSettings(s); err != nil {
		t.Fatal(err)
	}
	m.Shutdown()
	m2, err := New(dir, "")
	if err != nil {
		t.Fatal(err)
	}
	defer m2.Shutdown()
	if l := m2.Settings().Listen; !l.NoBackups || l.NoArchive {
		t.Errorf("after restart: %+v", l)
	}
}

func TestListenerHonoursTheArchiveAndBackupSwitches(t *testing.T) {
	dir := t.TempDir()
	m, err := New(dir, "")
	if err != nil {
		t.Fatal(err)
	}
	m.listenEvery = 5 * time.Millisecond
	cb := &clip{}
	m.SetClipboard(cb.source())
	m.Run()
	t.Cleanup(m.Shutdown)
	file := filepath.Join(dir, "links.txt")
	os.WriteFile(file, []byte("https://Recu.me/video/0/play\n"), 0644)
	set := func(l ListenSettings) {
		s := m.Settings()
		s.Listen = l
		if err := m.SetSettings(s); err != nil {
			t.Fatal(err)
		}
		time.Sleep(60 * time.Millisecond)
	}
	waitFor := func(what string, f func() bool) {
		t.Helper()
		deadline := time.Now().Add(3 * time.Second)
		for time.Now().Before(deadline) {
			if f() {
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
		t.Fatal("timed out: " + what)
	}
	has := func(sub string) func() bool {
		return func() bool { b, _ := os.ReadFile(file); return strings.Contains(string(b), sub) }
	}

	// archive on, backups off: links are added, no backup folder appears
	set(ListenSettings{File: file, Enabled: true, SkipDuplicates: true, NoBackups: true})
	cb.copy("https://Recu.me/video/1/play")
	waitFor("link 1 in the file", has("/video/1/play"))
	if _, err := os.Stat(backupDir(file)); !os.IsNotExist(err) {
		t.Errorf("backups are off but %s exists", backupDir(file))
	}

	// archive off (box on): the file is left alone, the window still gets the link
	set(ListenSettings{File: file, Enabled: true, SkipDuplicates: true, NoArchive: true})
	before := read(t, file)
	cb.copy("https://Recu.me/video/2/play")
	waitFor("link 2 offered to the box", func() bool {
		for _, l := range m.Snapshot().Listener.Links {
			if strings.Contains(l.Line, "/video/2/play") {
				return true
			}
		}
		return false
	})
	time.Sleep(150 * time.Millisecond)
	if read(t, file) != before {
		t.Error("the archive is off, but the file changed")
	}
	if _, err := os.Stat(backupDir(file)); !os.IsNotExist(err) {
		t.Error("archive off, but a backup folder was made")
	}

	// both on: links added and the backups appear
	set(ListenSettings{File: file, Enabled: true, SkipDuplicates: true})
	cb.copy("https://Recu.me/video/3/play")
	waitFor("link 3 in the file", has("/video/3/play"))
	if !hasBackups(file) {
		t.Error("both on, but no backups")
	}
	// the link copied while the archive was off was not saved behind the person's back
	if strings.Contains(read(t, file), "/video/2/play") {
		t.Error("a link copied while the archive was off ended up in the file")
	}
}

func tmpFilesIn(dir string) []string {
	m, _ := filepath.Glob(filepath.Join(dir, ".Recurbate-links-*.tmp"))
	return m
}

func TestTemporaryFileGoesToTheDataFolder(t *testing.T) {
	data, files := t.TempDir(), t.TempDir()
	p := filepath.Join(files, "links.txt")
	os.WriteFile(p, []byte("old\n"), 0644)
	var inData, inFiles int
	testBeforeSwap = func() { inData, inFiles = len(tmpFilesIn(data)), len(tmpFilesIn(files)) }
	defer func() { testBeforeSwap = nil }()
	if n, err := prependLinksIn(p, []string{"new"}, true, true, data); err != nil || n != 1 {
		t.Fatal(n, err)
	}
	if inData != 1 || inFiles != 0 {
		t.Errorf("while writing: %d temp file(s) in the data folder, %d next to the file; want 1 and 0", inData, inFiles)
	}
	if got := strings.ReplaceAll(read(t, p), "\r\n", "\n"); got != "new\nold\n" {
		t.Errorf("content %q", got)
	}
	if len(tmpFilesIn(data))+len(tmpFilesIn(files)) != 0 {
		t.Error("a temporary file was left behind")
	}
	// no folder given (or one that is not there): next to the file, as before
	testBeforeSwap = func() { inData, inFiles = len(tmpFilesIn(data)), len(tmpFilesIn(files)) }
	prependLinksIn(p, []string{"a"}, true, true, "")
	if inData != 0 || inFiles != 1 {
		t.Errorf("no folder given: %d in data, %d next to the file", inData, inFiles)
	}
	prependLinksIn(p, []string{"b"}, true, true, filepath.Join(data, "missing"))
	if inData != 0 || inFiles != 1 {
		t.Errorf("missing data folder: %d in data, %d next to the file", inData, inFiles)
	}
}

func TestTemporaryFileFallsBackNextToTheFileOnAnotherDrive(t *testing.T) {
	data, files := t.TempDir(), t.TempDir()
	p := filepath.Join(files, "links.txt")
	os.WriteFile(p, []byte("old\n"), 0644)
	// pretend moving from the data folder to the file's folder is not possible (another drive)
	renameFile = func(from, to string) error {
		if filepath.Dir(from) == data {
			return errors.New("The system cannot move the file to a different disk drive.")
		}
		return os.Rename(from, to)
	}
	defer func() { renameFile = os.Rename }()
	n, err := prependLinksIn(p, []string{"new"}, true, true, data)
	if err != nil || n != 1 {
		t.Fatal(n, err)
	}
	if got := strings.ReplaceAll(read(t, p), "\r\n", "\n"); got != "new\nold\n" {
		t.Errorf("content %q", got)
	}
	if len(tmpFilesIn(data))+len(tmpFilesIn(files)) != 0 {
		t.Error("a temporary file was left behind")
	}
	// a real failure next to the file still leaves the file as it was
	renameFile = func(from, to string) error { return errors.New("denied") }
	before := read(t, p)
	if _, err := prependLinksIn(p, []string{"x"}, true, true, data); err == nil {
		t.Error("expected an error")
	}
	if read(t, p) != before || len(tmpFilesIn(data))+len(tmpFilesIn(files)) != 0 {
		t.Error("file changed or temp left after a failed swap")
	}
}

func TestListenerKeepsItsTemporaryFileBesideSettingsJson(t *testing.T) {
	dir, other := t.TempDir(), t.TempDir()
	m, err := New(dir, "")
	if err != nil {
		t.Fatal(err)
	}
	m.listenEvery = 5 * time.Millisecond
	cb := &clip{}
	m.SetClipboard(cb.source())
	m.Run()
	t.Cleanup(m.Shutdown)
	dataDir := filepath.Dir(m.settingsPath())
	file := filepath.Join(other, "links.txt")
	os.WriteFile(file, []byte("https://Recu.me/video/0/play\n"), 0644)
	var mu sync.Mutex
	inData, inFiles := -1, -1
	testBeforeSwap = func() {
		mu.Lock()
		inData, inFiles = len(tmpFilesIn(dataDir)), len(tmpFilesIn(other))
		mu.Unlock()
	}
	defer func() { testBeforeSwap = nil }()
	s := m.Settings()
	s.Listen = ListenSettings{File: file, Enabled: true, SkipDuplicates: true}
	if err := m.SetSettings(s); err != nil {
		t.Fatal(err)
	}
	time.Sleep(60 * time.Millisecond)
	cb.copy("https://Recu.me/video/1/play")
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) && !strings.Contains(read(t, file), "/video/1/play") {
		time.Sleep(10 * time.Millisecond)
	}
	mu.Lock()
	defer mu.Unlock()
	if inData != 1 || inFiles != 0 {
		t.Errorf("temp files while saving: %d beside settings.json, %d beside the archive; want 1 and 0", inData, inFiles)
	}
}

// A settings.json written by an earlier version keeps what it says: the new
// install defaults (archive and backups off, nothing starts by itself) apply
// only to settings that are not there yet.
func TestSavedSettingsKeepTheirValuesWhenDefaultsChange(t *testing.T) {
	dir := t.TempDir()
	saved := `{"userAgent":"ua","cookie":"c","autoStart":true,"autoResume":true,
		"listen":{"enabled":false,"file":"` + filepath.ToSlash(filepath.Join(dir, "a.txt")) + `","toBox":false,"patterns":null,
		"skipDuplicates":false,"allowPrivate":false,"noArchive":false,"noBackups":false}}`
	if err := os.WriteFile(filepath.Join(dir, "settings.json"), []byte(saved), 0600); err != nil {
		t.Fatal(err)
	}
	m, err := New(dir, "")
	if err != nil {
		t.Fatal(err)
	}
	defer m.Shutdown()
	s := m.Settings()
	l := s.Listen
	if !s.AutoStart || !s.AutoResume || l.Enabled || l.NoArchive || l.NoBackups || l.SkipDuplicates || l.AllowPrivate || l.archivePath() == "" || !l.backupsOn() {
		t.Errorf("saved values were replaced by the defaults: %+v / %+v", s, l)
	}
}
