package main

import (
	"context"
	"fmt"
	"os"
	"time"
)

// The wait before a failed pass runs again starts at firstRetry and
// doubles after each failure, up to lastRetry.
const (
	firstRetry = time.Second
	lastRetry  = time.Minute
)

// runPasses runs pass for each wake until the context ends. The watch
// of the node's `liken.sh` ResourceSlice sends a wake for each change.
// A failed pass runs again after a
// backoff, which limits how often a failing write reaches the API
// server. No timer runs a pass while passes succeed.
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
			fmt.Fprintf(os.Stderr, "capabilities: the pass ended with an error, and runs again in %s: %v\n", wait, err)
			retry = time.After(wait)
			wait = min(wait*2, lastRetry)
			continue
		}
		retry, wait = nil, firstRetry
	}
}

// wake sends on a channel with a buffer of one without blocking. A wake
// that arrives while another is waiting is dropped, so a burst of
// changes runs one pass.
func wake(wakes chan<- struct{}) {
	select {
	case wakes <- struct{}{}:
	default:
	}
}
