package main

// The watch is an ordinary GET whose response never ends. The API
// server holds the connection open and writes one JSON event per
// change. The event carries no object to the loop, because every pass
// lists, so an event is only a wake.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"
)

// The clocks of the watch loop. A watch that closed sooner than
// watchShortLife after the server accepted it is a failure, whatever
// it delivered. A failure waits a backoff that starts at watchBackoffFirst and
// doubles to watchBackoffMax, and a watch that lived watchShortLife or
// longer starts the backoff again from the first delay. They are
// variables so a test holds them short.
var (
	watchShortLife    = time.Second
	watchBackoffFirst = time.Second
	watchBackoffMax   = 30 * time.Second
)

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

// watchCollection wakes the loop on every change in one collection. It
// keeps the three guards a watch loop written by hand needs:
//
//   - A watch that closes opens again from the last resourceVersion it
//     delivered, a bookmark's included, and lists nothing, because the
//     API server sends every change after that version.
//   - A 410 Gone, as a response or as an ERROR event, means the version
//     is too old. The loop lists at once, wakes the loop, and watches
//     from the list's version. When that watch also gets a 410, the
//     loop waits out the backoff before it lists again. Any other error,
//     a failed request, an ERROR event, or an event that does not
//     decode, waits out the backoff before the list, so a fault that
//     lasts makes no tight loop of lists.
//   - On any error the loop closes the stream at once and does not read
//     it to its end, because a server can hold it open for minutes.
//   - A watch that closed sooner than watchShortLife after the server
//     accepted it is a failure, whatever it delivered, so the backoff
//     applies. A watch
//     that lived that long resets the backoff, even when it ended with
//     an error, and a 410 that ends it counts as a first 410.
//
// Every time but the first, opening a watch calls restarted, which
// counts it in equipment_watch_restarts_total.
func watchCollection(ctx context.Context, client *Client, path, resourceVersion string, wake chan<- struct{}, restarted func(), list func() (string, error)) {
	watchCollectionBy(ctx, client, path, resourceVersion, wake, restarted, list, nil)
}

// watchKey answers the part of one object that a loop reads. An event
// that leaves it as it was wakes nothing. A nil key wakes the loop on
// every event.
type watchKey func(object json.RawMessage) string

// watchCollectionBy is watchCollection for a loop that reads only part
// of each object, such as a Receiver's spec but not its status. The
// memory of each object's key lasts for the whole watch, across the
// streams and the lists, because each list wakes the loop anyway.
func watchCollectionBy(ctx context.Context, client *Client, path, resourceVersion string, wake chan<- struct{}, restarted func(), list func() (string, error), key watchKey) {
	memory := newWatchMemory(key)
	backoff := watchBackoff{}
	// gone says the version the next watch opens from came from a list
	// made at once after a 410.
	gone := false
	first := true
	for ctx.Err() == nil {
		if !first {
			restarted()
		}
		first = false
		var ended watchEnd
		var lived time.Duration
		resourceVersion, ended, lived = memory.watch(ctx, client, path, resourceVersion, wake)
		if ctx.Err() != nil {
			return
		}
		if lived >= watchShortLife {
			// A watch that ran this long was a working watch, so a 410
			// that ends it is a first 410.
			backoff.reset()
			gone = false
		} else if ended == watchClosed {
			ended = watchShort
		}
		relistAtOnce := ended == watchGone && !gone
		gone = ended == watchGone
		switch {
		case ended == watchClosed:
			continue
		case relistAtOnce:
		default:
			select {
			case <-ctx.Done():
				return
			case <-time.After(backoff.next()):
			}
		}
		if ended == watchShort {
			continue
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

// watchEnd is how one watch ended.
type watchEnd int

const (
	// watchClosed is a stream that closed with no error.
	watchClosed watchEnd = iota
	// watchShort is a stream that closed with no error sooner than
	// watchShortLife after the server accepted it.
	watchShort
	// watchGone is a 410: the version the watch asked for is too old.
	watchGone
	// watchFailed is every other error: a failed request, a response
	// other than 200 or 410, an ERROR event, or an event that does not
	// decode.
	watchFailed
)

// watchBackoff is the wait before the next try after a failure.
type watchBackoff struct {
	delay time.Duration
}

// next answers the wait for this failure, and doubles the one after it.
func (b *watchBackoff) next() time.Duration {
	wait := max(b.delay, watchBackoffFirst)
	b.delay = min(wait*2, watchBackoffMax)
	return wait
}

// reset starts the backoff again from watchBackoffFirst.
func (b *watchBackoff) reset() {
	b.delay = 0
}

// watch opens one watch from resourceVersion and reads it until it
// ends. It answers the version the next watch opens from, how this one
// ended, and how long it lived. The life starts when the server
// accepted the watch with a 200, not when the request began, so a slow
// dial or a slow refusal is no watch that ran and resets no backoff.
// The request carries the context, because a read of a stream that
// never ends blocks until the far end writes, and closing the body from
// elsewhere waits on that same read.
func (m *watchMemory) watch(ctx context.Context, client *Client, path, resourceVersion string, wake chan<- struct{}) (string, watchEnd, time.Duration) {
	resp, err := WatchCollection(ctx, client, path, resourceVersion)
	if err != nil {
		return resourceVersion, watchFailed, 0
	}
	// The body closes at once and is never drained. After an error the
	// server can hold the stream open for minutes, and a loop that read
	// it to its end would lose every change in that time.
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusOK:
		accepted := time.Now()
		version, ended := m.readEnd(resp, resourceVersion, wake)
		return version, ended, time.Since(accepted)
	case http.StatusGone:
		return resourceVersion, watchGone, 0
	default:
		return resourceVersion, watchFailed, 0
	}
}

// readWatchStream reads one connection's events and wakes the loop on
// each one. The returned version is where the next watch resumes.
func readWatchStream(resp *http.Response, resourceVersion string, wake chan<- struct{}) string {
	return newWatchMemory(nil).read(resp, resourceVersion, wake)
}

// watchMemory holds the key each object last showed on one watch.
type watchMemory struct {
	key  watchKey
	seen map[string]string
}

func newWatchMemory(key watchKey) *watchMemory {
	return &watchMemory{key: key, seen: map[string]string{}}
}

// read reads one connection's events. The returned version is where the
// next watch resumes.
func (m *watchMemory) read(resp *http.Response, resourceVersion string, wake chan<- struct{}) string {
	version, _ := m.readEnd(resp, resourceVersion, wake)
	return version
}

// readEnd reads one connection's events, and answers the version the
// next watch resumes from and how the stream ended.
func (m *watchMemory) readEnd(resp *http.Response, resourceVersion string, wake chan<- struct{}) (string, watchEnd) {
	decoder := json.NewDecoder(resp.Body)
	for {
		var event struct {
			Type   string          `json:"type"`
			Object json.RawMessage `json:"object"`
		}
		if err := decoder.Decode(&event); err != nil {
			if errors.Is(err, io.EOF) {
				return resourceVersion, watchClosed
			}
			return resourceVersion, watchFailed
		}
		if event.Type == "ERROR" {
			var status struct {
				Code int `json:"code"`
			}
			_ = json.Unmarshal(event.Object, &status)
			if status.Code == http.StatusGone {
				return resourceVersion, watchGone
			}
			return resourceVersion, watchFailed
		}
		var object struct {
			Metadata ObjectMeta `json:"metadata"`
		}
		if err := json.Unmarshal(event.Object, &object); err != nil {
			return resourceVersion, watchFailed
		}
		if object.Metadata.ResourceVersion != "" {
			resourceVersion = object.Metadata.ResourceVersion
		}
		if event.Type == "BOOKMARK" {
			continue
		}
		if m.moved(event.Type, object.Metadata.Name, event.Object) {
			poke(wake)
		}
	}
}

// moved answers whether one event changes what the loop reads: any
// event with no key, an object the watch has not seen, a deleted
// object, and an object whose key differs from the one it last showed.
func (m *watchMemory) moved(kind, name string, object json.RawMessage) bool {
	if m.key == nil {
		return true
	}
	if kind == "DELETED" {
		delete(m.seen, name)
		return true
	}
	now := m.key(object)
	before, held := m.seen[name]
	m.seen[name] = now
	return !held || before != now
}
