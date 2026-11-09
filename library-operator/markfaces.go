package main

// markfaces.go is the third tier of the credits estimate, read only where a
// film has no exact clue and no inferred one. The appearances tool samples a
// film once a second for faces, and end credits are text on a plain
// background or over footage with no one in frame, so the detections record
// of a film ends in a run of samples with no face where the credits run. The
// appearances worker records where that run starts, and the walk copies it
// into the catalog. Plan 92 holds the measurements the limits below come
// from.

// The source of an estimate of this tier, so a reader of the marks table
// can tell it from the estimate of the two tiers above it.
const markSourceFaces = "faces"

// The limits on the faceless run. It must start in the last 15% of the
// film, because a film can hold a long shot with no face anywhere, and it
// must last two minutes, because a feature's credits run longer than that
// and a faceless last shot of the story runs shorter. A shorter limit
// fires on a few more films, and more of those marks start after the time
// rule would raise the card.
const (
	facelessCreditsWindowPercent = 15
	facelessCreditsShortest      = int64(120000)
)

// How far into the faceless run the estimate starts. A story often ends on
// a shot with no face in it, such as a landscape or a ship that leaves, and
// the credits start after that shot. A card that rises late costs a person
// little, because it rises over the credits, and a card that rises early
// covers the end of the story. So the estimate starts this much after the
// run, which made an early rise of more than 30 seconds rare in the
// measurement.
const facelessCreditsDelay = int64(90000)

// The genre whose titles the tier never reads. The detector finds few
// drawn faces, so an animated film can be faceless for minutes in its
// story.
const animationGenre = "Animation"

// The start of the credits that the faceless end of a film's detections
// record gives, 90 seconds into the run, and false where it gives none. An
// episode gives none, because the tier was measured on films alone, and an
// episode's credits run under a minute.
func (f estimateFile) facelessCredits() (int64, bool) {
	start := f.faceless
	switch {
	case f.series != "", f.animation, start <= 0:
		return 0, false
	case 100*start < (100-facelessCreditsWindowPercent)*f.duration:
		return 0, false
	case f.duration-start < facelessCreditsShortest:
		return 0, false
	}
	return start + facelessCreditsDelay, true
}
