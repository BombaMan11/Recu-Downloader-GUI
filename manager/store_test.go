package manager

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func readStr(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// the backup beside a file is a copy of the previous good version, refreshed at
// most every backupEvery, and never made from a damaged file
func TestBackupCopyIsKeptAndNeverMadeFromADamagedFile(t *testing.T) {
	p := filepath.Join(t.TempDir(), "queue.json")
	write := func(s string) {
		t.Helper()
		if err := writeFileAtomic(p, []byte(s), 0600); err != nil {
			t.Fatal(err)
		}
	}
	write(`{"v":1}`)
	if _, err := os.Stat(p + ".bak"); err == nil {
		t.Fatal("a backup of nothing was made")
	}
	write(`{"v":2}`)
	if got := readStr(t, p+".bak"); got != `{"v":1}` {
		t.Fatalf("backup %q, want the previous version", got)
	}
	write(`{"v":3}`)
	if got := readStr(t, p+".bak"); got != `{"v":1}` {
		t.Fatalf("backup %q: it was refreshed again at once", got)
	}
	old := time.Now().Add(-2 * backupEvery)
	os.Chtimes(p+".bak", old, old)
	write(`{"v":4}`)
	if got := readStr(t, p+".bak"); got != `{"v":3}` {
		t.Fatalf("backup %q, want the version before this save once it is old enough", got)
	}
	// the file gets damaged (a crash): the next save must not copy it over the good backup
	os.WriteFile(p, nil, 0600)
	os.Chtimes(p+".bak", old, old)
	write(`{"v":5}`)
	if got := readStr(t, p+".bak"); got != `{"v":3}` {
		t.Fatalf("an empty file replaced the good backup: %q", got)
	}
	if _, err := os.Stat(p + ".tmp"); err == nil {
		t.Error("a temporary file was left behind")
	}
}

func queueWithRows(t *testing.T, dir string) {
	t.Helper()
	m, err := New(dir, "")
	if err != nil {
		t.Fatal(err)
	}
	if n, problems := m.Add("https://Recu.me/video/111/play\nhttps://Recu.me/video/222/play"); n != 2 {
		t.Fatalf("added %d: %v", n, problems)
	}
	m.mu.Lock()
	m.saveQueueLocked() // the second save leaves a backup of the first
	m.saveQueueLocked()
	m.mu.Unlock()
	m.Shutdown()
	if _, err := os.Stat(filepath.Join(dir, "queue.json.bak")); err != nil {
		t.Fatal("no backup was made:", err)
	}
}

func TestEmptyQueueFileIsRestoredFromTheBackup(t *testing.T) {
	dir := t.TempDir()
	queueWithRows(t, dir)
	if err := os.WriteFile(filepath.Join(dir, "queue.json"), nil, 0600); err != nil { // what a crash leaves
		t.Fatal(err)
	}
	m, err := New(dir, "")
	if err != nil {
		t.Fatalf("a blank queue.json must not stop the program: %v", err)
	}
	defer m.Shutdown()
	if n := len(m.Snapshot().Items); n != 2 {
		t.Fatalf("%d rows after the restore, want 2", n)
	}
	rec := m.Snapshot().Recovery
	if !strings.Contains(rec, "queue.json is empty") || !strings.Contains(rec, "restored from queue.json.bak") {
		t.Errorf("message: %q", rec)
	}
	if !strings.Contains(readStr(t, filepath.Join(dir, "queue.json")), "111") {
		t.Error("queue.json was not put back")
	}
	m.DismissRecovery()
	if m.Snapshot().Recovery != "" {
		t.Error("the message was not dismissed")
	}
}

func TestDamagedQueueFileIsKeptAsideAndRestored(t *testing.T) {
	dir := t.TempDir()
	queueWithRows(t, dir)
	good := readStr(t, filepath.Join(dir, "queue.json"))
	os.WriteFile(filepath.Join(dir, "queue.json"), []byte(good[:len(good)/2]), 0600) // cut off half way
	m, err := New(dir, "")
	if err != nil {
		t.Fatal(err)
	}
	defer m.Shutdown()
	if n := len(m.Snapshot().Items); n != 2 {
		t.Fatalf("%d rows, want 2", n)
	}
	if !strings.Contains(m.Snapshot().Recovery, "queue.json is damaged") {
		t.Errorf("message: %q", m.Snapshot().Recovery)
	}
	aside, _ := filepath.Glob(filepath.Join(dir, "queue.json.damaged-*"))
	if len(aside) != 1 || readStr(t, aside[0]) != good[:len(good)/2] {
		t.Errorf("the damaged file was not kept: %v", aside)
	}
}

func TestBlankQueueWithoutABackupStartsEmptyAndSaysSo(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "queue.json"), nil, 0600)
	m, err := New(dir, "")
	if err != nil {
		t.Fatalf("a blank queue.json must not stop the program: %v", err)
	}
	defer m.Shutdown()
	if len(m.Snapshot().Items) != 0 {
		t.Error("rows from nowhere")
	}
	rec := m.Snapshot().Recovery
	if !strings.Contains(rec, "no usable backup") || !strings.Contains(rec, "empty list") {
		t.Errorf("message: %q", rec)
	}
	if n, _ := m.Add("https://Recu.me/video/333/play"); n != 1 {
		t.Error("the list does not work after starting empty")
	}
}

func TestBlankSettingsAreRestoredOrReplacedByDefaults(t *testing.T) {
	dir := t.TempDir()
	m, err := New(dir, "")
	if err != nil {
		t.Fatal(err)
	}
	s := m.Settings()
	s.UserAgent, s.Cookie = "my-agent", "sid=abc"
	if err := m.SetSettings(s); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-2 * backupEvery) // the copy is only refreshed every backupEvery
	os.Chtimes(filepath.Join(dir, "settings.json.bak"), old, old)
	s.Theme = "dark" // this save leaves a backup of the one before
	if err := m.SetSettings(s); err != nil {
		t.Fatal(err)
	}
	m.Shutdown()
	os.WriteFile(filepath.Join(dir, "settings.json"), nil, 0600)
	m2, err := New(dir, "")
	if err != nil {
		t.Fatalf("a blank settings.json must not stop the program: %v", err)
	}
	if got := m2.Settings(); got.Cookie != "sid=abc" || got.UserAgent != "my-agent" {
		t.Errorf("settings not restored: %+v", got)
	}
	if !strings.Contains(m2.Snapshot().Recovery, "settings.json is empty") {
		t.Errorf("message: %q", m2.Snapshot().Recovery)
	}
	m2.Shutdown()

	// no backup: defaults, and a message
	dir2 := t.TempDir()
	os.WriteFile(filepath.Join(dir2, "settings.json"), []byte("{not json"), 0600)
	m3, err := New(dir2, "")
	if err != nil {
		t.Fatal(err)
	}
	defer m3.Shutdown()
	if m3.Settings().Theme != "system" || !strings.Contains(m3.Snapshot().Recovery, "no usable backup") {
		t.Errorf("defaults/message: %+v %q", m3.Settings(), m3.Snapshot().Recovery)
	}
	if aside, _ := filepath.Glob(filepath.Join(dir2, "settings.json.damaged-*")); len(aside) != 1 {
		t.Errorf("damaged settings.json not kept: %v", aside)
	}
}

// a missing file is a normal first start, not a recovery
func TestFreshStartHasNothingToRecover(t *testing.T) {
	m, err := New(t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	defer m.Shutdown()
	if r := m.Snapshot().Recovery; r != "" {
		t.Errorf("recovery on a first start: %q", r)
	}
}
