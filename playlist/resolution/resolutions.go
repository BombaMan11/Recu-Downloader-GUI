package resolution

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
)

// The original pattern was `RESOLUTION=\d+x(\d+).+\n(.+)`, which needs at least
// one character between the height and the line break and so misread the
// height (720 -> 72) when RESOLUTION was the last attribute on the line.
var regexResolution = regexp.MustCompile(`RESOLUTION=\d+x(\d+)[^\n]*\n([^\r\n]+)`)

// playlists maps a video height to its playlist link (one link per height;
// when a height appears twice the later entry wins).
type playlists struct {
	heights []int // ascending
	links   map[int]string
}

// given a raw playist of resolutions, packages playlists into usable functions
func New(playlistsRaw, prefix string) (p playlists, err error) {
	p.links = make(map[int]string)
	matches := regexResolution.FindAllStringSubmatch(playlistsRaw, -1)
	for _, match := range matches {
		if len(match) < 3 {
			continue
		}
		resH, aerr := strconv.Atoi(match[1])
		if aerr != nil {
			return p, fmt.Errorf("Atoi error in resolution.New(): %v", aerr)
		}
		if _, seen := p.links[resH]; !seen {
			p.heights = append(p.heights, resH)
		}
		p.links[resH] = prefix + match[2]
	}
	sort.Ints(p.heights)
	return
}

// Max returns the link of the tallest playlist whose height is <= maxRes,
// or the smallest playlist when every one is taller than maxRes.
func (self playlists) Max(maxRes int) (playlistUrl string) {
	for i := len(self.heights) - 1; i >= 0; i-- {
		if self.heights[i] <= maxRes {
			return self.links[self.heights[i]]
		}
	}
	if len(self.heights) > 0 {
		return self.links[self.heights[0]]
	}
	return ""
}
