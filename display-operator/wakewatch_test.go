package main

// These tests cover the watches that only wake a pass: the one on
// Displays, the one on Layouts, and the one on this node's pods.

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// The path each kind's watch opens, and what the path must carry.
var wakeWatchPaths = []struct {
	kind  string
	path  string
	wants string
}{
	{"Display", displaysWatchPath(), DisplaysPath + "?watch=true"},
	{"Layout", layoutsWatchPath(), LayoutsPath + "?watch=true"},
	{"Pod", podsWatchPath("node-1"), "fieldSelector=spec.nodeName=node-1&watch=true"},
}

func TestEachWakeWatchOpensAWatchOnItsCollection(t *testing.T) {
	for _, watch := range wakeWatchPaths {
		t.Run(watch.kind, func(t *testing.T) {
			if !strings.Contains(watch.path, watch.wants) {
				t.Errorf("the %s watch opens %s, want it to carry %s", watch.kind, watch.path, watch.wants)
			}
		})
	}
}

// The open is a wake of its own, and each event is one more. The pass
// that the open's wake starts reads everything after the watch is
// live, so a change made while no connection stood is found at once
// and not on the backstop tick. A refused watch is not live, and it
// wakes nothing: a pass on every retry would read the resources over
// and over while the API server is down.
func TestAWakeWatchWakesOnItsOpenAndOnEveryEvent(t *testing.T) {
	cases := []struct {
		name    string
		status  int
		events  int
		wakes   int
		fails   bool
		answers bool
	}{
		{name: "two events", status: http.StatusOK, events: 2, wakes: 3, answers: true},
		{name: "no event", status: http.StatusOK, events: 0, wakes: 1},
		{name: "refused", status: http.StatusInternalServerError, wakes: 0, fails: true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			client := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(c.status)
				for i := range c.events {
					fmt.Fprintf(w, `{"type":"MODIFIED","object":{"metadata":{"name":"panel-%d"}}}`, i)
				}
			}))

			wakes := 0
			delivered, err := streamWakes(t.Context(), client, displaysWatchPath(), func() { wakes++ })
			if (err != nil) != c.fails {
				t.Fatalf("the watch answered %v, want a failure: %v", err, c.fails)
			}
			if wakes != c.wakes {
				t.Errorf("the watch woke the loop %d times, want %d", wakes, c.wakes)
			}
			if delivered != c.answers {
				t.Errorf("the watch reported delivered=%v, want %v", delivered, c.answers)
			}
		})
	}
}

// How many watches the loop opens in half a second against a server
// that answers each one the same way. A watch that delivered events
// ran as it should, and the next one opens at once, because the open's
// wake covers whatever changed in between. A watch that the server
// refused, or closed at once with no event, is a failure, and the loop
// backs off before the next one instead of asking in a tight loop.
func TestTheWakeWatchReopensAtOnceOnlyAfterAWatchThatRan(t *testing.T) {
	cases := []struct {
		name    string
		status  int
		events  int
		atMost  int64
		atLeast int64
	}{
		{name: "delivered an event", status: http.StatusOK, events: 1, atLeast: 3, atMost: 1 << 30},
		{name: "closed at once", status: http.StatusOK, events: 0, atLeast: 1, atMost: 2},
		{name: "refused", status: http.StatusInternalServerError, atLeast: 1, atMost: 2},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var opens atomic.Int64
			client := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				opens.Add(1)
				w.WriteHeader(c.status)
				for i := range c.events {
					fmt.Fprintf(w, `{"type":"MODIFIED","object":{"metadata":{"name":"panel-%d"}}}`, i)
				}
			}))
			ctx, stop := context.WithTimeout(t.Context(), 500*time.Millisecond)
			defer stop()

			watchWakes(ctx, client, kindDisplay, "displays", displaysWatchPath(), func() {}, nil)

			if got := opens.Load(); got < c.atLeast || got > c.atMost {
				t.Errorf("the loop opened %d watches in half a second, want between %d and %d",
					got, c.atLeast, c.atMost)
			}
		})
	}
}
