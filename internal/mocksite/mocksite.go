// Package mocksite imitates the video site (page, API, playlists, CDN segments)
// closely enough to run the real download engine against it. It is test
// support only.
package mocksite

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// mockSite imitates the whole flow the downloader walks through:
//
//	GET  /video/<id>/play            page with window.token / window.requestId
//	GET  /api/video/<id>             <source src="...m3u8"> (needs the cookie)
//	GET  <cdn>/hls/user<id>/<date>/playlist.m3u8          master playlist
//	GET  <cdn>/hls/user<id>/<date>/<res>/playlist.m3u8    media playlist
//	GET  <cdn>/hls/user<id>/<date>/<res>/segN.ts?...&check=...
//
// Videos with an even id live on cdn 0, odd ids on cdn 1.
type Site struct {
	main    *httptest.Server
	cdns    []*httptest.Server
	Lines   int // segment lines in the media playlist (the downloader drops the first and last)
	SegSize int
	Delay   time.Duration
	Cookie  string
	UA      string

	mu        sync.Mutex
	rid       int
	minValid  int
	failOnce  map[int]bool
	subscribe bool
	problems  []string
	resolves  int
	shift     int // moves every video to the other server when odd

	expiredCode int             // status for expired links (410 by default)
	linkLife    time.Duration   // how long handed-out links work
	blockSegs   bool            // every segment request is refused with a Cloudflare page
	blockPages  int             // the next n page requests are refused with a Cloudflare page
	limit429    int             // the next n segment requests answer 429
	refusals    int             // segment requests refused as expired / blocked
	extraCookie map[string]bool // more cookies the site accepts
	cookieUses  map[string]int  // requests seen per cookie value
}

const cfPage = `<!DOCTYPE html><html><head><title>Attention Required! | Cloudflare</title></head><body><h1>Sorry, you have been blocked</h1><p>cf-ray: 8a1b2c3d4e5f</p></body></html>`

// New starts the site: one main server and two CDN servers.
func New() *Site {
	s := &Site{
		Lines: 22, SegSize: 2048, Delay: 5 * time.Millisecond,
		Cookie: "session=good", UA: "TestAgent/1.0", failOnce: map[int]bool{},
		expiredCode: 410, linkLife: time.Hour,
	}
	s.main = httptest.NewServer(http.HandlerFunc(s.mainHandler))
	for i := 0; i < 2; i++ {
		s.cdns = append(s.cdns, httptest.NewServer(s.cdnHandler(i)))
	}
	return s
}

// Close stops all servers.
func (s *Site) Close() {
	s.main.Close()
	for _, c := range s.cdns {
		c.Close()
	}
}

// Problems lists requests the real site would have rejected or that the
// downloader should not have made (wrong check value, cookie on a playlist, ...).
func (s *Site) Problems() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.problems...)
}

// Resolves counts how many times a video page was turned into a playlist.
func (s *Site) Resolves() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.resolves
}

// FailOnce makes the first request for segment n return a server error.
func (s *Site) FailOnce(n int) {
	s.mu.Lock()
	s.failOnce[n] = true
	s.mu.Unlock()
}

// SetExpiredStatus chooses what expired links answer: 410, or 403 like a
// CDN that signs its links (the answer then is a Cloudflare page).
func (s *Site) SetExpiredStatus(code int) {
	s.mu.Lock()
	s.expiredCode = code
	s.mu.Unlock()
}

// SetLinkLife sets how long the segment links handed out from now on work.
func (s *Site) SetLinkLife(d time.Duration) {
	s.mu.Lock()
	s.linkLife = d
	s.mu.Unlock()
}

// SetCookie changes the cookie the site accepts (the person signed in again).
func (s *Site) SetCookie(c string) {
	s.mu.Lock()
	s.Cookie = c
	s.mu.Unlock()
}

// AlsoAccept makes the site accept one more cookie next to the current one.
func (s *Site) AlsoAccept(c string) {
	s.mu.Lock()
	if s.extraCookie == nil {
		s.extraCookie = map[string]bool{}
	}
	s.extraCookie[c] = true
	s.mu.Unlock()
}

// CookieUses is how many page / segment requests came with that cookie.
func (s *Site) CookieUses(c string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cookieUses[c]
}

// cookieOK counts the request's cookie and says whether the site accepts it.
func (s *Site) cookieOK(got string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cookieUses == nil {
		s.cookieUses = map[string]int{}
	}
	s.cookieUses[got]++
	return got == s.Cookie || s.extraCookie[got]
}

// BlockSegments makes every segment request answer 403 (Cloudflare page)
// until it is turned off again.
func (s *Site) BlockSegments(v bool) {
	s.mu.Lock()
	s.blockSegs = v
	s.mu.Unlock()
}

// BlockPages makes the next n video page requests answer 403 (Cloudflare page).
func (s *Site) BlockPages(n int) {
	s.mu.Lock()
	s.blockPages = n
	s.mu.Unlock()
}

// Limit429 makes the next n segment requests answer 429.
func (s *Site) Limit429(n int) {
	s.mu.Lock()
	s.limit429 = n
	s.mu.Unlock()
}

// Refusals counts the segment requests that were turned away as expired or blocked.
func (s *Site) Refusals() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.refusals
}

// SetSubscribe makes the API answer "shall_subscribe" (daily views used up).
func (s *Site) SetSubscribe(v bool) {
	s.mu.Lock()
	s.subscribe = v
	s.mu.Unlock()
}

func (s *Site) problem(format string, a ...any) {
	s.mu.Lock()
	s.problems = append(s.problems, fmt.Sprintf(format, a...))
	s.mu.Unlock()
}

// VideoURL is the address to give the downloader.
func (s *Site) VideoURL(id int) string { return fmt.Sprintf("%s/video/%d/play", s.main.URL, id) }

// ServerOf is what the downloader reports as the server of a video.
func (s *Site) ServerOf(id int) string {
	return strings.TrimPrefix(s.cdns[s.cdnIndex(id)].URL, "http://")
}

func (s *Site) cdnIndex(id int) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return (id + s.shift) % 2
}

// MoveVideos makes every video come from the other server from now on, like
// the real site handing out a different server on a later visit.
func (s *Site) MoveVideos() {
	s.mu.Lock()
	s.shift++
	s.mu.Unlock()
}

// Filename is the name the downloader gives the video (without .ts).
func (s *Site) Filename(id int) string { return fmt.Sprintf("user%d_24-01-02_10-30", id) }

// ExpireAll makes every segment link handed out so far return 410.
func (s *Site) ExpireAll() {
	s.mu.Lock()
	s.minValid = s.rid + 1
	s.mu.Unlock()
}

// SegBytes is the content of segment n.
func SegBytes(user, res string, n, size int) []byte {
	head := []byte(fmt.Sprintf("%s/%s/%04d|", user, res, n))
	b := make([]byte, size)
	copy(b, head)
	for i := len(head); i < size; i++ {
		b[i] = byte((n*31 + i*7) % 251)
	}
	return b
}

// Expected is the file content for the playlist entries [from,to) of a video
// (entry k is segment k+1 because the first line is dropped).
func (s *Site) Expected(id int, res string, from, to int) []byte {
	var buf bytes.Buffer
	for k := from; k < to; k++ {
		buf.Write(SegBytes(fmt.Sprintf("user%d", id), res, k+1, s.SegSize))
	}
	return buf.Bytes()
}

func (s *Site) mainHandler(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	switch {
	case len(parts) == 3 && parts[0] == "video" && parts[2] == "play":
		s.mu.Lock()
		blocked := s.blockPages > 0
		if blocked {
			s.blockPages--
		}
		s.mu.Unlock()
		if blocked {
			w.WriteHeader(http.StatusForbidden)
			fmt.Fprint(w, cfPage)
			return
		}
		fmt.Fprintf(w, `<html><script>window.token = "TOK%s"; window.requestId = "REQ%s";</script></html>`, parts[1], parts[1])
	case len(parts) == 3 && parts[0] == "api" && parts[1] == "video":
		id := parts[2]
		if r.Header.Get("X-Api-Token") != "TOK"+id || r.URL.Query().Get("parent-request") != "REQ"+id {
			fmt.Fprint(w, "wrong_token")
			return
		}
		if !strings.HasSuffix(r.Header.Get("Referer"), "/video/"+id+"/play") {
			s.problem("api Referer wrong: %q", r.Header.Get("Referer"))
		}
		s.mu.Lock()
		sub := s.subscribe
		s.mu.Unlock()
		if sub {
			fmt.Fprint(w, "shall_subscribe")
			return
		}
		if !s.cookieOK(r.Header.Get("Cookie")) {
			fmt.Fprint(w, "shall_signin")
			return
		}
		n, _ := strconv.Atoi(id)
		s.mu.Lock()
		s.rid++
		s.resolves++
		s.mu.Unlock()
		fmt.Fprintf(w, `<video><source src="%s/hls/user%s/2024-01-02,10-30-45/playlist.m3u8?x=1&amp;y=2" type="application/x-mpegURL"></video>`,
			s.cdns[s.cdnIndex(n)].URL, id)
	default:
		http.NotFound(w, r)
	}
}

var segRe = regexp.MustCompile(`^seg(\d+)\.ts$`)

func (s *Site) cdnHandler(idx int) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
		if len(parts) < 4 || parts[0] != "hls" {
			http.NotFound(w, r)
			return
		}
		user := parts[1]
		switch {
		case len(parts) == 4 && parts[3] == "playlist.m3u8": // master
			if r.Header.Get("Cookie") != "" {
				s.problem("playlist request carried the cookie")
			}
			if r.Header.Get("Origin") != s.main.URL {
				s.problem("playlist Origin = %q, want %q", r.Header.Get("Origin"), s.main.URL)
			}
			fmt.Fprint(w, "#EXTM3U\n#EXT-X-STREAM-INF:BANDWIDTH=800000,RESOLUTION=640x360\n360p/playlist.m3u8\n#EXT-X-STREAM-INF:BANDWIDTH=2000000,RESOLUTION=1280x720\n720p/playlist.m3u8\n")
		case len(parts) == 5 && parts[4] == "playlist.m3u8": // media
			res := parts[3]
			s.mu.Lock()
			rid := s.rid
			s.mu.Unlock()
			s.mu.Lock()
			life := s.linkLife
			s.mu.Unlock()
			expires := time.Now().Add(life).Unix()
			var b strings.Builder
			b.WriteString("#EXTM3U\n#EXT-X-TARGETDURATION:4\n")
			for i := 0; i < s.Lines; i++ {
				fmt.Fprintf(&b, "#EXTINF:4.0,\n%s/seg%d.ts?uid=U%07d&expires=%d&request_id=r%04d\n", res, i, i, expires, rid)
			}
			b.WriteString("#EXT-X-ENDLIST\n")
			fmt.Fprint(w, b.String())
		case len(parts) == 5 && segRe.MatchString(parts[4]):
			res := parts[3]
			n, _ := strconv.Atoi(segRe.FindStringSubmatch(parts[4])[1])
			q := r.URL.Query()
			uid, exp, rid := q.Get("uid"), q.Get("expires"), q.Get("request_id")
			if len(uid) < 6 || len(exp) < 4 || len(rid) < 4 {
				http.Error(w, "bad query", 400)
				return
			}
			want := rid[:4] + uid[2:6] + exp[len(exp)-4:]
			if q.Get("check") != want {
				s.problem("segment %d: check=%q want %q", n, q.Get("check"), want)
				http.Error(w, "bad check", 403)
				return
			}
			rn, _ := strconv.Atoi(strings.TrimPrefix(rid, "r"))
			expUnix, _ := strconv.ParseInt(exp, 10, 64)
			s.mu.Lock()
			expired := rn < s.minValid || time.Now().Unix() > expUnix
			fail := s.failOnce[n]
			delete(s.failOnce, n)
			code := s.expiredCode
			block := s.blockSegs
			limited := s.limit429 > 0
			if limited {
				s.limit429--
			}
			if (expired || block) && !limited {
				s.refusals++
			}
			s.mu.Unlock()
			if limited {
				http.Error(w, "slow down", 429)
				return
			}
			if block {
				w.WriteHeader(http.StatusForbidden)
				fmt.Fprint(w, cfPage)
				return
			}
			if expired {
				if code == 403 {
					w.WriteHeader(http.StatusForbidden)
					fmt.Fprint(w, cfPage)
					return
				}
				http.Error(w, "expired", code)
				return
			}
			if !s.cookieOK(r.Header.Get("Cookie")) || r.Header.Get("User-Agent") != s.UA {
				s.problem("segment request lacks the cookie / user agent")
			}
			if fail {
				http.Error(w, "boom", 500)
				return
			}
			time.Sleep(s.Delay)
			w.Write(SegBytes(user, res, n, s.SegSize))
		default:
			http.NotFound(w, r)
		}
	}
}

// CDNURL is the base address of CDN server i (0 or 1).
func (s *Site) CDNURL(i int) string { return s.cdns[i].URL }

// MainURL is the address of the site itself.
func (s *Site) MainURL() string { return s.main.URL }
