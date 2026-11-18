package main

// status.sequences has one entry for each sequence in the spec, with
// its numbers of frames archived and pending. The runner adds one to
// pending for each frame it saves. The promotion loop sets archived
// from each Job's report, takes the frames it archived out of pending,
// and sets status.progress and the Complete condition. Both match the
// entries to the spec with sequenceProgress.

import (
	"fmt"
	"time"

	"github.com/liken-sh/liken/astrophotography-operator/astrophotography"
)

// sequenceSettings are the settings of one sequence that the runner
// writes to the camera before an exposure. A nil pointer leaves the
// camera's current value.
type sequenceSettings struct {
	filter                string
	exposure              time.Duration
	binning, gain, offset *int32
}

// settingsOf reads a sequence's settings. The CRD accepts only an
// exposure that time.ParseDuration can parse, so an error means the
// object did not pass through API server validation.
func settingsOf(s astrophotography.Sequence) (sequenceSettings, error) {
	exposure, err := s.ExposureDuration()
	if err != nil {
		return sequenceSettings{}, fmt.Errorf("parsing the exposure %q of a sequence: %w", s.Exposure, err)
	}
	return sequenceSettings{filter: s.Filter, exposure: exposure, binning: s.Binning, gain: s.Gain, offset: s.Offset}, nil
}

// String formats the settings for a message, such as "Luminance 5m0s".
func (s sequenceSettings) String() string {
	filter := s.filter
	if filter == "" {
		filter = "no filter"
	}
	return filter + " " + s.exposure.String()
}

// alignSequences matches status.sequences to the spec and keeps the
// counts of each sequence that is still in the spec.
func alignSequences(t *astrophotography.Target) {
	t.Status.Sequences = sequenceProgress(t.Spec.Sequences, t.Status.Sequences, nil, true)
}

// aligned returns a copy of the target with its sequences matched to
// the spec. The target itself does not change.
func aligned(t *astrophotography.Target) *astrophotography.Target {
	c := *t
	alignSequences(&c)
	return &c
}

// workLeft returns how many more frames a sequence needs. It is never
// below zero, because a sequence can have more frames than its count.
func workLeft(p astrophotography.SequenceProgress) int32 {
	return max(0, p.Count-p.Archived-p.Pending)
}

// nextSequence returns the index of the first sequence with work left,
// and false when no sequence has any. Finishing one sequence before the
// next keeps filter changes few, because a filter change can need a
// refocus.
func nextSequence(t *astrophotography.Target) (int, bool) {
	for i, p := range t.Status.Sequences {
		if workLeft(p) > 0 {
			return i, true
		}
	}
	return 0, false
}
