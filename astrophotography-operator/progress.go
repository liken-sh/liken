package main

// A target's progress, as the runner reads it (plan 01). status.sequences
// holds one entry for each sequence of the spec, with the frames
// archived and the frames pending. The promotion loop owns archived,
// status.progress, and Complete (promotionstatus.go), and the runner
// adds only to pending, so both align the list with the spec by one
// rule, sequenceProgress.

import (
	"fmt"
	"time"

	"github.com/liken-sh/liken/astrophotography-operator/astrophotography"
)

// sequenceSettings are the settings of one sequence that the runner
// writes before an exposure. A nil pointer leaves the camera's own
// value.
type sequenceSettings struct {
	filter                string
	exposure              time.Duration
	binning, gain, offset *int32
}

// settingsOf reads a sequence's settings. The CRD accepts only an
// exposure that time.ParseDuration reads, so an error means the object
// did not come through the API server.
func settingsOf(s astrophotography.Sequence) (sequenceSettings, error) {
	exposure, err := s.ExposureDuration()
	if err != nil {
		return sequenceSettings{}, fmt.Errorf("exposure %q: %w", s.Exposure, err)
	}
	return sequenceSettings{filter: s.Filter, exposure: exposure, binning: s.Binning, gain: s.Gain, offset: s.Offset}, nil
}

// String names the settings in a message, such as "Luminance 5m0s".
func (s sequenceSettings) String() string {
	filter := s.filter
	if filter == "" {
		filter = "no filter"
	}
	return filter + " " + s.exposure.String()
}

// alignSequences aligns status.sequences with the spec, and keeps the
// counts of each sequence that stays.
func alignSequences(t *astrophotography.Target) {
	t.Status.Sequences = sequenceProgress(t.Spec.Sequences, t.Status.Sequences, nil, true)
}

// aligned answers a copy of a target with its sequences aligned, and
// leaves the target as it was.
func aligned(t *astrophotography.Target) *astrophotography.Target {
	c := *t
	alignSequences(&c)
	return &c
}

// workLeft answers how many frames a sequence still needs, which is
// never below zero: a target can end with more frames than it asked
// for when a promotion lags.
func workLeft(p astrophotography.SequenceProgress) int32 {
	return max(0, p.Count-p.Archived-p.Pending)
}

// nextSequence answers the index of the first sequence with work left,
// and false when none has any. One filter at a time is the cheapest
// order, because a change of filter costs a refocus on a real rig.
func nextSequence(t *astrophotography.Target) (int, bool) {
	for i, p := range t.Status.Sequences {
		if workLeft(p) > 0 {
			return i, true
		}
	}
	return 0, false
}
