package main

// The bound that status publishes for the clock's offset.
//
// Status holds a bound, such as "±5ms", and not the exact offset,
// because each status write costs a raft round on every leader and
// the exact offset moves on every poll (machine/clock.go has the
// bounds and what each one means). A clock whose offset stays near the
// line between two bounds would still cross it on many polls, so the
// bound moves with hysteresis. A worse bound publishes at once,
// because a clock that got worse is the news a person needs. A better
// bound publishes only after three polls in a row inside it, the same
// three polls after which a silent source counts as gone.

import "github.com/liken-sh/liken/liken/machine"

// boundStreak is how many polls in a row must measure a better bound
// before status publishes it.
const boundStreak = 3

// boundHold counts the polls in a row that measured a better bound
// than the published one, and the worst bound among them.
type boundHold struct {
	better int
	worst  machine.OffsetBound
}

// next takes one poll's bound and answers the bound to publish, and
// whether it differs from the published one. A published bound that
// does not parse, such as an empty one after a boot step, is replaced
// by the first poll, because there is no claim to protect. A streak
// publishes the worst bound it measured, because that is the bound
// that every poll in it fits inside.
func (h *boundHold) next(published string, fresh machine.OffsetBound) (string, bool) {
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

// reset forgets a streak, for a publish that changed the standing
// another way.
func (h *boundHold) reset() {
	h.better = 0
}
