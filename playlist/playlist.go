package playlist

import (
	"fmt"
	"regexp"
	"strings"
)

type Playlist struct {
	M3u8     []byte
	List     []string
	Filename string
}

// New builds a playlist from raw m3u8 data. warn (optional) receives
// non-fatal problems with individual entries.
func New(warn func(error), raw_m3u8 []byte, url string) (playList Playlist, err error) {
	filename, err := createFilename(url)
	if err != nil {
		return
	}
	playlistLines := strings.Split(string(raw_m3u8), "\n")
	list := make([]string, 0, len(playlistLines)/2)
	for _, line := range playlistLines {
		line = strings.TrimRight(line, "\r")
		if len(line) < 2 || line[0] == '#' {
			continue
		}
		appendedCheck, aerr := appendCheck(line)
		if aerr != nil {
			if warn != nil {
				warn(fmt.Errorf("issue with the TS fragment check parameter creator: %v", aerr))
			}
			list = append(list, line)
		} else {
			list = append(list, appendedCheck)
		}
	}
	if len(list) >= 2 {
		list = list[1 : len(list)-1]
	}
	playList = Playlist{
		M3u8:     raw_m3u8,
		List:     list,
		Filename: filename,
	}
	return
}
func (p *Playlist) Len() int {
	return len(p.List)
}
func (p *Playlist) IsNil() bool {
	return p.M3u8 == nil
}

// returns playlists domain name
func (p *Playlist) PlaylistOrigin() (domain string, err error) {
	if len(p.List) == 0 {
		err = fmt.Errorf("playlist contains no data")
		return
	}
	var second int
	last := 0
	for x := range [3]int{} {
		loc := strings.Index(p.List[0][last:], "/") + 1
		if loc == 0 {
			err = fmt.Errorf("playlist doesn't contain urls")
			return
		}
		last += loc
		if x == 1 {
			second = last
		}
	}
	domain = p.List[0][second : last-1]
	return
}

// creates the filename from a given m3u8 url
func createFilename(url string) (filename string, err error) {
	urlSplit := strings.Split(url, "/")
	if len(urlSplit) < 6 {
		return "", fmt.Errorf("wrong url format")
	}
	// parse username and date
	username := urlSplit[4]
	date := strings.ReplaceAll(urlSplit[5], ",", "-")
	dateSplit := strings.Split(date, "-")
	if len(dateSplit) < 5 {
		return "", fmt.Errorf("wrong date format")
	}
	if len(dateSplit[0]) == 4 {
		dateSplit[0] = dateSplit[0][2:]
	}
	filename = fmt.Sprintf("%s_%s-%s-%s_%s-%s", username, dateSplit[0], dateSplit[1], dateSplit[2], dateSplit[3], dateSplit[4])
	return
}

var (
	regexUID     = regexp.MustCompile("uid=([^&]*)")
	regexExpires = regexp.MustCompile("expires=([^&]*)")
	regexRequest = regexp.MustCompile("request_id=([^&]*)")
)

// determine check parameter for playlist fragment link
func appendCheck(url string) (appended string, err error) {
	defer func() {
		if err != nil {
			err = fmt.Errorf("playlist deobfuscation error: %v", err)
		}
	}()
	uidMatches := regexUID.FindStringSubmatch(url)
	if len(uidMatches) < 2 {
		return url, fmt.Errorf("uid not found")
	}
	uidMatch := uidMatches[1]
	if len(uidMatch) < 6 {
		return url, fmt.Errorf("uid too short")
	}
	expiresMatches := regexExpires.FindStringSubmatch(url)
	if len(expiresMatches) < 2 {
		return url, fmt.Errorf("expires not found")
	}
	expiresMatch := expiresMatches[1]
	if len(expiresMatch) < 4 {
		return url, fmt.Errorf("expires too short")
	}
	requestMatches := regexRequest.FindStringSubmatch(url)
	if len(requestMatches) < 2 {
		return url, fmt.Errorf("request_id not found")
	}
	requestMatch := requestMatches[1]
	if len(requestMatch) < 4 {
		return url, fmt.Errorf("request_id too short")
	}
	expiredSeg := reverseString(reverseString(expiresMatch)[0:4])
	appended = fmt.Sprintf("%s&check=%s%s%s", url, requestMatch[0:4], uidMatch[2:6], expiredSeg)
	return
}

func reverseString(s string) string {
	runes := []rune(s)
	for i, j := 0, len(runes)-1; i < j; i, j = i+1, j-1 {
		runes[i], runes[j] = runes[j], runes[i]
	}
	return string(runes)
}
