package main

// This file decides when the loop runs the next pass after a pass that
// failed.
//
// A pass that ends with a failure requests a retry, and the loop keeps
// one timer for it. The delay depends on the kind of failure
// (outcome.go). A failure that a retry can clear starts at one second
// and doubles up to ten. A write that failed because the API server
// restarted is then retried within a second or two of the server's
// return, and a server that stays down is tried every ten seconds. A
// failure that will not clear by itself starts at ten seconds and
// doubles up to five minutes, so a misconfigured machine does not send
// the same refused request every ten seconds forever. When a person
// fixes the spec, the watch event starts a pass at once.
//
// No pass runs sooner than the Retry-After of a 429, even a pass that
// a step asked for, because it would send the same requests to a
// throttled API server.
//
// Each delay gets a random addition of up to a tenth. When the API
// server fails, every machine in the fleet fails at the same moment,
// and without the jitter they would all retry at the same moments too.

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

// retrySchedule keeps the current delay for each kind of failure
// between passes. A zero delay means the last pass had no failure of
// that kind.
type retrySchedule struct {
	transient time.Duration
	lasting   time.Duration

	// jitter returns a number in [0, 1). When nil, it uses math/rand.
	jitter func() float64
}

// next returns when the loop should run its next pass, given a pass
// that ended at now with this outcome. It returns false when nothing
// requires another pass.
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

// grow returns the next delay for one kind of failure: zero when the
// pass had no such failure, first when the previous pass had none, and
// otherwise double the last delay, up to limit.
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
