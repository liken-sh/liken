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
//
// The read before the watch is a list under the same field selector,
// not a get, and the watch starts from the list's resourceVersion. An
// object's own resourceVersion is the revision of its last write. An
// object that has not changed in a while carries a revision older than
// the API server's watch window, so a watch from it answers 410 Gone
// at once, and a read that gets the object answers the same old
// revision again. The list's resourceVersion is the revision of the
// collection when the API server answered, so the watch starts inside
// the window. The list also answers a revision when the object is
// absent, so a later create arrives as an event.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"time"
)

// namedWatch follows one object in one namespace. changed receives
// the object's JSON on each ADDED and MODIFIED event and on each read
// that finds the object. removed runs on a DELETED event and on a read
// whose list is empty. An error from changed is reported, and the watch
// continues, because the next version of the object can be valid.
type namedWatch struct {
	client    *Client
	namespace string
	resource  string
	name      string

	changed func(object json.RawMessage) error
	removed func()

	// The bounds of the wait after a failed watch or read, and the
	// shortest watch that counts as one that ran. watchloop.go gives
	// the rules. They are fields so a test drives the loop in
	// milliseconds.
	backoffStart time.Duration
	backoffMax   time.Duration
	minLife      time.Duration

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
		backoffStart: watchBackoffStart,
		backoffMax:   watchBackoffMax,
		minLife:      watchMinLife,
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

// read lists the collection under the object's name once, hands the
// object to its owner, and answers the list's resourceVersion for the
// watch to start from. An absent object is not an error: removed runs,
// and the watch starts from the list's version all the same.
func (w *namedWatch) read() (string, error) {
	query := url.Values{"fieldSelector": {"metadata.name=" + w.name}}
	var list struct {
		Metadata ListMeta          `json:"metadata"`
		Items    []json.RawMessage `json:"items"`
	}
	if err := w.client.RequestJSON(http.MethodGet, w.collectionPath()+"?"+query.Encode(), nil, &list); err != nil {
		return "", fmt.Errorf("reading %s: %w", w.subject(), err)
	}
	version := list.Metadata.ResourceVersion
	if len(list.Items) == 0 {
		w.removed()
		return version, nil
	}
	if err := w.changed(list.Items[0]); err != nil {
		return version, fmt.Errorf("reading %s: %w", w.subject(), err)
	}
	return version, nil
}

// follow is the watch loop, from resourceVersion on. watchLoop in
// watchloop.go holds the recovery. A version of "" reads the object
// first. A 410 Gone reads it again, and that read also delivers a
// change or a delete that the lost window held.
func (w *namedWatch) follow(ctx context.Context, resourceVersion string) {
	query := url.Values{
		"watch":               {"true"},
		"allowWatchBookmarks": {"true"},
		"fieldSelector":       {"metadata.name=" + w.name},
	}
	path := w.collectionPath() + "?" + query.Encode()
	loop := watchLoop{
		subject: w.subject(),
		list:    w.read,
		open: func(ctx context.Context, version string, live func()) watchEnd {
			return openWatch(ctx, w.client, path, version, live, w.deliver)
		},
		backoffStart: w.backoffStart,
		backoffMax:   w.backoffMax,
		minLife:      w.minLife,
		now:          time.Now,
		pause:        w.pause,
		report:       w.report,
	}
	loop.run(ctx, resourceVersion)
}

// deliver hands one event's object to the owner. An object the owner
// cannot use is its report, not a fault of the watch, because the next
// version of the object can be valid. A bookmark carries no object.
func (w *namedWatch) deliver(eventType string, object []byte) error {
	switch eventType {
	case "ADDED", "MODIFIED":
		if err := w.changed(object); err != nil {
			w.report(fmt.Sprintf("reading %s: %v", w.subject(), err))
		}
	case "DELETED":
		w.removed()
	}
	return nil
}
