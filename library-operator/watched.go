package main

// watched.go states the one rule that says a person watched a work, and the
// jellyfin role writes that state to a Jellyfin server. The credits half of
// the rule reads the file's marks the way the up-next card in
// media-operator's display reads them (display/src/marks.rs), so a work
// counts as watched where the card rises. media-browser/src/catalog/progress.rs
// states the remaining-time half alone for what the browser draws, and it
// reads no mark.

import (
	"slices"
	"sort"
)

// The two amounts of a work that may remain when it counts as watched: a
// share of its length, and a number of seconds. The rule takes whichever
// leaves less time, so the seconds apply only to a work over 100 minutes.
// Credits run past the story, so a play that stopped in them counts as
// watched.
const (
	watchedPercent = 5
	watchedSeconds = 300
)

// One credits candidate, in seconds from the start of the file, as the
// audience message carries it. An absent start is the start of the file, and
// an absent end is the end of the file.
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

// The start of the earliest merged credits span that starts in the second
// half of the work, and whether one does. The merge is the display's.
// Candidates that overlap form one group, and the group becomes one span
// from the median start and the median end of its members, so one submission
// that is off moves the line less than an average would. Groups that do not
// overlap stay apart, because a film can carry the main credits, then a scene,
// then more credits. A credits span in the first half is an opening title
// sequence and moves nothing. A candidate with a negative edge is dropped,
// and one that ends where it starts, or before, marks nothing.
func creditsLine(duration int, credits []creditsSpan) (float64, bool) {
	if duration <= 0 {
		return 0, false
	}
	length := float64(duration)
	ranges := [][2]float64{}
	for _, candidate := range credits {
		start, end := 0.0, length
		if candidate.Start != nil {
			start = *candidate.Start
		}
		if candidate.End != nil {
			end = *candidate.End
		}
		if start < 0 || end < 0 || end <= start {
			continue
		}
		ranges = append(ranges, [2]float64{start, end})
	}
	sort.SliceStable(ranges, func(a, b int) bool { return ranges[a][0] < ranges[b][0] })

	var groups [][][2]float64
	reach := 0.0
	for at, one := range ranges {
		if at > 0 && one[0] < reach {
			groups[len(groups)-1] = append(groups[len(groups)-1], one)
		} else {
			groups = append(groups, [][2]float64{one})
		}
		reach = max(reach, one[1])
	}
	for _, group := range groups {
		starts, ends := make([]float64, len(group)), make([]float64, len(group))
		for at, one := range group {
			starts[at], ends[at] = one[0], one[1]
		}
		start, end := median(starts), median(ends)
		if end > start && start >= length/2 {
			return start, true
		}
	}
	return 0, false
}

// The middle value, or the mean of the two middle values when the count is
// even. The caller never passes an empty list.
func median(values []float64) float64 {
	slices.Sort(values)
	middle := len(values) / 2
	if len(values)%2 == 0 {
		return (values[middle-1] + values[middle]) / 2
	}
	return values[middle]
}
