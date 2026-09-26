package main

// How the serio walk retries an attachment that failed.
//
// An attachment fails in two ways. The kernel refuses one of the
// attach calls, or the read ends while the tty stays. The second is
// what cdc_acm does on a USB reset-resume: acm_reset_resume hangs up
// the tty and keeps it, and serport's hangup ends the read. A plain
// USB reset goes further. cdc_acm has no post_reset, so the USB core
// unbinds it and probes it again, and a new tty takes the old name
// within milliseconds, often inside one settle of the uevent burst.
//
// So a refusal is kept for the USB port the adapter is plugged into,
// the sysfs path of its USB device, with the tty it happened on,
// identified by the inode of the tty's sysfs directory. The kernel
// gives a tty that registers again a new directory with a new inode.
// A refusal on the same tty retries after a backoff that doubles from
// one second to five minutes. Opening the line on every uevent instead
// would toggle the adapter's modem lines to get the same refusal
// again, and uevents arrive often on a machine that runs pods.
//
// A new tty on the port attaches at once after the first failure, so
// an adapter that resets once comes back at once. The failure count
// belongs to the port, not to the tty, so an adapter that enumerates
// again on every attach still backs off from its second failure on.
// A refusal stays while its port is empty, so an adapter that is
// plugged in again keeps its count: a long hold before the unplug
// already started the count over.

import (
	"time"
)

const (
	// serioRetryBase is the first backoff, and serioRetryMax the bound
	// it doubles to.
	serioRetryBase = time.Second
	serioRetryMax  = 5 * time.Minute

	// serioStableHold is how long a holder must hold its port for its
	// end to start the backoff over. An adapter that resets once a day
	// then waits one second to come back, not five minutes.
	serioStableHold = time.Minute
)

// serioRefusal is one refused USB port: the identity of the tty the
// refusal happened on, the message the status carries, the count of
// failures in a row on the port, and the time of the next attempt.
type serioRefusal struct {
	identity uint64
	message  string
	failures int
	retryAt  time.Time
}

// serioBackoff is the wait after a count of failures in a row.
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

// nextFailures counts one more failure, and starts the count over
// after a holder that held its port for serioStableHold or longer.
func nextFailures(prior int, held time.Duration) int {
	if held >= serioStableHold {
		return 1
	}
	return prior + 1
}

// refuse records a failure on a holder's USB port.
func (r *serioRegistry) refuse(h *serioHolder, message string, held time.Duration) {
	failures := nextFailures(h.failures, held)
	r.refusals[h.usbPath] = serioRefusal{
		identity: h.identity,
		message:  message,
		failures: failures,
		retryAt:  r.now().Add(serioBackoff(failures)),
	}
}

// prune removes the holders that ended, and records why each ended on
// its USB port. A holder whose tty registered again while it held the
// read ends the same way, and replace records it (seriostatus.go).
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

// recordEnd records the failure a holder's end stands for.
func (r *serioRegistry) recordEnd(h *serioHolder) {
	if h.attachErr != nil {
		r.refuse(h, h.attachErr.Error(), 0)
		return
	}
	r.refuse(h, endMessage(h.endErr), r.now().Sub(h.started))
}

// endMessage words a holder's end for the status.
func endMessage(err error) string {
	if err != nil {
		return "read: " + err.Error()
	}
	return "read: the kernel ended the attachment while the tty stayed"
}

// nextRetry returns the wait until the earliest moment a walk is due
// with no uevent to announce it: a refusal's next attempt, or the end
// of an unbound port's grace (seriostatus.go). A moment that has
// passed is not due again. The walk at that moment either acted on it
// or moved it forward, and a refusal whose port is empty has nothing
// to act on, so counting past moments would wake the walk in a loop.
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
