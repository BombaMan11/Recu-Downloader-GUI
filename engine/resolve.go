package engine

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"Recurbate/playlist"
	"Recurbate/playlist/resolution"
	"Recurbate/tools"
)

// ErrKind classifies why a video could not be resolved.
type ErrKind int

const (
	KindOther ErrKind = iota
	KindCookie
	KindWait
	KindCloudflare
	KindNetwork // the site could not be reached at all
)

// ResolveError is returned by Resolve.
type ResolveError struct {
	Kind ErrKind
	Err  error
}

func (e *ResolveError) Error() string {
	switch e.Kind {
	case KindCloudflare:
		return fmt.Sprintf("The site refused the request (%v)", e.Err)
	case KindNetwork:
		return fmt.Sprintf("Could not reach the site: %v", e.Err)
	case KindCookie:
		return "Please log in: the cookie is missing or has expired"
	case KindWait:
		return "Daily view limit used"
	}
	if e.Err != nil {
		return e.Err.Error()
	}
	return "unknown error"
}
func (e *ResolveError) Unwrap() error { return e.Err }

// Resolved is the result of resolving a video page into its segment list.
type Resolved struct {
	Playlist    playlist.Playlist
	PlaylistURL string
	Server      string    // host the segments are downloaded from
	At          time.Time // when it was resolved
	Expires     time.Time // when the segment links stop working (zero = unknown)
	Duration    float64   // length of the video in seconds, from the playlist (0 = not stated)
}

// Stale reports whether the segment links have expired (or are about to
// within margin) and the video should be resolved again.
func (r *Resolved) Stale(margin time.Duration) bool {
	if r == nil {
		return true
	}
	if !r.Expires.IsZero() {
		return time.Now().Add(margin).After(r.Expires)
	}
	return time.Since(r.At) > 10*time.Minute
}

var (
	regexVideoId   = regexp.MustCompile(`([\d]*)/play`)
	regexToken     = regexp.MustCompile(`window\.token = "([^"]+)"`)
	regexRequestId = regexp.MustCompile(`window\.requestId = "([^"]+)"`)
	regexExpires   = regexp.MustCompile(`[?&]expires=(\d+)`)
)

func firstMatch(re *regexp.Regexp, text, what string) (string, error) {
	matches := re.FindStringSubmatch(text)
	if len(matches) > 1 {
		return matches[1], nil
	}
	return "", fmt.Errorf("%s not found", what)
}

// download with the original retry settings: 3 retries, 10s timeout (+30s per
// failure), 200ms between tries.
func (e *Env) get(url string, header map[string]string) ([]byte, error) {
	data, code, err := tools.RequestRetry(e.ctx(), url, 3, func(error) {
		e.warn("request failed, retrying...")
	}, []int{200}, 30, 200*time.Millisecond, 10, header, nil, "GET")
	if err != nil && code != 0 && code != 200 {
		err = &StatusError{Code: code, Body: string(data)}
	}
	return data, err
}

// netErr wraps a failed request as a ResolveError unless the download was
// cancelled, in which case the context error is returned untouched. An
// answer such as 403 or a Cloudflare page is KindCloudflare, a request that
// got no answer at all is KindNetwork; anything else is kind.
func (e *Env) netErr(kind ErrKind, err error) error {
	if cerr := e.ctx().Err(); cerr != nil {
		return cerr
	}
	var re *ResolveError
	if errors.As(err, &re) {
		return err
	}
	var se *StatusError
	switch {
	case errors.As(err, &se):
		if se.Blocked() {
			kind = KindCloudflare
		}
	default:
		kind = KindNetwork
	}
	return &ResolveError{Kind: kind, Err: err}
}

// Resolve takes a video page URL and returns its segment list
// (the original Parse()).
func Resolve(env *Env, videoURL string) (*Resolved, error) {
	if err := env.ctx().Err(); err != nil {
		return nil, err
	}
	origin := originOf(videoURL)
	// getting webpage
	env.log("Downloading page")
	htmldata, err := env.get(videoURL, tools.FormatedHeader(env.Header, "", origin, tools.HTML_HEADER, nil))
	if err != nil {
		return nil, env.netErr(KindOther, err)
	}
	html := string(htmldata)
	// determine video ID, unique page token and request ID
	id, err := firstMatch(regexVideoId, videoURL, "video id match")
	if err != nil {
		return nil, &ResolveError{Kind: KindOther, Err: err}
	}
	token, err := firstMatch(regexToken, html, "video TOKEN")
	if err != nil {
		return nil, &ResolveError{Kind: KindOther, Err: err}
	}
	requestId, err := firstMatch(regexRequestId, html, "requestId match")
	if err != nil {
		return nil, &ResolveError{Kind: KindOther, Err: err}
	}
	// parse api url
	parts := strings.Split(videoURL, "/")
	if len(parts) < 3 {
		return nil, &ResolveError{Kind: KindOther, Err: fmt.Errorf("wrong url format")}
	}
	apiUrl := strings.Join(parts[:3], "/") + "/api/video/" + id + "?parent-request=" + requestId
	// request api
	env.log("Getting link to playlist")
	apidata, err := env.get(apiUrl, tools.FormatedHeader(env.Header, videoURL, origin, tools.API_PLAYLIST_HEADER, &token))
	if err != nil {
		return nil, env.netErr(KindOther, err)
	}
	api := string(apidata)
	// continue based on response from api
	switch strings.TrimSpace(api) {
	case "shall_subscribe":
		return nil, &ResolveError{Kind: KindWait}
	case "shall_signin":
		return nil, &ResolveError{Kind: KindCookie}
	case "wrong_token":
		return nil, &ResolveError{Kind: KindOther, Err: fmt.Errorf("wrong token")}
	}
	// search for m3u8 link from api response
	playlistUrl, err := tools.SearchString(api, `<source src="`, `"`)
	if err != nil {
		return nil, &ResolveError{Kind: KindOther, Err: err}
	}
	playlistUrl = strings.ReplaceAll(playlistUrl, "amp;", "")
	env.log("Downloading playlist")
	// get m3u8 playlist
	playlistData, err := env.get(playlistUrl, tools.FormatedHeader(env.Header, "", origin, tools.PLAYLIST_HEADER, &token))
	if err != nil {
		return nil, env.netErr(KindOther, err)
	}
	// determine url prefix for playlist entries
	prefix := playlistUrl[:strings.LastIndex(playlistUrl, "/")+1]
	// if playlist contains resolution selection
	playlistData, err = env.resolutionPlaylist(playlistData, prefix, origin)
	if err != nil {
		return nil, env.netErr(KindOther, err)
	}
	// add prefix to playlist
	playlistLines := strings.Split(string(playlistData), "\n")
	for i, line := range playlistLines {
		line = strings.TrimRight(line, "\r")
		playlistLines[i] = line
		if len(line) < 2 || line[0] == '#' {
			continue
		}
		if !strings.Contains(line, prefix) {
			playlistLines[i] = prefix + line
		}
	}
	duration := playlistSeconds(playlistLines)
	pl, err := playlist.New(func(werr error) { env.warn(werr.Error()) }, []byte(strings.Join(playlistLines, "\n")), playlistUrl)
	if err != nil {
		return nil, &ResolveError{Kind: KindOther, Err: err}
	}
	res := &Resolved{Playlist: pl, PlaylistURL: playlistUrl, At: time.Now(), Duration: duration}
	if server, serr := pl.PlaylistOrigin(); serr == nil {
		res.Server = server
	} else {
		env.warn(serr.Error())
	}
	if len(pl.List) > 0 {
		if m := regexExpires.FindStringSubmatch(pl.List[0]); len(m) > 1 {
			if n, perr := strconv.ParseInt(m[1], 10, 64); perr == nil {
				switch {
				case n > 1e11 && n < 4e12: // milliseconds
					res.Expires = time.UnixMilli(n)
				case n > 1e9 && n < 4e9: // seconds
					res.Expires = time.Unix(n, 0)
				}
			}
		}
	}
	return res, nil
}

// If playlist contains list of resolutions, return the maximum Resolution playlist
func (e *Env) resolutionPlaylist(playlistData []byte, prefix, origin string) ([]byte, error) {
	playlistRef := string(playlistData)
	if strings.Contains(playlistRef, "EXT-X-STREAM-INF") {
		playlists, err := resolution.New(playlistRef, prefix)
		if err != nil {
			return nil, &ResolveError{Kind: KindOther, Err: err}
		}
		playlistUrl := playlists.Max(e.maxRes())
		if playlistUrl == "" {
			return nil, &ResolveError{Kind: KindOther, Err: errors.New("no playlist found for the available resolutions")}
		}
		playlistData, err = e.get(playlistUrl, tools.FormatedHeader(e.Header, "", origin, tools.PLAYLIST_HEADER, nil))
		if err != nil {
			return nil, err
		}
	}
	return playlistData, nil
}

// ScanPage downloads a page and returns the video links found on it
// (the original "parse" command).
func ScanPage(env *Env, pageURL string) ([]string, error) {
	data, code, err := tools.Request(env.ctx(), pageURL, 10, tools.FormatedHeader(env.Header, "", originOf(pageURL), tools.HTML_HEADER, nil), nil, "GET")
	if err != nil {
		return nil, err
	}
	if code != 200 {
		return nil, fmt.Errorf("status code: %d, the page may be blocked by cloudflare", code)
	}
	urlSplit := strings.Split(pageURL, "/")
	if len(urlSplit) < 5 {
		return nil, fmt.Errorf("wrong url format")
	}
	name := urlSplit[4]
	prefix := strings.Join(urlSplit[:3], "/")
	seen := map[string]bool{}
	var urls []string
	for _, line := range strings.Split(string(data), "\n") {
		code, err := tools.SearchString(line, fmt.Sprintf(`href="/%s/video/`, name), `/play"`)
		if err != nil {
			continue
		}
		u := fmt.Sprintf("%s/%s/video/%s/play", prefix, name, code)
		if !seen[u] {
			seen[u] = true
			urls = append(urls, u)
		}
	}
	return urls, nil
}

// playlistSeconds is the length of the video in seconds: the #EXTINF lengths
// of the segments that get downloaded (the first and the last entry of a
// playlist are dropped, as in playlist.New), 0 when the playlist states none.
func playlistSeconds(lines []string) float64 {
	var segs []float64
	var pending float64
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "#EXTINF:") {
			rest := strings.TrimPrefix(line, "#EXTINF:")
			if i := strings.IndexByte(rest, ','); i >= 0 {
				rest = rest[:i]
			}
			if v, err := strconv.ParseFloat(strings.TrimSpace(rest), 64); err == nil && v > 0 {
				pending = v
			}
			continue
		}
		if len(line) < 2 || line[0] == '#' {
			continue
		}
		segs = append(segs, pending)
		pending = 0
	}
	if len(segs) >= 2 {
		segs = segs[1 : len(segs)-1]
	}
	var total float64
	for _, v := range segs {
		total += v
	}
	return total
}
