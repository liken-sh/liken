package main

// This file holds the recovery every hand-written watch here shares:
// the watches in watch.go that wake the reconcile loop, and the watch
// on one named object in namedwatch.go. A watch is an ordinary GET with
// watch=true whose response does not end: the API server holds the
// connection open and writes one JSON event per change.
//
// The loop keeps three guards, and each one prevents a request loop
// against the API server:
//
//   - A watch that closes opens again from the last resourceVersion it
//     delivered, and bookmarks move that version while nothing changes.
//     A close is routine, and a list after each one would read the
//     whole collection every few minutes for nothing.
//   - A 410 Gone lists again at once, one time. When the watch from
//     that fresh list also answers 410, the loop waits out the backoff
//     before the next list. Any other error event, and an event that
//     does not decode, waits out the backoff before the list, because
//     the same fault answers the next attempt the same way.
//   - A watch that closed less than a second after it opened is a
//     failure, whatever it delivered, so the backoff applies. A watch
//     that ran a second or longer resets the backoff, even when it
//     ended with an error, because the next failure is a new fault.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

// A failed watch waits before the next attempt. The wait starts here
// and doubles to the ceiling, so a fault that lasts costs one request a
// minute and not several a second. They are variables so a test drives
// the collection watches in milliseconds.
var (
	watchBackoffStart = time.Second
	watchBackoffMax   = time.Minute
)

// watchMinLife is the shortest watch that counts as a watch that ran.
// A watch that closes sooner is a failure, whatever it delivered, and
// the loop waits before it opens the next one. It is a variable for the
// same reason as the backoff.
var watchMinLife = time.Second

// watchEnd is how one watch ended. version is the last resourceVersion
// the watch delivered, or the version it opened from. opened says the
// API server answered 200 or 410. gone says the API server no longer
// holds the version. failed says the stream carried an error event or
// an event that did not decode. reason is the words for a watch that
// did not open or that failed.
type watchEnd struct {
	version string
	opened  bool
	gone    bool
	failed  bool
	reason  string
}

// watchLoop runs one watch and every watch after it. list reads the
// whole state and answers the version to watch from. open runs one
// watch to its end, and calls live when the API server accepts it.
// restarted, when set, runs before each watch after the first.
//
// The clock, the wait, and the report are fields, so a test drives the
// loop with a script and a clock of its own.
type watchLoop struct {
	subject   string
	list      func() (string, error)
	open      func(ctx context.Context, version string, live func()) watchEnd
	restarted func()

	backoffStart time.Duration
	backoffMax   time.Duration
	minLife      time.Duration

	now    func() time.Time
	pause  func(ctx context.Context, wait time.Duration) bool
	report func(line string)
}

// run follows the watch until ctx ends. A version of "" lists first,
// because a watch with no version replays every object before the
// changes, which costs as much as a list and delivers it as events.
//
// One line reports each change of state: the first failure of a
// fault, and the return after it. An attempt that fails the same way
// as the one before it reports nothing.
func (l *watchLoop) run(ctx context.Context, version string) {
	wait := l.backoffStart
	fault := ""
	backoff := func(reason string) bool {
		if reason != fault {
			l.report(fmt.Sprintf("watching %s: %s", l.subject, reason))
			fault = reason
		}
		if !l.pause(ctx, wait) {
			return false
		}
		wait = min(wait*2, l.backoffMax)
		return true
	}
	live := func() {
		if fault != "" {
			l.report(fmt.Sprintf("watching %s again", l.subject))
			fault = ""
		}
	}

	relist := version == ""
	// relistedForGone is true while the version came from a list that
	// a 410 asked for, so a second 410 in a row waits.
	relistedForGone := false
	first := true
	for ctx.Err() == nil {
		if relist {
			listed, err := l.list()
			if err != nil {
				if !backoff(err.Error()) {
					return
				}
				continue
			}
			version, relist = listed, false
		}
		if !first && l.restarted != nil {
			l.restarted()
		}
		first = false

		began := l.now()
		end := l.open(ctx, version, live)
		version = end.version
		if ctx.Err() != nil {
			return
		}
		ran := l.now().Sub(began) >= l.minLife
		if ran {
			wait = l.backoffStart
		}

		if end.gone {
			relist = true
			if !relistedForGone {
				relistedForGone = true
				continue
			}
			if !backoff("the API server no longer holds the version the watch asked for") {
				return
			}
			continue
		}
		relistedForGone = false

		switch {
		case !end.opened:
			// A refused watch lists nothing: the version it asked for is
			// still good, and a 410 says when it is not.
			if !backoff(end.reason) {
				return
			}
		case end.failed:
			relist = true
			if !backoff(end.reason) {
				return
			}
		case !ran:
			if !backoff("the watch closed less than a second after it opened") {
				return
			}
		}
	}
}

// openWatch runs one watch on path from version to its end. path holds
// the watch=true query and any selector; the version goes last. each
// takes every event other than an ERROR, and an error from it counts as
// an event that did not decode.
func openWatch(ctx context.Context, c *Client, path, version string, live func(),
	each func(eventType string, object []byte) error) watchEnd {
	resp, err := c.Do(http.MethodGet, path+"&resourceVersion="+url.QueryEscape(version), nil)
	if err != nil {
		return watchEnd{version: version, reason: err.Error()}
	}
	// The body is closed, not drained. A stream that this loop leaves
	// after an ERROR event can stay open on the server side, and a drain
	// would wait on it forever.
	defer resp.Body.Close()
	stop := closeOnCancel(ctx, resp.Body)
	defer stop()

	// A 410 on the request itself is the same answer as a 410 in an
	// ERROR event. A Role that does not grant the watch answers 403,
	// and the reason reaches the loop's one line.
	if resp.StatusCode == http.StatusGone {
		return watchEnd{version: version, opened: true, gone: true}
	}
	if resp.StatusCode != http.StatusOK {
		message, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return watchEnd{version: version, reason: fmt.Sprintf("%s: %s", resp.Status, message)}
	}
	live()
	return readWatchEvents(resp.Body, version, each)
}

// readWatchEvents reads one stream to its end and answers how it ended.
// The version moves with each event that carries one, bookmarks
// included, so the next watch resumes after the last event delivered.
//
// An event that does not decode ends the stream as a failure. The loop
// then lists after the backoff. A resume from the same version would
// read the same event again, and the watch would not move past it.
func readWatchEvents(body io.Reader, version string, each func(eventType string, object []byte) error) watchEnd {
	decoder := json.NewDecoder(body)
	for {
		var event struct {
			Type   string          `json:"type"`
			Object json.RawMessage `json:"object"`
		}
		if err := decoder.Decode(&event); err != nil {
			var syntax *json.SyntaxError
			var mistyped *json.UnmarshalTypeError
			if errors.As(err, &syntax) || errors.As(err, &mistyped) {
				return watchEnd{version: version, opened: true, failed: true,
					reason: "an event did not decode: " + err.Error()}
			}
			// io.EOF, or the connection ending under the read: a close.
			return watchEnd{version: version, opened: true}
		}
		if event.Type == "ERROR" {
			var status struct {
				Code    int    `json:"code"`
				Message string `json:"message"`
			}
			_ = json.Unmarshal(event.Object, &status)
			if status.Code == http.StatusGone {
				return watchEnd{version: version, opened: true, gone: true}
			}
			return watchEnd{version: version, opened: true, failed: true,
				reason: fmt.Sprintf("the API server sent an error: %d %s", status.Code, status.Message)}
		}
		var meta struct {
			Metadata ObjectMeta `json:"metadata"`
		}
		if err := json.Unmarshal(event.Object, &meta); err != nil {
			return watchEnd{version: version, opened: true, failed: true,
				reason: "an event did not decode: " + err.Error()}
		}
		if err := each(event.Type, event.Object); err != nil {
			return watchEnd{version: version, opened: true, failed: true,
				reason: "an event did not decode: " + err.Error()}
		}
		if meta.Metadata.ResourceVersion != "" {
			version = meta.Metadata.ResourceVersion
		}
	}
}

// closeOnCancel closes the body when the context ends, because the
// client's Do carries no context and the decoder would otherwise
// block on a stream nobody reads.
func closeOnCancel(ctx context.Context, body io.Closer) func() {
	done := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			_ = body.Close()
		case <-done:
		}
	}()
	return func() { close(done) }
}

// waiting pauses for the wait and answers false when the context
// ended first, which is how each watch goroutine stops.
func waiting(ctx context.Context, pause time.Duration) bool {
	timer := time.NewTimer(pause)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
