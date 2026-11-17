package main

// watched.go states the one rule that says a person watched a work, and the
// jellyfin role writes that state to a Jellyfin server. The credits half of
// the rule reads the credits the Play carries, which is the operator's one
// answer for the file (markestimate.go), and which the up-next card in
// media-operator's display rises at too. So a work counts as watched where
// the card rises.
//
// The media browser states the same rule in
// media-browser/src/catalog/progress/watched.rs, and reads the same answer
// for each play's file from the catalog. So a title reads as finished on the
// screen exactly where this role writes it played in Jellyfin. Both sides
// test the rule against the one table in
// media-browser/src/catalog/progress/watched.json.

import "slices"

// The two amounts of a work that may remain when it counts as watched: a
// share of its length, and a number of seconds. The rule takes whichever
// leaves less time, so the seconds apply only to a work over 100 minutes.
// Credits run past the story, so a play that stopped in them counts as
// watched.
const (
	watchedPercent = 5
	watchedSeconds = 300
)

// One credits span, in seconds from the start of the file, as the audience
// message carries it. An absent start is the start of the file, and an
// absent end is the end of the file.
type creditsSpan struct {
	Start *float64 `json:"start,omitempty"`
	End   *float64 `json:"end,omitempty"`
}

// Whether a person watched this work. A work with no duration is never
// watched, because nothing says how long it is.
//
// The two amounts above estimate where the credits start, and credits do not
// scale with the runtime. So where the file's marks place the credits in the
// second half of the work, the work counts as watched from the start of those
// credits, whatever time is left. creditsLine finds that start.
func watched(position, duration int, credits []creditsSpan) bool {
	if duration <= 0 {
		return false
	}
	if line, held := creditsLine(duration, credits); held {
		return float64(position) >= line
	}
	return position >= duration-min(duration*watchedPercent/100, watchedSeconds)
}

// The start of the credits, and whether the work has one. The operator
// writes one credits span for a file, in the second half of the work, and a
// Play that the browser creates carries that span alone. The rule ranks and
// merges nothing. A Play from another client can carry several spans, and
// the earliest one that starts in the second half counts: a credits span in
// the first half is an opening title sequence and moves nothing. A span with
// a negative edge is dropped, and one that ends where it starts, or before,
// marks nothing.
func creditsLine(duration int, credits []creditsSpan) (float64, bool) {
	if duration <= 0 {
		return 0, false
	}
	length := float64(duration)
	var starts []float64
	for _, span := range credits {
		start, end := 0.0, length
		if span.Start != nil {
			start = *span.Start
		}
		if span.End != nil {
			end = *span.End
		}
		if start >= 0 && end > start && start >= length/2 {
			starts = append(starts, start)
		}
	}
	if len(starts) == 0 {
		return 0, false
	}
	return slices.Min(starts), true
}
