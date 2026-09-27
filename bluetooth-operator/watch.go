package main

// A watch keeps this operator's view of a collection current without a
// timer. The API server sends each change to the collection as it
// happens, and a watch with no change to send costs nothing.
//
// The order is list, then watch from the list's resourceVersion. The
// list gives the whole collection and the version it was read at. The
// watch starts at that version and not at the present, so the API
// server sends every change made after the list, including a change
// made between the two requests.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"time"
)

const (
	// watchRetry is the first wait after a failed list or watch, and
	// watchRetryLimit is the longest that wait grows to. A failure here
	// is an API server that is down or a grant that is missing, and a
	// request every second does not fix either of them faster.
	watchRetry      = time.Second
	watchRetryLimit = time.Minute

	// watchTimeout is how long the API server keeps one watch
	// connection open before it closes the stream. The next connection
	// starts at the last version the stream delivered, so no change is
	// lost. A watch with no end would keep one connection through every
	// network fault between here and the API server.
	watchTimeout = 290 * time.Second

	// shortWatch is the shortest time a watch with no events may last
	// and still count as a stream that ended normally. A server that
	// answers a watch and closes it at once is failing, and a watcher
	// that reopened such a stream with no wait would send thousands of
	// requests each second. client-go's reflector makes the same test.
	shortWatch = time.Second
)

// errWatchExpired is the API server's 410 Gone: the version the watch
// asked for is older than any version the API server keeps. Only a new
// list gives a version to start from.
var errWatchExpired = errors.New("the watch's resource version expired")

// errShortWatch is a watch that the API server closed within
// shortWatch and before it sent an event.
var errShortWatch = errors.New("the watch closed with no events in under a second")

// listThenWatch keeps one collection current until the context ends.
// It calls listed with the whole collection after each list, and
// changed with each ADDED, MODIFIED, or DELETED event after that.
//
// A stream that the API server closes at watchTimeout resumes at the
// last version it delivered, with no new list. A 410 Gone lists again
// at once. Any other failure lists again after a wait, because a
// failed watch may have lost events, and only a list recovers them. A
// stream that closed within shortWatch with no events is a failure.
func listThenWatch[T any](ctx context.Context, c *Client, collection, what string, listed func([]T), changed func(event string, object T)) {
	version := ""
	delay := watchRetry
	for ctx.Err() == nil {
		if version == "" {
			list, err := get[struct {
				Metadata ObjectMeta `json:"metadata"`
				Items    []T        `json:"items"`
			}](c, fromCache(collection))
			if err != nil {
				fmt.Fprintf(os.Stderr, "listing %s: %v\n", what, err)
				delay = pauseWatch(ctx, delay)
				continue
			}
			version = list.Metadata.ResourceVersion
			listed(list.Items)
		}
		next, err := streamChanges(ctx, c, collection, version, changed)
		switch {
		case ctx.Err() != nil:
			return
		case errors.Is(err, errWatchExpired):
			version = ""
			delay = watchRetry
		case err != nil:
			fmt.Fprintf(os.Stderr, "watching %s: %v\n", what, err)
			version = ""
			delay = pauseWatch(ctx, delay)
		default:
			version = next
			delay = watchRetry
		}
	}
}

// pauseWatch waits out one failure, and returns the wait for the next
// failure.
func pauseWatch(ctx context.Context, delay time.Duration) time.Duration {
	select {
	case <-ctx.Done():
	case <-time.After(delay):
	}
	return min(2*delay, watchRetryLimit)
}

// streamChanges reads one watch connection until the API server closes
// it. It returns the last version the stream delivered, so the next
// connection starts there.
//
// Bookmarks are on. A bookmark carries only a newer version, and it
// lets a quiet collection resume at a version the API server still
// keeps, instead of one old enough to answer 410 Gone.
func streamChanges[T any](ctx context.Context, c *Client, collection, version string, changed func(string, T)) (string, error) {
	path := fmt.Sprintf("%s?watch=true&allowWatchBookmarks=true&resourceVersion=%s&timeoutSeconds=%d",
		collection, url.QueryEscape(version), int(watchTimeout.Seconds()))
	body, err := c.Watch(ctx, path)
	if err != nil {
		return version, err
	}
	defer drain(body)

	opened := time.Now()
	delivered := false
	events := json.NewDecoder(body)
	for {
		var event struct {
			Type   string          `json:"type"`
			Object json.RawMessage `json:"object"`
		}
		if err := events.Decode(&event); err != nil {
			if errors.Is(err, io.EOF) {
				if !delivered && time.Since(opened) < shortWatch {
					return version, errShortWatch
				}
				return version, nil
			}
			return version, err
		}
		var meta struct {
			Code     int        `json:"code"`
			Metadata ObjectMeta `json:"metadata"`
		}
		if err := json.Unmarshal(event.Object, &meta); err != nil {
			return version, err
		}
		delivered = true
		switch event.Type {
		case "ERROR":
			if meta.Code == 410 {
				return version, errWatchExpired
			}
			return version, fmt.Errorf("the watch ended with %s", event.Object)
		case "BOOKMARK":
		default:
			var object T
			if err := json.Unmarshal(event.Object, &object); err != nil {
				return version, err
			}
			changed(event.Type, object)
		}
		version = meta.Metadata.ResourceVersion
	}
}
