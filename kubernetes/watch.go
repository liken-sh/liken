package kubernetes

// This file implements the watch protocol: how a controller receives
// changes the moment they happen, and how it recovers when the
// stream drops.

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/liken-sh/liken/machine"
)

// WatchMachines turns the API server's watch mechanism into a channel
// of fresh Machine objects. A watch is an ordinary GET request with
// ?watch=true. The response never ends, and each line of it is a
// JSON event, such as {"type": "MODIFIED", "object": {…}}, sent the
// moment the object changes. This is the mechanism that informers,
// kubectl get -w, and every controller's responsiveness are built on.
//
// fieldSelector limits the scope of the watch, and this is where the
// two operators differ. The machine operator passes
// metadata.name=<self>, because its own object is the only one whose
// changes concern it. The cluster operator passes "" and hears about
// the whole fleet, because the Cluster's status is derived from
// every Machine. The server filters the results either way; a
// selector is only a query parameter on the same request.
//
// resourceVersion tells the server where to resume, so no change is
// missed between reconnects. The loop follows three rules, the ones
// every hand-written watch loop in liken follows:
//
//   - The server ends watches on its own schedule, so a clean close
//     after a healthy watch is routine. The loop opens the next watch
//     at once from the last resourceVersion the stream delivered, and
//     does not list. allowWatchBookmarks asks the server for an
//     occasional BOOKMARK event: no object change, only "you are
//     current through version X". Without bookmarks a quiet watch
//     would keep an old version, and the next open would more likely
//     find it compacted away.
//   - A 410 Gone, as the response or as an ERROR event, means the
//     version was compacted away. The loop lists at once and watches
//     from the list's own resourceVersion, the current revision of the
//     whole collection. A single object's version does not work here:
//     it is the revision of that object's own last write, and on a
//     quiet object it can be old enough to be compacted away too. If
//     the watch from that fresh list also gets a 410, the loop waits
//     out the pause before it lists again, so a server that keeps
//     answering 410 does not get a tight list loop. Every other
//     failure, including an event whose object does not decode, also
//     waits out the pause and then lists. On any error the loop closes
//     the stream at once and does not read the rest of it: a server
//     can hold the stream open for minutes after an ERROR event, and
//     every change in that time would reach the caller late.
//   - A watch that closes less than a second after the server
//     accepted it is a failure, whatever it delivered, because a watch
//     opened with no version replays every object first, and a broken
//     stream can still deliver events. The life starts when the 200
//     arrives, not when the request began, so a slow dial or a slow
//     refusal never counts as a watch that ran. A watch that lived for
//     a second or longer
//     counts as healthy even when it ended with an error, so a 410
//     that ends it is a first 410 again. The pause is RetryPause's
//     five to seven and a half seconds every time, so no backoff
//     grows that a healthy watch would reset.
//
// The recovery list's items are delivered as events, so the caller's
// working copy is refreshed along the way.
func WatchMachines(c *Client, fieldSelector, resourceVersion string, events chan<- *machine.Machine, restarted func()) {
	w := &machineWatch{
		c:             c,
		fieldSelector: fieldSelector,
		events:        events,
		minLife:       time.Second,
		pause:         func() { RetryPause() },
	}
	w.run(resourceVersion, restarted)
}

// machineWatch holds what one watch loop needs. minLife and pause are
// fields so a test can make every watch short or healthy, and can see
// each pause, without waiting out real time.
type machineWatch struct {
	c             *Client
	fieldSelector string
	events        chan<- *machine.Machine
	minLife       time.Duration
	pause         func()
}

// watchOutcome is how one watch ended.
type watchOutcome int

const (
	watchClosed watchOutcome = iota // the server ended the stream cleanly
	watchGone                       // 410: the version was compacted away
	watchFailed                     // any other error
)

func (w *machineWatch) run(resourceVersion string, restarted func()) {
	relistedForGone := false
	for {
		var outcome watchOutcome
		var accepted time.Time
		outcome, accepted, resourceVersion = w.watch(resourceVersion)

		// Every arrival here is one restart: the stream ended, and
		// the code below opens it again. A caller counts these,
		// because a low rate is the API server's own schedule and a
		// high rate is a stream that breaks faster than the loop can
		// use it.
		restarted()

		healthy := !accepted.IsZero() && time.Since(accepted) >= w.minLife
		if healthy {
			relistedForGone = false
		}
		switch {
		case outcome == watchClosed && healthy:
			continue
		case outcome == watchGone && !relistedForGone:
			relistedForGone = true
		default:
			w.pause()
		}
		if listed, ok := w.list(); ok {
			resourceVersion = listed
		}
	}
}

// watch opens one watch from resourceVersion, delivers its events,
// and returns how the watch ended, when the server accepted it (zero
// when it did not), and the last version it delivered.
func (w *machineWatch) watch(resourceVersion string) (watchOutcome, time.Time, string) {
	path := MachinesPath + "?watch=true&allowWatchBookmarks=true" +
		"&resourceVersion=" + url.QueryEscape(resourceVersion) + w.selector("&")
	resp, err := w.c.Do(http.MethodGet, path, "", nil)
	if err != nil {
		return watchFailed, time.Time{}, resourceVersion
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusGone:
		return watchGone, time.Time{}, resourceVersion
	default:
		return watchFailed, time.Time{}, resourceVersion
	}
	accepted := time.Now()

	decoder := json.NewDecoder(resp.Body)
	for {
		var event struct {
			Type   string          `json:"type"`
			Object json.RawMessage `json:"object"`
		}
		if err := decoder.Decode(&event); errors.Is(err, io.EOF) {
			return watchClosed, accepted, resourceVersion
		} else if err != nil {
			return watchFailed, accepted, resourceVersion
		}
		if event.Type == "ERROR" {
			// The object of an ERROR event is a Status, and its code
			// is the HTTP status the server would have answered.
			var status struct {
				Code int `json:"code"`
			}
			if json.Unmarshal(event.Object, &status) == nil && status.Code == http.StatusGone {
				return watchGone, accepted, resourceVersion
			}
			return watchFailed, accepted, resourceVersion
		}
		var m machine.Machine
		if err := json.Unmarshal(event.Object, &m); err != nil {
			return watchFailed, accepted, resourceVersion
		}
		if m.Metadata.ResourceVersion != "" {
			resourceVersion = m.Metadata.ResourceVersion
		}
		if event.Type == "BOOKMARK" {
			// A bookmark only moves the resume point. There is no
			// change to reconcile.
			continue
		}
		w.events <- &m
	}
}

// list reads the collection, delivers each item as an event, and
// returns the list's own resourceVersion.
func (w *machineWatch) list() (string, bool) {
	var list struct {
		Metadata struct {
			ResourceVersion string `json:"resourceVersion"`
		} `json:"metadata"`
		Items []machine.Machine `json:"items"`
	}
	if err := w.c.RequestJSON(http.MethodGet, MachinesPath+w.selector("?"), nil, &list); err != nil {
		return "", false
	}
	for i := range list.Items {
		w.events <- &list.Items[i]
	}
	return list.Metadata.ResourceVersion, true
}

// selector is the fieldSelector query parameter after sep, or nothing
// when the watch spans the whole collection.
func (w *machineWatch) selector(sep string) string {
	if w.fieldSelector == "" {
		return ""
	}
	return sep + "fieldSelector=" + url.QueryEscape(w.fieldSelector)
}
