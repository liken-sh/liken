package main

// Some objects this operator reads change rarely and matter the moment
// they change: a Secret that holds a certificate, the ConfigMap that
// holds the cluster's client authority, and display-api's capture
// sidecar pods. A read on a clock would find each change up to one
// period late and cost a request every period in between. A watch
// costs nothing while the objects stand, and delivers a change as it
// lands.

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

// The first wait after a failed listing or watch, and the longest
// that wait grows to. A failure here is an API server that is down or
// a grant that is missing, and neither one is fixed faster by a
// request every few seconds.
const (
	objectWatchRetry = time.Second
	objectWatchLimit = time.Minute
)

// watchNamed calls seen with the object each time it changes, and with
// nil when the object does not exist. It runs until the context ends.
// The field selector names the object, which is also how RBAC matches a
// list and a watch against a rule's resourceNames.
func watchNamed[T any](ctx context.Context, c *Client, collection, name string, what string, seen func(held *T)) {
	path := collection + "?fieldSelector=" + url.QueryEscape("metadata.name="+name)
	watchList(ctx, c, path, what,
		func(items []T) {
			if len(items) == 0 {
				seen(nil)
				return
			}
			seen(&items[0])
		},
		func(kind string, held T) {
			if kind == "DELETED" {
				seen(nil)
				return
			}
			seen(&held)
		})
}

// watchList keeps listed and changed current with the objects at path,
// until the context ends. listed gets every object a listing answers,
// and changed gets each ADDED, MODIFIED, or DELETED event after it, with
// its object. The path carries its own query, a selector at least.
//
// A listing gives the whole truth and a resource version, and the
// watch that follows starts at that version, so no change between the
// two is missed. A watch that the API server ends on its timeout, or
// that a reset connection ends, resumes at the last version it
// delivered, and bookmarks move that version while nothing changes. A
// version the API server no longer holds answers 410 Gone, as the
// response or as an ERROR event, and the loop lists again at once, and
// after a wait when the watch that opened from that fresh listing meets
// 410 again. Any other ERROR event lists again after a wait, and so
// does an event whose object does not decode, because the reopened
// watch would meet the same event at the same version.
func watchList[T any](ctx context.Context, c *Client, path, what string,
	listed func(items []T), changed func(kind string, held T)) {
	version := ""
	delay := objectWatchRetry
	// Whether the listing the loop holds was taken because of a 410.
	// A second 410 in a row names a version the API server just gave,
	// so a listing at once would only meet it again.
	afterGone := false
	for ctx.Err() == nil {
		if version == "" {
			items, listedAt, err := listAt[T](c, path)
			if err != nil {
				fmt.Fprintf(os.Stderr, "listing %s: %v\n", what, err)
				delay = pauseWatch(ctx, delay)
				continue
			}
			version = listedAt
			listed(items)
		}
		next, accepted, err := streamList(ctx, c, path, version, changed)
		if ctx.Err() != nil {
			return
		}
		if errors.Is(err, errWatchExpired) {
			version = ""
			if afterGone {
				delay = pauseWatch(ctx, delay)
				continue
			}
			// The listing runs at once, and the wait keeps what it has
			// grown to: only a watch that ran resets it.
			afterGone = true
			continue
		}
		afterGone = false
		if err != nil {
			fmt.Fprintf(os.Stderr, "watching %s: %v\n", what, err)
		}
		// An ERROR event, or an event whose object does not decode,
		// says the watch cannot go on from its version, so the loop
		// lists again, after a wait. Any other end, a
		// timeout or a reset connection, leaves the version good, and the
		// next watch resumes there with no listing.
		var ended *watchErrorEvent
		if errors.As(err, &ended) {
			version = ""
		} else {
			version = next
		}
		// The lifetime decides the wait, not what the watch delivered.
		// A watch that lived a second or more ran, so a load balancer
		// that resets a long connection costs one reopen and not a wait
		// that climbs toward a minute. The life counts from when the API
		// server accepted the watch, so a slow dial or a slow refusal is
		// a failure. A shorter one is a server or a
		// proxy that ends each watch at once, and the wait keeps the loop
		// from a tight loop of requests.
		if ranFor(accepted, objectWatchRetry) {
			delay = objectWatchRetry
			if version != "" {
				continue
			}
		}
		delay = pauseWatch(ctx, delay)
	}
}

// Wait out one failure, and answer the wait after the next one.
func pauseWatch(ctx context.Context, delay time.Duration) time.Duration {
	select {
	case <-ctx.Done():
	case <-time.After(delay):
	}
	return nextDialDelay(delay, objectWatchLimit)
}

// One listing: every object, and the version the watch starts at.
func listAt[T any](c *Client, path string) ([]T, string, error) {
	list, err := get[struct {
		Metadata struct {
			ResourceVersion string `json:"resourceVersion"`
		} `json:"metadata"`
		Items []T `json:"items"`
	}](c, path)
	if err != nil {
		return nil, "", err
	}
	return list.Items, list.Metadata.ResourceVersion, nil
}

// errWatchExpired is the API server's 410 Gone: the version the watch
// asked for is older than any it holds, and only a new listing gives
// a version to start from.
var errWatchExpired = fmt.Errorf("the watch's resource version expired")

// watchErrorEvent is an ERROR event other than 410 Gone. The API
// server ends the watch with it, and the error carries its Status
// object word for word.
type watchErrorEvent struct {
	status string
}

func (e *watchErrorEvent) Error() string {
	return "the watch ended with " + e.status
}

// undecodable is an event whose object this program cannot read. It
// counts as an ERROR event, and the error carries the decoder's text
// and the object word for word.
func undecodable(object json.RawMessage, err error) *watchErrorEvent {
	return &watchErrorEvent{status: fmt.Sprintf("an object that does not decode (%v): %s", err, object)}
}

// One watch connection. It answers the last version it delivered, so
// the next connection resumes there, and when the API server accepted
// the watch, which is zero for a watch it refused.
func streamList[T any](ctx context.Context, c *Client, path, version string, changed func(string, T)) (string, time.Time, error) {
	stream := fmt.Sprintf("%s&watch=true&allowWatchBookmarks=true&resourceVersion=%s&timeoutSeconds=%d",
		path, url.QueryEscape(version), int(displayWatchTimeout.Seconds()))
	// The connection has a context of its own. A watch that ends on an
	// event it cannot use returns with the stream still open, and the
	// API server holds it until its timeout, so the drain below would
	// wait out that timeout and every event in it would be lost. The
	// cancel ends the connection first, and it runs before the drain.
	watchCtx, cancel := context.WithCancel(ctx)
	body, err := c.Watch(watchCtx, stream)
	if errors.Is(err, ErrGone) {
		cancel()
		return version, time.Time{}, errWatchExpired
	}
	if err != nil {
		cancel()
		return version, time.Time{}, err
	}
	accepted := time.Now()
	defer drain(body)
	defer cancel()

	events := json.NewDecoder(body)
	for {
		var event struct {
			Type   string          `json:"type"`
			Object json.RawMessage `json:"object"`
		}
		if err := events.Decode(&event); err != nil {
			if err == io.EOF {
				return version, accepted, nil
			}
			return version, accepted, err
		}
		var meta struct {
			Code     int `json:"code"`
			Metadata struct {
				ResourceVersion string `json:"resourceVersion"`
			} `json:"metadata"`
		}
		if err := json.Unmarshal(event.Object, &meta); err != nil {
			return version, accepted, undecodable(event.Object, err)
		}
		switch event.Type {
		case "ERROR":
			if meta.Code == 410 {
				return version, accepted, errWatchExpired
			}
			return version, accepted, &watchErrorEvent{status: string(event.Object)}
		case "ADDED", "MODIFIED", "DELETED":
			var held T
			if err := json.Unmarshal(event.Object, &held); err != nil {
				return version, accepted, undecodable(event.Object, err)
			}
			changed(event.Type, held)
		}
		// An event with no version leaves the last one standing, so
		// the next watch still resumes and does not list again.
		if meta.Metadata.ResourceVersion != "" {
			version = meta.Metadata.ResourceVersion
		}
	}
}

// ranFor reports whether a watch the API server accepted at accepted
// has lived for at least life. A watch it never accepted never ran.
func ranFor(accepted time.Time, life time.Duration) bool {
	return !accepted.IsZero() && time.Since(accepted) >= life
}
