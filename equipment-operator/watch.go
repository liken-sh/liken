package main

// The watch is an ordinary GET whose response never ends. The API
// server holds the connection open and writes one JSON event per
// change. The event carries no object to the loop, because every pass
// re-lists, so an event is only a wake.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"time"
)

// How long a watch waits before it re-lists after a dropped stream.
var watchRetryPause = 2 * time.Second

// watchReceivers wakes the loop on every change to a Receiver.
func watchReceivers(ctx context.Context, client *Client, resourceVersion string, wake chan<- struct{}, readings *metrics) {
	watchCollection(ctx, client, receiversPath, resourceVersion, wake, readings.watchRestarted, func() (string, error) {
		list, err := ListReceivers(client)
		if err != nil {
			return "", err
		}
		return list.Metadata.ResourceVersion, nil
	})
}

// watchCollection wakes the loop on every change in one collection and
// resumes each stream from a resourceVersion, so no change is missed
// between reconnects. A dropped stream and a 410 Gone recover the same
// way: list the collection, wake the loop, and watch again from the
// list's own version. Every time but the first, opening that new watch
// calls restarted, which counts it in equipment_watch_restarts_total.
func watchCollection(ctx context.Context, client *Client, path, resourceVersion string, wake chan<- struct{}, restarted func(), list func() (string, error)) {
	first := true
	for ctx.Err() == nil {
		if !first {
			restarted()
		}
		first = false
		// The request carries the context, because a read of a stream that
		// never ends blocks until the far end writes, and closing the body
		// from elsewhere waits on that same read.
		resp, err := WatchCollection(ctx, client, path, resourceVersion)
		if err == nil && resp.StatusCode == http.StatusOK {
			resourceVersion = readWatchStream(resp, resourceVersion, wake)
		}
		if resp != nil {
			drain(resp.Body)
		}

		select {
		case <-ctx.Done():
			return
		case <-time.After(watchRetryPause):
		}
		listed, err := list()
		if err != nil {
			fmt.Fprintf(os.Stderr, "listing %s to resume the watch: %v\n", path, err)
			continue
		}
		resourceVersion = listed
		poke(wake)
	}
}

// readWatchStream reads one connection's events. The returned version
// is where the next watch resumes.
func readWatchStream(resp *http.Response, resourceVersion string, wake chan<- struct{}) string {
	decoder := json.NewDecoder(resp.Body)
	for {
		var event struct {
			Type   string `json:"type"`
			Object struct {
				Metadata ObjectMeta `json:"metadata"`
			} `json:"object"`
		}
		if err := decoder.Decode(&event); err != nil {
			return resourceVersion
		}
		if event.Type == "ERROR" {
			return resourceVersion
		}
		if event.Object.Metadata.ResourceVersion != "" {
			resourceVersion = event.Object.Metadata.ResourceVersion
		}
		if event.Type == "BOOKMARK" {
			continue
		}
		poke(wake)
	}
}
