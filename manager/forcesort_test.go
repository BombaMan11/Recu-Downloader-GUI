package manager

import "testing"

func names(m *Manager) []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []string
	for _, it := range m.items {
		out = append(out, displayName(it))
	}
	return out
}

// Force Name Sort keeps the rows that are not finished in name order (numbers
// as numbers, capitals ignored). Finished rows keep their places, and a row that
// still shows its link, because its page has not been read, goes last.
func TestForceNameSortOrder(t *testing.T) {
	e := newTestEnv(t, "series", nil)
	e.m.mu.Lock()
	e.m.items = []*Item{
		{ID: 1, Status: StatusComplete, FileBase: "b"},
		{ID: 2, Status: StatusQueued, FileBase: "user10_x"},
		{ID: 3, Status: StatusQueued, URL: "https://Recu.me/video/1/play"},
		{ID: 4, Status: StatusQueued, FileBase: "User9_x"},
		{ID: 5, Status: StatusComplete, FileBase: "a"},
		{ID: 6, Status: StatusDownloading, FileBase: "Amy"},
	}
	for _, c := range []struct {
		force string
		want  []int64
	}{
		{"", []int64{1, 2, 3, 4, 5, 6}},
		{"az", []int64{1, 6, 4, 2, 5, 3}},
		{"za", []int64{1, 2, 4, 6, 5, 3}},
	} {
		e.m.settings.ForceNameSort = c.force
		e.m.sortQueueLocked()
		var got []int64
		for _, it := range e.m.items {
			got = append(got, it.ID)
		}
		if len(got) != len(c.want) {
			t.Fatalf("force %q: %v", c.force, got)
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Errorf("force %q: order %v, want %v", c.force, got, c.want)
				break
			}
		}
	}
	e.m.mu.Unlock()
}

// Rows added behind a downloading one are read at once, and as their names come
// in they are put in order: the downloading row is not stopped, it just ends up
// where its name belongs.
func TestForceNameSortOnAddedRows(t *testing.T) {
	e := newTestEnv(t, "series", slowSite)
	e.setSettings(func(s *Settings) { s.ForceNameSort = "az" })
	first := e.addVideos(30)[0]
	e.waitFor("the first to download", func() bool { return e.item(first).Status == "downloading" })
	rest := e.addVideos(12, 4)
	want := []int64{rest[1], rest[0], first} // user4, user12, user30
	e.waitFor("the rows to be put in order", func() bool {
		items := e.m.Snapshot().Items
		if len(items) != 3 {
			return false
		}
		for i, id := range want {
			if items[i].ID != id {
				return false
			}
		}
		return true
	})
	e.waitComplete(want...)
}

func TestForceNameSortIsKept(t *testing.T) {
	e := newTestEnv(t, "series", nil)
	if DefaultSettings().ForceNameSort != "" {
		t.Error("on in a new install")
	}
	e.setSettings(func(s *Settings) { s.ForceNameSort = "za" })
	m2, err := New(e.dir, "")
	if err != nil {
		t.Fatal(err)
	}
	if got := m2.Settings().ForceNameSort; got != "za" {
		t.Errorf("after reopening: %q, want za", got)
	}
	e.setSettings(func(s *Settings) { s.ForceNameSort = "nonsense" })
	if got := e.m.Settings().ForceNameSort; got != "" {
		t.Errorf("an unknown value was kept: %q", got)
	}
}
