package kubernetes

// These tests check the three guards of a hand-written watch loop:
// resume from the last delivered version after a clean close, list
// again at once only on the first 410, and treat a short watch as a
// failure. Each test scripts the watch responses in order and reads
// back the requests and pauses in the order the loop made them.

import (
	"net/http"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"github.com/liken-sh/liken/machine"
)

// scriptedWatches answers the nth watch request with the nth entry
// of script, and holds every later watch open until the client goes
// away, so the loop goes quiet when the script ends. Every list gets
// a MachineList at version 99. Each request and each pause arrives
// on steps, in order.
type scriptedWatches struct {
	script []func(http.ResponseWriter, *http.Request)
	steps  chan string
	done   chan struct{}
}

func (s *scriptedWatches) handler() http.Handler {
	s.steps = make(chan string, 64)
	s.done = make(chan struct{})
	var watches atomic.Int32
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("watch") != "true" {
			s.steps <- "list"
			_, _ = w.Write([]byte(`{"metadata":{"resourceVersion":"99"},"items":[]}`))
			return
		}
		s.steps <- "watch from " + r.URL.Query().Get("resourceVersion")
		n := int(watches.Add(1))
		if n > len(s.script) {
			s.hold(w, r)
			return
		}
		s.script[n-1](w, r)
	})
}

// hold keeps a watch stream open until the client closes it. The
// test server's Close waits for every active handler, so a held
// stream also ends when the test does.
func (s *scriptedWatches) hold(w http.ResponseWriter, r *http.Request) {
	w.(http.Flusher).Flush()
	select {
	case <-r.Context().Done():
	case <-s.done:
	}
}

// next reads the loop's next n steps.
func (s *scriptedWatches) next(t *testing.T, n int) []string {
	t.Helper()
	var got []string
	for range n {
		select {
		case step := <-s.steps:
			got = append(got, step)
		case <-time.After(5 * time.Second):
			t.Fatalf("the loop stopped after %q", got)
		}
	}
	return got
}

func stream(lines ...string) func(http.ResponseWriter, *http.Request) {
	return func(w http.ResponseWriter, _ *http.Request) {
		for _, line := range lines {
			_, _ = w.Write([]byte(line + "\n"))
		}
	}
}

// streamThenHold sends the lines and then keeps the stream open, the
// way an API server keeps a watch open after an ERROR event. A loop
// that drains the stream after an error waits here until the test
// ends.
func (s *scriptedWatches) streamThenHold(lines ...string) func(http.ResponseWriter, *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		stream(lines...)(w, r)
		s.hold(w, r)
	}
}

func status(code int) func(http.ResponseWriter, *http.Request) {
	return func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(code) }
}

const goneEvent = `{"type":"ERROR","object":{"kind":"Status","status":"Failure","code":410,"reason":"Expired"}}`

// runScripted starts the loop against the script. minLife is the
// shortest watch that counts as healthy: zero makes every watch
// healthy, and an hour makes every watch short.
func runScripted(t *testing.T, minLife time.Duration, script ...func(http.ResponseWriter, *http.Request)) *scriptedWatches {
	t.Helper()
	fake := &scriptedWatches{script: script}
	startScripted(t, fake, minLife)
	return fake
}

// startScripted starts the loop against a script the caller built.
func startScripted(t *testing.T, fake *scriptedWatches, minLife time.Duration) {
	t.Helper()
	client := testClient(t, fake.handler())
	t.Cleanup(func() { close(fake.done) })
	w := &machineWatch{
		c:       client,
		events:  make(chan *machine.Machine, 64),
		minLife: minLife,
		pause:   func() { fake.steps <- "pause" },
	}
	go w.run("1", func() {})
}

func TestWatchGuards(t *testing.T) {
	cases := []struct {
		name    string
		minLife time.Duration
		script  []func(http.ResponseWriter, *http.Request)
		want    []string
	}{
		{
			"a healthy watch that closes resumes from its last version without a list",
			0,
			[]func(http.ResponseWriter, *http.Request){stream(watchEvent(t, "MODIFIED", "node-1", "7"), watchEvent(t, "BOOKMARK", "node-1", "8"))},
			[]string{"watch from 1", "watch from 8"},
		},
		{
			"a short watch is a failure, whatever it delivered",
			time.Hour,
			[]func(http.ResponseWriter, *http.Request){stream(watchEvent(t, "MODIFIED", "node-1", "7"))},
			[]string{"watch from 1", "pause", "list", "watch from 99"},
		},
		{
			"the first 410 event lists at once",
			0,
			[]func(http.ResponseWriter, *http.Request){stream(goneEvent)},
			[]string{"watch from 1", "list", "watch from 99"},
		},
		{
			"the first 410 response lists at once",
			0,
			[]func(http.ResponseWriter, *http.Request){status(http.StatusGone)},
			[]string{"watch from 1", "list", "watch from 99"},
		},
		{
			"a 410 on the watch from the fresh list waits out the backoff",
			time.Hour,
			[]func(http.ResponseWriter, *http.Request){stream(goneEvent), stream(goneEvent)},
			[]string{"watch from 1", "list", "watch from 99", "pause", "list", "watch from 99"},
		},
		{
			"a 410 after a healthy watch lists at once again",
			0,
			[]func(http.ResponseWriter, *http.Request){stream(goneEvent), stream(goneEvent)},
			[]string{"watch from 1", "list", "watch from 99", "list", "watch from 99"},
		},
		{
			"another error event waits, then lists",
			0,
			[]func(http.ResponseWriter, *http.Request){stream(`{"type":"ERROR","object":{"kind":"Status","code":500}}`)},
			[]string{"watch from 1", "pause", "list", "watch from 99"},
		},
		{
			"an event whose object does not decode is an error event",
			0,
			[]func(http.ResponseWriter, *http.Request){stream(`{"type":"MODIFIED","object":{"metadata":"not an object"}}`)},
			[]string{"watch from 1", "pause", "list", "watch from 99"},
		},
		{
			"another error response waits, then lists",
			0,
			[]func(http.ResponseWriter, *http.Request){status(http.StatusInternalServerError)},
			[]string{"watch from 1", "pause", "list", "watch from 99"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			fake := runScripted(t, c.minLife, c.script...)
			if got := fake.next(t, len(c.want)); !slices.Equal(got, c.want) {
				t.Errorf("steps = %q, want %q", got, c.want)
			}
		})
	}
}

func TestWatchClosesTheStreamAtOnceAfterAnError(t *testing.T) {
	cases := []struct {
		name string
		line string
		want []string
	}{
		{"a 410 event", goneEvent, []string{"watch from 1", "list", "watch from 99"}},
		{"another error event", `{"type":"ERROR","object":{"kind":"Status","code":500}}`,
			[]string{"watch from 1", "pause", "list", "watch from 99"}},
		{"an event that does not decode", `{"type":"MODIFIED","object":{"metadata":"not an object"}}`,
			[]string{"watch from 1", "pause", "list", "watch from 99"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			fake := &scriptedWatches{}
			fake.script = []func(http.ResponseWriter, *http.Request){fake.streamThenHold(c.line)}
			startScripted(t, fake, 0)
			if got := fake.next(t, len(c.want)); !slices.Equal(got, c.want) {
				t.Errorf("steps = %q, want %q", got, c.want)
			}
		})
	}
}
