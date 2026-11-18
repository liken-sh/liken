package main

// The operator must not release its Lease while a write is in flight:
// a status write, a command to a receiver, or a message on the media
// bus. Such a write could reach the API server after another copy took
// the Lease. So every goroutine of a receiver unit or a session that
// can write is added to a WaitGroup in the context. On shutdown the
// Receiver loop stops the units and waits for the group. When the group
// does not empty in time, serve returns errStillWriting, and the Lease
// expires instead of being released.

import (
	"context"
	"errors"
	"sync"
	"time"
)

type workKey struct{}

// withWork returns a context with group. goWork adds every goroutine
// it starts under that context, or a context derived from it, to group.
func withWork(ctx context.Context, group *sync.WaitGroup) context.Context {
	return context.WithValue(ctx, workKey{}, group)
}

// goWork runs work on a goroutine in the group of ctx. When ctx has no
// group, as in a test that runs one pass, goWork still runs work.
func goWork(ctx context.Context, work func()) {
	if group, ok := ctx.Value(workKey{}).(*sync.WaitGroup); ok {
		group.Go(work)
		return
	}
	go work()
}

// workStopWait is how long shutdown waits for the goroutines of the
// group. Each one stops when its context ends, including a wait after a
// 429 response (withWaits). A request that was already sent can still
// take up to leaderRequestTimeout, which is longer than this wait, and
// serve then returns errStillWriting.
const workStopWait = 5 * time.Second

// errStillWriting means that a goroutine that can write did not stop
// within workStopWait, or that the Event recorder's last write did not
// return within events.ShutdownTimeout. The operator then exits without
// releasing its Lease.
var errStillWriting = errors.New("a write can still be in flight, so the operator leaves its Lease to expire")

// awaitWork waits for group and returns false when it is not empty
// after within.
func awaitWork(group *sync.WaitGroup, within time.Duration) bool {
	done := make(chan struct{})
	go func() {
		group.Wait()
		close(done)
	}()
	select {
	case <-done:
		return true
	case <-time.After(within):
		return false
	}
}
