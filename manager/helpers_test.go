package manager

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"Recurbate/internal/mocksite"
)

type testEnv struct {
	t    *testing.T
	site *mocksite.Site
	m    *Manager
	dir  string
}

// newTestEnv starts a mock site and a manager in a temp folder with working
// credentials, in the given mode.
func newTestEnv(t *testing.T, mode string, tweak func(*mocksite.Site)) *testEnv {
	t.Helper()
	site := mocksite.New()
	t.Cleanup(site.Close)
	if tweak != nil {
		tweak(site)
	}
	dir := t.TempDir()
	e := &testEnv{t: t, site: site, dir: dir}
	e.m = e.openManager()
	e.setSettings(func(s *Settings) {
		s.UserAgent = site.UA
		s.Cookie = site.Cookie
		s.AutoStart, s.AutoResume = true, true // off in a new install; most tests want rows to run
	})
	if err := e.m.SetMode(mode); err != nil {
		t.Fatal(err)
	}
	return e
}

func (e *testEnv) openManager() *Manager {
	m, err := New(e.dir, "")
	if err != nil {
		e.t.Fatal(err)
	}
	m.Run()
	e.t.Cleanup(m.Shutdown)
	return m
}

func (e *testEnv) setSettings(f func(*Settings)) {
	e.t.Helper()
	s := e.m.Settings()
	f(&s)
	if err := e.m.SetSettings(s); err != nil {
		e.t.Fatal(err)
	}
}

// add adds one row per line and returns their ids (in order).
func (e *testEnv) add(lines ...string) []int64 {
	e.t.Helper()
	before := len(e.m.Snapshot().Items)
	n, problems := e.m.Add(strings.Join(lines, "\n"))
	if n != len(lines) {
		e.t.Fatalf("added %d of %d rows: %v", n, len(lines), problems)
	}
	var ids []int64
	for _, it := range e.m.Snapshot().Items[before:] {
		ids = append(ids, it.ID)
	}
	return ids
}

func (e *testEnv) addVideos(ids ...int) []int64 {
	var lines []string
	for _, id := range ids {
		lines = append(lines, e.site.VideoURL(id))
	}
	return e.add(lines...)
}

func (e *testEnv) item(id int64) ItemView {
	for _, it := range e.m.Snapshot().Items {
		if it.ID == id {
			return it
		}
	}
	e.t.Fatalf("item %d not in the table", id)
	return ItemView{}
}

func (e *testEnv) busy(id int64) bool {
	e.m.mu.Lock()
	defer e.m.mu.Unlock()
	for _, it := range e.m.items {
		if it.ID == id {
			return it.busy
		}
	}
	return false
}

func (e *testEnv) offset(id int64) (int, int64) {
	e.m.mu.Lock()
	defer e.m.mu.Unlock()
	for _, it := range e.m.items {
		if it.ID == id {
			return it.Offset, it.FileSize
		}
	}
	return 0, 0
}

func (e *testEnv) waitFor(desc string, cond func() bool) {
	e.t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(3 * time.Millisecond)
	}
	e.t.Fatalf("timed out waiting for %s\nitems: %+v\nsite problems: %v", desc, e.m.Snapshot().Items, e.site.Problems())
}

func (e *testEnv) waitStatus(id int64, status Status) {
	e.t.Helper()
	e.waitFor(fmt.Sprintf("item %d to be %s", id, status), func() bool { return e.item(id).Status == string(status) })
}

func (e *testEnv) waitComplete(ids ...int64) {
	e.t.Helper()
	for _, id := range ids {
		e.waitStatus(id, StatusComplete)
	}
}

func (e *testEnv) path(sub, name string) string {
	return filepath.Join(e.dir, sub, name)
}

func (e *testEnv) readDone(id int) []byte {
	e.t.Helper()
	b, err := os.ReadFile(e.path("", e.site.Filename(id)+".ts"))
	if err != nil {
		e.t.Fatalf("done file for video %d: %v", id, err)
	}
	return b
}

func (e *testEnv) noSiteProblems() {
	e.t.Helper()
	if p := e.site.Problems(); len(p) > 0 {
		e.t.Fatalf("mock site saw wrong requests: %v", p)
	}
}

// sampler records which rows are working at each instant.
type sampler struct {
	stop chan struct{}
	wg   sync.WaitGroup
	mu   sync.Mutex
	hist []map[int64]Status // working rows (resolving / downloading) per sample
}

func (e *testEnv) sample() *sampler {
	s := &sampler{stop: make(chan struct{})}
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		for {
			select {
			case <-s.stop:
				return
			case <-time.After(2 * time.Millisecond):
			}
			cur := map[int64]Status{}
			for _, it := range e.m.Snapshot().Items {
				if it.Status == string(StatusResolving) || it.Status == string(StatusDownloading) {
					cur[it.ID] = Status(it.Status)
				}
			}
			s.mu.Lock()
			s.hist = append(s.hist, cur)
			s.mu.Unlock()
		}
	}()
	return s
}

func (s *sampler) finish() [][]int64 {
	close(s.stop)
	s.wg.Wait()
	s.mu.Lock()
	defer s.mu.Unlock()
	var out [][]int64
	for _, h := range s.hist {
		var ids []int64
		for id := range h {
			ids = append(ids, id)
		}
		out = append(out, ids)
	}
	return out
}

// maxWorking is the most rows that were ever working at the same time.
func maxWorking(hist [][]int64) int {
	m := 0
	for _, h := range hist {
		if len(h) > m {
			m = len(h)
		}
	}
	return m
}

func working(h []int64, id int64) bool {
	for _, x := range h {
		if x == id {
			return true
		}
	}
	return false
}

// together reports whether the given rows were all working at the same instant.
func together(hist [][]int64, ids ...int64) bool {
	for _, h := range hist {
		all := true
		for _, id := range ids {
			if !working(h, id) {
				all = false
			}
		}
		if all {
			return true
		}
	}
	return false
}

// startOrder lists the rows in the order they were first seen working.
func startOrder(hist [][]int64) []int64 {
	seen := map[int64]bool{}
	var order []int64
	for _, h := range hist {
		for _, id := range h {
			if !seen[id] {
				seen[id] = true
				order = append(order, id)
			}
		}
	}
	return order
}
