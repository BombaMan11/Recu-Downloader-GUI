package engine

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
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

// Blocked reports whether err means the site is refusing the client (as
// opposed to being unreachable or the video being unavailable).
func Blocked(err error) bool {
	var se *StatusError
	if errors.As(err, &se) && se.Blocked() {
		return true
	}
	var re *ResolveError
	return errors.As(err, &re) && re.Kind == KindCloudflare
}

// BlockedHint tells the user what to do when the site refused.
const BlockedHint = "The site refused. Open the site in your browser, copy the new cookie, press the key button at the top right (Replace cookie), then press Start: it carries on from where it stopped."
