package manager

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"

	"Recurbate/internal/mocksite"
)

func catIDByName(t *testing.T, m *Manager, name string) string {
	t.Helper()
	for _, c := range m.Settings().Categories {
		if c.Name == name {
			return c.ID
		}
	}
	t.Fatalf("no category %q in %+v", name, m.Settings().Categories)
	return ""
}

func (e *testEnv) addTo(category string, videos ...int) []int64 {
	e.t.Helper()
	before := len(e.m.Snapshot().Items)
	var lines string
	for _, v := range videos {
		lines += e.site.VideoURL(v) + "\n"
	}
	if n, problems := e.m.AddTo(lines, category); n != len(videos) {
		e.t.Fatalf("added %d of %d: %v", n, len(videos), problems)
	}
	var ids []int64
	for _, it := range e.m.Snapshot().Items[before:] {
		ids = append(ids, it.ID)
	}
	return ids
}

func TestNormalizeCategories(t *testing.T) {
	s := Settings{Categories: []Category{
		{Name: "  Movies "}, {Name: "movies"}, {Name: ""}, {ID: "c7", Name: "Shows"}, {ID: "c7", Name: "Clips"},
	}}
	s.normalize()
	if len(s.Categories) != 3 {
		t.Fatalf("%+v", s.Categories)
	}
	seen := map[string]bool{}
	for _, c := range s.Categories {
		if c.ID == "" || seen[c.ID] {
			t.Fatalf("ids must be set and unique: %+v", s.Categories)
		}
		seen[c.ID] = true
	}
	if s.Categories[0].Name != "Movies" || s.Categories[1].ID != "c7" {
		t.Fatalf("%+v", s.Categories)
	}
	again := s
	again.Categories = append([]Category(nil), s.Categories...)
	again.normalize()
	for i := range s.Categories {
		if again.Categories[i].ID != s.Categories[i].ID {
			t.Fatal("ids must not change when settings are saved again")
		}
	}
}

func TestCategoryFoldersAndFallback(t *testing.T) {
	e := newTestEnv(t, "normal", nil)
	away := t.TempDir()
	e.setSettings(func(s *Settings) {
		s.Categories = []Category{
			{Name: "Movies", DoneDir: filepath.Join(away, "movies", "done"), IncompleteDir: filepath.Join(away, "movies", "inc"), M3U8Dir: filepath.Join(away, "movies", "m3u8")},
			{Name: "Half", DoneDir: filepath.Join(away, "half")}, // other folders blank: normal ones
		}
	})
	movies, half := catIDByName(t, e.m, "Movies"), catIDByName(t, e.m, "Half")
	a := e.addTo(movies, 2)[0]
	b := e.addTo(half, 4)[0]
	c := e.addTo("", 6)[0]
	e.waitComplete(a, b, c)

	if got := e.item(a); got.CategoryName != "Movies" {
		t.Fatalf("category name %q", got.CategoryName)
	}
	for _, p := range []string{
		filepath.Join(away, "movies", "done", e.site.Filename(2)+".ts"),
		filepath.Join(away, "half", e.site.Filename(4)+".ts"),
		e.path("", e.site.Filename(6)+".ts"),
	} {
		if !pathExists(p) {
			t.Errorf("expected %s", p)
		}
	}
	if pathExists(e.path("", e.site.Filename(2)+".ts")) || pathExists(e.path("", e.site.Filename(4)+".ts")) {
		t.Error("categorised video ended up in the normal Done folder")
	}
	if want := e.site.Expected(2, "720p", 0, 20); !bytes.Equal(mustRead(t, filepath.Join(away, "movies", "done", e.site.Filename(2)+".ts")), want) {
		t.Error("content differs")
	}

	// playlists go to the category's playlist folder, or the normal one
	e.m.SaveM3U8([]int64{a, b})
	e.waitFor("playlists saved", func() bool { return e.item(a).M3U8 && e.item(b).M3U8 })
	if !pathExists(filepath.Join(away, "movies", "m3u8", e.site.Filename(2)+".m3u8")) {
		t.Error("playlist not in the category folder")
	}
	if !pathExists(e.path("", e.site.Filename(4)+".m3u8")) {
		t.Error("blank category playlist folder should use the normal one")
	}
}

func mustRead(t *testing.T, p string) []byte {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestChangeCategoryOfFinishedRowDeletesAndRestarts(t *testing.T) {
	e := newTestEnv(t, "normal", nil)
	away := t.TempDir()
	e.setSettings(func(s *Settings) {
		s.Categories = []Category{
			{Name: "A", DoneDir: filepath.Join(away, "a")},
			{Name: "B", DoneDir: filepath.Join(away, "b")},
		}
	})
	A, B := catIDByName(t, e.m, "A"), catIDByName(t, e.m, "B")
	id := e.addTo(A, 2)[0]
	e.waitComplete(id)
	e.m.SaveM3U8([]int64{id})
	e.waitFor("playlist", func() bool { return e.item(id).M3U8 })
	old := filepath.Join(away, "a", e.site.Filename(2)+".ts")
	oldM3U8 := e.path("", e.site.Filename(2)+".m3u8")
	if !pathExists(old) || !pathExists(oldM3U8) {
		t.Fatal("setup failed")
	}

	n, err := e.m.SetCategory([]int64{id}, B)
	if err != nil || n != 1 {
		t.Fatalf("n=%d err=%v", n, err)
	}
	e.waitComplete(id) // it restarted and finished again
	if pathExists(old) || pathExists(oldM3U8) {
		t.Fatal("old files were not deleted")
	}
	got := filepath.Join(away, "b", e.site.Filename(2)+".ts")
	if !bytes.Equal(mustRead(t, got), e.site.Expected(2, "720p", 0, 20)) {
		t.Fatal("restarted file is wrong")
	}
	if it := e.item(id); it.CategoryName != "B" || it.M3U8 {
		t.Fatalf("%+v", it)
	}
	// same category again: nothing happens
	if n, _ := e.m.SetCategory([]int64{id}, B); n != 0 {
		t.Fatal("changing to the same category must do nothing")
	}
	if !pathExists(got) {
		t.Fatal("file removed by a no-op")
	}
}

func TestChangeCategoryWhileDownloadingStopsDeletesAndRestarts(t *testing.T) {
	e := newTestEnv(t, "normal", func(s *mocksite.Site) { s.Lines = 62; s.Delay = 12 * time.Millisecond })
	away := t.TempDir()
	e.setSettings(func(s *Settings) {
		s.Categories = []Category{
			{Name: "A", IncompleteDir: filepath.Join(away, "a-inc"), DoneDir: filepath.Join(away, "a-done")},
			{Name: "B", IncompleteDir: filepath.Join(away, "b-inc"), DoneDir: filepath.Join(away, "b-done")},
		}
	})
	A, B := catIDByName(t, e.m, "A"), catIDByName(t, e.m, "B")
	id := e.addTo(A, 2)[0]
	e.waitFor("some progress", func() bool { return e.item(id).Percent > 20 })
	oldPartial := filepath.Join(away, "a-inc", e.site.Filename(2)+".ts")
	if !pathExists(oldPartial) {
		t.Fatal("partial file not in the category's unfinished folder")
	}
	if _, err := e.m.SetCategory([]int64{id}, B); err != nil {
		t.Fatal(err)
	}
	e.waitComplete(id)
	if pathExists(oldPartial) {
		t.Fatal("old partial file was not deleted")
	}
	if !bytes.Equal(mustRead(t, filepath.Join(away, "b-done", e.site.Filename(2)+".ts")), e.site.Expected(2, "720p", 0, 60)) {
		t.Fatal("restarted download is damaged")
	}
	if entries, _ := os.ReadDir(filepath.Join(away, "a-done")); len(entries) != 0 {
		t.Fatal("nothing should have been finished in the old category")
	}
	e.noSiteProblems()
}

func TestRemovingACategoryKeepsPartialFilesUsable(t *testing.T) {
	e := newTestEnv(t, "normal", func(s *mocksite.Site) { s.Lines = 62; s.Delay = 12 * time.Millisecond })
	away := t.TempDir()
	e.setSettings(func(s *Settings) {
		s.Categories = []Category{{Name: "A", IncompleteDir: filepath.Join(away, "a-inc"), DoneDir: filepath.Join(away, "a-done")}}
	})
	A := catIDByName(t, e.m, "A")
	id := e.addTo(A, 2)[0]
	e.waitFor("some progress", func() bool { return e.item(id).Percent > 20 })
	e.m.Pause([]int64{id})
	e.waitFor("stopped", func() bool { return !e.busy(id) })

	e.setSettings(func(s *Settings) { s.Categories = nil }) // delete the category
	if it := e.item(id); it.Category != "" {
		t.Fatalf("row should be uncategorised: %+v", it)
	}
	e.m.Start([]int64{id})
	e.waitComplete(id) // resumed from the partial file where it was, finished in the normal folder
	if !bytes.Equal(e.readDone(2), e.site.Expected(2, "720p", 0, 60)) {
		t.Fatal("resumed file is damaged")
	}
	if pathExists(filepath.Join(away, "a-inc", e.site.Filename(2)+".ts")) {
		t.Fatal("partial file should have moved to Done")
	}
}

func TestCategorySurvivesRestart(t *testing.T) {
	e := newTestEnv(t, "normal", func(s *mocksite.Site) { s.Lines = 62; s.Delay = 15 * time.Millisecond })
	away := t.TempDir()
	e.setSettings(func(s *Settings) {
		s.Categories = []Category{{Name: "A", IncompleteDir: filepath.Join(away, "a-inc"), DoneDir: filepath.Join(away, "a-done")}}
	})
	A := catIDByName(t, e.m, "A")
	id := e.addTo(A, 2)[0]
	e.waitFor("some progress", func() bool { return e.item(id).Percent > 20 })
	e.m.Shutdown()

	m2 := e.openManager()
	for _, it := range m2.Snapshot().Items {
		if it.Category != A || it.CategoryName != "A" {
			t.Fatalf("category lost: %+v", it)
		}
	}
	e.m = m2
	e.waitComplete(id)
	if !pathExists(filepath.Join(away, "a-done", e.site.Filename(2)+".ts")) {
		t.Fatal("resumed download did not finish in the category folder")
	}
}

func TestColumnWidthsAreKeptAndCleaned(t *testing.T) {
	dir := t.TempDir()
	m, err := New(dir, "")
	if err != nil {
		t.Fatal(err)
	}
	full := map[string]int{}
	for _, k := range ColumnKeys {
		full[k] = 100
	}
	full["speed"] = 5 // below the minimum
	if err := m.SetColumnWidths(full); err != nil {
		t.Fatal(err)
	}
	if got := m.Snapshot().Columns; got["speed"] != minColumnWidth || got["name"] != 100 {
		t.Fatalf("widths %v", got)
	}
	// saving the Settings page must not touch them
	s := m.Settings()
	s.ColumnWidths = nil
	if err := m.SetSettings(s); err != nil {
		t.Fatal(err)
	}
	m.Shutdown()
	m2, err := New(dir, "")
	if err != nil {
		t.Fatal(err)
	}
	defer m2.Shutdown()
	if got := m2.Snapshot().Columns; got["name"] != 100 || len(got) != len(ColumnKeys) {
		t.Fatalf("after restart: %v", got)
	}
	// an incomplete layout means the default one
	if err := m2.SetColumnWidths(map[string]int{"name": 200}); err != nil {
		t.Fatal(err)
	}
	if m2.Snapshot().Columns != nil {
		t.Fatal("incomplete layout should reset to default")
	}
}
