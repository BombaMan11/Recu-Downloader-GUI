package manager

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"Recurbate/appdata"
	"Recurbate/applog"
)

func pathExists(p string) bool { _, err := os.Stat(p); return err == nil }

func TestFoldersAreBlankByDefault(t *testing.T) {
	m, err := New(t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	defer m.Shutdown()
	s := m.Settings()
	if s.DoneDir != "" || s.IncompleteDir != "" || s.M3U8Dir != "" || s.DataDir != "" {
		t.Fatalf("expected every folder setting blank: %+v", s)
	}
	for _, n := range []string{"Done", "Incomplete", "M3U8"} {
		if pathExists(filepath.Join(m.baseDir, n)) {
			t.Errorf("default subfolder %s should not be created", n)
		}
	}
}

func TestOldDefaultNamesBecomeBlank(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "settings.json"), []byte(`{"doneDir":"Done","incompleteDir":"Incomplete","m3u8Dir":"Mine"}`), 0600)
	m, err := New(dir, "")
	if err != nil {
		t.Fatal(err)
	}
	defer m.Shutdown()
	s := m.Settings()
	if s.DoneDir != "" || s.IncompleteDir != "" || s.M3U8Dir != "Mine" {
		t.Fatalf("%+v", s)
	}
}

func TestMoveDataFolderElsewhere(t *testing.T) {
	e := newTestEnv(t, "series", nil)
	data := filepath.Join(t.TempDir(), "all", "mine")

	e.addVideos(1)
	// a window profile in use in the old data folder
	profile := filepath.Join(e.dir, appdata.ProfileName)
	os.MkdirAll(filepath.Join(profile, "Default"), 0755)
	os.WriteFile(filepath.Join(profile, "Default", "Preferences"), []byte("prefs"), 0600)

	e.setSettings(func(s *Settings) { s.DataDir = data })

	if !pathExists(filepath.Join(data, "settings.json")) || !pathExists(filepath.Join(data, "queue.json")) {
		t.Fatal("files were not written to the new folder")
	}
	if pathExists(filepath.Join(e.dir, "settings.json")) || pathExists(filepath.Join(e.dir, "queue.json")) {
		t.Fatal("old copies were left behind")
	}
	raw, _ := os.ReadFile(filepath.Join(data, "settings.json"))
	if strings.Contains(string(raw), "dataDir") {
		t.Fatalf("the location must not be stored in settings.json: %s", raw)
	}
	loc := appdata.Load(e.dir)
	if loc.Data != data || loc.MoveProfileFrom != e.dir {
		t.Fatalf("pointer file: %+v", loc)
	}
	if loc.ActiveProfileDir(e.dir) != profile {
		t.Fatal("the profile in use must stay where it is until the next start")
	}

	// next start: the window data follows
	e.m.Shutdown()
	if err := appdata.FinishProfileMove(e.dir); err != nil {
		t.Fatal(err)
	}
	moved := filepath.Join(data, appdata.ProfileName, "Default", "Preferences")
	if b, _ := os.ReadFile(moved); string(b) != "prefs" || pathExists(profile) {
		t.Fatal("browser profile did not move")
	}
	if appdata.Load(e.dir).MoveProfileFrom != "" {
		t.Fatal("pending move not cleared")
	}

	// everything is found again
	m2, err := New(e.dir, "")
	if err != nil {
		t.Fatal(err)
	}
	defer m2.Shutdown()
	if m2.Settings().UserAgent != e.site.UA || m2.Settings().DataDir != data {
		t.Fatalf("settings not found after restart: %+v", m2.Settings())
	}
	if len(m2.Snapshot().Items) != 1 {
		t.Fatal("queue not found after restart")
	}

	// back to blank: everything comes home and the pointer file goes away
	s := m2.Settings()
	s.DataDir = ""
	if err := m2.SetSettings(s); err != nil {
		t.Fatal(err)
	}
	if !pathExists(filepath.Join(e.dir, "settings.json")) || !pathExists(filepath.Join(e.dir, "queue.json")) {
		t.Fatal("files did not come home")
	}
	if pathExists(filepath.Join(data, "settings.json")) {
		t.Fatal("old copy still there")
	}
	if loc := appdata.Load(e.dir); loc.Data != "" || loc.MoveProfileFrom != data {
		t.Fatalf("pointer file after moving back: %+v", loc)
	}
}

func TestMoveBackBeforeRestartCancelsProfileMove(t *testing.T) {
	e := newTestEnv(t, "series", nil)
	os.MkdirAll(filepath.Join(e.dir, appdata.ProfileName), 0755)
	other := t.TempDir()
	e.setSettings(func(s *Settings) { s.DataDir = other })
	e.setSettings(func(s *Settings) { s.DataDir = "" })
	if loc := appdata.Load(e.dir); loc != (appdata.Locations{}) {
		t.Fatalf("expected no pointer file left: %+v", loc)
	}
}

func TestMoveRefusedWhenTargetHasFiles(t *testing.T) {
	e := newTestEnv(t, "series", nil)
	other := t.TempDir()
	os.WriteFile(filepath.Join(other, "settings.json"), []byte(`{"userAgent":"precious"}`), 0600)

	s := e.m.Settings()
	s.DataDir = other
	s.Theme = "dark" // must not be applied either
	err := e.m.SetSettings(s)
	if err == nil || !strings.Contains(err.Error(), "already has a settings.json") {
		t.Fatalf("expected a refusal, got %v", err)
	}
	if data, _ := os.ReadFile(filepath.Join(other, "settings.json")); !strings.Contains(string(data), "precious") {
		t.Fatal("the existing file was overwritten")
	}
	if got := e.m.Settings(); got.DataDir != "" || got.Theme == "dark" {
		t.Fatalf("settings changed despite the refusal: %+v", got)
	}
	if !pathExists(filepath.Join(e.dir, "settings.json")) || pathExists(filepath.Join(e.dir, appdata.FileName)) {
		t.Fatal("state on disk changed")
	}
}

func TestMoveRefusedWhenNotWritable(t *testing.T) {
	e := newTestEnv(t, "series", nil)
	file := filepath.Join(t.TempDir(), "afile")
	os.WriteFile(file, []byte("x"), 0600)
	s := e.m.Settings()
	s.DataDir = filepath.Join(file, "sub") // under a file: can not exist
	if err := e.m.SetSettings(s); err == nil {
		t.Fatal("expected an error")
	}
	if e.m.Settings().DataDir != "" {
		t.Fatal("location changed despite the error")
	}
}

func TestRelativeLocationIsNextToProgram(t *testing.T) {
	e := newTestEnv(t, "series", nil)
	e.setSettings(func(s *Settings) { s.DataDir = "data" })
	if !pathExists(filepath.Join(e.dir, "data", "queue.json")) {
		t.Fatal("relative folder is not under the program folder")
	}
}

func TestDownloadsGoToCustomFolders(t *testing.T) {
	e := newTestEnv(t, "series", nil)
	away := t.TempDir()
	e.setSettings(func(s *Settings) {
		s.DoneDir = filepath.Join(away, "finished")
		s.IncompleteDir = filepath.Join(away, "partial")
		s.DataDir = filepath.Join(away, "data")
	})
	ids := e.addVideos(1)
	e.waitComplete(ids...)
	files, _ := os.ReadDir(filepath.Join(away, "finished"))
	if len(files) != 1 {
		t.Fatalf("finished folder: %v", files)
	}
	if m, _ := filepath.Glob(filepath.Join(e.dir, "*.ts")); len(m) != 0 {
		t.Fatalf("video went to the default folder: %v", m)
	}
}

func dirNotEmpty(p string) bool { f, _ := os.ReadDir(p); return len(f) > 0 }

func TestLogMoves(t *testing.T) {
	base := t.TempDir()
	if err := applog.Open(base); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { applog.Move(t.TempDir()) })
	os.WriteFile(filepath.Join(base, applog.FileName), []byte("old line\n"), 0600)
	dst := filepath.Join(t.TempDir(), "logs")
	if err := applog.Move(dst); err != nil {
		t.Fatal(err)
	}
	if pathExists(filepath.Join(base, applog.FileName)) {
		t.Fatal("old log not moved")
	}
	data, _ := os.ReadFile(filepath.Join(dst, applog.FileName))
	if !strings.Contains(string(data), "old line") {
		t.Fatalf("log content lost: %q", data)
	}
}
