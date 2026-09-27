package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"
)

// oneObject stands in for the API server's list and watch of one
// named object. The test sends each watch event down events, and every
// list and every watch the server receives is reported on a channel,
// so a test waits for the request it expects instead of for a clock.
type oneObject struct {
	mu sync.Mutex
	// version is what a list answers as the collection's version.
	version string
	// failLists and goneWatches answer that many lists with a 500 and
	// that many watch opens with a 410 before answering normally.
	failLists   int
	goneWatches int
	// refuseWatches answers every watch open with a 500 after
	// refusalDelay, the way a slow or overloaded API server refuses.
	refuseWatches bool
	refusalDelay  time.Duration

	events  chan string
	listed  chan string
	watched chan string
	server  *httptest.Server
}

// endWatch is the event a test sends to make the fake API server end
// the open watch the way its own timeout ends it.
const endWatch = "end"

func newOneObject(t *testing.T) *oneObject {
	t.Helper()
	fake := &oneObject{
		version: "10",
		events:  make(chan string, 8),
		listed:  make(chan string, 8),
		watched: make(chan string, 8),
	}
	fake.server = httptest.NewServer(http.HandlerFunc(fake.serve))
	t.Cleanup(fake.server.Close)
	return fake
}

func (f *oneObject) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	watching := r.URL.Query().Get("watch") == "true"
	failing := !watching && f.failLists > 0
	gone := watching && f.goneWatches > 0
	if failing {
		f.failLists--
	}
	if gone {
		f.goneWatches--
	}
	version := f.version
	refusing, refusalDelay := watching && f.refuseWatches, f.refusalDelay
	f.mu.Unlock()

	if !watching {
		f.listed <- r.URL.RawQuery
		if failing {
			http.Error(w, "etcdserver: request timed out", http.StatusInternalServerError)
			return
		}
		fmt.Fprintf(w, `{"metadata":{"resourceVersion":%q},"items":[]}`, version)
		return
	}
	f.watched <- r.URL.RawQuery
	if refusing {
		time.Sleep(refusalDelay)
		http.Error(w, "etcdserver: too many requests", http.StatusInternalServerError)
		return
	}
	if gone {
		http.Error(w, "too old resource version: 10 (42)", http.StatusGone)
		return
	}
	w.WriteHeader(http.StatusOK)
	w.(http.Flusher).Flush()
	for {
		select {
		case <-r.Context().Done():
			return
		case event := <-f.events:
			if event == endWatch {
				return
			}
			fmt.Fprintln(w, event)
			w.(http.Flusher).Flush()
			// The API server closes a watch after an ERROR event.
			if strings.Contains(event, `"type":"ERROR"`) {
				return
			}
		}
	}
}

// modified is one watch event for the object at a resource version.
func modified(version string) string {
	body, _ := json.Marshal(map[string]any{
		"type":   "MODIFIED",
		"object": map[string]any{"metadata": map[string]string{"name": captureTLSSecret, "resourceVersion": version}},
	})
	return string(body)
}

// expired is the event the API server sends when a watch starts from
// a version older than the window it keeps.
const expired = `{"type":"ERROR","object":{"kind":"Status","code":410,"message":"too old resource version: 10 (42)"}}`

// follow runs a watch on the fake's object until the test ends, and
// reports every change and every complaint on a channel.
func (f *oneObject) follow(t *testing.T) (changed chan struct{}, complaints chan error) {
	t.Helper()
	changed, complaints, _ = f.followWith(t, nil)
	return changed, complaints
}

// followWith runs the watch after shape changes it, and reports every
// wait the loop asks for on waited, where the wait ends at once. A
// test reads the backoff from the waits and runs no clock.
func (f *oneObject) followWith(t *testing.T, shape func(*objectWatch)) (changed chan struct{}, complaints chan error, waited chan time.Duration) {
	t.Helper()
	changed, complaints, waited = make(chan struct{}, 8), make(chan error, 8), make(chan time.Duration, 8)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	watch := &objectWatch{
		client:     NewClient(f.server.URL, f.server.Client(), ""),
		kind:       "Secret",
		collection: secretsPath("liken-system"),
		selector:   "metadata.name=" + captureTLSSecret,
		changed:    func() { changed <- struct{}{} },
		complain:   func(err error) { complaints <- err },
		retry:      time.Millisecond,
		retryLimit: 10 * time.Millisecond,
	}
	if shape != nil {
		watch.after = func(d time.Duration) <-chan time.Time {
			waited <- d
			now := make(chan time.Time, 1)
			now <- time.Now()
			return now
		}
		shape(watch)
	}
	go watch.run(ctx)
	return changed, complaints, waited
}

// next waits for one value on a channel, for at most five seconds.
func next[T any](t *testing.T, from chan T, what string) T {
	t.Helper()
	select {
	case got := <-from:
		return got
	case <-time.After(5 * time.Second):
		t.Fatalf("no %s within five seconds", what)
		var none T
		return none
	}
}

func TestAChangeToTheObjectArrivesAsAnEvent(t *testing.T) {
	fake := newOneObject(t)
	changed, _ := fake.follow(t)

	listed := next(t, fake.listed, "list")
	next(t, changed, "change for the list")
	opened := next(t, fake.watched, "watch")
	fake.events <- modified("11")
	next(t, changed, "change for the event")

	// Both requests name the object, so RBAC restricts them to it.
	for _, query := range []string{listed, opened} {
		if !strings.Contains(query, "fieldSelector=metadata.name%3D"+captureTLSSecret) {
			t.Errorf("%q does not select the object by name", query)
		}
	}
	if !strings.Contains(opened, "resourceVersion=10") {
		t.Errorf("the watch %q does not start from the list's version", opened)
	}
}

func TestAWatchTheServerEndsResumesFromTheLastEvent(t *testing.T) {
	fake := newOneObject(t)
	changed, _ := fake.follow(t)
	next(t, fake.listed, "list")
	next(t, fake.watched, "watch")
	fake.events <- modified("11")
	next(t, changed, "change for the event")

	fake.events <- endWatch
	reopened := next(t, fake.watched, "second watch")

	if !strings.Contains(reopened, "resourceVersion=11") {
		t.Errorf("the second watch %q does not resume from the last event", reopened)
	}
	if len(fake.listed) != 0 {
		t.Error("a watch that ended cleanly was followed by a list")
	}
}

func TestAnExpiredVersionIsListedAgain(t *testing.T) {
	for _, test := range []struct {
		name     string
		expireAs func(fake *oneObject)
	}{
		{"an ERROR event on the stream", func(fake *oneObject) { fake.events <- expired }},
		{"a 410 on the open", func(fake *oneObject) { fake.goneWatches = 1 }},
	} {
		t.Run(test.name, func(t *testing.T) {
			fake := newOneObject(t)
			test.expireAs(fake)
			changed, _ := fake.follow(t)
			next(t, fake.listed, "first list")
			next(t, changed, "change for the first list")

			next(t, fake.listed, "list after the version expired")
			next(t, changed, "change for the second list")
		})
	}
}

func TestAFailedListIsTriedAgainAndReportsTheServersWords(t *testing.T) {
	fake := newOneObject(t)
	fake.failLists = 1
	changed, complaints := fake.follow(t)

	complaint := next(t, complaints, "complaint")
	next(t, fake.listed, "failed list")
	next(t, fake.listed, "second list")
	next(t, changed, "change for the second list")

	if !strings.Contains(complaint.Error(), "etcdserver: request timed out") {
		t.Errorf("the complaint %q does not carry the API server's text", complaint)
	}
}

// The API follows the capture Secret with a watch, and a delete of the
// Secret is minted again when the event arrives. The hourly lifetime
// check never runs in this test, so only the event can bring the
// Secret back.
func TestADeletedCaptureSecretIsMintedAgainWhenTheWatchReportsIt(t *testing.T) {
	store := newObjectStore(t)
	fake := newOneObject(t)
	secrets := secretsPath("liken-system")
	// The store answers the gets and the writes, and the fake answers
	// the list and the watch of the Secrets.
	both := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == secrets && r.Method == http.MethodGet {
			fake.serve(w, r)
			return
		}
		store.serve(w, r)
	}))
	t.Cleanup(both.Close)
	server := newAPIServer(NewClient(both.URL, both.Client(), ""), "liken-system")
	if _, err := server.certs.ensure(); err != nil {
		t.Fatal(err)
	}
	first := store.secretData(t, captureTLSSecret)[tlsCertFile]

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	server.followCertificateObjects(ctx, func(error) {})
	next(t, fake.watched, "watch on the Secret")

	store.mu.Lock()
	delete(store.secrets, captureTLSSecret)
	store.mu.Unlock()
	fake.events <- `{"type":"DELETED","object":{"metadata":{"resourceVersion":"11"}}}`

	deadline := time.Now().Add(5 * time.Second)
	for !store.holdsSecret(captureTLSSecret) && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	again := store.secretData(t, captureTLSSecret)[tlsCertFile]
	if string(again) == string(first) {
		t.Error("the Secret carries the deleted leaf")
	}
}

// audio_watch_restarts_total counts a watch reopening, and not the
// watch's first open: the first connection is the start of watching,
// and only a connection the API server or a fault closed is a restart.
func TestAWatchThatReopensCountsOneRestart(t *testing.T) {
	fake := newOneObject(t)
	readings := newMetrics("test")
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	watch := &objectWatch{
		client:     NewClient(fake.server.URL, fake.server.Client(), ""),
		kind:       SinkKind,
		collection: SinksPath,
		selector:   "status.node=liken-1",
		changed:    func() {},
		complain:   func(error) {},
		restarted:  func() { readings.watchRestarted(SinkKind) },
		retry:      time.Millisecond,
		retryLimit: 10 * time.Millisecond,
	}
	go watch.run(ctx)
	next(t, fake.watched, "watch")
	fake.events <- endWatch
	next(t, fake.watched, "second watch")

	if got := testutil.ToFloat64(readings.watchRestarts.WithLabelValues(SinkKind)); got != 1 {
		t.Errorf("audio_watch_restarts_total{kind=Sink} = %v, want 1", got)
	}
}

// noWait says the loop asked for no wait before its next request.
func noWait(t *testing.T, waited chan time.Duration, before string) {
	t.Helper()
	select {
	case d := <-waited:
		t.Errorf("the loop waited %s before %s", d, before)
	default:
	}
}

// The first 410 lists again at once. A 410 on the watch that opens
// from that fresh list is a server that keeps no version, and the loop
// waits out the backoff before it lists a third time.
func TestA410OnTheFreshListsWatchWaitsOutTheBackoff(t *testing.T) {
	fake := newOneObject(t)
	fake.goneWatches = 2
	// A 410 on the open ends the watch at once, so the watch is short.
	_, _, waited := fake.followWith(t, func(w *objectWatch) { w.shortLife = time.Hour })

	next(t, fake.listed, "first list")
	next(t, fake.watched, "watch that gets the first 410")
	next(t, fake.listed, "list after the first 410")
	next(t, fake.watched, "watch that gets the second 410")
	next(t, fake.listed, "list after the second 410")
	if got := next(t, waited, "wait before the third list"); got != time.Millisecond {
		t.Errorf("the loop waited %s, want the first backoff", got)
	}
	next(t, fake.watched, "watch that opens")
	if len(waited) != 0 {
		t.Errorf("the loop waited %d more times", len(waited))
	}
}

// An event that does not decode leaves no version to trust, so it
// counts as an error event: the loop reports it, waits, and lists.
// Opening again at the same version would read the same event again.
func TestAnEventThatDoesNotDecodeListsAgainAfterABackoff(t *testing.T) {
	for _, event := range []string{
		`this line is not JSON`,
		`{"type":"MODIFIED","object":"not an object"}`,
		`{"type":"MODIFIED","object":{"metadata":{}}}`,
		`{"type":"ERROR","object":{"kind":"Status","code":500,"message":"etcdserver: leader changed"}}`,
	} {
		t.Run(event, func(t *testing.T) {
			fake := newOneObject(t)
			_, complaints, waited := fake.followWith(t, func(*objectWatch) {})
			next(t, fake.listed, "first list")
			next(t, fake.watched, "watch")
			fake.events <- event

			next(t, complaints, "complaint")
			next(t, waited, "wait")
			next(t, fake.listed, "list after the bad event")
		})
	}
}

// A watch that closes under a second after it opened is a failure,
// whatever it delivered, so the waits grow. A watch that lived a
// second or more resets the backoff, even when it ended with an
// error, and one that ended cleanly opens again at once.
func TestAWatchsLifetimeDecidesTheBackoff(t *testing.T) {
	errorEvent := `{"type":"ERROR","object":{"kind":"Status","code":500,"message":"etcdserver: leader changed"}}`
	cases := []struct {
		name      string
		shortLife time.Duration
		end       string
		want      []time.Duration
	}{
		{"short watches that end cleanly", time.Hour, endWatch,
			[]time.Duration{time.Millisecond, 2 * time.Millisecond, 4 * time.Millisecond}},
		{"short watches that end on an error", time.Hour, errorEvent,
			[]time.Duration{time.Millisecond, 2 * time.Millisecond, 4 * time.Millisecond}},
		{"long watches that end on an error", 0, errorEvent,
			[]time.Duration{time.Millisecond, time.Millisecond, time.Millisecond}},
		{"long watches that end cleanly", 0, endWatch, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			fake := newOneObject(t)
			_, _, waited := fake.followWith(t, func(w *objectWatch) { w.shortLife = c.shortLife })
			var got []time.Duration
			for range 3 {
				next(t, fake.watched, "watch")
				fake.events <- modified("11")
				fake.events <- c.end
			}
			next(t, fake.watched, "fourth watch")
			for len(waited) > 0 {
				got = append(got, <-waited)
			}
			if !slices.Equal(got, c.want) {
				t.Errorf("waits = %v, want %v", got, c.want)
			}
		})
	}
}

// A watch's life starts when the server accepts it, not when the
// request began. A refusal that takes longer than the short life is
// still a watch that never ran, so the backoff grows.
func TestASlowRefusalStillGrowsTheBackoff(t *testing.T) {
	fake := newOneObject(t)
	fake.refuseWatches = true
	fake.refusalDelay = 50 * time.Millisecond
	_, _, waited := fake.followWith(t, func(w *objectWatch) { w.shortLife = 10 * time.Millisecond })

	var got []time.Duration
	for range 3 {
		got = append(got, next(t, waited, "wait after a refused watch"))
	}
	want := []time.Duration{time.Millisecond, 2 * time.Millisecond, 4 * time.Millisecond}
	if !slices.Equal(got, want) {
		t.Errorf("waits = %v, want %v", got, want)
	}
}
