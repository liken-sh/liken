package main

// This file holds the watch on one named object, such as a CA
// ConfigMap or the serving Secret. The watches in watch.go follow a
// whole collection and only wake the reconcile loop; this one follows
// a single object and hands each version of it to its owner, so a
// rotated certificate reaches the next TLS handshake when the API
// server writes it.
//
// The watch opens the collection with the field selector
// metadata.name=<name>. The API server authorizes that request against
// a Role's resourceNames, because it reads the name from the selector,
// so a Role that names one object grants its watch. A GET on the
// object's own path with watch=true is not a watch: the API server
// answers it as a plain get and closes the stream.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"time"
)

// A watch that the API server refuses, and a read that fails, wait
// before the next attempt. The wait starts here and doubles to the
// ceiling, so a fault that lasts costs one request a minute rather
// than several a second. A watch that opens resets the wait, because
// the next failure is a new fault and not the same one.
const (
	watchBackoffStart = time.Second
	watchBackoffMax   = time.Minute
)

// namedWatch follows one object in one namespace. changed receives
// the object's JSON on each ADDED and MODIFIED event and on each read
// that finds the object. removed runs on a DELETED event and on a read
// that answers 404. An error from changed is reported, and the watch
// continues, because the next version of the object can be valid.
type namedWatch struct {
	client    *Client
	namespace string
	resource  string
	name      string

	changed func(object json.RawMessage) error
	removed func()

	// How long an ended stream waits before the next watch opens. It
	// is a field so a test drives a reconnect in milliseconds.
	retry time.Duration

	// The bounds of the wait after a refused watch or a failed read.
	// They are fields for the same reason.
	backoffStart time.Duration
	backoffMax   time.Duration

	// pause is the wait itself, and report is where a change of state
	// goes. Both are fields so a test drives the loop with no clock of
	// its own and reads what the loop said.
	pause  func(ctx context.Context, wait time.Duration) bool
	report func(line string)
}

func newNamedWatch(client *Client, namespace, resource, name string,
	changed func(json.RawMessage) error, removed func()) *namedWatch {
	return &namedWatch{
		client:       client,
		namespace:    namespace,
		resource:     resource,
		name:         name,
		changed:      changed,
		removed:      removed,
		retry:        watchRetryPause,
		backoffStart: watchBackoffStart,
		backoffMax:   watchBackoffMax,
		pause:        waiting,
		report:       func(line string) { fmt.Fprintln(os.Stderr, line) },
	}
}

// subject names the object in every line and error, for example
// "configmap liken-system/media-api-ca".
func (w *namedWatch) subject() string {
	kind := w.resource
	if len(kind) > 0 && kind[len(kind)-1] == 's' {
		kind = kind[:len(kind)-1]
	}
	return kind + " " + w.namespace + "/" + w.name
}

func (w *namedWatch) collectionPath() string {
	return podPrefix + w.namespace + "/" + w.resource
}

// read gets the object once and hands it to its owner. It answers the
// resourceVersion a watch resumes from. An absent object is not an
// error: removed runs, and the empty version opens a watch that starts
// at the current state, so a later create arrives as an ADDED event.
func (w *namedWatch) read() (string, error) {
	var object json.RawMessage
	err := w.client.RequestJSON(http.MethodGet, w.collectionPath()+"/"+url.PathEscape(w.name), nil, &object)
	if errors.Is(err, ErrNotFound) {
		w.removed()
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("reading %s: %w", w.subject(), err)
	}
	version := resourceVersionOf(object)
	if err := w.changed(object); err != nil {
		return version, fmt.Errorf("reading %s: %w", w.subject(), err)
	}
	return version, nil
}

// follow is the watch loop. It streams from the last resourceVersion,
// and when a stream ends it opens the next one from the version of the
// last event. When the API server no longer holds that version, a 410
// Gone, the loop reads the object again for a current version first.
// The read also delivers a change or a delete that the lost window
// held.
//
// One line reports each change of state: the first refusal of a
// fault, and the return after it. An attempt that fails the same way
// as the one before it reports nothing.
func (w *namedWatch) follow(ctx context.Context, resourceVersion string) {
	wait := w.backoffStart
	fault := ""
	stale := false
	failed := func(reason string) bool {
		if reason != fault {
			w.report(fmt.Sprintf("watching %s: %s", w.subject(), reason))
			fault = reason
		}
		if !w.pause(ctx, wait) {
			return false
		}
		wait = min(wait*2, w.backoffMax)
		return true
	}
	for ctx.Err() == nil {
		if stale {
			version, err := w.read()
			if err != nil {
				if !failed(err.Error()) {
					return
				}
				continue
			}
			resourceVersion, stale = version, false
		}
		version, opened, gone, reason := w.stream(ctx, resourceVersion)
		resourceVersion = version
		if gone {
			stale = true
			if !w.pause(ctx, w.retry) {
				return
			}
			continue
		}
		if !opened {
			if !failed(reason) {
				return
			}
			continue
		}
		if fault != "" {
			w.report(fmt.Sprintf("watching %s again", w.subject()))
			fault = ""
		}
		wait = w.backoffStart
		if !w.pause(ctx, w.retry) {
			return
		}
	}
}

// stream opens one watch and reads it to its end. It answers the
// version to resume from, whether the watch opened, whether the API
// server answered 410 Gone, and the reason a watch did not open. The
// caller owns the reporting, because only the caller knows whether the
// reason is the same one as last time.
func (w *namedWatch) stream(ctx context.Context, resourceVersion string) (string, bool, bool, string) {
	query := url.Values{
		"watch":               {"true"},
		"allowWatchBookmarks": {"true"},
		"fieldSelector":       {"metadata.name=" + w.name},
		"resourceVersion":     {resourceVersion},
	}
	resp, err := w.client.Do(http.MethodGet, w.collectionPath()+"?"+query.Encode(), nil)
	if err != nil {
		return resourceVersion, false, false, err.Error()
	}
	// The body is closed, not drained. A stream that this loop leaves
	// after an ERROR event can stay open on the server side, and a drain
	// would wait on it forever.
	defer resp.Body.Close()
	stop := closeOnCancel(ctx, resp.Body)
	defer stop()

	// A Role that does not name this object answers 403, and the reason
	// reaches the caller's one line. A 410 on the request itself is
	// the same answer as a 410 in an ERROR event.
	if resp.StatusCode == http.StatusGone {
		return resourceVersion, true, true, ""
	}
	if resp.StatusCode != http.StatusOK {
		message, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return resourceVersion, false, false, fmt.Sprintf("%s: %s", resp.Status, message)
	}
	version, gone := w.events(resp.Body, resourceVersion)
	return version, true, gone, ""
}

// events hands each event's object to the owner and answers the
// version of the last event, and whether the stream ended on a 410
// Gone. The API server sends a 410 as an ERROR event whose object is
// a Status; any other ERROR ends the stream, and the next watch
// resumes from the same version.
func (w *namedWatch) events(body io.Reader, resourceVersion string) (string, bool) {
	decoder := json.NewDecoder(body)
	for {
		var event struct {
			Type   string          `json:"type"`
			Object json.RawMessage `json:"object"`
		}
		if err := decoder.Decode(&event); err != nil {
			return resourceVersion, false
		}
		if event.Type == "ERROR" {
			var status struct {
				Code int `json:"code"`
			}
			_ = json.Unmarshal(event.Object, &status)
			return resourceVersion, status.Code == http.StatusGone
		}
		if version := resourceVersionOf(event.Object); version != "" {
			resourceVersion = version
		}
		switch event.Type {
		case "ADDED", "MODIFIED":
			if err := w.changed(event.Object); err != nil {
				w.report(fmt.Sprintf("reading %s: %v", w.subject(), err))
			}
		case "DELETED":
			w.removed()
		}
	}
}

func resourceVersionOf(object json.RawMessage) string {
	var meta struct {
		Metadata ObjectMeta `json:"metadata"`
	}
	_ = json.Unmarshal(object, &meta)
	return meta.Metadata.ResourceVersion
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
