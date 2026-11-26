package manager

import (
	"os"
	"path/filepath"
	"testing"

	"Recurbate/engine"
	"Recurbate/playlist"
)

func part(id int64, link, start, end string) *Item {
	return &Item{ID: id, URL: link, Status: StatusQueued, SectionRef: []string{start, end, "2:00:00"}}
}

func named5(m *Manager, name string, rows ...*Item) {
	rows[0].res = &engine.Resolved{Playlist: playlist.Playlist{Filename: name}}
	m.mu.Lock()
	m.nameRowsLocked()
	m.mu.Unlock()
}

// The parts of one link are numbered by their start time as soon as the name is
// known: the one nearest 00:00:00 has the plain name, the next name_01, and so
// on, whatever order they are in the list.
func TestPartsAreNumberedByStartTime(t *testing.T) {
	m, err := New(t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	defer m.Shutdown()
	const link = "https://Recu.me/video/1/play"
	late, first, middle := part(1, link, "1:00:00", "1:20:00"), part(2, link, "0:00", "20:00"), part(3, link, "20:00", "40:00")
	unread := &Item{ID: 4, URL: "https://Recu.me/video/2/play", Status: StatusQueued}
	m.items = []*Item{late, first, middle, unread}
	named5(m, "clip", middle) // only one of them has been read: the name is the same for all
	for it, want := range map[*Item]string{first: "clip", middle: "clip_01", late: "clip_02", unread: ""} {
		if it.FileBase != want {
			t.Errorf("row %d is %q, want %q", it.ID, it.FileBase, want)
		}
	}
	// a part added later gets the next number after the highest, whatever its time
	early := part(5, link, "0:00", "5:00")
	m.items = append(m.items, early)
	named5(m, "clip", early)
	if early.FileBase != "clip_03" {
		t.Errorf("a part added later is %q, want clip_03", early.FileBase)
	}
}

// A single part keeps the plain name; a name a file already has is skipped.
func TestPartNamesSkipNamesInUse(t *testing.T) {
	m, err := New(t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	defer m.Shutdown()
	const link = "https://Recu.me/video/1/play"
	single := part(1, link, "10:00", "20:00")
	m.items = []*Item{single}
	named5(m, "solo", single)
	if single.FileBase != "solo" {
		t.Errorf("a single part is %q, want solo (no number)", single.FileBase)
	}
	m.mu.Lock()
	done := m.doneDirLocked(single)
	m.mu.Unlock()
	os.MkdirAll(done, 0755)
	os.WriteFile(filepath.Join(done, "clip_01.ts"), nil, 0600)
	a, b, c := part(2, "https://Recu.me/video/3/play", "0:00", "1:00"), part(3, "https://Recu.me/video/3/play", "1:00", "2:00"), part(4, "https://Recu.me/video/3/play", "2:00", "3:00")
	m.items = append(m.items, c, b, a)
	named5(m, "clip", a)
	if a.FileBase != "clip" || b.FileBase != "clip_02" || c.FileBase != "clip_03" {
		t.Errorf("names %q %q %q, want clip clip_02 clip_03 (clip_01 is a file already)", a.FileBase, b.FileBase, c.FileBase)
	}
}
