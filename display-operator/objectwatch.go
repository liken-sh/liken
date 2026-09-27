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
	"strings"
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
		nil,
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
// its object. opened, when it is not nil, runs each time the API server
// accepts a watch.
//
// A nil listed takes no listing. The watch then opens from no version,
// and the API server sends every object as an ADDED event before the
// changes, so that replay is the baseline. A caller whose events only
// wake a pass that reads everything uses this form.
//
// A listing gives the whole truth and a resource version, and the
// watch that follows starts at that version, so no change between the
// two is missed. A watch that the API server ends on its timeout, or
// that a reset connection ends, resumes at the last version it
// delivered, and bookmarks move that version while nothing changes.
//
// A version the API server no longer holds answers 410 Gone, as the
// response or as an ERROR event, and the loop lists again at once. When
// the watch that opened from that fresh listing meets 410 again and did
// not run, the loop waits first. Any other ERROR event, an event whose
// object does not decode, and a line that is not JSON all end the watch
// at once, and the loop waits, then lists again, because a watch that
// resumed would meet the same event at the same version.
//
// A watch that ran, which is one that lived for objectWatchRetry or
// longer from when the API server accepted it, resets the wait, however
// it ended. A load balancer that resets a long connection then costs
// one reopen, not a wait that climbs toward a minute. Any other watch
// is a failure, and the wait grows, so a server or a proxy that ends
// every watch at once, or refuses it slowly, gets a few requests and
// not a tight loop of them.
func watchList[T any](ctx context.Context, c *Client, path, what string,
	listed func(items []T), opened func(), changed func(kind string, held T)) {
	version := ""
	delay := objectWatchRetry
	// Whether the baseline the loop holds was taken because of a 410.
	// A second 410 in a row from a watch that did not run names a
	// version the API server just gave, so a listing at once would only
	// meet it again.
	afterGone := false
	for ctx.Err() == nil {
		if version == "" && listed != nil {
			items, listedAt, err := listAt[T](c, path)
			if err != nil {
				fmt.Fprintf(os.Stderr, "listing %s: %v\n", what, err)
				delay = pauseWatch(ctx, delay)
				continue
			}
			version = listedAt
			listed(items)
		}
		next, accepted, err := streamList(ctx, c, path, version, opened, changed)
		if ctx.Err() != nil {
			return
		}
		ran := ranFor(accepted, objectWatchRetry)
		if ran {
			delay = objectWatchRetry
			afterGone = false
		}
		if errors.Is(err, errWatchExpired) {
			version = ""
			if afterGone {
				delay = pauseWatch(ctx, delay)
				continue
			}
			afterGone = true
			continue
		}
		afterGone = false
		if err != nil {
			fmt.Fprintf(os.Stderr, "watching %s: %v\n", what, err)
		}
		var ended *watchErrorEvent
		if errors.As(err, &ended) {
			version = ""
		} else {
			version = next
		}
		if ran && ended == nil {
			continue
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
	if len(object) == 0 {
		return &watchErrorEvent{status: fmt.Sprintf("a line that does not decode: %v", err)}
	}
	return &watchErrorEvent{status: fmt.Sprintf("an object that does not decode (%v): %s", err, object)}
}

// One watch connection. It answers the last version it delivered, so
// the next connection resumes there, and when the API server accepted
// the watch, which is zero for a watch it refused. A watch from no
// version sends no resourceVersion, and the API server replays every
// object first.
func streamList[T any](ctx context.Context, c *Client, path, version string,
	opened func(), changed func(string, T)) (string, time.Time, error) {
	separator := "?"
	if strings.Contains(path, "?") {
		separator = "&"
	}
	stream := fmt.Sprintf("%s%swatch=true&allowWatchBookmarks=true&timeoutSeconds=%d",
		path, separator, int(displayWatchTimeout.Seconds()))
	if version != "" {
		stream += "&resourceVersion=" + url.QueryEscape(version)
	}
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
	if opened != nil {
		opened()
	}

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
			// A line that is not JSON, or not an event, is the same
			// failure as an object that does not decode. Any other error
			// is the connection's own, and the version still stands.
			var syntax *json.SyntaxError
			var mistyped *json.UnmarshalTypeError
			if errors.As(err, &syntax) || errors.As(err, &mistyped) {
				return version, accepted, undecodable(nil, err)
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
