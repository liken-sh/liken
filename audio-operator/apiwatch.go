package main

// The objects a field selector takes, followed with a list and a watch
// instead of a read on a timer.
//
// The API follows two objects this way: the Secret
// audio-capture-server, which every capture container mounts, and the
// ConfigMap extension-apiserver-authentication, where the API server
// publishes the cluster's client certificate authority. The API server
// sends a change to either object as an event on the open watch, so
// the API acts on the change when it happens. An object that does not
// change costs one open connection and no reads.
//
// For those two, the list and the watch both carry the field selector
// metadata.name. RBAC authorizes a list or a watch against
// resourceNames only when the request selects that one name, so the
// selector is what lets the Role grant list and watch on one Secret
// and not on every Secret in the namespace.
//
// The operator follows its own Sinks and Sources the same way, with
// the field selector status.node, which the CRDs declare as a
// selectable field. The API server then sends a machine's operator the
// changes to that machine's resources alone, and not a change to every
// Sink in the cluster.
//
// The API follows the operator's own pods with the label selector
// app=audio-operator, and keeps them in memory, because every tap
// needs the pod on one node and a read per tap would load the API
// server. The watch hands each object to a keeper, which holds them.

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

// objectWatchTimeout asks the API server to end each watch. The client
// ends a watch that outlives it by a minute: a connection that died
// with no reset sends nothing, and the read on it would otherwise
// never return.
//
// objectWatchRetry is the first wait after a failure, and each failure
// in a row doubles it up to objectWatchRetryLimit. An API server that
// is down gets one list a minute from each watch, not one a second.
//
// objectWatchShortLife is the shortest life of a watch that counts as
// working. A watch that closes sooner is a failure whatever it
// delivered, because an API server that accepts each watch and closes
// it at once would otherwise get a new one in a tight loop.
const (
	objectWatchTimeout    = 290 * time.Second
	objectWatchRetry      = time.Second
	objectWatchRetryLimit = time.Minute
	objectWatchShortLife  = time.Second
)

// objectWatch follows the objects one selector takes. changed runs
// once for every list and once for every event that changes one of
// them, deleted included. A caller that reads the objects again itself
// sets changed alone, and the watch carries no part of an object but
// its resource version. A caller that holds the objects in memory sets
// keep, which takes the list's items and each event's object.
type objectWatch struct {
	client     *Client
	kind       string
	collection string
	// selector is the field selector, such as metadata.name=<name>,
	// or the label selector when labels is true.
	selector string
	labels   bool
	changed  func()
	keep     objectKeeper
	complain func(error)

	// restarted runs each time a watch opens after the first, and is
	// nil when nothing counts the reopens.
	restarted func()

	// The waits and the short life are fields so a test runs a retry
	// in a millisecond and chooses which watches count as short.
	retry, retryLimit, shortLife time.Duration

	// after is the wait, time.After when nil, a field so a test reads
	// each backoff without a clock.
	after func(time.Duration) <-chan time.Time
}

// followObject starts the watch with the production waits.
func followObject(ctx context.Context, client *Client, kind, collection, name string,
	changed func(), complain func(error)) {
	watch := &objectWatch{
		client: client, kind: kind, collection: collection, selector: "metadata.name=" + name,
		changed: changed, complain: complain,
		retry: objectWatchRetry, retryLimit: objectWatchRetryLimit, shortLife: objectWatchShortLife,
	}
	go watch.run(ctx)
}

// run lists, then watches from the list's version, until the context
// ends.
//
// A watch that the API server ends opens again from the version of the
// last event, so no change between the two watches is lost and no list
// is needed. A version the API server no longer keeps answers 410
// Gone, and the loop lists again at once: the list reads the present
// state, which covers every change the lost window held. A second 410
// on the watch that opens from that fresh list is a fault the list
// cannot cure, so the loop waits out the backoff before it lists
// again.
//
// Every other failure waits out the backoff, and each failure in a row
// doubles it. A watch that closed within shortLife of its open is a
// failure, even when it ended cleanly. A watch that lived longer
// resets the backoff, even when it ended on an error, and one that
// ended cleanly opens again at once.
func (w *objectWatch) run(ctx context.Context) {
	version := ""
	delay := w.retry
	opened := false
	relisted := false
	for ctx.Err() == nil {
		if version == "" {
			listed, err := w.list()
			if err != nil {
				w.complain(err)
				if !w.wait(ctx, delay) {
					return
				}
				delay = min(2*delay, w.retryLimit)
				continue
			}
			version = listed
		}
		if opened && w.restarted != nil {
			w.restarted()
		}
		opened = true
		started := time.Now()
		last, err := w.follow(ctx, version)
		lived := time.Since(started) >= w.shortLife
		if ctx.Err() != nil {
			return
		}
		version = last
		gone := errors.Is(err, ErrGone)
		if gone {
			version = ""
			if !relisted || lived {
				relisted = true
				continue
			}
		} else {
			relisted = false
		}
		if lived {
			delay = w.retry
			if err == nil {
				continue
			}
		}
		if err != nil {
			w.complain(err)
		}
		if !w.wait(ctx, delay) {
			return
		}
		delay = min(2*delay, w.retryLimit)
	}
}

// wait waits out one backoff, and answers false when the context ended
// first.
func (w *objectWatch) wait(ctx context.Context, delay time.Duration) bool {
	after := w.after
	if after == nil {
		after = time.After
	}
	select {
	case <-ctx.Done():
		return false
	case <-after(delay):
		return true
	}
}

// objectKeeper holds the objects a watch follows in memory. replace
// takes a list's items, which is the whole present state, and apply
// takes one event's type and object.
type objectKeeper interface {
	replace(items json.RawMessage) error
	apply(kind string, object json.RawMessage) error
}

// query is the query string that carries the selector.
func (w *objectWatch) query() string {
	if w.labels {
		return "labelSelector=" + url.QueryEscape(w.selector)
	}
	return "fieldSelector=" + url.QueryEscape(w.selector)
}

// notify runs changed, when the caller set it.
func (w *objectWatch) notify() {
	if w.changed != nil {
		w.changed()
	}
}

// list reads the collection's version, and runs changed: the list is
// the present state, and the object may have changed while no watch
// was open.
func (w *objectWatch) list() (string, error) {
	var list struct {
		Metadata struct {
			ResourceVersion string `json:"resourceVersion"`
		} `json:"metadata"`
		Items json.RawMessage `json:"items"`
	}
	if err := w.client.RequestJSON(http.MethodGet, w.collection+"?"+w.query(), nil, &list); err != nil {
		return "", fmt.Errorf("listing the %s %s: %w", w.kind, w.selector, err)
	}
	if w.keep != nil {
		if err := w.keep.replace(list.Items); err != nil {
			return "", fmt.Errorf("reading the list of the %s %s: %w", w.kind, w.selector, err)
		}
	}
	w.notify()
	return list.Metadata.ResourceVersion, nil
}

// follow holds one watch open from version, and answers the version of
// the last event it read, so the next watch starts there.
//
// A bookmark carries a newer version and no change, so it moves the
// version on and runs nothing. An ERROR event carries a Status: code
// 410 is ErrGone, and any other code is a failure with the API
// server's own message.
func (w *objectWatch) follow(ctx context.Context, version string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, objectWatchTimeout+time.Minute)
	defer cancel()
	body, err := w.client.Watch(ctx, fmt.Sprintf(
		"%s?%s&watch=true&allowWatchBookmarks=true&resourceVersion=%s&timeoutSeconds=%d",
		w.collection, w.query(), url.QueryEscape(version), int(objectWatchTimeout.Seconds())))
	if err != nil {
		return version, fmt.Errorf("watching the %s %s: %w", w.kind, w.selector, err)
	}
	// The cancel comes before the drain. A watch this side leaves early
	// is a stream the server still holds open, and a drain on it would
	// read until the server's own timeout.
	defer func() {
		cancel()
		drain(body)
	}()

	events := json.NewDecoder(body)
	for {
		var event struct {
			Type   string          `json:"type"`
			Raw    json.RawMessage `json:"object"`
			Object struct {
				Metadata struct {
					ResourceVersion string `json:"resourceVersion"`
				} `json:"metadata"`
				Code    int    `json:"code"`
				Message string `json:"message"`
			} `json:"-"`
		}
		if err := events.Decode(&event); err != nil {
			if err == io.EOF {
				return version, nil
			}
			return version, fmt.Errorf("watching the %s %s: %w", w.kind, w.selector, err)
		}
		// An event whose object does not decode, or carries no
		// version, leaves no version to open the next watch from. The
		// empty version sends the loop back to a list after the
		// backoff, so a watch never opens again at the same version to
		// read the same event.
		if err := json.Unmarshal(event.Raw, &event.Object); err != nil {
			return "", fmt.Errorf("reading an event on the %s %s: %w", w.kind, w.selector, err)
		}
		switch event.Type {
		case "ERROR":
			if event.Object.Code == http.StatusGone {
				return version, fmt.Errorf("%w: %s", ErrGone, event.Object.Message)
			}
			// Any other error event lists again after the backoff,
			// because the watch says nothing about the version it
			// stopped at.
			return "", fmt.Errorf("watching the %s %s: %d: %s",
				w.kind, w.selector, event.Object.Code, event.Object.Message)
		case "BOOKMARK":
			if event.Object.Metadata.ResourceVersion == "" {
				return "", fmt.Errorf("reading an event on the %s %s: a bookmark with no version", w.kind, w.selector)
			}
			version = event.Object.Metadata.ResourceVersion
		default:
			if event.Object.Metadata.ResourceVersion == "" {
				return "", fmt.Errorf("reading an event on the %s %s: a %s event with no version",
					w.kind, w.selector, event.Type)
			}
			version = event.Object.Metadata.ResourceVersion
			if w.keep != nil {
				// An object the keeper cannot read leaves the memory
				// short of it, so the empty version sends the loop
				// back to a list, which reads the whole state again.
				if err := w.keep.apply(event.Type, event.Raw); err != nil {
					return "", fmt.Errorf("reading an event on the %s %s: %w", w.kind, w.selector, err)
				}
			}
			w.notify()
		}
	}
}
