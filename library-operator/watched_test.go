package main

// What these tests prove. How little of a work must be left before a play
// of it counts as watched, at the boundary second and the second before
// it.

import "testing"

func TestWhenAWorkCountsAsWatched(t *testing.T) {
	for _, test := range []struct {
		name     string
		position int
		duration int
		watched  bool
	}{
		{name: "a 22 minute sitcom with 1m06s left", position: 1320 - 66, duration: 1320, watched: true},
		{name: "a 22 minute sitcom one second before that", position: 1320 - 67, duration: 1320},
		{name: "a 42 minute drama with 2m06s left", position: 2520 - 126, duration: 2520, watched: true},
		{name: "a 42 minute drama one second before that", position: 2520 - 127, duration: 2520},
		{name: "a 100 minute film with 5m00s left", position: 6000 - 300, duration: 6000, watched: true},
		{name: "a 100 minute film one second before that", position: 6000 - 301, duration: 6000},
		{name: "a 150 minute film with 5m00s left", position: 9000 - 300, duration: 9000, watched: true},
		{name: "a 150 minute film one second before that", position: 9000 - 301, duration: 9000},
		{name: "a film played to the end", position: 6000, duration: 6000, watched: true},
		{name: "a film played past its stated end", position: 6100, duration: 6000, watched: true},
		{name: "a film at the start", position: 0, duration: 6000},
		{name: "a play that carried no duration", position: 0, duration: 0},
		{name: "a position in a play that carried no duration", position: 600, duration: 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := watched(test.position, test.duration, nil); got != test.watched {
				t.Errorf("watched = %v, want %v", got, test.watched)
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

// Where a credits line is held, the work counts as watched from it, whatever
// time is left. Where none is, the remaining-time rule applies.
func TestACreditsLineMovesTheWatchedLine(t *testing.T) {
	for _, test := range []struct {
		name     string
		position int
		duration int
		credits  []creditsSpan
		watched  bool
	}{
		{name: "a 150 minute film at the start of its 9 minutes of credits",
			position: 8460, duration: 9000, credits: []creditsSpan{credit(8460, 9000)}, watched: true},
		{name: "the same film one second before them",
			position: 8459, duration: 9000, credits: []creditsSpan{credit(8460, 9000)}},
		{name: "the same film where one early outlier stands beside two good candidates",
			position: 8000, duration: 9000,
			credits: []creditsSpan{credit(8460, 9000), credit(8470, 9000), credit(7200, 9000)}},
		{name: "a 22 minute sitcom whose credits start 30 seconds from the end, at 1m06s left",
			position: 1320 - 66, duration: 1320, credits: []creditsSpan{credit(1290, noEdge)}},
		{name: "the same sitcom at the start of its credits",
			position: 1290, duration: 1320, credits: []creditsSpan{credit(1290, noEdge)}, watched: true},
		{name: "credits in the first half alone leave the remaining-time rule",
			position: 9000 - 300, duration: 9000, credits: []creditsSpan{credit(30, 200)}, watched: true},
		{name: "no duration, whatever the marks",
			position: 600, duration: 0, credits: []creditsSpan{credit(500, noEdge)}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := watched(test.position, test.duration, test.credits); got != test.watched {
				t.Errorf("watched = %v, want %v", got, test.watched)
			}
		})
	}
}
