package main

// The three guards of a watch loop written by hand, against a server
// that answers each watch from a script: resume from the last version
// the watch delivered; list again at once only after the first 410,
// and after a backoff on every other failure; and a watch that closed
// in under watchShortLife is a failure, while a longer one resets the
// backoff.

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

// watchStep is what the scripted server answers one watch with: a
// status, the events it writes, how long it holds the stream open
// after them, and how long it waits before them. accept is how long it
// waits before it answers at all, the way a slow server does.
type watchStep struct {
	status int
	events []string
	hold   time.Duration
	delay  time.Duration
	accept time.Duration
}

// watchRequest is one request the loop sent: a watch from a version,
// or a list, when it arrived, and when the server finished its answer.
type watchRequest struct {
	what  string
	at    time.Time
	ended time.Time
}

// scriptedWatch answers the watches of one collection from a script,
// answers each list with a new version, and records every request.
type scriptedWatch struct {
	mutex    sync.Mutex
	steps    []watchStep
	lists    int
	requests []watchRequest
}

func (s *scriptedWatch) handle(w http.ResponseWriter, r *http.Request) {
	s.mutex.Lock()
	if r.URL.Query().Get("watch") != "true" {
		s.lists++
		version := fmt.Sprintf("L%d", s.lists)
		now := time.Now()
		s.requests = append(s.requests, watchRequest{"list", now, now})
		s.mutex.Unlock()
		fmt.Fprintf(w, `{"metadata":{"resourceVersion":%q},"items":[]}`, version)
		return
	}
	index := len(s.requests)
	s.requests = append(s.requests, watchRequest{what: "watch " + r.URL.Query().Get("resourceVersion"), at: time.Now()})
	if len(s.steps) == 0 {
		s.mutex.Unlock()
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		<-r.Context().Done()
		return
	}
	step := s.steps[0]
	s.steps = s.steps[1:]
	s.mutex.Unlock()
	select {
	case <-time.After(step.accept):
	case <-r.Context().Done():
	}
	w.WriteHeader(step.status)
	w.(http.Flusher).Flush()
	select {
	case <-time.After(step.delay):
	case <-r.Context().Done():
	}
	for _, event := range step.events {
		fmt.Fprintln(w, event)
	}
	w.(http.Flusher).Flush()
	select {
	case <-time.After(step.hold):
	case <-r.Context().Done():
	}
	s.mutex.Lock()
	s.requests[index].ended = time.Now()
	s.mutex.Unlock()
}

// requestsSeen waits until the loop has sent count requests, and
// answers them.
func (s *scriptedWatch) requestsSeen(t *testing.T, count int) []watchRequest {
	t.Helper()
	deadline := time.Now().Add(testTimeout)
	for {
		s.mutex.Lock()
		seen := append([]watchRequest(nil), s.requests...)
		s.mutex.Unlock()
		if len(seen) >= count {
			return seen[:count]
		}
		if time.Now().After(deadline) {
			t.Fatalf("the loop sent %d requests, want %d: %v", len(seen), count, seen)
		}
		time.Sleep(time.Millisecond)
	}
}

// The clocks the tests hold: a short watch lives under 50 ms, and the
// first backoff is 150 ms. A request that follows at once comes well
// inside 100 ms, and one after a backoff comes after at least 150 ms.
const (
	testShortLife = 50 * time.Millisecond
	testBackoff   = 150 * time.Millisecond
	atOnce        = 100 * time.Millisecond
)

// runScriptedWatch runs watchCollection against a script until the
// test ends.
func runScriptedWatch(t *testing.T, steps ...watchStep) *scriptedWatch {
	t.Helper()
	life, first, most := watchShortLife, watchBackoffFirst, watchBackoffMax
	watchShortLife, watchBackoffFirst, watchBackoffMax = testShortLife, testBackoff, 4*testBackoff
	script := &scriptedWatch{steps: steps}
	client := testAPIClient(t, http.HandlerFunc(script.handle))
	ctx, cancel := context.WithCancel(context.Background())
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		watchCollection(ctx, client, receiversPath, "1", make(chan struct{}, 1), func() {}, func() (string, error) {
			list, err := ListReceivers(client)
			if err != nil {
				return "", err
			}
			return list.Metadata.ResourceVersion, nil
		})
	}()
	t.Cleanup(func() {
		cancel()
		<-stopped
		watchShortLife, watchBackoffFirst, watchBackoffMax = life, first, most
	})
	return script
}

// event is one watch event of a Receiver at a version.
func event(kind, version string) string {
	return fmt.Sprintf(`{"type":%q,"object":{"metadata":{"name":"den","resourceVersion":%q}}}`, kind, version)
}

// statusEvent is an ERROR event that carries a Status with a code.
func statusEvent(code int) string {
	return fmt.Sprintf(`{"type":"ERROR","object":{"kind":"Status","code":%d}}`, code)
}

// long is a stream that lives past the short life before it closes.
const long = 2 * testShortLife

// gap is the time from the end of one answer to the next request.
func gap(requests []watchRequest, index int) time.Duration {
	return requests[index].at.Sub(requests[index-1].ended)
}

func whats(requests []watchRequest) string {
	names := make([]string, 0, len(requests))
	for _, request := range requests {
		names = append(names, request.what)
	}
	return strings.Join(names, ", ")
}

// Each case is a script and the requests the loop must send for it, and
// for each request after the first, whether it follows at once or after
// a backoff.
func TestTheWatchLoopGuards(t *testing.T) {
	cases := []struct {
		name     string
		steps    []watchStep
		requests string
		backoff  []bool
	}{
		{"a watch that closes resumes from its last version, with a bookmark's too",
			[]watchStep{{http.StatusOK, []string{event("ADDED", "7"), event("BOOKMARK", "9")}, long, 0, 0}},
			"watch 1, watch 9", []bool{false}},
		{"a 410 response lists at once",
			[]watchStep{{http.StatusGone, nil, 0, 0, 0}},
			"watch 1, list, watch L1", []bool{false, false}},
		{"a 410 event lists at once",
			[]watchStep{{http.StatusOK, []string{statusEvent(410)}, 0, 0, 0}},
			"watch 1, list, watch L1", []bool{false, false}},
		{"a second 410 from the fresh list waits before it lists",
			[]watchStep{{http.StatusGone, nil, 0, 0, 0}, {http.StatusGone, nil, 0, 0, 0}},
			"watch 1, list, watch L1, list, watch L2", []bool{false, false, true, false}},
		{"another error event waits before it lists",
			[]watchStep{{http.StatusOK, []string{statusEvent(500)}, 0, 0, 0}},
			"watch 1, list, watch L1", []bool{true, false}},
		{"an event that does not decode waits before it lists",
			[]watchStep{{http.StatusOK, []string{`{"type":"MODIFIED","object":`}, 0, 0, 0}},
			"watch 1, list, watch L1", []bool{true, false}},
		{"an error response waits before it lists",
			[]watchStep{{http.StatusInternalServerError, nil, 0, 0, 0}},
			"watch 1, list, watch L1", []bool{true, false}},
		{"a 410 that ends a watch which ran long counts as a first 410",
			[]watchStep{{http.StatusGone, nil, 0, 0, 0}, {http.StatusOK, []string{statusEvent(410)}, 0, long, 0}},
			"watch 1, list, watch L1, list, watch L2", []bool{false, false, false, false}},
		{"a watch that closes at once waits, and resumes from its version",
			[]watchStep{{http.StatusOK, []string{event("MODIFIED", "7")}, 0, 0, 0}},
			"watch 1, watch 7", []bool{true}},
		{"a watch that lived long resets the backoff even when it ends in an error",
			[]watchStep{{http.StatusOK, nil, 0, 0, 0}, {http.StatusOK, nil, 0, 0, 0}, {http.StatusOK, []string{statusEvent(500)}, long, 0, 0}},
			"watch 1, watch 1, watch 1, list, watch L1", []bool{true, true, true, false}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			script := runScriptedWatch(t, c.steps...)

			requests := script.requestsSeen(t, len(c.backoff)+1)

			mustMatch(t, whats(requests), c.requests)
			for index, waited := range c.backoff {
				between := gap(requests, index+1)
				if waited && between < testBackoff {
					t.Errorf("request %d (%s) came %s after the one before, inside the backoff", index+1, requests[index+1].what, between)
				}
				if !waited && between >= atOnce {
					t.Errorf("request %d (%s) came %s after the one before, not at once", index+1, requests[index+1].what, between)
				}
			}
		})
	}
}

// The backoff doubles while watches keep failing, and a watch that
// lived long starts it again from the first delay.
func TestTheBackoffDoublesAndResets(t *testing.T) {
	script := runScriptedWatch(t,
		watchStep{http.StatusOK, nil, 0, 0, 0},
		watchStep{http.StatusOK, nil, 0, 0, 0},
		watchStep{http.StatusOK, nil, long, 0, 0},
		watchStep{http.StatusOK, nil, 0, 0, 0},
	)

	requests := script.requestsSeen(t, 5)

	if second := gap(requests, 2); second < 2*testBackoff {
		t.Errorf("the second failure waited %s, want the doubled backoff", second)
	}
	if reset := gap(requests, 4); reset >= 2*testBackoff {
		t.Errorf("the failure after a long watch waited %s, want the first backoff", reset)
	}
}

// A server can hold a watch open for minutes after an error event, and
// every event in that time is lost to a loop that reads on. So the loop
// closes the stream at the error and lists after the backoff, not after
// the server lets go.
func TestAWatchClosesTheStreamAtAnError(t *testing.T) {
	const held = 5 * time.Second
	script := runScriptedWatch(t, watchStep{http.StatusOK, []string{statusEvent(500)}, held, 0, 0})

	requests := script.requestsSeen(t, 2)

	mustMatch(t, whats(requests), "watch 1, list")
	if waited := requests[1].at.Sub(requests[0].at); waited >= held {
		t.Errorf("the list came %s after the watch opened, after the server let the stream go", waited)
	}
}

// A watch's life starts when the server accepts it, at the 200, and not
// when the request began. A server that answers slowly with an error
// never ran a watch, so the backoff keeps doubling.
func TestASlowRefusalDoesNotResetTheBackoff(t *testing.T) {
	refusal := watchStep{status: http.StatusInternalServerError, accept: long}
	script := runScriptedWatch(t, refusal, refusal, refusal)

	requests := script.requestsSeen(t, 5)

	mustMatch(t, whats(requests), "watch 1, list, watch L1, list, watch L2")
	if doubled := requests[3].at.Sub(requests[2].at) - long; doubled < 2*testBackoff {
		t.Errorf("the second slow refusal waited %s, want the doubled backoff", doubled)
	}
}
