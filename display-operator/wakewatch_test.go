package main

// These tests cover the watches that only wake a pass: the one on
// Displays, the one on Layouts, and the one on this node's pods.

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// The collection each kind's watch opens, and what the path must carry.
var wakeWatchPaths = []struct {
	kind  string
	path  string
	wants string
}{
	{"Display", displaysWatchPath(), DisplaysPath},
	{"Layout", layoutsWatchPath(), LayoutsPath},
	{"Pod", podsWatchPath("node-1"), PodsPath + "?fieldSelector=spec.nodeName=node-1"},
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
		name   string
		status int
		events int
		wakes  int
	}{
		{name: "two events", status: http.StatusOK, events: 2, wakes: 3},
		{name: "no event", status: http.StatusOK, events: 0, wakes: 1},
		{name: "refused", status: http.StatusInternalServerError, wakes: 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var wakes atomic.Int64
			countOpens(t, 300*time.Millisecond, func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(c.status)
				for i := range c.events {
					fmt.Fprintf(w, `{"type":"MODIFIED","object":{"metadata":{"name":"panel-%d","resourceVersion":"%d"}}}`, i, i+2)
				}
				if c.status == http.StatusOK {
					w.(http.Flusher).Flush()
					<-r.Context().Done()
				}
			}, func() { wakes.Add(1) })
			if got := wakes.Load(); got != int64(c.wakes) {
				t.Errorf("the watch woke the loop %d times, want %d", got, c.wakes)
			}
		})
	}
}

// How many watches the loop opens against a server that answers each
// one the same way. Each watch opens with a replay of every object, so
// events say nothing about whether a watch ran: its lifetime does. A
// watch that lived under a second is a failure, whether it replayed
// objects, closed empty, or was refused, and the loop backs off before
// the next one instead of asking in a tight loop.
func TestTheWakeWatchBacksOffAfterAShortWatch(t *testing.T) {
	cases := []struct {
		name   string
		status int
		events int
	}{
		{name: "replayed the objects", status: http.StatusOK, events: 3},
		{name: "closed empty", status: http.StatusOK, events: 0},
		{name: "refused", status: http.StatusInternalServerError},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			opens := countOpens(t, 500*time.Millisecond, func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(c.status)
				for i := range c.events {
					fmt.Fprintf(w, `{"type":"ADDED","object":{"metadata":{"name":"panel-%d"}}}`, i)
				}
			}, nil)
			if opens < 1 || opens > 2 {
				t.Errorf("the loop opened %d watches in half a second, want one or two", opens)
			}
		})
	}
}

// A watch that lived a second or more ran, and the next one opens at
// once, because the open's wake covers whatever changed in between.
// That holds for a connection that ended with an error too: a load
// balancer that resets a long connection every few minutes must not
// push the reopen toward a minute.
func TestTheWakeWatchReopensAtOnceAfterAWatchThatLived(t *testing.T) {
	cases := []struct {
		name  string
		reset bool
	}{
		{name: "ended cleanly"},
		{name: "ended with a reset", reset: true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			opens := countOpens(t, 3*time.Second, func(w http.ResponseWriter, r *http.Request) {
				fmt.Fprint(w, `{"type":"ADDED","object":{"metadata":{"name":"panel-0"}}}`)
				w.(http.Flusher).Flush()
				time.Sleep(1100 * time.Millisecond)
				if c.reset {
					conn, _, err := http.NewResponseController(w).Hijack()
					if err == nil {
						_ = conn.Close()
					}
				}
			}, nil)
			if opens < 3 {
				t.Errorf("the loop opened %d watches in three seconds, want three: one per lifetime", opens)
			}
		})
	}
}

// countOpens runs the Display watch against a server that answers each
// watch with answer, for the time given, and counts the watches opened.
// A nil wake wakes nothing.
func countOpens(t *testing.T, window time.Duration, answer func(w http.ResponseWriter, r *http.Request), wake func()) int64 {
	t.Helper()
	if wake == nil {
		wake = func() {}
	}
	var opens atomic.Int64
	client := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		opens.Add(1)
		answer(w, r)
	}))
	ctx, stop := context.WithTimeout(t.Context(), window)
	defer stop()
	watchWakes(ctx, client, kindDisplay, "displays", displaysWatchPath(), wake, nil)
	return opens.Load()
}

// A watch's life counts from when the API server accepted it. A watch
// refused after more than a second never ran, so it still grows the
// wait: opens at 0 s and 2.2 s fit in three seconds, and a third does
// not.
func TestASlowRefusalStillGrowsTheWakeWatchWait(t *testing.T) {
	opens := countOpens(t, 3*time.Second, func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(1200 * time.Millisecond)
		w.WriteHeader(http.StatusInternalServerError)
	}, nil)
	if opens != 2 {
		t.Errorf("the loop opened %d watches in three seconds, want 2: a slow refusal is a failure", opens)
	}
}

// An ERROR event, a Status that does not decode, or a line that is not
// JSON ends the watch at once, even while the server holds the stream
// open, and the next watch opens after the wait: at 1 s, inside the
// window.
func TestTheWakeWatchClosesOnAnErrorAndOpensAgain(t *testing.T) {
	cases := []struct {
		name string
		line string
	}{
		{name: "an error event", line: `{"type":"ERROR","object":{"kind":"Status","code":500}}`},
		{name: "a status that does not decode", line: `{"type":"ERROR","object":5}`},
		{name: "a line that is not JSON", line: "not json"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			opens := countOpens(t, 1600*time.Millisecond, func(w http.ResponseWriter, r *http.Request) {
				fmt.Fprintln(w, c.line)
				w.(http.Flusher).Flush()
				<-r.Context().Done()
			}, nil)
			if opens != 2 {
				t.Errorf("the loop opened %d watches in 1.6 s, want 2: close at once, wait, open again", opens)
			}
		})
	}
}

// The first watch opens from no version, and its replay is the
// baseline. Each watch after it resumes from the last version the
// stream delivered, with bookmarks on, so a reopen replays nothing. A
// 410 says that version is gone, and the next watch opens from no
// version again.
func TestTheWakeWatchResumesFromTheLastVersion(t *testing.T) {
	var mu sync.Mutex
	var asked []string
	var bookmarks []string
	client := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		asked = append(asked, r.URL.Query().Get("resourceVersion"))
		bookmarks = append(bookmarks, r.URL.Query().Get("allowWatchBookmarks"))
		n := len(asked)
		mu.Unlock()
		switch n {
		case 1:
			fmt.Fprint(w, `{"type":"ADDED","object":{"metadata":{"name":"panel-0","resourceVersion":"7"}}}`)
			w.(http.Flusher).Flush()
			time.Sleep(1100 * time.Millisecond)
		case 2:
			fmt.Fprint(w, `{"type":"ERROR","object":{"kind":"Status","code":410}}`)
		default:
			<-r.Context().Done()
		}
	}))
	ctx, stop := context.WithTimeout(t.Context(), 1600*time.Millisecond)
	defer stop()

	watchWakes(ctx, client, kindDisplay, "displays", displaysWatchPath(), func() {}, nil)

	mu.Lock()
	defer mu.Unlock()
	want := []string{"", "7", ""}
	if !slices.Equal(asked, want) {
		t.Errorf("the watches asked for versions %q, want %q", asked, want)
	}
	for i, on := range bookmarks {
		if on != "true" {
			t.Errorf("watch %d asked for bookmarks %q, want true", i, on)
		}
	}
}
