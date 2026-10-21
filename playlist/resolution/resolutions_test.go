package resolution

import "testing"

const master = `#EXTM3U
#EXT-X-STREAM-INF:BANDWIDTH=500000,RESOLUTION=426x240
240p/playlist.m3u8
#EXT-X-STREAM-INF:BANDWIDTH=1000000,RESOLUTION=1280x720
720p/playlist.m3u8
#EXT-X-STREAM-INF:BANDWIDTH=800000,RESOLUTION=854x480
480p/playlist.m3u8
#EXT-X-STREAM-INF:BANDWIDTH=3000000,RESOLUTION=1920x1080
1080p/playlist.m3u8
`

func TestMax(t *testing.T) {
	p, err := New(master, "https://cdn/x/")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		max  int
		want string
	}{
		{6969, "1080p"}, {1080, "1080p"}, {1079, "720p"}, {720, "720p"},
		{600, "480p"}, {480, "480p"}, {240, "240p"},
		{100, "240p"}, // everything is taller: smallest one
	} {
		if got := p.Max(tc.max); got != "https://cdn/x/"+tc.want+"/playlist.m3u8" {
			t.Fatalf("Max(%d) = %s", tc.max, got)
		}
	}
}

func TestSameHeightLaterEntryWins(t *testing.T) {
	p, _ := New("#EXTM3U\n#X,RESOLUTION=1280x720,A\na.m3u8\n#X,RESOLUTION=1280x720,B\nb.m3u8\n", "p/")
	if got := p.Max(720); got != "p/b.m3u8" {
		t.Fatalf("got %s", got)
	}
}

func TestEmpty(t *testing.T) {
	p, _ := New("#EXTM3U\n", "p/")
	if got := p.Max(720); got != "" {
		t.Fatalf("got %q", got)
	}
}
