package main

// The serio watch retries an attachment that failed.
//
// An attachment fails when the kernel refuses one of the attach calls,
// or when serport's read ends while the tty still exists. The second
// case happens with cdc_acm on a USB reset-resume, because
// acm_reset_resume hangs up the tty but keeps it. A plain USB reset is
// different. cdc_acm has no post_reset, so the USB core unbinds the
// driver and probes it again, and a new tty registers under the old
// name within milliseconds, often before the uevents of the reset stop.
//
// The registry records each failure under the sysfs path of the USB
// device, which stays the same while the adapter stays in the same
// port. The record also has the identity of the tty, the inode of its
// sysfs directory, which changes each time a tty registers. A retry on
// the same tty waits for a backoff that doubles from serioRetryBase to
// serioRetryMax. Each open of the line toggles the adapter's modem
// lines, and a machine that runs pods receives uevents often, so a
// retry on every uevent would disturb the adapter.
//
// After the first failure, a new tty on the same port attaches at once,
// so an adapter that resets once comes back at once. The failure count
// belongs to the port, so an adapter that enumerates again on every
// attach waits for the backoff from its second failure on. The record
// stays while the port is empty, so an adapter that is plugged in again
// keeps its count.

import (
	"time"
)

const (
	serioRetryBase = time.Second
	serioRetryMax  = 5 * time.Minute

	// A holder that held its port for serioStableHold resets the
	// failure count when it ends. Without the reset, the count of an
	// adapter that resets once a day would grow until each reset waits
	// serioRetryMax.
	serioStableHold = time.Minute
)

// serioRefusal is the latest failure on one USB port. failures counts
// the failures in a row on the port.
type serioRefusal struct {
	identity uint64
	message  string
	failures int
	retryAt  time.Time
}

// serioBackoff returns how long to wait after the given number of
// failures in a row.
func serioBackoff(failures int) time.Duration {
	wait := serioRetryBase
	for range failures - 1 {
		wait *= 2
		if wait >= serioRetryMax {
			return serioRetryMax
		}
	}
	return wait
}

// nextFailures returns the failure count after one more failure by a
// holder that held its port for held.
func nextFailures(prior int, held time.Duration) int {
	if held >= serioStableHold {
		return 1
	}
	return prior + 1
}

// refuse records a failure of a holder on its USB port, and schedules
// the next attempt.
func (r *serioRegistry) refuse(h *serioHolder, message string, held time.Duration) {
	failures := nextFailures(h.failures, held)
	r.refusals[h.usbPath] = serioRefusal{
		identity: h.identity,
		message:  message,
		failures: failures,
		retryAt:  r.now().Add(serioBackoff(failures)),
	}
}

// prune removes the holders that have ended and records each end as a
// failure on its USB port. A holder whose tty registered again is
// already gone, because lineStatus (seriostatus.go) removes it and
// records its failure.
func (r *serioRegistry) prune() {
	for tty, h := range r.holders {
		select {
		case <-h.done:
		default:
			continue
		}
		delete(r.holders, tty)
		r.recordEnd(h)
	}
}

// recordEnd records a holder's end as a failure. An attach error does
// not count the time held, because the holder never held the line.
func (r *serioRegistry) recordEnd(h *serioHolder) {
	if h.attachErr != nil {
		r.refuse(h, h.attachErr.Error(), 0)
		return
	}
	r.refuse(h, endMessage(h.endErr), r.now().Sub(h.started))
}

func endMessage(err error) string {
	if err != nil {
		return "read: " + err.Error()
	}
	return "read: the kernel ended the attachment while the tty stayed"
}

// nextRetry returns how long until the next walk that no uevent
// triggers, or false when there is none. That walk is due at the
// earliest retryAt of a refusal, or when an unbound port's
// serioBindGrace ends (seriostatus.go). nextRetry skips times already
// past. The walk at such a time either acted on it or moved it
// forward, and a refusal on an empty port has nothing to act on, so a
// past time would wake the walk in a loop.
func (r *serioRegistry) nextRetry() (time.Duration, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := r.now()
	var earliest time.Time
	due := func(at time.Time) {
		if at.After(now) && (earliest.IsZero() || at.Before(earliest)) {
			earliest = at
		}
	}
	for _, refusal := range r.refusals {
		due(refusal.retryAt)
	}
	for _, u := range r.unbound {
		due(u.since.Add(serioBindGrace))
	}
	if earliest.IsZero() {
		return 0, false
	}
	return earliest.Sub(now), true
}
