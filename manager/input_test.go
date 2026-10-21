package manager

import (
	"reflect"
	"strings"
	"testing"
)

func TestParseInput(t *testing.T) {
	const u1 = "https://Recu.me/video/1234567/play"
	const u2 = "https://Recu.me/video/7654321/play"
	tests := []struct {
		name     string
		in       string
		want     []Entry
		problems int
	}{
		{"plain url", u1, []Entry{{URL: u1}}, 0},
		{"two plain lines with blank and CRLF", u1 + "\r\n\r\n" + u2 + "\r\n", []Entry{{URL: u1}, {URL: u2}}, 0},
		{"json line", `["` + u1 + `","55:00","1:10:00","1:30:00"]`, []Entry{{URL: u1, Section: []string{"55:00", "1:10:00", "1:30:00"}}}, 0},
		{"json lines with trailing commas", `["` + u1 + `","55:00","1:10:00","1:30:00"],` + "\n" + `"` + u2 + `",`,
			[]Entry{{URL: u1, Section: []string{"55:00", "1:10:00", "1:30:00"}}, {URL: u2}}, 0},
		{"mixed", u1 + "\n" + `["` + u2 + `","0:10","0:20","1:00"]`,
			[]Entry{{URL: u1}, {URL: u2, Section: []string{"0:10", "0:20", "1:00"}}}, 0},
		{"single element array", `["` + u1 + `"]`, []Entry{{URL: u1}}, 0},
		{"space separated", u1 + " 1:00 2:00 3:00", []Entry{{URL: u1, Section: []string{"1:00", "2:00", "3:00"}}}, 0},
		{"whole config.json", `{"urls":["` + u1 + `",["` + u2 + `","1:00","2:00","3:00"]],"header":{}}`,
			[]Entry{{URL: u1}, {URL: u2, Section: []string{"1:00", "2:00", "3:00"}}}, 0},
		{"array of arrays", `[["` + u1 + `"],["` + u2 + `","1:00","2:00","3:00"]]`,
			[]Entry{{URL: u1}, {URL: u2, Section: []string{"1:00", "2:00", "3:00"}}}, 0},
		{"comments are ignored", "# my list\n" + u1, []Entry{{URL: u1}}, 0},
		{"complete rows from an old config are skipped", `["` + u1 + `","COMPLETE"]` + "\n" + u2, []Entry{{URL: u2}}, 1},
		{"bad url", "not a link\n" + u1, []Entry{{URL: u1}}, 1},
		{"end before start", `["` + u1 + `","1:10:00","55:00","1:30:00"]`, nil, 1},
		{"start after video end", `["` + u1 + `","2:00:00","2:10:00","1:30:00"]`, nil, 1},
		{"bad time", `["` + u1 + `","a:b","1:00","2:00"]`, nil, 1},
		{"start and end without the video length", `["` + u1 + `","1:00","2:00"]`, []Entry{{URL: u1, Section: []string{"1:00", "2:00"}}}, 0},
		{"start and end, space separated", u1 + " 1:00 2:00", []Entry{{URL: u1, Section: []string{"1:00", "2:00"}}}, 0},
		{"end before start without the length", `["` + u1 + `","2:00","1:00"]`, nil, 1},
		{"bad json", `["` + u1 + `",`, nil, 1},
		{"empty", "   \n ", nil, 1},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, problems := ParseInput(tc.in)
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("entries = %+v, want %+v", got, tc.want)
			}
			if len(problems) != tc.problems {
				t.Fatalf("problems = %v, want %d", problems, tc.problems)
			}
		})
	}
}

func TestParseInputReportsLineNumbers(t *testing.T) {
	_, problems := ParseInput("https://a.b/video/1/play\nnope\n")
	if len(problems) != 1 || !strings.HasPrefix(problems[0], "line 2:") {
		t.Fatalf("problems = %v", problems)
	}
}
