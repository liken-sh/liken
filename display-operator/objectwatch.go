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
// response or as an ERROR event, and the loop lists again at once. Any
// other ERROR event lists again after a wait.
func watchList[T any](ctx context.Context, c *Client, path, what string,
	listed func(items []T), changed func(kind string, held T)) {
	version := ""
	delay := objectWatchRetry
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
		opened := time.Now()
		next, err := streamList(ctx, c, path, version, changed)
		if ctx.Err() != nil {
			return
		}
		if errors.Is(err, errWatchExpired) {
			version = ""
			delay = objectWatchRetry
			continue
		}
		if err != nil {
			fmt.Fprintf(os.Stderr, "watching %s: %v\n", what, err)
		}
		// An ERROR event says the watch cannot go on from its version,
		// so the loop lists again, after a wait. Any other end, a
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
		// that climbs toward a minute. A shorter one is a server or a
		// proxy that ends each watch at once, and the wait keeps the loop
		// from a tight loop of requests.
		if time.Since(opened) >= objectWatchRetry {
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

// One watch connection. It answers the last version it delivered, so
// the next connection resumes there.
func streamList[T any](ctx context.Context, c *Client, path, version string, changed func(string, T)) (string, error) {
	stream := fmt.Sprintf("%s&watch=true&allowWatchBookmarks=true&resourceVersion=%s&timeoutSeconds=%d",
		path, url.QueryEscape(version), int(displayWatchTimeout.Seconds()))
	body, err := c.Watch(ctx, stream)
	if errors.Is(err, ErrGone) {
		return version, errWatchExpired
	}
	if err != nil {
		return version, err
	}
	defer drain(body)

	events := json.NewDecoder(body)
	for {
		var event struct {
			Type   string          `json:"type"`
			Object json.RawMessage `json:"object"`
		}
		if err := events.Decode(&event); err != nil {
			if err == io.EOF {
				return version, nil
			}
			return version, err
		}
		var meta struct {
			Code     int `json:"code"`
			Metadata struct {
				ResourceVersion string `json:"resourceVersion"`
			} `json:"metadata"`
		}
		if err := json.Unmarshal(event.Object, &meta); err != nil {
			return version, err
		}
		switch event.Type {
		case "ERROR":
			if meta.Code == 410 {
				return version, errWatchExpired
			}
			return version, &watchErrorEvent{status: string(event.Object)}
		case "ADDED", "MODIFIED", "DELETED":
			var held T
			if err := json.Unmarshal(event.Object, &held); err != nil {
				return version, err
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
