package tools

import (
	"bytes"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

// Every request to the site leaves a line in the log (path only, no query or
// headers), except segments that came through fine.
func TestRequestsAreLogged(t *testing.T) {
	var buf bytes.Buffer
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/blocked.ts"):
			w.WriteHeader(403)
			w.Write([]byte("<title>Attention Required! | Cloudflare</title>"))
		default:
			w.Write([]byte("ok"))
		}
	}))
	defer srv.Close()
	hdr := map[string]string{"Cookie": "session=secret"}
	for _, p := range []string{"/video/7/play?token=secret", "/hls/u/seg1.ts?check=secret", "/hls/u/blocked.ts"} {
		Request(nil, srv.URL+p, 5, hdr, nil, "GET")
	}
	Request(nil, "http://127.0.0.1:1/video/9/play?token=secret", 2, hdr, nil, "GET") // nobody there
	out := buf.String()
	if !strings.Contains(out, "/video/7/play -> 200") {
		t.Errorf("the page request is not logged:\n%s", out)
	}
	if strings.Contains(out, "seg1.ts") {
		t.Errorf("a segment that came through is logged:\n%s", out)
	}
	if !strings.Contains(out, "/hls/u/blocked.ts -> 403") || !strings.Contains(out, "cloudflare") {
		t.Errorf("a refused segment is not logged as such:\n%s", out)
	}
	if !strings.Contains(out, "/video/9/play -> error") {
		t.Errorf("a failed request is not logged:\n%s", out)
	}
	if strings.Contains(out, "secret") {
		t.Errorf("the log holds a token or cookie:\n%s", out)
	}
}
