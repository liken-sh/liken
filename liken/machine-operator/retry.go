package main

// When the loop runs the next pass after one that did not finish.
//
// A pass that leaves a failure behind asks for a retry, and the loop
// keeps one timer for it. The delay depends on the kind of failure
// (outcome.go). A failure that a retry can clear starts at one second
// and doubles up to ten, so a write that failed because the API server
// restarted lands within a second or two of the server's return, and
// no later than about the ten-second pace the loop's ticker sets. A
// failure that will not clear by itself starts at ten seconds and
// doubles up to five minutes, so a misconfigured machine does not send
// the same refused request every ten seconds forever. A person who
// fixes the spec sends a watch event, and that pass tries at once.
//
// No pass comes sooner than a 429's Retry-After asked, not even one a
// step asked for, because that pass would send the same requests to a
// throttled API server.
//
// Each delay gets up to a tenth more at random. When the API server
// fails, every machine in the fleet fails at the same moment, and
// without the jitter they would all retry at the same moments too.

import (
	"math/rand/v2"
	"time"
)

const (
	transientFirstRetry = time.Second
	transientRetryLimit = 10 * time.Second
	lastingFirstRetry   = 10 * time.Second
	lastingRetryLimit   = 5 * time.Minute
)

// retrySchedule holds the current delay for each kind of failure, from
// one pass to the next. A zero delay means the last pass had no failure
// of that kind.
type retrySchedule struct {
	transient time.Duration
	lasting   time.Duration

	// jitter answers a number in [0, 1). Nil means math/rand.
	jitter func() float64
}

// next answers when the loop should run a pass after one that ended at
// now with this outcome, and false when nothing asks for one.
func (s *retrySchedule) next(out *passOutcome, now time.Time) (time.Time, bool) {
	var hasTransient, hasLasting bool
	for _, f := range out.failures {
		switch f.kind {
		case transient:
			hasTransient = true
		case lasting:
			hasLasting = true
		}
	}
	s.transient = grow(s.transient, hasTransient, transientFirstRetry, transientRetryLimit)
	s.lasting = grow(s.lasting, hasLasting, lastingFirstRetry, lastingRetryLimit)

	var delay time.Duration
	switch {
	case hasTransient:
		delay = s.transient
	case hasLasting:
		delay = s.lasting
	}

	at, ok := time.Time{}, false
	if delay > 0 {
		at, ok = now.Add(delay+time.Duration(float64(delay)*s.random()/10)), true
	}
	if !out.wake.IsZero() && (!ok || out.wake.Before(at)) {
		at, ok = out.wake, true
	}
	if ok && at.Before(out.notBefore) {
		at = out.notBefore
	}
	return at, ok
}

// grow answers the next delay for one kind of failure: the first delay
// after a pass without one, double the last after a pass with one, and
// zero after a pass without one.
func grow(last time.Duration, failed bool, first, limit time.Duration) time.Duration {
	switch {
	case !failed:
		return 0
	case last == 0:
		return first
	}
	return min(2*last, limit)
}

func (s *retrySchedule) random() float64 {
	if s.jitter == nil {
		return rand.Float64()
	}
	return s.jitter()
}
