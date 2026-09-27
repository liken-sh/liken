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

// watchServer is an API server for one collection. Each list answers
// the version "list-N", where N counts the lists. Each watch
// connection plays the next script of events.
type watchServer struct {
	collection string
	items      string
	scripts    [][]string

	mu       sync.Mutex
	lists    int
	versions []string
	opened   chan struct{}
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

// A watch that the API server closes at once with no events is a
// failure, not a stream that ran to its timeout. Without the backoff
// the watcher opens the next watch at once, and a server that answers
// every watch that way takes thousands of requests each second.
func TestAnEmptyShortWatchWaitsBeforeTheNextOne(t *testing.T) {
	empty := make([][]string, 100)
	for index := range empty {
		empty[index] = []string{}
	}
	server := newWatchServer("/things", "[]", empty...)
	client := testClient(t, server)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		listThenWatch(ctx, client, "/things", "the things",
			func([]ObjectMeta) {}, func(string, ObjectMeta) {})
	}()

	server.awaitWatches(t, 1)
	time.Sleep(300 * time.Millisecond)
	cancel()
	<-done

	if _, versions := server.seen(); len(versions) != 1 {
		t.Fatalf("the watcher opened %d watches in 300 ms, want 1", len(versions))
	}
}
