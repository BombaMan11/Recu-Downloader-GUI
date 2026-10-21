// Package server serves the user interface (embedded in the executable) and
// the JSON / event-stream API it talks to. It only listens on 127.0.0.1 and
// every request needs the per-launch token.
package server

import (
	"crypto/subtle"
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"Recurbate/manager"
)

//go:embed web
var webFS embed.FS

type Options struct {
	Manager     *manager.Manager
	Token       string
	Host        string // "127.0.0.1:port" the server is reachable on
	ExitOnClose bool   // quit when the window has been closed for a few seconds
	OnQuit      func()
	// Stop pauses the downloads and saves everything (blocks until done). The
	// Quit button calls it before the window is told to close.
	Stop       func()
	OpenFolder func(path string) error
	// PickFolder shows a folder chooser and returns the folder ("" = cancelled)
	PickFolder func(start string) (string, error)
	// ReadClipboard returns the text on the system clipboard (nil = not available;
	// the page then tries the browser's own clipboard read, which asks permission)
	ReadClipboard func() (string, error)
	// RevealFile shows a file in the file manager, selected in its folder
	RevealFile func(path string) error
	// timing, only changed by tests
	QuitGrace time.Duration // window gone -> quit (default 8s)
}

type Server struct {
	opts Options

	mu            sync.Mutex
	clients       int
	everConnected bool
	idle          *time.Timer
	stop          chan struct{}
	stopOnce      sync.Once
}

func New(opts Options) *Server {
	if opts.QuitGrace == 0 {
		opts.QuitGrace = 8 * time.Second
	}
	return &Server{opts: opts, stop: make(chan struct{})}
}

// Close ends the open event streams.
func (s *Server) Close() { s.stopOnce.Do(func() { close(s.stop) }) }

func (s *Server) tokenOK(t string) bool {
	return subtle.ConstantTimeCompare([]byte(t), []byte(s.opts.Token)) == 1
}

// allowed checks the Host header (DNS rebinding) and the launch token.
// It returns false after it has written the response itself.
func (s *Server) allowed(w http.ResponseWriter, r *http.Request) bool {
	if r.Host != s.opts.Host && r.Host != "localhost:"+strings.TrimPrefix(s.opts.Host, "127.0.0.1:") {
		http.Error(w, "forbidden host", http.StatusForbidden)
		return false
	}
	if t := r.URL.Query().Get("t"); t != "" && s.tokenOK(t) {
		http.SetCookie(w, &http.Cookie{Name: "rp", Value: t, Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode})
		if r.Method == http.MethodGet && !strings.HasPrefix(r.URL.Path, "/api/") {
			http.Redirect(w, r, r.URL.Path, http.StatusSeeOther)
			return false
		}
		return true
	}
	c, err := r.Cookie("rp")
	if err != nil || !s.tokenOK(c.Value) {
		http.Error(w, "Open Recurbate from its own window.", http.StatusUnauthorized)
		return false
	}
	return true
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	sub, _ := fs.Sub(webFS, "web")
	static := http.FileServer(http.FS(sub))
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if !s.allowed(w, r) {
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		static.ServeHTTP(w, r)
	})
	mux.HandleFunc("/api/ping", func(w http.ResponseWriter, r *http.Request) {
		if !s.allowed(w, r) {
			return
		}
		fmt.Fprint(w, "pong")
	})
	mux.HandleFunc("/api/state", s.api(http.MethodGet, s.handleState))
	mux.HandleFunc("/api/events", s.handleEvents)
	mux.HandleFunc("/api/add", s.api(http.MethodPost, s.handleAdd))
	mux.HandleFunc("/api/action", s.api(http.MethodPost, s.handleAction))
	mux.HandleFunc("/api/mode", s.api(http.MethodPost, s.handleMode))
	mux.HandleFunc("/api/scan", s.api(http.MethodPost, s.handleScan))
	mux.HandleFunc("/api/open", s.api(http.MethodPost, s.handleOpen))
	mux.HandleFunc("/api/reveal", s.api(http.MethodPost, s.handleReveal))
	mux.HandleFunc("/api/browse", s.api(http.MethodPost, s.handleBrowse))
	mux.HandleFunc("/api/clipboard", s.api(http.MethodPost, s.handleClipboard))
	mux.HandleFunc("/api/cookie", s.api(http.MethodPost, s.handleCookie))
	mux.HandleFunc("/api/listen", s.api(http.MethodPost, s.handleListen))
	mux.HandleFunc("/api/listen/test", s.api(http.MethodPost, s.handleListenTest))
	mux.HandleFunc("/api/quit", s.api(http.MethodPost, s.handleQuit))
	mux.HandleFunc("/api/columns", s.api(http.MethodPost, s.handleColumns))
	mux.HandleFunc("/api/settings", s.api("", s.handleSettings))
	return mux
}

// api wraps a handler with the access checks. method "" accepts GET and PUT.
// Writes need the X-RP header, which a web page on another origin cannot send
// without a CORS preflight that this server never answers.
func (s *Server) api(method string, h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !s.allowed(w, r) {
			return
		}
		if method != "" && r.Method != method || method == "" && r.Method != http.MethodGet && r.Method != http.MethodPut {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if r.Method != http.MethodGet && r.Header.Get("X-RP") != "1" {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		h(w, r)
	}
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

func fail(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

func readJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 8<<20)
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		fail(w, http.StatusBadRequest, "invalid request: "+err.Error())
		return false
	}
	return true
}

func (s *Server) handleState(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, s.opts.Manager.Snapshot())
}

// handleEvents streams the table to the window. The open connection also tells
// us the window is still there: when the last one closes the program exits.
func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	if !s.allowed(w, r) {
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Accel-Buffering", "no")
	s.clientUp()
	defer s.clientDown()

	var last uint64
	send := func() bool {
		snap := s.opts.Manager.Snapshot()
		b, err := json.Marshal(snap)
		if err != nil {
			return false
		}
		if _, err := fmt.Fprintf(w, "data: %s\n\n", b); err != nil {
			return false
		}
		flusher.Flush()
		last = snap.Version
		return true
	}
	if !send() {
		return
	}
	tick := time.NewTicker(300 * time.Millisecond)
	defer tick.Stop()
	keepAlive := time.NewTicker(15 * time.Second)
	defer keepAlive.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-s.stop:
			return
		case <-keepAlive.C:
			if _, err := fmt.Fprint(w, ": keep-alive\n\n"); err != nil {
				return
			}
			flusher.Flush()
		case <-tick.C:
			if s.opts.Manager.Version() != last && !send() {
				return
			}
		}
	}
}

func (s *Server) clientUp() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.clients++
	s.everConnected = true
	if s.idle != nil {
		s.idle.Stop()
		s.idle = nil
	}
}

func (s *Server) clientDown() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.clients--
	if s.clients > 0 || !s.opts.ExitOnClose || !s.everConnected || s.opts.OnQuit == nil {
		return
	}
	noClients := func() bool {
		s.mu.Lock()
		defer s.mu.Unlock()
		return s.clients == 0
	}
	s.idle = time.AfterFunc(s.opts.QuitGrace, func() {
		if noClients() {
			s.opts.OnQuit()
		}
	})
}

type addRequest struct {
	Text     string `json:"text"`
	Category string `json:"category"` // category id, "" = none
}

func (s *Server) handleAdd(w http.ResponseWriter, r *http.Request) {
	var req addRequest
	if !readJSON(w, r, &req) {
		return
	}
	added, problems := s.opts.Manager.AddTo(req.Text, req.Category)
	writeJSON(w, map[string]any{"added": added, "problems": problems})
}

type actionRequest struct {
	Action   string  `json:"action"`
	IDs      []int64 `json:"ids"`
	Category string  `json:"category"` // for "setcategory"
}

func plural(n int, one, many string) string {
	if n == 1 {
		return fmt.Sprintf("1 %s", one)
	}
	return fmt.Sprintf("%d %s", n, many)
}

func (s *Server) handleAction(w http.ResponseWriter, r *http.Request) {
	var req actionRequest
	if !readJSON(w, r, &req) {
		return
	}
	m := s.opts.Manager
	msg := ""
	switch req.Action {
	case "start":
		m.Start(req.IDs)
	case "pause":
		m.Pause(req.IDs)
	case "forcestart":
		if n := m.ForceStart(req.IDs); n > 0 {
			msg = plural(n, "inactive row was", "inactive rows were") + " not started. Set them to active first."
		}
	case "forcestartall":
		if n := m.ForceStartAll(); n > 0 {
			msg = plural(n, "inactive row was", "inactive rows were") + " not started. Set them to active first."
		}
	case "startall":
		m.StartAll()
	case "pauseall":
		m.PauseAll()
	case "top", "up", "down", "bottom":
		m.Move(req.IDs, req.Action)
	case "arrangetop", "arrangebottom":
		m.Arrange(req.IDs, req.Action == "arrangebottom")
	case "remove":
		m.Remove(req.IDs, false)
	case "removefiles":
		m.Remove(req.IDs, true)
	case "clearcompleted":
		m.ClearCompleted()
	case "m3u8":
		m.SaveM3U8(req.IDs)
		msg = "Getting the playlist…"
	case "setcategory":
		n, err := m.SetCategory(req.IDs, req.Category)
		if err != nil {
			fail(w, http.StatusBadRequest, err.Error())
			return
		}
		msg = "Changed the category of " + plural(n, "row", "rows") + ". They start again in the new folders."
		if n == 0 {
			msg = ""
		}
	case "activate":
		m.SetActive(req.IDs, true)
	case "deactivate":
		m.SetActive(req.IDs, false)
	default:
		fail(w, http.StatusBadRequest, "unknown action "+req.Action)
		return
	}
	writeJSON(w, map[string]string{"message": msg})
}

func (s *Server) handleMode(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Mode string `json:"mode"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	if err := s.opts.Manager.SetMode(req.Mode); err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, map[string]string{"mode": req.Mode})
}

func (s *Server) handleColumns(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Widths map[string]int `json:"widths"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	if err := s.opts.Manager.SetColumnWidths(req.Widths); err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, map[string]any{"widths": s.opts.Manager.Settings().ColumnWidths})
}

func (s *Server) handleScan(w http.ResponseWriter, r *http.Request) {
	var req struct {
		URL string `json:"url"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	if u := strings.TrimSpace(req.URL); !strings.HasPrefix(u, "http://") && !strings.HasPrefix(u, "https://") {
		fail(w, http.StatusBadRequest, "Enter the address of a page, starting with https://")
		return
	}
	urls, err := s.opts.Manager.ScanPage(strings.TrimSpace(req.URL))
	if err != nil {
		fail(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, map[string]any{"urls": urls})
}

func (s *Server) handleOpen(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Which string `json:"which"`
		ID    *int64 `json:"id"` // optional: the folder as it applies to this row's category
	}
	if !readJSON(w, r, &req) {
		return
	}
	var dir string
	var err error
	if req.ID != nil {
		dir, err = s.opts.Manager.DirFor(*req.ID, req.Which)
	} else {
		dir, err = s.opts.Manager.Dir(req.Which)
	}
	if err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	if s.opts.OpenFolder == nil {
		fail(w, http.StatusNotImplemented, "can not open folders here")
		return
	}
	if err := s.opts.OpenFolder(dir); err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, map[string]string{"path": dir})
}

func (s *Server) handleReveal(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ID int64 `json:"id"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	path, err := s.opts.Manager.FilePath(req.ID)
	if err != nil {
		fail(w, http.StatusNotFound, err.Error())
		return
	}
	if s.opts.RevealFile == nil {
		fail(w, http.StatusNotImplemented, "Can not open folders here.")
		return
	}
	if err := s.opts.RevealFile(path); err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, map[string]string{"path": path})
}

func (s *Server) handleBrowse(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Start string `json:"start"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	if s.opts.PickFolder == nil {
		fail(w, http.StatusNotImplemented, "Choosing a folder is not available here. Type the path instead.")
		return
	}
	start := ""
	if filepath.IsAbs(req.Start) {
		start = req.Start
	}
	path, err := s.opts.PickFolder(start)
	if err != nil {
		fail(w, http.StatusInternalServerError, "Could not open the folder chooser: "+err.Error())
		return
	}
	writeJSON(w, map[string]string{"path": path})
}

// handleClipboard answers with the text currently on the clipboard.
func (s *Server) handleClipboard(w http.ResponseWriter, r *http.Request) {
	if s.opts.ReadClipboard == nil {
		fail(w, http.StatusNotImplemented, "Reading the clipboard is not available here.")
		return
	}
	text, err := s.opts.ReadClipboard()
	if err != nil {
		fail(w, http.StatusInternalServerError, "Could not read the clipboard: "+err.Error())
		return
	}
	writeJSON(w, map[string]string{"text": text})
}

// handleCookie replaces the saved cookie (the main window's Replace cookie button).
func (s *Server) handleCookie(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Cookie string `json:"cookie"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	paused, err := s.opts.Manager.SetCookie(req.Cookie)
	if err != nil {
		fail(w, http.StatusBadRequest, "Replace cookie: "+err.Error())
		return
	}
	writeJSON(w, map[string]int{"length": len(strings.TrimSpace(req.Cookie)), "paused": paused})
}

// handleListen switches the link listener on or off.
func (s *Server) handleListen(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Enabled bool `json:"enabled"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	if err := s.opts.Manager.SetListenEnabled(req.Enabled); err != nil {
		fail(w, http.StatusBadRequest, "Link listener: "+err.Error())
		return
	}
	writeJSON(w, map[string]bool{"enabled": req.Enabled})
}

// handleListenTest shows which links some patterns would pick out of a text,
// for the test box on the settings page. Nothing is saved or written.
func (s *Server) handleListenTest(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Patterns []string `json:"patterns"`
		Text     string   `json:"text"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	found, err := manager.TryPatterns(req.Patterns, req.Text)
	if err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	links, box := []string{}, []string{} // the text file gets plain links, the link box gets entries
	seen := map[string]bool{}
	for _, f := range found {
		box = append(box, f.Entry)
		if !seen[f.Link] {
			seen[f.Link] = true
			links = append(links, f.Link)
		}
	}
	writeJSON(w, map[string]interface{}{"links": links, "box": box})
}

// handleQuit stops the downloads gracefully, answers, and only then lets the
// program exit, so the page can close its window knowing everything is saved.
func (s *Server) handleQuit(w http.ResponseWriter, r *http.Request) {
	if s.opts.Stop != nil {
		s.opts.Stop()
	}
	writeJSON(w, map[string]string{"message": "stopped"})
	if s.opts.OnQuit != nil {
		time.AfterFunc(500*time.Millisecond, s.opts.OnQuit) // let the answer reach the page
	}
}

func (s *Server) handleSettings(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		writeJSON(w, s.opts.Manager.Settings())
		return
	}
	var st manager.Settings
	if !readJSON(w, r, &st) {
		return
	}
	if err := s.opts.Manager.SetSettings(st); err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, s.opts.Manager.Settings())
}
