package tools

import (
	"fmt"
	"strconv"
	"strings"
)

// Looks for the first occurence of start and end and returns the string in between
func SearchString(str string, start string, end string) (string, error) {
	if len(str) <= len(start)+len(end) {
		return "", fmt.Errorf("search term longer than the given string")
	}
	index1 := strings.Index(str, start)
	if index1 == -1 {
		return "", fmt.Errorf("could not find {%v} and/or {%v}", start, end)
	}
	index2 := strings.Index(str[index1+len(start):], end)
	if index2 == -1 {
		return "", fmt.Errorf("could not find {%v} and/or {%v}", start, end)
	}
	return str[index1+len(start) : index1+len(start)+index2], nil
}

// String Shorten
func ShortenString(str any, ln int) string {
	if ln < 0 {
		ln = 0
	}
	switch i := str.(type) {
	case string:
		if len(i) > ln {
			return i[:ln]
		}
		return i
	case error:
		if len(i.Error()) > ln {
			return i.Error()[:ln]
		}
		return i.Error()
	default:
		return fmt.Sprintf("Type:%v", i)
	}
}

// ParseClock converts "ss", "mm:ss" or "hh:mm:ss" into seconds.
func ParseClock(s string) (int, error) {
	parts := strings.Split(strings.TrimSpace(s), ":")
	if len(parts) < 1 || len(parts) > 3 {
		return 0, fmt.Errorf("%q is not a time (use mm:ss or hh:mm:ss)", s)
	}
	secs, mul := 0, 1
	for j := len(parts) - 1; j >= 0; j-- {
		w, err := strconv.Atoi(strings.TrimSpace(parts[j]))
		if err != nil || w < 0 {
			return 0, fmt.Errorf("%q is not a time (use mm:ss or hh:mm:ss)", s)
		}
		secs += w * mul
		mul *= 60
	}
	return secs, nil
}

// PercentParse converts [start, end, total] timestamps into the
// [start%, end%] window of the video that should be downloaded.
func PercentParse(times []string) ([2]float32, error) {
	if len(times) != 3 {
		return [2]float32{0, 100}, fmt.Errorf("expected start, end and total length")
	}
	var secs [3]int
	for i, t := range times {
		v, err := ParseClock(t)
		if err != nil {
			return [2]float32{0, 100}, err
		}
		secs[i] = v
	}
	return PercentFromSeconds(secs[0], secs[1], secs[2])
}

// PercentFromSeconds is PercentParse for times already in seconds.
func PercentFromSeconds(start, end, total int) ([2]float32, error) {
	if total <= 0 {
		return [2]float32{0, 100}, fmt.Errorf("total length must be greater than zero")
	}
	if start >= total {
		return [2]float32{0, 100}, fmt.Errorf("start is not before the end of the video")
	}
	if end <= start {
		return [2]float32{0, 100}, fmt.Errorf("end must be after start")
	}
	s := float32(start) / float32(total) * 100
	e := float32(end) / float32(total) * 100
	if e > 100 {
		e = 100
	}
	return [2]float32{s, e}, nil
}

// ClockRange reads a start and an end time ("mm:ss" or "hh:mm:ss") in seconds.
func ClockRange(start, end string) (s, e int, err error) {
	if s, err = ParseClock(start); err != nil {
		return
	}
	if e, err = ParseClock(end); err != nil {
		return
	}
	if e <= s {
		err = fmt.Errorf("end must be after start")
	}
	return
}

// FormatClock writes seconds as hh:mm:ss.
func FormatClock(secs int) string {
	if secs < 0 {
		secs = 0
	}
	return fmt.Sprintf("%02d:%02d:%02d", secs/3600, secs/60%60, secs%60)
}

type HeaderType int

const (
	PLAYLIST_HEADER HeaderType = iota
	API_PLAYLIST_HEADER
	API_TOKEN_SWAP_HEADER
	HTML_HEADER
)

// Return Formated Headers. origin is "scheme://host" of the video page;
// videoUrl is needed only for API_PLAYLIST_HEADER.
func FormatedHeader(refHeader map[string]string, videoUrl string, origin string, headerStyle HeaderType, token *string) (header map[string]string) {
	var xtoken string
	if token != nil {
		xtoken = *token
	}
	header = make(map[string]string)
	for k, v := range refHeader {
		header[k] = v
	}
	header["Accept-Language"] = "en-US,en;q=0.9"
	header["Connection"] = "keep-alive"
	switch headerStyle {
	case HTML_HEADER:
		header["Accept"] = "*/*"
		header["X-Requested-With"] = "XMLHttpRequest"
		header["Sec-Fetch-Dest"] = "empty"
		header["Sec-Fetch-Mode"] = "cors"
		header["Sec-Fetch-Site"] = "same-origin"
		header["Priority"] = "u=0"
		header["TE"] = "trailers"
	case API_PLAYLIST_HEADER:
		header["Referer"] = videoUrl
		header["Sec-Fetch-Site"] = "same-origin"
		header["X-Requested-With"] = "XMLHttpRequest"
		header["X-Api-Token"] = xtoken
	case API_TOKEN_SWAP_HEADER:
		header["Accept"] = "*/*"
		header["X-Requested-With"] = "XMLHttpRequest"
		header["Content-Type"] = "application/x-www-form-urlencoded; charset=UTF-8"
		header["Origin"] = origin
		header["Sec-Fetch-Dest"] = "empty"
		header["Sec-Fetch-Mode"] = "cors"
		header["Sec-Fetch-Site"] = "same-origin"
		header["Priority"] = "u=4"
		header["X-Api-Token"] = xtoken
	default: // playlist
		delete(header, "Cookie")
		header["Accept"] = "*/*"
		header["Origin"] = origin
		header["Sec-Fetch-Dest"] = "empty"
		header["Sec-Fetch-Mode"] = "cors"
		header["Sec-Fetch-Site"] = "cross-site"
	}
	return
}
