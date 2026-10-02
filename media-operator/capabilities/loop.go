package main

// The loop that runs the pass. The watch of the `liken.sh` slice wakes
// it, and a pass that fails runs again after a backoff. The backoff is
// a clock, not a read of state: it bounds how often a failing write
// goes to the API server, and a wake from the watch runs the pass at
// once.

import (
	"context"
	"fmt"
	"os"
	"time"
)

// The bounds of the wait after a failed pass. A pass fails when the
// API server refuses or does not answer a write, so the wait grows to a
// minute while the API server is down.
const (
	firstRetry = time.Second
	lastRetry  = time.Minute
)

// runPasses runs pass once for each wake, and again after a failure,
// until the context ends.
func runPasses(ctx context.Context, wakes <-chan struct{}, pass func(context.Context) error) {
	wait := firstRetry
	var retry <-chan time.Time
	for {
		select {
		case <-ctx.Done():
			return
		case <-wakes:
		case <-retry:
		}
		if err := pass(ctx); err != nil {
			fmt.Fprintf(os.Stderr, "capabilities: the pass failed, and runs again in %s: %v\n", wait, err)
			retry = time.After(wait)
			wait = min(wait*2, lastRetry)
			continue
		}
		retry, wait = nil, firstRetry
	}
}

// wake sends on a channel with one slot and never blocks, so a burst of
// events makes one wake.
func wake(wakes chan<- struct{}) {
	select {
	case wakes <- struct{}{}:
	default:
	}
}
