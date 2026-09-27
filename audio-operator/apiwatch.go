package main

// One named object, followed with a list and a watch instead of a read
// on a timer.
//
// The API follows two objects this way: the Secret
// audio-capture-server, which every capture container mounts, and the
// ConfigMap extension-apiserver-authentication, where the API server
// publishes the cluster's client certificate authority. The API server
// sends a change to either object as an event on the open watch, so
// the API acts on the change when it happens. An object that does not
// change costs one open connection and no reads.
//
// The list and the watch both carry the field selector metadata.name.
// RBAC authorizes a list or a watch against resourceNames only when
// the request selects that one name, so the selector is what lets the
// Role grant list and watch on one Secret and not on every Secret in
// the namespace.

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

// objectWatchTimeout asks the API server to end each watch, the same
// bound as the endpoint watches in sinks.go. The client ends a watch
// that outlives it by a minute: a connection that died with no reset
// sends nothing, and the read on it would otherwise never return.
//
// objectWatchRetry is the first wait after a failure, and each failure
// in a row doubles it up to objectWatchRetryLimit. An API server that
// is down gets one list a minute from this API, not one a second.
const (
	objectWatchTimeout    = 290 * time.Second
	objectWatchRetry      = time.Second
	objectWatchRetryLimit = time.Minute
)

// objectWatch follows one object. changed runs once for every list and
// once for every event that changes the object, deleted included. It
// reads the object again itself, so the watch carries no part of the
// object but its resource version.
type objectWatch struct {
	client     *Client
	kind       string
	collection string
	name       string
	changed    func()
	complain   func(error)

	// The two waits are fields so a test runs a retry in a
	// millisecond.
	retry, retryLimit time.Duration
}

// followObject starts the watch with the production waits.
func followObject(ctx context.Context, client *Client, kind, collection, name string,
	changed func(), complain func(error)) {
	watch := &objectWatch{
		client: client, kind: kind, collection: collection, name: name,
		changed: changed, complain: complain,
		retry: objectWatchRetry, retryLimit: objectWatchRetryLimit,
	}
	go watch.run(ctx)
}

// run lists, then watches from the list's version, until the context
// ends.
//
// A watch that the API server ends opens again from the version of the
// last event, so no change between the two watches is lost and no list
// is needed. A version the API server no longer keeps answers 410
// Gone, and only then does the loop list again: the list reads the
// present state, which covers every change the lost window held.
func (w *objectWatch) run(ctx context.Context) {
	version := ""
	delay := w.retry
	for ctx.Err() == nil {
		var err error
		if version == "" {
			version, err = w.list()
		}
		if err == nil {
			version, err = w.follow(ctx, version)
		}
		switch {
		case ctx.Err() != nil:
			return
		case errors.Is(err, ErrGone):
			version = ""
			continue
		case err != nil:
			w.complain(err)
		default:
			delay = w.retry
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(delay):
		}
		if err != nil {
			delay = min(2*delay, w.retryLimit)
		}
	}
}

// selector is the query that names the one object.
func (w *objectWatch) selector() string {
	return "fieldSelector=" + url.QueryEscape("metadata.name="+w.name)
}

// list reads the collection's version, and runs changed: the list is
// the present state, and the object may have changed while no watch
// was open.
func (w *objectWatch) list() (string, error) {
	var list struct {
		Metadata struct {
			ResourceVersion string `json:"resourceVersion"`
		} `json:"metadata"`
	}
	if err := w.client.RequestJSON(http.MethodGet, w.collection+"?"+w.selector(), nil, &list); err != nil {
		return "", fmt.Errorf("listing the %s %s: %w", w.kind, w.name, err)
	}
	w.changed()
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
		w.collection, w.selector(), url.QueryEscape(version), int(objectWatchTimeout.Seconds())))
	if err != nil {
		return version, fmt.Errorf("watching the %s %s: %w", w.kind, w.name, err)
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
			Type   string `json:"type"`
			Object struct {
				Metadata struct {
					ResourceVersion string `json:"resourceVersion"`
				} `json:"metadata"`
				Code    int    `json:"code"`
				Message string `json:"message"`
			} `json:"object"`
		}
		if err := events.Decode(&event); err != nil {
			if err == io.EOF {
				return version, nil
			}
			return version, fmt.Errorf("watching the %s %s: %w", w.kind, w.name, err)
		}
		switch event.Type {
		case "ERROR":
			if event.Object.Code == http.StatusGone {
				return version, fmt.Errorf("%w: %s", ErrGone, event.Object.Message)
			}
			return version, fmt.Errorf("watching the %s %s: %d: %s",
				w.kind, w.name, event.Object.Code, event.Object.Message)
		case "BOOKMARK":
			version = event.Object.Metadata.ResourceVersion
		default:
			version = event.Object.Metadata.ResourceVersion
			w.changed()
		}
	}
}
