package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"Recurbate/manager"
)

// The Quit button's request must finish stopping the downloads before it is
// answered, so the page can close its window knowing everything is saved.
func TestQuitStopsBeforeAnswering(t *testing.T) {
	m, err := manager.New(t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(m.Shutdown)
	var stopped, quit atomic.Bool
	srv := New(Options{
		Manager: m, Token: "tok", Host: "127.0.0.1:1",
		Stop:   func() { time.Sleep(50 * time.Millisecond); stopped.Store(true) },
		OnQuit: func() { quit.Store(true) },
	})
	req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:1/api/quit", nil)
	req.Host = "127.0.0.1:1"
	req.Header.Set("X-RP", "1")
	req.AddCookie(&http.Cookie{Name: "rp", Value: "tok"})
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	if !stopped.Load() {
		t.Fatal("answered before the downloads were stopped")
	}
	deadline := time.Now().Add(3 * time.Second)
	for !quit.Load() && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if !quit.Load() {
		t.Fatal("program did not exit after the answer")
	}
}

func TestClipboardEndpoint(t *testing.T) {
	m, err := manager.New(t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(m.Shutdown)
	post := func(opts Options) *httptest.ResponseRecorder {
		opts.Manager, opts.Token, opts.Host = m, "tok", "127.0.0.1:1"
		req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:1/api/clipboard", nil)
		req.Host = "127.0.0.1:1"
		req.Header.Set("X-RP", "1")
		req.AddCookie(&http.Cookie{Name: "rp", Value: "tok"})
		rec := httptest.NewRecorder()
		New(opts).Handler().ServeHTTP(rec, req)
		return rec
	}
	if rec := post(Options{ReadClipboard: func() (string, error) { return "a=1; b=2", nil }}); rec.Code != 200 || !strings.Contains(rec.Body.String(), `"a=1; b=2"`) {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
	if rec := post(Options{ReadClipboard: func() (string, error) { return "", errors.New("no text") }}); rec.Code != 500 || !strings.Contains(rec.Body.String(), "no text") {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
	if rec := post(Options{}); rec.Code != http.StatusNotImplemented {
		t.Fatalf("%d", rec.Code)
	}
}

func TestCookieEndpointReplacesOnlyTheCookie(t *testing.T) {
	m, err := manager.New(t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(m.Shutdown)
	s := m.Settings()
	s.UserAgent, s.Cookie = "My-UA", "old=1"
	if err := m.SetSettings(s); err != nil {
		t.Fatal(err)
	}
	post := func(body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:1/api/cookie", strings.NewReader(body))
		req.Host = "127.0.0.1:1"
		req.Header.Set("X-RP", "1")
		req.AddCookie(&http.Cookie{Name: "rp", Value: "tok"})
		rec := httptest.NewRecorder()
		New(Options{Manager: m, Token: "tok", Host: "127.0.0.1:1"}).Handler().ServeHTTP(rec, req)
		return rec
	}
	if rec := post(`{"cookie":"  new=2; x=3\r\n"}`); rec.Code != 200 {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
	got := m.Settings()
	if got.Cookie != "new=2; x=3" || got.UserAgent != "My-UA" {
		t.Errorf("cookie %q, user agent %q", got.Cookie, got.UserAgent)
	}
	for _, body := range []string{`{"cookie":""}`, `{"cookie":"   "}`, `{}`, `{"cookie":"` + strings.Repeat("a", 70<<10) + `"}`, `not json`} {
		if rec := post(body); rec.Code != 400 {
			t.Errorf("%.30q: status %d, want 400", body, rec.Code)
		}
	}
	if m.Settings().Cookie != "new=2; x=3" {
		t.Error("a refused request changed the cookie")
	}
}

// After the first full snapshot the event stream sends only the rows that
// changed, and the row order only when it changed.
func TestStreamSendsOnlyChangedRows(t *testing.T) {
	a, b, c := manager.ItemView{ID: 1, Name: "a"}, manager.ItemView{ID: 2, Name: "b"}, manager.ItemView{ID: 3, Name: "c"}
	snap := func(items ...manager.ItemView) manager.Snapshot { return manager.Snapshot{Items: items} }
	js := func(v any) string { out, _ := json.Marshal(v); return string(out) }

	b2 := b
	b2.Percent = 50
	msg := changes(snap(a, b, c), snap(a, b2, c))
	if len(msg.Changed) != 1 || msg.Changed[0] != b2 || msg.Order != nil {
		t.Fatalf("one row changed: got changed=%v order=%v", msg.Changed, msg.Order)
	}
	if s := js(msg); strings.Contains(s, `"items"`) || !strings.Contains(s, `"order":null`) {
		t.Fatalf("message must leave out items and send order null: %s", s)
	}
	if msg := changes(snap(a, b, c), snap(c, a)); len(msg.Changed) != 0 || js(msg.Order) != "[3,1]" {
		t.Fatalf("removed and moved: got changed=%v order=%v", msg.Changed, msg.Order)
	}
	if msg := changes(snap(a), snap(a, b)); len(msg.Changed) != 1 || js(msg.Order) != "[1,2]" {
		t.Fatalf("added: got changed=%v order=%v", msg.Changed, msg.Order)
	}
	if s := js(changes(snap(a), snap())); !strings.Contains(s, `"order":[]`) || !strings.Contains(s, `"changed":[]`) {
		t.Fatalf("list emptied must send order []: %s", s)
	}
}
