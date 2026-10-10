package election

import "time"

// These give the tests in election_test the state they check.

func (l *Lead) Unelected() bool          { return l.unelected.Load() }
func (l *Lead) Acting() bool             { return l.acting() }
func (l *Lead) Started() <-chan struct{} { return l.started }
func (l *Lead) LastRenewal() time.Time   { return l.lock.lastRenewal() }

// Halt ends the election without a release, the way a test's cleanup
// leaves it, and answers the channel that closes when it has ended.
func (l *Lead) Halt() <-chan struct{} {
	l.stepping.Store(true)
	l.cancel()
	return l.done
}

// ClearIfHeld is the release that follows client-go's own.
var ClearIfHeld = (*Lead).clearIfHeld

// Guarded is a Lead in the state the write guard reads, with no
// election behind it.
func Guarded(leading, stepping bool, renewed time.Time) *Lead {
	l := &Lead{started: make(chan struct{}), lock: &renewalClock{renewed: renewed}}
	if leading {
		close(l.started)
	}
	l.stepping.Store(stepping)
	return l
}
