package engine

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// StatusError means the server answered, but not with what was needed.
type StatusError struct {
	Code int
	Body string
}

func (e *StatusError) Error() string {
	text := fmt.Sprintf("HTTP %d %s", e.Code, http.StatusText(e.Code))
	if looksCloudflare(e.Body) {
		return text + " (Cloudflare)"
	}
	// a short plain-text answer helps; a page of HTML does not
	b := strings.Join(strings.Fields(e.Body), " ")
	if b != "" && !strings.HasPrefix(b, "<") {
		if len(b) > 80 {
			b = b[:80]
		}
		return text + ": " + b
	}
	return text
}

// Blocked reports whether the answer means "you are not let in right now"
// (an expired signed link, a Cloudflare check, a rate limit) as opposed to
// "this does not exist" or "the server is broken".
func (e *StatusError) Blocked() bool {
	switch e.Code {
	case http.StatusUnauthorized, http.StatusForbidden, http.StatusTooManyRequests:
		return true
	case http.StatusServiceUnavailable:
		return looksCloudflare(e.Body)
	}
	return false
}

func looksCloudflare(body string) bool {
	if len(body) > 20000 {
		body = body[:20000]
	}
	b := strings.ToLower(body)
	return strings.Contains(b, "cloudflare") || strings.Contains(b, "cf-ray") ||
		strings.Contains(b, "just a moment") || strings.Contains(b, "attention required")
}

// ErrDenied means the CDN keeps refusing segment requests (403 / 401 / too
// many 429s). Usually the signed links have expired and resolving the video
// again hands out new ones; if it persists the site is blocking the client.
var ErrDenied = errors.New("access denied")

// Blocked reports whether err means the site is refusing the client (as
// opposed to being unreachable or the video being unavailable).
func Blocked(err error) bool {
	if errors.Is(err, ErrDenied) {
		return true
	}
	var re *ResolveError
	return errors.As(err, &re) && re.Kind == KindCloudflare
}

// Temporary reports whether err is the kind of failure that may go away by
// itself if the download waits a little and asks the site again: a block or
// rate limit, an expired link, or the network being away.
func Temporary(err error) bool {
	if errors.Is(err, ErrDenied) || errors.Is(err, ErrExpired) {
		return true
	}
	var re *ResolveError
	return errors.As(err, &re) && (re.Kind == KindCloudflare || re.Kind == KindNetwork)
}

// BlockedHint tells the user what to do when the waiting did not help.
const BlockedHint = "The site keeps refusing the download. Open the site in your browser, copy the new cookie, press the key button at the top right (Replace cookie), then press Start: it carries on from where it stopped."

// BlockedWaits is how long a download waits before asking again after the
// site refused it, once refreshing the links did not help. After the last
// one it gives up and shows the error.
var BlockedWaits = []time.Duration{20 * time.Second, time.Minute, 3 * time.Minute, 10 * time.Minute}
