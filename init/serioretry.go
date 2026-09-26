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
// So a refusal is kept for the tty it happened on, identified by the
// inode of the tty's sysfs directory. The kernel gives a tty that
// registers again a new directory with a new inode, so a new tty under
// the old name is new hardware, and the walk attaches it at once. A
// refusal on the same tty retries after a backoff that doubles from
// one second to five minutes. Opening the line on every uevent instead
// would toggle the adapter's modem lines to get the same refusal
// again, and uevents arrive often on a machine that runs pods.

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

// serioRefusal is one refused tty: its identity when the refusal
// happened, the message the status carries, the count of failures in
// a row, and the time of the next attempt.
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

// refuse records a failure on a holder's tty.
func (r *serioRegistry) refuse(tty string, h *serioHolder, message string, held time.Duration) {
	failures := nextFailures(h.failures, held)
	r.refusals[tty] = serioRefusal{
		identity: h.identity,
		message:  message,
		failures: failures,
		retryAt:  r.now().Add(serioBackoff(failures)),
	}
}

// prune removes the holders that ended and records why each ended,
// and forgets the refusals of ttys that left or registered again.
func (r *serioRegistry) prune(lines []serialLineInfo) {
	current := map[string]uint64{}
	for _, l := range lines {
		current[l.tty] = l.identity
	}
	for tty, h := range r.holders {
		select {
		case <-h.done:
		default:
			continue
		}
		delete(r.holders, tty)
		if identity, present := current[tty]; !present || identity != h.identity {
			continue
		}
		if h.attachErr != nil {
			r.refuse(tty, h, h.attachErr.Error(), 0)
			continue
		}
		r.refuse(tty, h, endMessage(h.endErr), r.now().Sub(h.started))
	}
	for tty, refusal := range r.refusals {
		if identity, present := current[tty]; !present || identity != refusal.identity {
			delete(r.refusals, tty)
		}
	}
}

// endMessage words a holder's end for the status.
func endMessage(err error) string {
	if err != nil {
		return "read: " + err.Error()
	}
	return "read: the kernel ended the attachment while the tty stayed"
}

// nextRetry returns the wait until the earliest refusal's next
// attempt, and false when no refusal waits. No uevent announces that a
// backoff ran out, so the component sets a timer for it.
func (r *serioRegistry) nextRetry() (time.Duration, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var earliest time.Time
	for _, refusal := range r.refusals {
		if earliest.IsZero() || refusal.retryAt.Before(earliest) {
			earliest = refusal.retryAt
		}
	}
	if earliest.IsZero() {
		return 0, false
	}
	return max(earliest.Sub(r.now()), 0), true
}
