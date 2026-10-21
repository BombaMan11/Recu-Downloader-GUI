package manager

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// The link listener watches the clipboard (like JDownloader's clipboard
// observer). While it is switched on, every time something is copied it looks
// for links with the patterns from Settings and adds each one, on its own
// line, at the TOP of one text file.
//
// What keeps it safe:
//   - it only runs while the program is open; the main window shows when it
//     is on, and the person can switch it off there or in Settings;
//   - it only ever looks at text, never keeps or shows the rest of what was
//     copied, and ignores clipboards that password managers mark as private;
//   - patterns are Go regular expressions (RE2): they can't hang the program
//     however they are written; their number and length, the size of the
//     clipboard text and the number of links per copy are capped;
//   - it only writes to the one text file chosen in Settings (a plain-text
//     extension, never a program or settings file), and refuses a file that
//     does not look like text.

// ListenSettings is the "listen" part of settings.json.
type ListenSettings struct {
	Enabled        bool     `json:"enabled"`
	File           string   `json:"file"`           // the text file the links are added to ("" = none)
	Patterns       []string `json:"patterns"`       // regular expressions, see ExtractLinks
	SkipDuplicates bool     `json:"skipDuplicates"` // leave out links that are already in the file
	AllowPrivate   bool     `json:"allowPrivate"`   // also read copies the program that made them marked private
	// The archive is the text file above. Both switches are "No…" because
	// settings written by earlier versions always carry them, so those keep
	// what they had; a new install starts with both off (DefaultSettings).
	// Every link that is found goes into the link box of the main window.
	NoArchive bool `json:"noArchive"` // do not write the links to the text file
	NoBackups bool `json:"noBackups"` // do not keep the daily copies and the log of added links
}

// archivePath is the text file the links are added to, "" when the archive is off.
func (l ListenSettings) archivePath() string {
	if l.NoArchive {
		return ""
	}
	return l.File
}

// backupsOn: the daily copies of the archive and the log of added links. They
// are copies of the archive, so there are none without it.
func (l ListenSettings) backupsOn() bool { return !l.NoArchive && !l.NoBackups }

// DefaultListenPatterns finds the video links the program downloads, also
// inside a pasted ["link","start","end"] line.
var DefaultListenPatterns = []string{
	`https?://[^\s"'<>\[\](),]+/video/[^\s"'<>\[\](),/]+/play`,
}

const (
	maxListenPatterns = 50
	maxPatternLen     = 500
	maxClipboardText  = 1 << 20  // bigger clipboards are ignored
	maxLinksPerCopy   = 500      // more links than this in one copy are cut off
	maxLinkLen        = 4096     //
	maxListenFile     = 50 << 20 // a bigger file is refused (it is rewritten for every copy)
	maxPendingLinks   = 5000
	listenEvery       = 400 * time.Millisecond
)

// listenExts are the file types the listener may write to.
var listenExts = map[string]bool{".txt": true, ".text": true, ".list": true, ".lst": true, ".md": true, ".csv": true}

func (l *ListenSettings) normalize() {
	l.File = strings.TrimSpace(l.File)
	l.Patterns = cleanPatterns(l.Patterns)
	if len(l.Patterns) == 0 {
		l.Patterns = append([]string(nil), DefaultListenPatterns...)
	}
}

// cleanPatterns trims, drops blank lines, # comments and repeats, and applies the caps.
func cleanPatterns(in []string) []string {
	var out []string
	seen := map[string]bool{}
	for _, p := range in {
		p = strings.TrimSpace(p)
		if p == "" || strings.HasPrefix(p, "#") || seen[p] || len(out) >= maxListenPatterns {
			continue
		}
		if r := []rune(p); len(r) > maxPatternLen {
			p = string(r[:maxPatternLen])
		}
		seen[p] = true
		out = append(out, p)
	}
	return out
}

// compilePatterns compiles every pattern; the error names the first bad one.
func compilePatterns(pats []string) ([]*regexp.Regexp, error) {
	var out []*regexp.Regexp
	for i, p := range pats {
		re, err := regexp.Compile(p)
		if err != nil {
			return nil, fmt.Errorf("pattern %d is not a valid regular expression: %s", i+1, strings.TrimPrefix(err.Error(), "error parsing regexp: "))
		}
		out = append(out, re)
	}
	return out, nil
}

// validate is checked when settings are saved.
func (l ListenSettings) validate() error {
	if _, err := compilePatterns(l.Patterns); err != nil {
		return err
	}
	if l.NoArchive {
		return nil
	}
	if l.File != "" && !listenExts[strings.ToLower(filepath.Ext(l.File))] {
		return errors.New("the link listener can only write to a text file (.txt, .text, .list, .lst, .md or .csv)")
	}
	return nil
}

// Found is one link the patterns picked out of a copied text.
//
// Link is the plain link, and is what goes into the text file. Entry is what
// goes into the link box: the same as Link, unless the link was copied inside
// an entry like ["https://…/play","0:00:00","1:30:00","1:30:00"], in which case
// Entry is that whole entry (without the trailing comma) so its start and end
// times are kept.
type Found struct {
	Link  string `json:"link"`
	Entry string `json:"entry"`
}

// entryRe is an entry as Recurbate's link box understands it: a quoted link
// followed by two or three more quoted values (start, end, and the length of
// the whole video).
var entryRe = regexp.MustCompile(`\[\s*"(?:[^"\\]|\\.)*"(?:\s*,\s*"(?:[^"\\]|\\.)*"){2,3}\s*\]`)

// ExtractFound returns the links found in text, in the order they appear.
// Each pattern is a regular expression. When it has a capture group, group 1
// is the link (so `"(https://[^"]+)"` leaves out the quotes); without one the
// whole match is the link. The same entry twice in one text is returned once.
func ExtractFound(text string, res []*regexp.Regexp) []Found {
	type hit struct {
		pos, end int
		link     string
	}
	var hits []hit
	for _, re := range res {
		for _, ix := range re.FindAllStringSubmatchIndex(text, -1) {
			s, e := ix[0], ix[1]
			if re.NumSubexp() > 0 && ix[2] >= 0 && ix[3] > ix[2] {
				s, e = ix[2], ix[3]
			}
			link := strings.TrimSpace(text[s:e])
			if link == "" || len(link) > maxLinkLen || strings.ContainsAny(link, "\r\n\x00") {
				continue // a line of the file is one link
			}
			hits = append(hits, hit{s, e, link})
			if len(hits) > 20*maxLinksPerCopy {
				break
			}
		}
	}
	sort.SliceStable(hits, func(i, j int) bool { return hits[i].pos < hits[j].pos })

	entries := entryRe.FindAllStringIndex(text, -1)
	var out []Found
	seen := map[string]bool{}
	for _, h := range hits {
		entry := h.link
		for _, en := range entries {
			if en[0] <= h.pos && h.end <= en[1] {
				if t := text[en[0]:en[1]]; len(t) <= maxLinkLen && !strings.ContainsAny(t, "\r\n\x00") {
					entry = t
				}
				break
			}
		}
		if seen[entry] {
			continue
		}
		seen[entry] = true
		out = append(out, Found{Link: h.link, Entry: entry})
		if len(out) >= maxLinksPerCopy {
			break
		}
	}
	return out
}

// ExtractLinks returns just the plain links (what goes into the text file),
// each once.
func ExtractLinks(text string, res []*regexp.Regexp) []string {
	var out []string
	seen := map[string]bool{}
	for _, f := range ExtractFound(text, res) {
		if !seen[f.Link] {
			seen[f.Link] = true
			out = append(out, f.Link)
		}
	}
	return out
}

// TryPatterns is what the Settings page's test box runs: what the given
// patterns (one per line) would pick out of text.
func TryPatterns(patterns []string, text string) ([]Found, error) {
	res, err := compilePatterns(cleanPatterns(patterns))
	if err != nil {
		return nil, err
	}
	if len(res) == 0 {
		res, _ = compilePatterns(DefaultListenPatterns)
	}
	if len(text) > maxClipboardText {
		text = text[:maxClipboardText]
	}
	return ExtractFound(text, res), nil
}

// ---- writing the file ----

// prependLinks puts links, one per line, above everything already in the file
// at path. It can only ADD: the old content is carried over byte for byte (line
// endings and byte order mark included) and the result is checked before it
// replaces the file. With skipDup, links that already are a line of the file
// are left out. It returns how many links were added.
//
// Safeguards, because this file is somebody's master record:
//   - the new file is written next to the old one, read back and compared, and
//     only then swapped in; any failure leaves the old file untouched
//   - if another program changed the file in the meantime (a helper that
//     appends to it, a sync client) nothing is swapped; it reads again and
//     retries, so the other program's lines are never lost
//   - once a day, before the first change, the file is copied to
//     "<file>.backups/" (never overwritten, never deleted by the program) and
//     every added link is noted in added.log there
//   - a file that is missing but has backups is NOT created again, so a file
//     that went missing (unplugged drive, sync trouble) is never silently
//     replaced by a new one with a single line in it
func prependLinks(path string, links []string, skipDup bool) (int, error) {
	return prependLinksWith(path, links, skipDup, true)
}

// prependLinksWith is prependLinks where the daily copy and the log of added
// links (backups) can be switched off.
func prependLinksWith(path string, links []string, skipDup, backups bool) (int, error) {
	return prependLinksIn(path, links, skipDup, backups, "")
}

// prependLinksIn is prependLinksWith where the temporary file goes in tmpDir
// (when that is on the same drive as the file; see tempDirFor).
func prependLinksIn(path string, links []string, skipDup, backups bool, tmpDir string) (int, error) {
	if !listenExts[strings.ToLower(filepath.Ext(path))] {
		return 0, errors.New("the listener file must be a text file (.txt, .text, .list, .lst, .md or .csv)")
	}
	dir := filepath.Dir(path)
	if st, err := os.Stat(dir); err != nil || !st.IsDir() {
		return 0, fmt.Errorf("the folder %s does not exist", dir)
	}
	unlock, err := lockFile(path)
	if err != nil {
		return 0, err
	}
	defer unlock()
	for attempt := 0; attempt < 5; attempt++ {
		n, err := prependOnce(path, links, skipDup, backups, tmpDir)
		if err != errChanged {
			return n, err
		}
		time.Sleep(time.Duration(attempt+1) * 40 * time.Millisecond)
	}
	return 0, errors.New("another program keeps changing the file, so nothing was written (the links are tried again)")
}

// lockFile takes "<file>.lock" for the time of one update. Another program that
// writes the same file can use the same lock file, so the two never write it at
// the same moment. A lock older than 30 seconds is left over from a crash and is
// broken. (The compare-before-swap in prependOnce covers a writer that does
// not know about the lock.)
func lockFile(path string) (func(), error) {
	lock := path + ".lock"
	deadline := time.Now().Add(10 * time.Second)
	for {
		f, err := os.OpenFile(lock, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0644)
		if err == nil {
			f.Close()
			return func() { os.Remove(lock) }, nil
		}
		if !os.IsExist(err) && !os.IsPermission(err) {
			return nil, err
		}
		if st, serr := os.Stat(lock); serr == nil && time.Since(st.ModTime()) > 30*time.Second {
			os.Remove(lock)
			continue
		}
		if time.Now().After(deadline) {
			return nil, errors.New("the file is busy: another program is writing to it (the links are tried again)")
		}
		time.Sleep(25 * time.Millisecond)
	}
}

// testBeforeSwap lets a test change the file at the worst possible moment.
var testBeforeSwap func()

// errChanged: the file was changed by someone else while it was being updated.
var errChanged = errors.New("file changed while it was being updated")

// backupDir is where the daily copies and the log of added links are kept.
func backupDir(path string) string { return path + ".backups" }

// hasBackups: the program has looked after this file before.
func hasBackups(path string) bool {
	entries, err := os.ReadDir(backupDir(path))
	return err == nil && len(entries) > 0
}

// backupOnce copies the file's current content to the backups folder, once
// per day. An existing backup is never touched.
func backupOnce(path string, content []byte) error {
	dir := backupDir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	name := filepath.Join(dir, filepath.Base(path)+"."+time.Now().Format("2006-01-02")+".bak")
	f, err := os.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0644)
	if os.IsExist(err) {
		return nil // today's copy is already there
	}
	if err != nil {
		return err
	}
	_, werr := f.Write(content)
	if werr == nil {
		werr = f.Sync()
	}
	if cerr := f.Close(); werr == nil {
		werr = cerr
	}
	if werr != nil {
		os.Remove(name) // our own half-written copy
		return werr
	}
	return nil
}

// logAdded notes the links in added.log (append only, best effort).
func logAdded(path string, links []string) {
	dir := backupDir(path)
	if os.MkdirAll(dir, 0755) != nil {
		return
	}
	f, err := os.OpenFile(filepath.Join(dir, "added.log"), os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0644)
	if err != nil {
		return
	}
	defer f.Close()
	stamp := time.Now().Format("2006-01-02 15:04:05")
	var b strings.Builder
	for _, l := range links {
		b.WriteString(stamp + "\t" + l + "\n")
	}
	f.WriteString(b.String())
}

func prependOnce(path string, links []string, skipDup, backups bool, tmpDir string) (int, error) {
	var old []byte
	mode := os.FileMode(0644)
	exists := false
	st, err := os.Stat(path)
	switch {
	case err == nil:
		if st.IsDir() {
			return 0, fmt.Errorf("%s is a folder, not a text file", path)
		}
		if st.Size() > maxListenFile {
			return 0, fmt.Errorf("%s is too big (over %d MB)", filepath.Base(path), maxListenFile>>20)
		}
		exists = true
		mode = st.Mode().Perm()
		if old, err = os.ReadFile(path); err != nil {
			return 0, err
		}
		if int64(len(old)) != st.Size() {
			return 0, errChanged
		}
		head := old
		if len(head) > 8192 {
			head = head[:8192]
		}
		if bytes.IndexByte(head, 0) >= 0 {
			return 0, fmt.Errorf("%s does not look like a text file", filepath.Base(path))
		}
	case os.IsNotExist(err):
		if backups && hasBackups(path) {
			return 0, fmt.Errorf("%s is missing, but it has backups in %s. It was not created again, so nothing gets mixed up. Put the file back (the newest .bak there is the latest copy, and added.log lists every link added). To start a new file on purpose, move that folder away first", filepath.Base(path), filepath.Base(backupDir(path)))
		}
	default:
		return 0, err
	}

	bom := []byte{0xEF, 0xBB, 0xBF}
	hasBOM := bytes.HasPrefix(old, bom)
	body := old
	if hasBOM {
		body = old[len(bom):]
	}
	eol := "\n"
	if bytes.Contains(body, []byte("\r\n")) || (len(body) == 0 && os.PathSeparator == '\\') {
		eol = "\r\n"
	}

	fresh := links
	if skipDup {
		have := map[string]bool{}
		for _, line := range strings.Split(string(body), "\n") {
			have[strings.TrimSpace(line)] = true
		}
		fresh = nil
		for _, l := range links {
			if !have[l] {
				have[l] = true
				fresh = append(fresh, l)
			}
		}
	}
	if len(fresh) == 0 {
		return 0, nil
	}
	var out bytes.Buffer
	if hasBOM {
		out.Write(bom)
	}
	out.WriteString(strings.Join(fresh, eol) + eol)
	out.Write(body)

	if !exists { // a new file: created only if nobody made it meanwhile
		f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
		if os.IsExist(err) {
			return 0, errChanged
		}
		if err != nil {
			return 0, err
		}
		_, werr := f.Write(out.Bytes())
		if werr == nil {
			werr = f.Sync()
		}
		if cerr := f.Close(); werr == nil {
			werr = cerr
		}
		if werr != nil {
			return 0, werr
		}
		if backups {
			logAdded(path, fresh)
		}
		return len(fresh), nil
	}

	if backups && len(old) > 0 {
		if err := backupOnce(path, old); err != nil {
			return 0, fmt.Errorf("could not make the daily backup of %s, so nothing was written: %v", filepath.Base(path), err)
		}
	}
	// The temporary file goes in tmpDir (Recurbate's own data folder, next to
	// settings.json) when that is on the same drive as the file, because the
	// swap at the end is only instant and all-or-nothing within one drive. On
	// another drive it goes next to the file instead.
	dir := filepath.Dir(path)
	tdir := tempDirFor(path, tmpDir)
	err = replaceFile(path, out.Bytes(), old, mode, tdir, tdir != dir)
	if err == errOtherDrive {
		err = replaceFile(path, out.Bytes(), old, mode, dir, false)
	}
	if err != nil {
		return 0, err
	}
	if backups {
		logAdded(path, fresh)
	}
	return len(fresh), nil
}

// tempDirFor is where the temporary file for path goes: tmpDir when it is
// given and on the same drive as path, otherwise next to path.
func tempDirFor(path, tmpDir string) string {
	dir := filepath.Dir(path)
	if tmpDir == "" {
		return dir
	}
	a, errA := filepath.Abs(tmpDir)
	b, errB := filepath.Abs(dir)
	if errA != nil || errB != nil || !strings.EqualFold(filepath.VolumeName(a), filepath.VolumeName(b)) {
		return dir
	}
	if st, err := os.Stat(a); err != nil || !st.IsDir() {
		return dir
	}
	return a
}

// errOtherDrive: the temporary file could not be moved over the file because
// it was somewhere else (another drive). The caller tries again next to the file.
var errOtherDrive = errors.New("temporary file is on another drive")

// renameFile is os.Rename; a test replaces it.
var renameFile = os.Rename

// replaceFile writes content to a temporary file in tdir, reads it back, checks
// that path is still exactly old, and swaps it over path. When it fails, path
// is as it was. A failed swap from a folder other than path's own gives
// errOtherDrive (when elsewhere is true).
func replaceFile(path string, content, old []byte, mode os.FileMode, tdir string, elsewhere bool) error {
	tmp, err := os.CreateTemp(tdir, ".Recurbate-links-*.tmp")
	if err != nil {
		if elsewhere {
			return errOtherDrive
		}
		return err
	}
	name := tmp.Name()
	fail := func(err error) error { tmp.Close(); os.Remove(name); return err }
	if _, err := tmp.Write(content); err != nil {
		return fail(err)
	}
	if err := tmp.Sync(); err != nil {
		return fail(err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(name)
		return err
	}
	// what reached the disk must be exactly the new links + the old content
	if back, err := os.ReadFile(name); err != nil || !bytes.Equal(back, content) {
		os.Remove(name)
		return fmt.Errorf("could not write %s safely, so it was left as it was", filepath.Base(path))
	}
	if testBeforeSwap != nil {
		testBeforeSwap()
	}
	// ... and the original must still be what was read: if another program
	// added something meanwhile, start over from its version
	if now, err := os.ReadFile(path); err != nil || !bytes.Equal(now, old) {
		os.Remove(name)
		return errChanged
	}
	os.Chmod(name, mode)
	if err := renameFile(name, path); err != nil {
		os.Remove(name)
		if elsewhere {
			return errOtherDrive
		}
		return err
	}
	return nil
}

// ---- the watcher ----

// Clipboard is where the listener gets its text from. Seq changes whenever
// something new is copied (cheap to call); Read returns the text.
//
// Private reports whether the program that made the last copy marked it as
// private (browsers do that for private/incognito windows, password managers
// for passwords); the listener skips those unless the person allowed them.
type Clipboard struct {
	Seq     func() uint32
	Read    func() (string, error)
	Private func() bool // nil = never private
}

// SetClipboard tells the manager where to read the clipboard from.
func (m *Manager) SetClipboard(c Clipboard) {
	m.mu.Lock()
	m.clip = c
	m.mu.Unlock()
}

// ListenerView is what the page shows about the listener.
type ListenerView struct {
	Enabled bool   `json:"enabled"`
	Added   int    `json:"added"` // links added since the program started
	Error   string `json:"error,omitempty"`
	// Links are the latest links found, newest last; the window puts the ones it
	// has not seen yet into the link box.
	Links []ListenedLink `json:"links,omitempty"`
}

// ListenedLink is one link the listener found.
type ListenedLink struct {
	ID   int64  `json:"id"`
	Line string `json:"line"` // a plain link or a whole ["url","start","end","length"] entry
}

const maxListenedLinks = 100

type listenState struct {
	armed   bool
	lastSeq uint32
	pending []string
	patKey  string
	res     []*regexp.Regexp
	lastErr string

	reading     bool      // a new copy is waiting to be read
	readUntil   time.Time // give up reading it after this (e.g. it holds no text)
	privateNote time.Time // when the "skipped a private copy" notice was last shown
}

// SetListenEnabled switches the listener on or off and remembers the choice.
func (m *Manager) SetListenEnabled(on bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	l := m.settings.Listen
	l.Enabled = on
	if err := l.validate(); err != nil {
		return err
	}
	m.settings.Listen = l
	m.listenErr = ""
	err := m.saveSettingsLocked()
	m.changedLocked(false)
	return err
}

func (m *Manager) listenLoop() {
	defer m.wg.Done()
	t := time.NewTicker(m.listenEvery)
	defer t.Stop()
	var st listenState
	for {
		select {
		case <-m.rootCtx.Done():
			return
		case <-t.C:
			m.listenTick(&st)
		}
	}
}

// listenTick is one look at the clipboard.
func (m *Manager) listenTick(st *listenState) {
	m.mu.Lock()
	cfg := m.settings.Listen
	clip := m.clip
	dataDir := m.loc.DataDir(m.baseDir) // where settings.json is
	m.mu.Unlock()
	if !cfg.Enabled || clip.Seq == nil || clip.Read == nil {
		st.armed, st.pending = false, nil
		return
	}
	seq := clip.Seq()
	if !st.armed { // what was copied before it was switched on is not added
		st.armed, st.lastSeq = true, seq
		return
	}
	if seq != st.lastSeq {
		st.lastSeq = seq
		st.reading, st.readUntil = true, time.Now().Add(3*time.Second)
	}
	if st.reading {
		switch {
		case clip.Private != nil && clip.Private() && !cfg.AllowPrivate:
			st.reading = false
			m.privateSkipped(st)
		default:
			// The program that copied may still be holding the clipboard for a moment,
			// so a failed read is tried again on the next looks for up to 3 seconds
			// (a copy with no text in it, such as an image, just runs out the time).
			if text, err := clip.Read(); err == nil {
				st.reading = false
				if len(text) <= maxClipboardText && !m.handleCopied(st, cfg, text) {
					return
				}
			} else if time.Now().After(st.readUntil) {
				st.reading = false
			}
		}
	}
	if len(st.pending) == 0 || cfg.archivePath() == "" {
		st.pending = nil
		return
	}
	path := cfg.archivePath()
	if !filepath.IsAbs(path) {
		path = filepath.Join(m.baseDir, path)
	}
	total := len(st.pending)
	n, err := prependLinksIn(path, st.pending, cfg.SkipDuplicates, cfg.backupsOn(), dataDir)
	if err != nil {
		m.listenFailed(st, err.Error())
		return // the links wait and are tried again
	}
	st.pending, st.lastErr = nil, ""
	m.mu.Lock()
	defer m.mu.Unlock()
	m.listenErr = ""
	if skipped := total - n; skipped > 0 && cfg.SkipDuplicates {
		// say so, or a copy that "did nothing" looks like a failure
		what := "1 link was"
		if skipped > 1 {
			what = fmt.Sprintf("%d links were", skipped)
		}
		m.notifyLocked("info", fmt.Sprintf("Listener: %s already in %s, so not added again", what, filepath.Base(path)))
	}
	if n > 0 {
		m.listenAdded += n
		word := "links"
		if n == 1 {
			word = "link"
		}
		m.notifyLocked("info", fmt.Sprintf("Listener: added %d %s to %s", n, word, filepath.Base(path)))
	}
	m.changedLocked(false)
}

// offerLinks hands lines (links or whole entries) to the window, which puts them in the link box.
func (m *Manager) offerLinks(links []string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, l := range links {
		m.listenSeq++
		m.listened = append(m.listened, ListenedLink{ID: m.listenSeq, Line: l})
	}
	if over := len(m.listened) - maxListenedLinks; over > 0 {
		m.listened = m.listened[over:]
	}
	m.changedLocked(false)
}

func (m *Manager) listenFailed(st *listenState, msg string) {
	if msg == st.lastErr {
		return
	}
	st.lastErr = msg
	m.mu.Lock()
	defer m.mu.Unlock()
	m.listenErr = msg
	m.notifyLocked("error", "Listener: "+msg)
}

// handleCopied looks for links in newly copied text. It reports false when the
// patterns can not be used (the problem has been shown).
func (m *Manager) handleCopied(st *listenState, cfg ListenSettings, text string) bool {
	key := strings.Join(cfg.Patterns, "\n")
	if st.res == nil || key != st.patKey {
		res, err := compilePatterns(cfg.Patterns)
		if err != nil {
			m.listenFailed(st, err.Error())
			return false
		}
		st.res, st.patKey = res, key
	}
	found := ExtractFound(text, st.res)
	if len(found) > 0 {
		lines := make([]string, len(found))
		for i, f := range found {
			lines[i] = f.Entry // the whole ["url","start","end","length"] entry if that was copied
		}
		m.offerLinks(lines)
	}
	if cfg.archivePath() != "" { // the text file only ever gets the plain link
		st.pending = append(st.pending, ExtractLinks(text, st.res)...)
	}
	if over := len(st.pending) - maxPendingLinks; over > 0 {
		st.pending = st.pending[over:]
	}
	return true
}

// privateSkipped tells the person (at most every 20 seconds) that a copy was
// not read because it was marked private, and how to allow that.
func (m *Manager) privateSkipped(st *listenState) {
	if time.Since(st.privateNote) < 20*time.Second {
		return
	}
	st.privateNote = time.Now()
	m.notify("warn", "Listener: a copy was skipped because the program that made it marked it as private (a private or incognito browser window, or a password manager). To read those too, tick “Also read copies from private windows” in Settings → Link listener.")
}
