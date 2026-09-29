package main

// The Deployment's operator releases its Lease only after the last
// write of the process that held it: a status write, a command to a
// receiver, or a message on the media bus. So each goroutine that a
// receiver unit or a session starts, and that can write, is counted in
// the group the Receiver loop's context carries. At a shutdown the loop
// stops every unit and then waits for the group, and serve reports
// whether it emptied in time (errStillWriting). A process whose writes
// did not stop leaves the Lease to expire instead of releasing it.

import (
	"context"
	"errors"
	"sync"
	"time"
)

// workKey is the context key of the group.
type workKey struct{}

// withWork answers a context that carries group, so every goroutine
// that goWork starts under it, or under a context made from it, is
// counted in group.
func withWork(ctx context.Context, group *sync.WaitGroup) context.Context {
	return context.WithValue(ctx, workKey{}, group)
}

// goWork runs work on its own goroutine, counted in the group ctx
// carries. A context with no group, such as a test's that runs one
// pass, counts nothing.
func goWork(ctx context.Context, work func()) {
	if group, ok := ctx.Value(workKey{}).(*sync.WaitGroup); ok {
		group.Go(work)
		return
	}
	go work()
}

// workStopWait bounds the wait for the counted goroutines at a
// shutdown. Each one ends on its context. A Receiver unit's wait after a
// 429 ends with its context too (withWaits), so the wait here is for a
// write already sent. Such a write ends within the shared client's
// request timeout, 30 seconds, which is longer than this wait: a write
// that the API server holds past it makes serve answer
// errStillWriting, and the process leaves its Lease to expire. It is a variable so a
// test holds it short.
var workStopWait = 5 * time.Second

// errStillWriting says that a goroutine that can write did not stop
// within workStopWait, so the process must not release its Lease.
var errStillWriting = errors.New("a receiver unit or a session did not stop in time, so the operator leaves its Lease to expire")

// awaitWork waits for group, and answers false when within ends first.
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
