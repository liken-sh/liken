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
// A connection that ran, which is one that delivered an event or lived
// for at least objectWatchRetry, is followed at once by the next. The
// next one's open is a wake, so a change made between the two is found
// by the pass that wake starts. A connection that the API server
// refused, or closed in under objectWatchRetry with no event, is a
// failure, and the loop waits before the next one, longer after each
// failure in a row, so a server or a proxy that ends every watch at
// once gets a few requests and not a tight loop of them.
func watchWakes(ctx context.Context, c *Client, kind reconcileKind, what, path string, wake func(), readings *metrics) {
	delay := objectWatchRetry
	first := true
	for ctx.Err() == nil {
		if !first {
			readings.watchRestarted(kind)
		}
		first = false
		opened := time.Now()
		delivered, err := streamWakes(ctx, c, path, wake)
		switch {
		case ctx.Err() != nil:
			return
		case err != nil:
			fmt.Fprintf(os.Stderr, "watching %s: %v\n", what, err)
			delay = pauseWatch(ctx, delay)
		case !delivered && time.Since(opened) < objectWatchRetry:
			delay = pauseWatch(ctx, delay)
		default:
			delay = objectWatchRetry
		}
	}
}

// One watch connection. It starts at the present and wakes the loop
// once as it opens. The pass that the wake starts reads every object
// after the watch is live, so a change made between two connections is
// found by that read, and no event is lost. An event carries nothing
// the pass uses, so the watch needs no resource version to resume from.
// The answer says whether the connection delivered any event.
func streamWakes(ctx context.Context, c *Client, path string, wake func()) (bool, error) {
	body, err := c.Watch(ctx, path)
	if err != nil {
		return false, err
	}
	defer drain(body)
	wake()

	delivered := false
	events := json.NewDecoder(body)
	for {
		var event struct {
			Type string `json:"type"`
		}
		if err := events.Decode(&event); err != nil {
			if err == io.EOF {
				return delivered, nil
			}
			return delivered, err
		}
		delivered = true
		wake()
	}
}
