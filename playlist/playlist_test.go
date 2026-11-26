package playlist

import (
	"strings"
	"testing"
)

func TestAppendCheck(t *testing.T) {
	// check = request_id[0:4] + uid[2:6] + last 4 chars of expires
	got, err := appendCheck("https://cdn.example/a/seg1.ts?uid=ABCDEFGH&expires=1700001234&request_id=rq98765")
	if err != nil {
		t.Fatal(err)
	}
	want := "https://cdn.example/a/seg1.ts?uid=ABCDEFGH&expires=1700001234&request_id=rq98765&check=rq98CDEF1234"
	if got != want {
		t.Fatalf("got  %s\nwant %s", got, want)
	}
	for _, bad := range []string{
		"https://x/s.ts",
		"https://x/s.ts?uid=ABC&expires=1700001234&request_id=rq98765",
		"https://x/s.ts?uid=ABCDEFGH&expires=17&request_id=rq98765",
		"https://x/s.ts?uid=ABCDEFGH&expires=1700001234&request_id=rq",
	} {
		if _, err := appendCheck(bad); err == nil {
			t.Fatalf("expected an error for %s", bad)
		}
	}
}

func TestCreateFilename(t *testing.T) {
	got, err := createFilename("https://cdn.example.com/hls/someuser/2024-03-05,21-45-10/playlist.m3u8")
	if err != nil || got != "someuser_24-03-05_21-45" {
		t.Fatalf("got %q, %v", got, err)
	}
	if _, err := createFilename("https://cdn.example.com/x"); err == nil {
		t.Fatal("short url accepted")
	}
	if _, err := createFilename("https://cdn.example.com/hls/u/baddate/playlist.m3u8"); err == nil {
		t.Fatal("bad date accepted")
	}
}

func TestNewDropsFirstAndLastSegmentAndOrigin(t *testing.T) {
	var lines []string
	lines = append(lines, "#EXTM3U")
	for i := 0; i < 5; i++ {
		lines = append(lines, "#EXTINF:4,", "https://cdn.example.com:8443/hls/u/seg"+string(rune('0'+i))+".ts?uid=ABCDEFGH&expires=1700001234&request_id=rq98765")
	}
	lines = append(lines, "#EXT-X-ENDLIST")
	var warned []error
	pl, err := New(func(e error) { warned = append(warned, e) }, []byte(strings.Join(lines, "\r\n")), "https://cdn.example.com/hls/u/2024-03-05,21-45-10/playlist.m3u8")
	if err != nil || len(warned) != 0 {
		t.Fatalf("err=%v warned=%v", err, warned)
	}
	if pl.Len() != 3 || !strings.Contains(pl.List[0], "seg1.ts") || !strings.Contains(pl.List[2], "seg3.ts") {
		t.Fatalf("list = %v", pl.List)
	}
	if !strings.HasSuffix(pl.List[0], "&check=rq98CDEF1234") || strings.Contains(pl.List[0], "\r") {
		t.Fatalf("entry = %q", pl.List[0])
	}
	origin, err := pl.PlaylistOrigin()
	if err != nil || origin != "cdn.example.com:8443" {
		t.Fatalf("origin = %q, %v", origin, err)
	}
}

func TestPlaylistOriginErrors(t *testing.T) {
	p := Playlist{}
	if _, err := p.PlaylistOrigin(); err == nil {
		t.Fatal("empty playlist has an origin")
	}
	p.List = []string{"nothing-here"}
	if _, err := p.PlaylistOrigin(); err == nil {
		t.Fatal("garbage entry has an origin")
	}
}
