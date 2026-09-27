package main

// Some watches carry nothing a pass uses but their arrival: the watch
// on Displays, the watch on Layouts, and the watch on this node's pods.
// Each event is one wake, and the pass that follows reads every object
// again, the same way every other wake in this operator works.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"time"
)

// watchWakes keeps one watch open on path until the context ends, and
// calls wake as each connection opens and on each event.
//
// The watch sends no resource version, so every connection opens with
// a replay of every object, and an event says nothing about whether the
// connection ran. Its lifetime does, counted from when the API server
// accepted it, so a slow dial or a slow refusal is not a watch that
// ran. A connection that lived for at least objectWatchRetry ran, and the next one opens at once, even
// when it ended with an error: a load balancer that resets a long
// connection costs one reopen, not a wait that climbs toward a minute.
// The next open is a wake, so a change made between the two is found
// by the pass that wake starts. A connection that lived less, refused
// or replayed or empty, is a failure, and the loop waits before the
// next one, longer after each failure in a row, so a server or a proxy
// that ends every watch at once gets a few requests and not a tight
// loop of them.
func watchWakes(ctx context.Context, c *Client, kind reconcileKind, what, path string, wake func(), readings *metrics) {
	delay := objectWatchRetry
	first := true
	for ctx.Err() == nil {
		if !first {
			readings.watchRestarted(kind)
		}
		first = false
		accepted, err := streamWakes(ctx, c, path, wake)
		if ctx.Err() != nil {
			return
		}
		if err != nil {
			fmt.Fprintf(os.Stderr, "watching %s: %v\n", what, err)
		}
		if ranFor(accepted, objectWatchRetry) {
			delay = objectWatchRetry
			continue
		}
		delay = pauseWatch(ctx, delay)
	}
}

// One watch connection. It opens with an ADDED event for every object
// that exists, and then delivers each change. It wakes the loop once as
// it opens and once on each event. The pass that the open's wake starts
// reads every object after the watch is live, so a change made between
// two connections is found by that read, and no event is lost. An event
// carries nothing the pass uses, so the watch needs no resource version
// to resume from. The answer carries when the API server accepted the
// watch, which is zero for a watch it refused.
func streamWakes(ctx context.Context, c *Client, path string, wake func()) (time.Time, error) {
	body, err := c.Watch(ctx, path)
	if err != nil {
		return time.Time{}, err
	}
	accepted := time.Now()
	defer drain(body)
	wake()

	events := json.NewDecoder(body)
	for {
		var event struct {
			Type string `json:"type"`
		}
		if err := events.Decode(&event); err != nil {
			if err == io.EOF {
				return accepted, nil
			}
			return accepted, err
		}
		wake()
	}
}
