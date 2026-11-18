package election

import "time"

// These expose the state of Lead to the tests in package election_test.

func (l *Lead) Started() <-chan struct{} { return l.started }
func (l *Lead) LastRenewal() time.Time   { return l.lock.lastRenewal() }

// Halt cancels the election as end does, but skips clearIfHeld. It
// returns the channel that closes when client-go's elector returns,
// after its own release of the Lease.
func (l *Lead) Halt() <-chan struct{} {
	l.stepping.Store(true)
	l.cancel()
	return l.done
}

// ClearIfHeld lets a test run the Lead's own release of the Lease on
// its own, after the election has stopped.
var ClearIfHeld = (*Lead).clearIfHeld

// Guarded returns a Lead with the given state for the write guard to
// read, and no election running behind it.
func Guarded(leading, stepping bool, renewed time.Time) *Lead {
	l := &Lead{started: make(chan struct{}), lock: &renewalRecordingLock{renewed: renewed}}
	if leading {
		close(l.started)
	}
	l.stepping.Store(stepping)
	return l
}
