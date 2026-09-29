package main

// What these tests prove. How little of a work must be left before a play
// of it counts as watched, at the boundary second and the second before
// it, and where the file's credits marks move that line.

import (
	"encoding/json"
	"os"
	"testing"
)

// The cases the media browser's own rule runs against, from the one file
// both sides read, so a play reads as finished on the screen exactly where
// the jellyfin role writes it played.
const watchedCasesFile = "media-browser/src/catalog/progress/watched.json"

// One case of that file: a position, a duration, the credits candidates in
// seconds, and whether the work counts as watched.
type watchedCase struct {
	Name     string        `json:"name"`
	Position int           `json:"position"`
	Duration int           `json:"duration"`
	Credits  []creditsSpan `json:"credits"`
	Watched  bool          `json:"watched"`
}

func watchedCases(t *testing.T) []watchedCase {
	t.Helper()
	body, err := os.ReadFile(watchedCasesFile)
	if err != nil {
		t.Fatalf("reading the shared cases: %v", err)
	}
	cases := []watchedCase{}
	if err := json.Unmarshal(body, &cases); err != nil {
		t.Fatalf("decoding the shared cases: %v", err)
	}
	return cases
}

// How little of a work must be left before a play of it counts as watched,
// at the boundary second and the second before it, with and without the
// file's credits marks.
func TestWhenAWorkCountsAsWatched(t *testing.T) {
	for _, test := range watchedCases(t) {
		t.Run(test.Name, func(t *testing.T) {
			if got := watched(test.Position, test.Duration, test.Credits); got != test.Watched {
				t.Errorf("watched = %v, want %v", got, test.Watched)
			}
		})
	}
}

// One credits candidate as the audience carries it, in seconds. A negative
// number stands for an absent edge, so a table row reads on one line.
func credit(start, end float64) creditsSpan {
	span := creditsSpan{}
	if start >= 0 {
		span.Start = &start
	}
	if end >= 0 {
		span.End = &end
	}
	return span
}

// No edge at all, which a test sets where a candidate states one it cannot.
const noEdge = -1.0

// Where the credits line falls reads the candidates the way the display in
// media-operator reads them (display/src/marks.rs): overlapping candidates
// merge into one span from their median start and median end, spans that do
// not overlap stay apart, and the line is the start of the earliest merged
// span that starts in the second half. These are the display's own cases,
// and the outlier case that the median absorbs.
func TestTheCreditsLineIsTheDisplays(t *testing.T) {
	for _, test := range []struct {
		name     string
		duration int
		credits  []creditsSpan
		line     float64
		held     bool
	}{
		{name: "one credits span", duration: 3316,
			credits: []creditsSpan{credit(3253, 3316)}, line: 3253, held: true},
		{name: "the main credits, then more after a scene", duration: 5500,
			credits: []creditsSpan{credit(5420, 5500), credit(5000, 5300)}, line: 5000, held: true},
		{name: "overlapping candidates merge into the median span", duration: 5500,
			credits: []creditsSpan{credit(5000, 5300), credit(5420, 5500), credit(5002, 5298)},
			line:    5001, held: true},
		{name: "a bad early outlier inside the second half is absorbed by the median", duration: 9000,
			credits: []creditsSpan{credit(8460, 9000), credit(8470, 9000), credit(7200, 9000)},
			line:    8460, held: true},
		{name: "an opening title sequence in the first half moves nothing", duration: 5500,
			credits: []creditsSpan{credit(30, 200), credit(5000, 5300)}, line: 5000, held: true},
		{name: "credits in the first half alone move nothing", duration: 5500,
			credits: []creditsSpan{credit(30, 200)}},
		{name: "credits that start at the middle count", duration: 5500,
			credits: []creditsSpan{credit(2750, noEdge)}, line: 2750, held: true},
		{name: "an absent end is the end of the file", duration: 5400,
			credits: []creditsSpan{credit(5000, noEdge)}, line: 5000, held: true},
		{name: "an absent start is the start of the file, in the first half", duration: 5400,
			credits: []creditsSpan{credit(noEdge, 5000)}},
		{name: "spans that only touch stay apart", duration: 6000,
			credits: []creditsSpan{credit(5000, 5400), credit(5400, 6000), credit(2000, 5000)},
			line:    5000, held: true},
		{name: "an even count takes the mean of the middle two", duration: 6000,
			credits: []creditsSpan{credit(5000, 5600), credit(5002, 5602)}, line: 5001, held: true},
		{name: "a candidate that ends where it starts, or before, marks nothing", duration: 6000,
			credits: []creditsSpan{credit(5000, 5000), credit(5100, 4000)}},
		{name: "a start past the end with no end of its own marks nothing", duration: 6000,
			credits: []creditsSpan{credit(6100, noEdge)}},
		{name: "a negative edge is a candidate the display drops", duration: 6000,
			credits: []creditsSpan{{Start: ptr(-5.0), End: ptr(5900.0)}}},
		{name: "no credits", duration: 5500},
		{name: "a zero duration", duration: 0, credits: []creditsSpan{credit(3253, 3316)}},
	} {
		t.Run(test.name, func(t *testing.T) {
			line, held := creditsLine(test.duration, test.credits)
			if held != test.held || line != test.line {
				t.Errorf("line = %v, %v, want %v, %v", line, held, test.line, test.held)
			}
		})
	}
}

func ptr(value float64) *float64 { return &value }
