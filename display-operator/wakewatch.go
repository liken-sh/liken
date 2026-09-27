package main

// Some watches carry nothing a pass uses but their arrival: the watch
// on Displays, the watch on Layouts, and the watch on this node's pods.
// Each event is one wake, and the pass that follows reads every object
// again, the same way every other wake in this operator works.

import (
	"context"
	"encoding/json"
)

// watchWakes keeps one watch open on path until the context ends, and
// calls wake as each connection opens and on each event. It is
// watchList with no listing: the first watch opens from no version, and
// the API server's replay of every object is the baseline. Each watch
// after it resumes from the last version the stream delivered, so a
// reopen replays nothing, and a 410 opens from no version again.
//
// The open is a wake of its own. The pass it starts reads every object
// after the watch is live, so a change made between two connections is
// found by that read, and no event is lost. watchList holds the rules
// for when the next watch opens and how long the loop waits first.
func watchWakes(ctx context.Context, c *Client, kind reconcileKind, what, path string, wake func(), readings *metrics) {
	first := true
	watchList(ctx, c, path, what, nil,
		func() {
			if !first {
				// The last connection ended and this one opens in its
				// place, the one restart milestone 65 counts.
				readings.watchRestarted(kind)
			}
			first = false
			wake()
		},
		func(string, json.RawMessage) { wake() })
}
