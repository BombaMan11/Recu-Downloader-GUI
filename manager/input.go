package manager

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"Recurbate/tools"
)

// Entry is one parsed line of the input box.
type Entry struct {
	URL     string
	Section []string // nil, [start, end] (the video length is looked up) or [start, end, total]
}

// ParseInput understands, one per line:
//
//	https://host/video/123/play
//	["https://host/video/123/play","55:00","1:10:00"]
//	["https://host/video/123/play","55:00","1:10:00","1:30:00"]
//
// (the last form states the length of the whole video; without it the length
// is looked up when the video's playlist is read)
//
// (trailing commas are fine, so lines can be pasted straight out of the old
// config.json). A pasted config.json / array of arrays works too.
func ParseInput(text string) (entries []Entry, problems []string) {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return nil, []string{"nothing to add"}
	}
	// a whole config.json or an array of entries
	var cfg struct {
		Urls []any `json:"urls"`
	}
	if strings.HasPrefix(trimmed, "{") && json.Unmarshal([]byte(trimmed), &cfg) == nil && len(cfg.Urls) > 0 {
		return fromList(cfg.Urls)
	}
	var list []any
	if strings.HasPrefix(trimmed, "[[") && json.Unmarshal([]byte(trimmed), &list) == nil {
		return fromList(list)
	}
	for i, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		line = strings.TrimSpace(strings.TrimRight(line, ","))
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "//") {
			continue
		}
		var toks []string
		if strings.HasPrefix(line, "[") {
			var arr []any
			if err := json.Unmarshal([]byte(line), &arr); err != nil {
				problems = append(problems, fmt.Sprintf("line %d: not valid JSON (%v)", i+1, err))
				continue
			}
			var err error
			if toks, err = tokens(arr); err != nil {
				problems = append(problems, fmt.Sprintf("line %d: %v", i+1, err))
				continue
			}
		} else {
			if strings.HasPrefix(line, `"`) {
				// a quoted url as written in the old config.json
				var q string
				if json.Unmarshal([]byte(line), &q) == nil {
					line = q
				} else {
					line = strings.Trim(line, `"`)
				}
			}
			toks = strings.Fields(line)
		}
		e, skip, err := entryFromTokens(toks)
		if err != nil {
			problems = append(problems, fmt.Sprintf("line %d: %v", i+1, err))
			continue
		}
		if skip {
			problems = append(problems, fmt.Sprintf("line %d: marked COMPLETE, skipped", i+1))
			continue
		}
		entries = append(entries, e)
	}
	if len(entries) == 0 && len(problems) == 0 {
		problems = append(problems, "nothing to add")
	}
	return
}

func fromList(list []any) (entries []Entry, problems []string) {
	for i, v := range list {
		var toks []string
		switch t := v.(type) {
		case string:
			toks = []string{t}
		case []any:
			var err error
			if toks, err = tokens(t); err != nil {
				problems = append(problems, fmt.Sprintf("entry %d: %v", i+1, err))
				continue
			}
		default:
			problems = append(problems, fmt.Sprintf("entry %d: unexpected format", i+1))
			continue
		}
		e, skip, err := entryFromTokens(toks)
		if err != nil {
			problems = append(problems, fmt.Sprintf("entry %d: %v", i+1, err))
			continue
		}
		if skip {
			problems = append(problems, fmt.Sprintf("entry %d: marked COMPLETE, skipped", i+1))
			continue
		}
		entries = append(entries, e)
	}
	return
}

func tokens(arr []any) ([]string, error) {
	toks := make([]string, 0, len(arr))
	for _, x := range arr {
		switch s := x.(type) {
		case string:
			toks = append(toks, s)
		case float64:
			toks = append(toks, strconv.FormatFloat(s, 'f', -1, 64))
		default:
			return nil, fmt.Errorf("unexpected value in the list")
		}
	}
	return toks, nil
}

// accepts the shapes of the old config.json: url | url,offset|COMPLETE |
// url,start,end,total | url,start,end,total,offset|COMPLETE, and url,start,end
func entryFromTokens(toks []string) (e Entry, skip bool, err error) {
	switch len(toks) {
	case 1, 2, 3, 4, 5:
	default:
		return e, false, fmt.Errorf("expected a URL, or [URL, start, end] or [URL, start, end, total]")
	}
	u := strings.TrimSpace(toks[0])
	pu, perr := url.Parse(u)
	if perr != nil || (pu.Scheme != "http" && pu.Scheme != "https") || pu.Host == "" {
		return e, false, fmt.Errorf("%q is not a http(s) link", tools.ShortenString(u, 60))
	}
	if len(toks) == 2 || len(toks) == 5 {
		if strings.EqualFold(strings.TrimSpace(toks[len(toks)-1]), "COMPLETE") {
			return e, true, nil
		}
	}
	e.URL = u
	if len(toks) == 3 {
		sec := []string{strings.TrimSpace(toks[1]), strings.TrimSpace(toks[2])}
		if _, _, cerr := tools.ClockRange(sec[0], sec[1]); cerr != nil {
			return e, false, fmt.Errorf("section %v: %v", sec, cerr)
		}
		e.Section = sec
	}
	if len(toks) >= 4 {
		sec := []string{strings.TrimSpace(toks[1]), strings.TrimSpace(toks[2]), strings.TrimSpace(toks[3])}
		if _, perr := tools.PercentParse(sec); perr != nil {
			return e, false, fmt.Errorf("section %v: %v", sec, perr)
		}
		e.Section = sec
	}
	return e, false, nil
}
