package main

// Some work holds a lock while it waits on INDI: work on an
// observatory's shared server, which the runners of several telescopes
// contend for (operator.siteLock), and work on the devices of one INDI
// server (indiServer.lock). A runner that waits for such a lock must
// still give up at its step's deadline, and sync.Mutex.Lock cannot be
// cancelled. So the lock is a channel with room for one token, and a
// waiter selects on the channel and on its context.

import "context"

type lock chan struct{}

func newLock() lock { return make(lock, 1) }

// acquire waits for the lock, or returns context.Cause(ctx) if ctx is
// done first.
func (l lock) acquire(ctx context.Context) error {
	select {
	case l <- struct{}{}:
		return nil
	case <-ctx.Done():
		return context.Cause(ctx)
	}
}

func (l lock) release() { <-l }
