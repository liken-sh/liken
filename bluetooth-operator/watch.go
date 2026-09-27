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
	"strings"
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

	// shortWatch is the shortest time a watch may last and still count
	// as one that ran. A watch's life starts when the API server accepts
	// it, and a watch the server refused never ran, however long the
	// refusal took. A watch that closes sooner is a failure, whatever
	// it delivered, and a watcher that reopened it with no wait would
	// send thousands of requests each second while the fault lasts. A
	// watch that ran this long or longer resets the backoff, even when
	// it ended with an error. client-go's reflector makes the same test.
	shortWatch = time.Second
)

// errWatchExpired is the API server's 410 Gone: the version the watch
// asked for is older than any version the API server keeps. Only a new
// list gives a version to start from.
var errWatchExpired = errors.New("the watch's resource version expired")

// errWatchEvent marks a watch that ended on an event: an ERROR event
// other than a 410, or an event that does not decode. Either one can
// stand for changes the watcher never received, so the next step is a
// list. A read that fails on the connection is not one of these: the
// watcher lost only what the stream had not sent yet, and the next
// watch resumes at the last version it delivered.
var errWatchEvent = errors.New("the watch ended on an event")

// listThenWatch keeps one collection current until the context ends.
// It calls listed with the whole collection after each list, and
// changed with each ADDED, MODIFIED, or DELETED event after that.
//
// Each way a watch ends has its own next step:
//
//   - A stream that the API server closes resumes at the last version
//     it delivered, with no new list.
//   - The first 410 Gone lists again at once, because only a list gives
//     a version to start from. A 410 on the watch from that fresh list
//     waits out the backoff before the next list. Without the wait, a
//     server that answers 410 to every version makes a tight loop.
//   - An ERROR event, or an event that does not decode, waits out the
//     backoff and then lists again. Such an event can stand for changes
//     the watch never sent, and only a list recovers them.
//   - A watch the server refused, and a connection that failed in the
//     middle of a stream, wait out the backoff and then resume at the
//     last version the stream delivered. The stream lost nothing it had
//     sent, so no list is needed.
//
// A watch that closed within shortWatch is a failure too, and waits out
// the backoff before it resumes. A watch that ran for shortWatch or
// longer resets the backoff and clears relisted, so a 410 that ends it
// counts as a first 410.
func listThenWatch[T any](ctx context.Context, c *Client, collection, what string, listed func([]T), changed func(event string, object T)) {
	version := ""
	delay := watchRetry
	// relisted is true when version comes from a list made at once
	// after a 410, which is the one list a 410 may skip the wait for.
	relisted := false
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
		next, accepted, err := streamChanges(ctx, c, collection, version, changed)
		if ctx.Err() != nil {
			return
		}
		ran := !accepted.IsZero() && time.Since(accepted) >= shortWatch
		if ran {
			delay, relisted = watchRetry, false
		}
		switch {
		case errors.Is(err, errWatchExpired) && !relisted:
			version, relisted = "", true
			continue
		case errors.Is(err, errWatchExpired):
			fmt.Fprintf(os.Stderr, "watching %s: the version from a new list expired too\n", what)
			version = ""
		case errors.Is(err, errWatchEvent):
			fmt.Fprintf(os.Stderr, "watching %s: %v\n", what, err)
			version, relisted = "", false
		case err != nil:
			fmt.Fprintf(os.Stderr, "watching %s: %v\n", what, err)
			version, relisted = next, false
		case !ran:
			fmt.Fprintf(os.Stderr, "watching %s: the watch closed in under %s\n", what, shortWatch)
			version, relisted = next, false
		default:
			version, relisted = next, false
			continue
		}
		delay = pauseWatch(ctx, delay)
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
//
// The watch has a context of its own, and the function cancels it
// before it closes the body. A watch can fail while the API server
// still holds the stream open, after an error event or an object that
// does not decode. The server holds that stream until timeoutSeconds,
// and a close that read the rest of it first would wait minutes before
// the next list, and miss every change in that time. The cancel ends
// the request, so the body closes at once.
//
// The time it returns is when the API server accepted the watch, which
// is when the 200 arrived. It is the zero time for a watch the server
// refused. A watch's life counts from that moment, because a slow dial
// or a slow refusal is not a watch that ran.
func streamChanges[T any](ctx context.Context, c *Client, collection, version string, changed func(string, T)) (string, time.Time, error) {
	var accepted time.Time
	// A collection can carry a query of its own, such as a label
	// selector, so the watch's parameters join that query.
	separator := "?"
	if strings.Contains(collection, "?") {
		separator = "&"
	}
	path := fmt.Sprintf("%s%swatch=true&allowWatchBookmarks=true&resourceVersion=%s&timeoutSeconds=%d",
		collection, separator, url.QueryEscape(version), int(watchTimeout.Seconds()))
	ctx, cancel := context.WithCancel(ctx)
	body, err := c.Watch(ctx, path)
	if err != nil {
		cancel()
		return version, accepted, err
	}
	accepted = time.Now()
	defer func() {
		cancel()
		drain(body)
	}()

	events := json.NewDecoder(body)
	for {
		var event struct {
			Type   string          `json:"type"`
			Object json.RawMessage `json:"object"`
		}
		if err := events.Decode(&event); err != nil {
			if errors.Is(err, io.EOF) {
				return version, accepted, nil
			}
			var syntax *json.SyntaxError
			var mistyped *json.UnmarshalTypeError
			if errors.As(err, &syntax) || errors.As(err, &mistyped) {
				return version, accepted, fmt.Errorf("%w: %v", errWatchEvent, err)
			}
			return version, accepted, err
		}
		var meta struct {
			Code     int        `json:"code"`
			Metadata ObjectMeta `json:"metadata"`
		}
		if err := json.Unmarshal(event.Object, &meta); err != nil {
			return version, accepted, fmt.Errorf("%w: %v", errWatchEvent, err)
		}
		switch event.Type {
		case "ERROR":
			if meta.Code == 410 {
				return version, accepted, errWatchExpired
			}
			return version, accepted, fmt.Errorf("%w: %s", errWatchEvent, event.Object)
		case "BOOKMARK":
		default:
			var object T
			if err := json.Unmarshal(event.Object, &object); err != nil {
				return version, accepted, fmt.Errorf("%w: %v", errWatchEvent, err)
			}
			changed(event.Type, object)
		}
		version = meta.Metadata.ResourceVersion
	}
}
