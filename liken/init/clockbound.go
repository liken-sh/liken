package main

// Status publishes a bound on the clock offset, such as "±5ms", and not
// the exact offset, because the exact offset changes on every poll and
// each status write is a write to the cluster's etcd, which every
// leader replicates. The bounds are listed in machine/clock.go. An
// offset near the edge between two bounds would still flip between
// them, so a worse bound is published at once and a better one only
// after boundStreak polls in a row measure it.

import (
	"strings"

	"github.com/liken-sh/liken/liken/machine"
)

// boundStreak matches machine.ClockStaleAfter, which treats a time
// source that misses three polls as gone.
const boundStreak = 3

// betterBoundStreak counts the polls in a row that measured a better
// bound than the published one, and keeps the worst bound among them.
type betterBoundStreak struct {
	better int
	worst  machine.OffsetBound
}

// next returns the bound to publish after a poll that measured fresh,
// and whether it differs from published. A published bound that does
// not parse, such as an empty one, is replaced at once. A finished
// streak publishes its worst bound, because every poll in it fits
// inside that bound.
func (h *betterBoundStreak) next(published string, fresh machine.OffsetBound) (string, bool) {
	current, ok := machine.ParseOffsetBound(published)
	switch {
	case !ok || fresh > current:
		h.better = 0
		return fresh.String(), true
	case fresh == current:
		h.better = 0
		return published, false
	}
	if h.better == 0 || fresh > h.worst {
		h.worst = fresh
	}
	h.better++
	if h.better < boundStreak {
		return published, false
	}
	h.better = 0
	return h.worst.String(), true
}

// reset restarts the streak. The caller calls it after it publishes a
// new status for another reason.
func (h *betterBoundStreak) reset() {
	h.better = 0
}

// consoleBound writes a bound in ASCII for a console line, and
// "unknown" for an empty one. init writes console lines to /dev/kmsg,
// and the kernel escapes bytes outside printable ASCII when it is read,
// so "±5ms" would reach the log as "\xc2\xb15ms".
func consoleBound(bound string) string {
	if bound == "" {
		return "unknown"
	}
	return strings.Replace(bound, "±", "+/-", 1)
}
