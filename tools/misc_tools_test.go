package tools

import "testing"

func TestParseClock(t *testing.T) {
	for in, want := range map[string]int{"45": 45, "1:05": 65, "55:00": 3300, "1:10:00": 4200, " 1:30:00 ": 5400, "0:00": 0} {
		got, err := ParseClock(in)
		if err != nil || got != want {
			t.Fatalf("ParseClock(%q) = %d, %v; want %d", in, got, err, want)
		}
	}
	for _, in := range []string{"", "a", "1:b", "1:2:3:4", "-5", "1::2"} {
		if _, err := ParseClock(in); err == nil {
			t.Fatalf("ParseClock(%q) accepted", in)
		}
	}
}

func TestPercentParse(t *testing.T) {
	// the example from the README: 55:00 - 1:10:00 of a 1:30:00 video
	got, err := PercentParse([]string{"55:00", "1:10:00", "1:30:00"})
	if err != nil {
		t.Fatal(err)
	}
	if d := got[0] - 61.111; d > 0.01 || d < -0.01 {
		t.Fatalf("start = %v", got[0])
	}
	if d := got[1] - 77.777; d > 0.01 || d < -0.01 {
		t.Fatalf("end = %v", got[1])
	}
	// an end past the total length is clamped to 100%
	if got, err := PercentParse([]string{"1:00", "2:00", "1:30"}); err != nil || got[1] != 100 {
		t.Fatalf("clamp: %v %v", got, err)
	}
	for _, bad := range [][]string{
		{"1:00", "2:00"}, {"2:00", "1:00", "3:00"}, {"1:00", "1:00", "3:00"},
		{"0:00", "1:00", "0:00"}, {"5:00", "6:00", "3:00"}, {"x", "1:00", "3:00"},
	} {
		if _, err := PercentParse(bad); err == nil {
			t.Fatalf("PercentParse(%v) accepted", bad)
		}
	}
}

func TestSearchString(t *testing.T) {
	got, err := SearchString(`<video><source src="http://a/b.m3u8" type="x"></video>`, `<source src="`, `"`)
	if err != nil || got != "http://a/b.m3u8" {
		t.Fatalf("got %q, %v", got, err)
	}
	if _, err := SearchString("short", "<a>", "</a>"); err == nil {
		t.Fatal("expected an error")
	}
	if _, err := SearchString("no markers in this text at all", "<a>", "</a>"); err == nil {
		t.Fatal("expected an error")
	}
	if _, err := SearchString("start only <a> and then nothing", "<a>", "</a>"); err == nil {
		t.Fatal("expected an error")
	}
}

func TestFormatedHeaderPlaylistHasNoCookie(t *testing.T) {
	h := FormatedHeader(map[string]string{"Cookie": "a=b", "User-Agent": "UA"}, "", "https://site.example", PLAYLIST_HEADER, nil)
	if _, ok := h["Cookie"]; ok {
		t.Fatal("playlist requests must not carry the cookie")
	}
	if h["Origin"] != "https://site.example" || h["User-Agent"] != "UA" || h["Sec-Fetch-Site"] != "cross-site" {
		t.Fatalf("headers = %v", h)
	}
	tok := "T"
	h = FormatedHeader(map[string]string{"Cookie": "a=b"}, "https://site.example/video/1/play", "https://site.example", API_PLAYLIST_HEADER, &tok)
	if h["Cookie"] != "a=b" || h["X-Api-Token"] != "T" || h["Referer"] != "https://site.example/video/1/play" {
		t.Fatalf("headers = %v", h)
	}
}
