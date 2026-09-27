package main

// The recovery every collection watch shares. A watch starts from a
// resourceVersion, and the API server sends every change after it, so
// the version is the whole resume point. Three rules decide what the
// watcher does when a watch ends:
//
//   - A watch that closes opens again from the last version it
//     delivered. Bookmarks move that version while nothing changes, so
//     the next watch starts close to the present, and no list is needed.
//   - A 410 Gone says the server no longer holds the version, so the
//     watcher lists the collection at once and watches from the list's
//     version. It does that once: when the watch from the fresh list
//     also gets a 410, it waits out the backoff first. Every other error
//     loses the resume point too, and it waits out the backoff before
//     the list, or a fault that lasts would make a tight list loop.
//   - A watch that closed less than a second after it opened is a
//     failure, whatever it delivered, and the backoff applies. A watch
//     that ran for a second or longer resets the backoff, even when it
//     ended on an error, and a 410 that ends it counts as a first 410.
//
// On any error the watcher closes the stream at once and never reads it
// to its end. A server can hold the stream open for minutes after an
// error event, and every change in that time would be lost.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"time"
)

// How one watch ended.
type watchOutcome int

const (
	// The stream ended or the connection dropped. The last delivered
	// version still names a point the server can resume from.
	watchClosed watchOutcome = iota
	// A 410 Gone, as a response or as an ERROR event.
	watchGone
	// Any other error: a refused watch, a failed request, an ERROR event
	// with another code, or an event whose object does not decode.
	watchFailed
)

// WatchMinLife is how long a watch must run to count as one that lived.
// It is a variable so a test can move it.
var watchMinLife = newPause(time.Second)

// WatchRetryCap is the longest the backoff grows to. An API server that
// stays away costs one attempt every thirty seconds.
const watchRetryCap = 30 * time.Second

// What the watcher does after one watch ended: how long it waits, and
// whether it lists the collection before the next watch.
type watchStep struct {
	wait   time.Duration
	relist bool
}

// The backoff state of one watcher: the wait the next failure costs,
// and whether the last watch opened from a list that a 410 caused.
type watchBackoff struct {
	delay        time.Duration
	relistedGone bool
}

// After decides the step after one watch that ran for lived and ended
// with outcome.
func (b *watchBackoff) after(outcome watchOutcome, lived time.Duration) watchStep {
	if b.delay == 0 || lived >= watchMinLife.get() {
		b.delay = watchRetryPause.get()
		b.relistedGone = false
	}
	if outcome == watchGone && !b.relistedGone {
		b.relistedGone = true
		return watchStep{relist: true}
	}
	if outcome != watchGone {
		b.relistedGone = false
	}
	if outcome == watchClosed && lived >= watchMinLife.get() {
		return watchStep{}
	}
	return watchStep{wait: b.grow(), relist: outcome != watchClosed}
}

// Grow returns the wait the current failure costs and doubles the next
// one, up to the cap.
func (b *watchBackoff) grow() time.Duration {
	if b.delay == 0 {
		b.delay = watchRetryPause.get()
	}
	wait := b.delay
	b.delay = min(b.delay*2, watchRetryCap)
	return wait
}

// One collection a watcher follows: the kind its restarts count under,
// the path its watch and list read, and the list that sets a new resume
// point.
type collectionWatch struct {
	kind string
	// The collection's path with its own query, ending in "?" or "&", so
	// the watch parameters follow it.
	path string
	// The plural the log names when a list fails.
	noun string
	list func(ctx context.Context, c *Client) (string, error)
}

// WatchCollection follows one collection for the life of the process,
// and wakes the loop on each change and after each list. A failed watch
// is never fatal: the backstop keeps the passes running while this loop
// is down.
func watchCollection(c *Client, w collectionWatch, resourceVersion string, wake chan<- struct{}, m *metrics) {
	var backoff watchBackoff
	for attempt := 0; ; attempt++ {
		if attempt > 0 {
			m.recordWatchRestart(w.kind)
		}
		opened := time.Now()
		outcome, delivered := openWatch(c, w.path, resourceVersion, wake)
		resourceVersion = delivered
		step := backoff.after(outcome, time.Since(opened))
		time.Sleep(step.wait)
		if !step.relist {
			continue
		}
		listed, err := w.list(watchContext(), c)
		if err != nil {
			// The watch after a failed list opens from the version the
			// watcher holds, and the backoff keeps a list that fails on
			// every turn, such as a collection nobody serves, from
			// spinning.
			fmt.Fprintf(os.Stderr, "listing %s to resume the watch: %v\n", w.noun, err)
			time.Sleep(backoff.grow())
			continue
		}
		resourceVersion = listed
		poke(wake)
	}
}

// OpenWatch runs one watch from a version, and returns how it ended and
// the last version it delivered.
func openWatch(c *Client, path, resourceVersion string, wake chan<- struct{}) (watchOutcome, string) {
	resp, err := c.Do(watchContext(), http.MethodGet,
		path+"watch=true&allowWatchBookmarks=true&resourceVersion="+resourceVersion, nil)
	if err != nil {
		return watchFailed, resourceVersion
	}
	// Only a stream that ended on its own is read to its end, so the
	// connection goes back to the pool. Every other answer is closed
	// unread.
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusOK:
		outcome, delivered := readWatchStream(resp, resourceVersion, wake)
		if outcome == watchClosed {
			drain(resp.Body)
		}
		return outcome, delivered
	case http.StatusGone:
		return watchGone, resourceVersion
	default:
		return watchFailed, resourceVersion
	}
}

// ReadWatchStream reads one connection's worth of events, and returns
// how the stream ended and the version the next watch resumes from.
func readWatchStream(resp *http.Response, resourceVersion string, wake chan<- struct{}) (watchOutcome, string) {
	decoder := json.NewDecoder(resp.Body)
	for {
		var event struct {
			Type   string `json:"type"`
			Object struct {
				Metadata ObjectMeta `json:"metadata"`
				// The code of the Status an ERROR event carries.
				Code int `json:"code"`
			} `json:"object"`
		}
		if err := decoder.Decode(&event); err != nil {
			var syntax *json.SyntaxError
			var mistyped *json.UnmarshalTypeError
			if errors.As(err, &syntax) || errors.As(err, &mistyped) {
				return watchFailed, resourceVersion
			}
			return watchClosed, resourceVersion
		}
		if event.Type == "ERROR" {
			if event.Object.Code == http.StatusGone {
				return watchGone, resourceVersion
			}
			return watchFailed, resourceVersion
		}
		if event.Object.Metadata.ResourceVersion != "" {
			resourceVersion = event.Object.Metadata.ResourceVersion
		}
		if event.Type == "BOOKMARK" {
			// A bookmark moves the resume point and reconciles
			// nothing, so it earns no wake.
			continue
		}
		poke(wake)
	}
}

// Poke never blocks, and the wake channel buffers exactly one. A wake
// already queued says everything a second one would say, because the
// pass that answers it reads the whole collection.
func poke(wake chan<- struct{}) {
	select {
	case wake <- struct{}{}:
	default:
	}
}
