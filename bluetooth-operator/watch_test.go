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

// refuseSlowly, as the first line of a script, answers the watch with
// a 500 a little later than shortWatch, so the refusal outlasts the
// time a watch must run to reset the backoff.
const refuseSlowly = "refuse slowly"

// resetConnection, as a line of a script, drops the connection in the
// middle of the stream, so the watcher's read fails with an error that
// is not a clean end of the stream.
const resetConnection = "reset"

// linger, as a line of a script, keeps the stream open a little longer
// than shortWatch, so the watch counts as one that ran.
const linger = "linger"

// forbid, as the first line of a script, answers the watch with a 403
// at once, the way an API server answers a grant that is missing.
const forbid = "forbid"

// expire, as the first line of a script, answers the watch with a 410
// response at once, and expireSlowly answers it a little later than
// shortWatch.
const (
	expire       = "expire"
	expireSlowly = "expire slowly"
)

// acceptSlowly, as the first line of a script, waits a little longer
// than shortWatch before the 200, and then plays the rest of the
// script.
const acceptSlowly = "accept slowly"

// pause, as a line of a script, holds the stream open until the test
// calls release, and then plays the rest of the script.
const pause = "pause"

// watchServer is an API server for one collection. Each list answers
// the version "list-N", where N counts the lists. Each watch
// connection plays the next script of events.
type watchServer struct {
	collection string
	items      string
	scripts    [][]string

	mu         sync.Mutex
	lists      int
	listTimes  []time.Time
	watchTimes []time.Time
	versions   []string
	selectors  []string
	holding    int
	opened     chan struct{}
	released   chan struct{}
}

func newWatchServer(collection, items string, scripts ...[]string) *watchServer {
	return &watchServer{
		collection: collection,
		items:      items,
		scripts:    scripts,
		opened:     make(chan struct{}, len(scripts)+1),
		released:   make(chan struct{}, 1),
	}
}

func (s *watchServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != s.collection {
		http.NotFound(w, r)
		return
	}
	query := r.URL.Query()
	s.mu.Lock()
	s.selectors = append(s.selectors, query.Get("labelSelector"))
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
	s.watchTimes = append(s.watchTimes, time.Now())
	s.mu.Unlock()
	s.opened <- struct{}{}

	if connection >= len(s.scripts) {
		<-r.Context().Done()
		return
	}
	for _, line := range s.scripts[connection] {
		if line == holdOpen {
			w.(http.Flusher).Flush()
			s.hold(1)
			<-r.Context().Done()
			s.hold(-1)
			return
		}
		if line == forbid {
			http.Error(w, "the service account may not watch this collection", http.StatusForbidden)
			return
		}
		if line == expire || line == expireSlowly {
			if line == expireSlowly {
				time.Sleep(shortWatch + 200*time.Millisecond)
			}
			http.Error(w, "too old resource version", http.StatusGone)
			return
		}
		if line == pause {
			w.(http.Flusher).Flush()
			select {
			case <-s.released:
			case <-r.Context().Done():
				return
			}
			continue
		}
		if line == acceptSlowly {
			time.Sleep(shortWatch + 200*time.Millisecond)
			continue
		}
		if line == refuseSlowly {
			time.Sleep(shortWatch + 200*time.Millisecond)
			http.Error(w, "the server is overloaded", http.StatusInternalServerError)
			return
		}
		if line == resetConnection {
			w.(http.Flusher).Flush()
			connection, _, err := w.(http.Hijacker).Hijack()
			if err == nil {
				connection.Close()
			}
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

// release lets a paused stream play the rest of its script.
func (s *watchServer) release() { s.released <- struct{}{} }

// hold counts the streams the server holds open.
func (s *watchServer) hold(change int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.holding += change
}

// held answers how many streams the server holds open, and the label
// selector of each request, lists and watches in the order they came.
func (s *watchServer) held() (int, []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.holding, append([]string{}, s.selectors...)
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

// A watch that fails while the API server still holds its stream open
// closes the stream at once. A real server holds a stream open until
// timeoutSeconds, which is minutes, and a watcher that read the rest of
// that stream before it listed again would miss every change until
// then.
func TestAFailedWatchThatStaysOpenListsAgainAfterTheBackoff(t *testing.T) {
	cases := []struct {
		name  string
		event string
	}{
		{
			name:  "an object that does not decode",
			event: `{"type":"ADDED","object":{"metadata":{"resourceVersion":"2"},"name":42}}`,
		},
		{
			name:  "an error event",
			event: `{"type":"ERROR","object":{"kind":"Status","code":500}}`,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			server := newWatchServer("/things", "[]", []string{c.event, holdOpen}, []string{holdOpen})
			stop := runWatcher(t, server)
			defer stop()

			server.awaitWatches(t, 2)

			if lists, _ := server.seen(); lists != 2 {
				t.Fatalf("the watcher listed %d times, want 2", lists)
			}
		})
	}
}

// A watch's life starts when the server accepts it, not when the
// request begins. A refusal that takes longer than shortWatch is still
// a watch that never ran, so the backoff grows. The first refusal waits
// one second, and the second waits two: the gap between the second and
// the third watch is the slow refusal plus two seconds, about 3.2
// seconds, and a reset backoff would make it about 2.2.
func TestASlowRefusalStillGrowsTheBackoff(t *testing.T) {
	server := newWatchServer("/things", "[]", []string{refuseSlowly}, []string{refuseSlowly}, []string{holdOpen})
	stop := runWatcher(t, server)
	server.awaitWatches(t, 3)
	stop()

	server.mu.Lock()
	gap := server.watchTimes[2].Sub(server.watchTimes[1])
	server.mu.Unlock()
	if want := shortWatch + 200*time.Millisecond + 2*watchRetry; gap < want-300*time.Millisecond {
		t.Fatalf("the third watch came %s after the second, want about %s", gap, want)
	}
}

// A watch that ran for a second or longer clears the mark of a list
// made at once after a 410. So a 410 that ends such a watch is a first
// 410 again, and lists at once. The second list comes after the first
// 410, and the third comes after the second watch's life of about 1.2
// seconds. A wait of one second before the third list would make the
// gap about 2.2 seconds.
func TestA410AfterAWatchThatRanListsAtOnce(t *testing.T) {
	expired := `{"type":"ERROR","object":{"kind":"Status","code":410}}`
	server := newWatchServer("/things", "[]", []string{expired}, []string{linger, expired}, []string{holdOpen})
	stop := runWatcher(t, server)
	server.awaitWatches(t, 3)
	stop()

	server.mu.Lock()
	gap := server.listTimes[2].Sub(server.listTimes[1])
	server.mu.Unlock()
	if limit := shortWatch + 700*time.Millisecond; gap > limit {
		t.Fatalf("the third list came %s after the second, want under %s", gap, limit)
	}
}

// A connection that drops in the middle of a stream loses no event the
// watcher has not read, so the next watch resumes at the last version
// the stream delivered, and the watcher does not list again.
func TestADroppedConnectionResumesAtTheLastVersion(t *testing.T) {
	server := newWatchServer("/things", "[]", []string{bookmark("7"), linger, resetConnection}, []string{holdOpen})
	stop := runWatcher(t, server)
	server.awaitWatches(t, 2)
	stop()

	lists, versions := server.seen()
	if lists != 1 || fmt.Sprint(versions) != "[list-1 7]" {
		t.Fatalf("the watcher listed %d times and watched from %v, want 1 list and [list-1 7]", lists, versions)
	}
}
