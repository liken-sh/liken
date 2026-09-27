package main

// These tests cover the list and the watch that keep a collection
// current: the watch starts at the list's version, a stream that the
// API server closes resumes at the last version it delivered, and a
// version the API server no longer holds makes the watcher list again.

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"testing"
	"time"
)

// holdOpen, as the last line of a script, keeps the stream open until
// the watcher ends the request. Any other script ends the stream after
// its last line, the way the API server does at timeoutSeconds.
const holdOpen = "hold"

// linger, as a line of a script, keeps the stream open a little longer
// than shortWatch, so the watch counts as one that ran.
const linger = "linger"

// watchServer is an API server for one collection. Each list answers
// the version "list-N", where N counts the lists. Each watch
// connection plays the next script of events.
type watchServer struct {
	collection string
	items      string
	scripts    [][]string

	mu        sync.Mutex
	lists     int
	listTimes []time.Time
	versions  []string
	opened    chan struct{}
}

func newWatchServer(collection, items string, scripts ...[]string) *watchServer {
	return &watchServer{
		collection: collection,
		items:      items,
		scripts:    scripts,
		opened:     make(chan struct{}, len(scripts)+1),
	}
}

func (s *watchServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != s.collection {
		http.NotFound(w, r)
		return
	}
	query := r.URL.Query()
	s.mu.Lock()
	if query.Get("watch") != "true" {
		s.lists++
		s.listTimes = append(s.listTimes, time.Now())
		version := fmt.Sprintf("list-%d", s.lists)
		s.mu.Unlock()
		fmt.Fprintf(w, `{"metadata":{"resourceVersion":%q},"items":%s}`, version, s.items)
		return
	}
	connection := len(s.versions)
	s.versions = append(s.versions, query.Get("resourceVersion"))
	s.mu.Unlock()
	s.opened <- struct{}{}

	if connection >= len(s.scripts) {
		<-r.Context().Done()
		return
	}
	for _, line := range s.scripts[connection] {
		if line == holdOpen {
			w.(http.Flusher).Flush()
			<-r.Context().Done()
			return
		}
		if line == linger {
			w.(http.Flusher).Flush()
			time.Sleep(shortWatch + 200*time.Millisecond)
			continue
		}
		fmt.Fprintln(w, line)
	}
}

// seen answers how many lists the server answered, and the version
// each watch asked for.
func (s *watchServer) seen() (int, []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lists, append([]string{}, s.versions...)
}

// awaitWatches waits until the watcher has opened count watch
// connections.
func (s *watchServer) awaitWatches(t *testing.T, count int) {
	t.Helper()
	for range count {
		select {
		case <-s.opened:
		case <-time.After(5 * time.Second):
			t.Fatalf("the watcher opened fewer than %d watches", count)
		}
	}
}

func bookmark(version string) string {
	return fmt.Sprintf(`{"type":"BOOKMARK","object":{"metadata":{"resourceVersion":%q}}}`, version)
}

func TestTheWatchStartsWhereItsLastSourceEnded(t *testing.T) {
	cases := []struct {
		name         string
		scripts      [][]string
		wantLists    int
		wantVersions []string
	}{
		{
			name:         "the first watch starts at the list's version",
			scripts:      [][]string{{holdOpen}},
			wantLists:    1,
			wantVersions: []string{"list-1"},
		},
		{
			name:         "a stream the server closed resumes at its last version",
			scripts:      [][]string{{bookmark("20")}, {holdOpen}},
			wantLists:    1,
			wantVersions: []string{"list-1", "20"},
		},
		{
			name:         "an expired version lists again",
			scripts:      [][]string{{`{"type":"ERROR","object":{"kind":"Status","code":410}}`}, {holdOpen}},
			wantLists:    2,
			wantVersions: []string{"list-1", "list-2"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			server := newWatchServer("/things", "[]", c.scripts...)
			client := testClient(t, server)
			ctx, cancel := context.WithCancel(context.Background())
			done := make(chan struct{})
			go func() {
				defer close(done)
				listThenWatch(ctx, client, "/things", "the things",
					func([]ObjectMeta) {}, func(string, ObjectMeta) {})
			}()

			server.awaitWatches(t, len(c.scripts))
			cancel()
			<-done

			lists, versions := server.seen()
			if lists != c.wantLists {
				t.Errorf("the watcher listed %d times, want %d", lists, c.wantLists)
			}
			if fmt.Sprint(versions) != fmt.Sprint(c.wantVersions) {
				t.Errorf("the watches started at %v, want %v", versions, c.wantVersions)
			}
		})
	}
}

func TestTheWatchDeliversTheListAndEachChange(t *testing.T) {
	server := newWatchServer("/things", `[{"name":"first"}]`, []string{
		`{"type":"ADDED","object":{"metadata":{"resourceVersion":"2"},"name":"second"}}`,
		`{"type":"DELETED","object":{"metadata":{"resourceVersion":"3"},"name":"first"}}`,
		holdOpen,
	})
	client := testClient(t, server)
	type thing struct {
		Name string `json:"name"`
	}
	var mu sync.Mutex
	var seen []string
	record := func(line string) {
		mu.Lock()
		defer mu.Unlock()
		seen = append(seen, line)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		listThenWatch(ctx, client, "/things", "the things",
			func(items []thing) { record(fmt.Sprintf("listed %v", items)) },
			func(event string, item thing) { record(event + " " + item.Name) })
	}()

	want := "[listed [{first}] ADDED second DELETED first]"
	deadline := time.After(5 * time.Second)
	for {
		mu.Lock()
		got := fmt.Sprint(seen)
		mu.Unlock()
		if got == want {
			break
		}
		select {
		case <-deadline:
			t.Fatalf("the watcher delivered %s, want %s", got, want)
		case <-time.After(10 * time.Millisecond):
		}
	}
	cancel()
	<-done
}

// runWatcher runs listThenWatch against a server, and returns the
// function that stops it.
func runWatcher(t *testing.T, server *watchServer) func() {
	t.Helper()
	client := testClient(t, server)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		listThenWatch(ctx, client, "/things", "the things",
			func([]ObjectMeta) {}, func(string, ObjectMeta) {})
	}()
	return func() {
		cancel()
		<-done
	}
}

// repeat builds count copies of one script, so every watch the watcher
// opens gets the same answer.
func repeat(count int, script ...string) [][]string {
	scripts := make([][]string, count)
	for index := range scripts {
		scripts[index] = script
	}
	return scripts
}

// A failed watch waits out the backoff before the next request. Without
// the wait, a fault that lasts turns into thousands of requests each
// second. The one exception is the first 410 Gone, which lists again at
// once. A watch that closes in under a second is a failure whatever it
// delivered, and an object that does not decode is an error event.
func TestAFailedWatchWaitsBeforeTheNextRequest(t *testing.T) {
	added := `{"type":"ADDED","object":{"metadata":{"resourceVersion":"2"},"name":"first"}}`
	cases := []struct {
		name        string
		script      []string
		wantLists   int
		wantWatches int
	}{
		{
			name:        "a watch that closed at once with no events",
			script:      []string{},
			wantLists:   1,
			wantWatches: 1,
		},
		{
			name:        "a watch that closed at once after an event",
			script:      []string{added},
			wantLists:   1,
			wantWatches: 1,
		},
		{
			name:        "a 410 on the watch from a fresh list",
			script:      []string{`{"type":"ERROR","object":{"kind":"Status","code":410}}`},
			wantLists:   2,
			wantWatches: 2,
		},
		{
			name:        "an error event",
			script:      []string{`{"type":"ERROR","object":{"kind":"Status","code":500}}`},
			wantLists:   1,
			wantWatches: 1,
		},
		{
			name:        "an object that does not decode",
			script:      []string{`{"type":"ADDED","object":{"metadata":{"resourceVersion":"2"},"name":42}}`},
			wantLists:   1,
			wantWatches: 1,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			server := newWatchServer("/things", "[]", repeat(100, c.script...)...)
			stop := runWatcher(t, server)

			server.awaitWatches(t, c.wantWatches)
			time.Sleep(300 * time.Millisecond)
			stop()

			lists, versions := server.seen()
			if lists != c.wantLists || len(versions) != c.wantWatches {
				t.Fatalf("in 300 ms the watcher listed %d times and opened %d watches, want %d and %d",
					lists, len(versions), c.wantLists, c.wantWatches)
			}
		})
	}
}

// A watch that ran for a second or longer resets the backoff, even when
// it ended with an error. The first watch fails at once, so the wait
// before the second list is one second and the next wait would be two.
// The second watch runs past shortWatch before its error, so the wait
// before the third list is one second again.
func TestAWatchThatRanResetsTheBackoff(t *testing.T) {
	failure := `{"type":"ERROR","object":{"kind":"Status","code":500}}`
	server := newWatchServer("/things", "[]", []string{failure}, []string{linger, failure}, []string{holdOpen})
	stop := runWatcher(t, server)
	server.awaitWatches(t, 3)
	stop()

	server.mu.Lock()
	gap := server.listTimes[2].Sub(server.listTimes[1])
	server.mu.Unlock()
	// The second watch lasts shortWatch plus 200 ms, and the wait after
	// it is one second: about 2.2 seconds. A wait of two seconds would
	// make it about 3.2.
	if gap > shortWatch+200*time.Millisecond+1500*time.Millisecond {
		t.Fatalf("the third list came %s after the second, want about %s",
			gap, shortWatch+200*time.Millisecond+watchRetry)
	}
}
