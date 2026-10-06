package main

// Two kinds of work hold a lock across INDI waits: the work on an
// observatory's server, which the runners of several telescopes share
// (operator.siteLock), and the work on one INDI server's devices
// (indiServer.lock). A runner that waits for such a lock must still
// stop at its step's deadline, and a sync.Mutex cannot be abandoned.
// So the lock is a channel that holds one token, and a waiter selects
// on it and on its context.

import "context"

type lock chan struct{}

func newLock() lock { return make(lock, 1) }

// acquire takes the lock, or answers the cause when ctx ends first.
func (l lock) acquire(ctx context.Context) error {
	select {
	case l <- struct{}{}:
		return nil
	case <-ctx.Done():
		return context.Cause(ctx)
	}
}

// release gives the lock back to the next waiter.
func (l lock) release() { <-l }
