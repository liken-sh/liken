package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// namedObjects is an API server that holds one collection of named
// objects. It answers a get, a listing by field selector, a watch
// that delivers each change, a create, and an update, which is every
// request the certificate and anchor loops make.
type namedObjects struct {
	*httptest.Server
	t *testing.T

	mu       sync.Mutex
	version  int
	objects  map[string]map[string]any
	watchers []namedWatcher
	// Every change, in order, as the watch line it makes and the
	// version it made, so a watch that starts at an older version
	// first gets the changes it missed, the way the API server's watch
	// cache replays them.
	history []namedChange
	// The resourceVersion each watch asked for, in order, so a test
	// reads where a watch resumed.
	asked []string
}

type namedChange struct {
	name    string
	version int
	line    string
}

// One open watch: the name its field selector names, and the lines
// still to be written to it.
type namedWatcher struct {
	name   string
	events chan string
}

func newNamedObjects(t *testing.T) *namedObjects {
	t.Helper()
	objects := &namedObjects{t: t, objects: map[string]map[string]any{}}
	objects.Server = httptest.NewServer(http.HandlerFunc(objects.serve))
	t.Cleanup(objects.Close)
	return objects
}

func (o *namedObjects) client() *Client {
	return NewClient(o.URL, o.Client(), "")
}

// put stores an object under its name, the way a writer's create or
// update lands, and tells every open watch.
func (o *namedObjects) put(object any) {
	o.t.Helper()
	raw, err := json.Marshal(object)
	if err != nil {
		o.t.Fatal(err)
	}
	var held map[string]any
	if err := json.Unmarshal(raw, &held); err != nil {
		o.t.Fatal(err)
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	name := held["metadata"].(map[string]any)["name"].(string)
	kind := "MODIFIED"
	if _, there := o.objects[name]; !there {
		kind = "ADDED"
	}
	o.store(name, held)
	o.announce(kind, held)
}

// remove deletes an object and tells every open watch.
func (o *namedObjects) remove(name string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	held := o.objects[name]
	delete(o.objects, name)
	o.version++
	held["metadata"].(map[string]any)["resourceVersion"] = strconv.Itoa(o.version)
	o.announce("DELETED", held)
}

// expire ends every open watch with 410 Gone, the answer to a version
// the API server no longer holds.
func (o *namedObjects) expire() {
	o.mu.Lock()
	defer o.mu.Unlock()
	for _, watcher := range o.watchers {
		watcher.events <- `{"type":"ERROR","object":{"kind":"Status","code":410}}`
	}
}

// hangUp ends every open watch with no error, the way the API server
// ends a watch at its timeout.
func (o *namedObjects) hangUp() {
	o.mu.Lock()
	defer o.mu.Unlock()
	for _, watcher := range o.watchers {
		watcher.events <- ""
	}
}

func (o *namedObjects) watchesAsked() []string {
	o.mu.Lock()
	defer o.mu.Unlock()
	return append([]string(nil), o.asked...)
}

func (o *namedObjects) watching() int {
	o.mu.Lock()
	defer o.mu.Unlock()
	return len(o.watchers)
}

// Held under mu.
func (o *namedObjects) store(name string, held map[string]any) {
	o.version++
	held["metadata"].(map[string]any)["resourceVersion"] = strconv.Itoa(o.version)
	o.objects[name] = held
}

// Held under mu.
func (o *namedObjects) announce(kind string, held map[string]any) {
	line, _ := json.Marshal(map[string]any{"type": kind, "object": held})
	name := held["metadata"].(map[string]any)["name"].(string)
	o.history = append(o.history, namedChange{name: name, version: o.version, line: string(line)})
	for _, watcher := range o.watchers {
		if watcher.name == name {
			watcher.events <- string(line)
		}
	}
}

func (o *namedObjects) serve(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", jsonMediaType)
	segments := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	last := segments[len(segments)-1]
	named := last != "secrets" && last != "configmaps"
	switch {
	case r.Method == http.MethodGet && r.URL.Query().Get("watch") == "true":
		o.stream(w, r)
	case r.Method == http.MethodGet && named:
		o.mu.Lock()
		held, there := o.objects[last]
		o.mu.Unlock()
		if !there {
			w.WriteHeader(http.StatusNotFound)
			fmt.Fprint(w, `{"kind":"Status","code":404}`)
			return
		}
		_ = json.NewEncoder(w).Encode(held)
	case r.Method == http.MethodGet:
		name := strings.TrimPrefix(r.URL.Query().Get("fieldSelector"), "metadata.name=")
		o.mu.Lock()
		items := []any{}
		if held, there := o.objects[name]; there {
			items = append(items, held)
		}
		list := map[string]any{"metadata": map[string]any{"resourceVersion": strconv.Itoa(o.version)}, "items": items}
		o.mu.Unlock()
		_ = json.NewEncoder(w).Encode(list)
	default:
		var held map[string]any
		body, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(body, &held); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		name := held["metadata"].(map[string]any)["name"].(string)
		o.mu.Lock()
		kind := "MODIFIED"
		if _, there := o.objects[name]; !there {
			kind = "ADDED"
		}
		o.store(name, held)
		o.announce(kind, held)
		o.mu.Unlock()
		_ = json.NewEncoder(w).Encode(held)
	}
}

func (o *namedObjects) stream(w http.ResponseWriter, r *http.Request) {
	events := make(chan string, 64)
	name := strings.TrimPrefix(r.URL.Query().Get("fieldSelector"), "metadata.name=")
	asked := r.URL.Query().Get("resourceVersion")
	from, _ := strconv.Atoi(asked)
	o.mu.Lock()
	o.asked = append(o.asked, asked)
	for _, change := range o.history {
		if change.name == name && change.version > from {
			events <- change.line
		}
	}
	o.watchers = append(o.watchers, namedWatcher{name: name, events: events})
	o.mu.Unlock()
	defer func() {
		o.mu.Lock()
		defer o.mu.Unlock()
		for i, watcher := range o.watchers {
			if watcher.events == events {
				o.watchers = append(o.watchers[:i], o.watchers[i+1:]...)
				break
			}
		}
	}()
	w.WriteHeader(http.StatusOK)
	w.(http.Flusher).Flush()
	for {
		select {
		case <-r.Context().Done():
			return
		case line := <-events:
			if line == "" {
				return
			}
			fmt.Fprintln(w, line)
			w.(http.Flusher).Flush()
			if strings.Contains(line, `"ERROR"`) {
				return
			}
		}
	}
}

// eventually waits for a condition a loop reaches on a goroutine of
// its own, and fails the test when five seconds pass first.
func eventually(t *testing.T, what string, reached func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !reached() {
		if time.Now().After(deadline) {
			t.Fatalf("%s did not happen within five seconds", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func configMapNamed(name, value string) ConfigMap {
	return ConfigMap{Metadata: objectMeta{Name: name}, Data: map[string]string{"value": value}}
}

// What a watch on one ConfigMap has seen, in order: each value, and
// "gone" for an object that does not exist.
type seenValues struct {
	mu     sync.Mutex
	values []string
}

func (s *seenValues) see(held *ConfigMap) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if held == nil {
		s.values = append(s.values, "gone")
		return
	}
	s.values = append(s.values, held.Data["value"])
}

func (s *seenValues) last() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.values) == 0 {
		return ""
	}
	return s.values[len(s.values)-1]
}

func watchOneConfigMap(t *testing.T, objects *namedObjects) *seenValues {
	t.Helper()
	seen := &seenValues{}
	ctx, stop := context.WithCancel(context.Background())
	t.Cleanup(stop)
	go watchNamed(ctx, objects.client(), "/api/v1/namespaces/test/configmaps", "tracked", "the test ConfigMap", seen.see)
	eventually(t, "the watch opening", func() bool { return objects.watching() > 0 })
	return seen
}

// The listing answers the object as it stands, and each change after
// it arrives on the watch: an update, a removal, and a new object.
func TestTheWatchSeesEachChangeToTheObject(t *testing.T) {
	objects := newNamedObjects(t)
	objects.put(configMapNamed("tracked", "first"))
	objects.put(configMapNamed("other", "ignored"))
	seen := watchOneConfigMap(t, objects)
	eventually(t, "the listing", func() bool { return seen.last() == "first" })

	objects.put(configMapNamed("tracked", "second"))
	eventually(t, "the update", func() bool { return seen.last() == "second" })
	objects.remove("tracked")
	eventually(t, "the removal", func() bool { return seen.last() == "gone" })
	objects.put(configMapNamed("tracked", "third"))
	eventually(t, "the new object", func() bool { return seen.last() == "third" })
}

// An object that does not exist at the listing is seen as gone.
func TestTheWatchSeesAnAbsentObjectAsGone(t *testing.T) {
	objects := newNamedObjects(t)
	seen := watchOneConfigMap(t, objects)

	eventually(t, "the listing", func() bool { return seen.last() == "gone" })
}

// 410 Gone ends the watch, and the loop lists again and watches from
// the listing's version, which it would not reach by resuming.
func TestAnExpiredWatchListsAgain(t *testing.T) {
	objects := newNamedObjects(t)
	objects.put(configMapNamed("tracked", "first"))
	seen := watchOneConfigMap(t, objects)
	eventually(t, "the listing", func() bool { return seen.last() == "first" })

	objects.expire()
	eventually(t, "the second watch", func() bool { return len(objects.watchesAsked()) == 2 && objects.watching() == 1 })
	objects.put(configMapNamed("tracked", "second"))

	eventually(t, "the update after the new listing", func() bool { return seen.last() == "second" })
}

// A watch the API server ends at its timeout resumes at the last
// version it delivered, with no new listing.
func TestAWatchThatTimesOutResumes(t *testing.T) {
	objects := newNamedObjects(t)
	objects.put(configMapNamed("tracked", "first"))
	seen := watchOneConfigMap(t, objects)
	objects.put(configMapNamed("tracked", "second"))
	eventually(t, "the update", func() bool { return seen.last() == "second" })

	objects.hangUp()

	eventually(t, "the second watch", func() bool { return len(objects.watchesAsked()) == 2 })
	if got := objects.watchesAsked(); got[1] != "2" {
		t.Errorf("the watches asked for versions %v, want the second to resume at 2", got)
	}
}

// A watch that lived under a second is a failure, whatever it
// delivered, and the loop waits before it opens the next one. So a
// server or a proxy that ends every watch at once gets a few requests
// and not a tight loop of them. An ERROR event other than 410 Gone
// waits too, before the listing that follows it.
func TestAShortWatchWaitsBeforeTheNext(t *testing.T) {
	cases := []struct {
		name   string
		answer string
	}{
		{name: "delivered an event", answer: `{"type":"MODIFIED","object":{"metadata":{"name":"tracked","resourceVersion":"%d"}}}`},
		{name: "closed empty", answer: ""},
		{name: "an error event", answer: `{"type":"ERROR","object":{"kind":"Status","code":500,"metadata":{"resourceVersion":"%d"}}}`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var requests atomic.Int64
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				n := requests.Add(1)
				if r.URL.Query().Get("watch") != "true" {
					fmt.Fprint(w, `{"metadata":{"resourceVersion":"1"},"items":[]}`)
					return
				}
				if c.answer != "" {
					fmt.Fprintf(w, c.answer, n+1)
				}
			}))
			t.Cleanup(server.Close)
			ctx, stop := context.WithTimeout(t.Context(), 500*time.Millisecond)
			defer stop()

			watchNamed(ctx, NewClient(server.URL, server.Client(), ""), "/api/v1/namespaces/test/configmaps",
				"tracked", "the test ConfigMap", func(*ConfigMap) {})

			if got := requests.Load(); got > 3 {
				t.Errorf("the loop sent %d requests in half a second, want it to wait between watches", got)
			}
		})
	}
}

// A watch that lived a second or more ran. When it ends with a
// connection reset, as a load balancer ends a long connection, the next
// watch opens at once and resumes at the last version it delivered,
// with no new listing and no wait.
func TestAWatchThatLivedResumesAtOnceAfterAReset(t *testing.T) {
	var mu sync.Mutex
	var lists int
	var asked []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("watch") != "true" {
			mu.Lock()
			lists++
			mu.Unlock()
			fmt.Fprint(w, `{"metadata":{"resourceVersion":"1"},"items":[]}`)
			return
		}
		mu.Lock()
		asked = append(asked, r.URL.Query().Get("resourceVersion"))
		first := len(asked) == 1
		mu.Unlock()
		if !first {
			<-r.Context().Done()
			return
		}
		fmt.Fprint(w, `{"type":"ADDED","object":{"metadata":{"name":"tracked","resourceVersion":"2"}}}`)
		w.(http.Flusher).Flush()
		time.Sleep(1100 * time.Millisecond)
		if conn, _, err := http.NewResponseController(w).Hijack(); err == nil {
			_ = conn.Close()
		}
	}))
	t.Cleanup(server.Close)
	ctx, stop := context.WithTimeout(t.Context(), 1600*time.Millisecond)
	defer stop()

	watchNamed(ctx, NewClient(server.URL, server.Client(), ""), "/api/v1/namespaces/test/configmaps",
		"tracked", "the test ConfigMap", func(*ConfigMap) {})

	mu.Lock()
	defer mu.Unlock()
	if lists != 1 || len(asked) != 2 || asked[1] != "2" {
		t.Errorf("the loop listed %d times and watched from %v, want one listing and a second watch from 2",
			lists, asked)
	}
}

// A watch the API server answers with 410 Gone as the response, and
// not as an event, names a version it no longer holds. Resuming there
// would ask for the same version forever, so the loop lists again at
// once.
func TestAWatchRefusedWithGoneListsAgain(t *testing.T) {
	var mu sync.Mutex
	var lists int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if r.URL.Query().Get("watch") != "true" {
			lists++
			fmt.Fprintf(w, `{"metadata":{"resourceVersion":"%d"},"items":[]}`, lists)
			return
		}
		if r.URL.Query().Get("resourceVersion") == "1" {
			w.WriteHeader(http.StatusGone)
			fmt.Fprint(w, `{"kind":"Status","code":410}`)
			return
		}
	}))
	t.Cleanup(server.Close)
	ctx, stop := context.WithTimeout(t.Context(), 300*time.Millisecond)
	defer stop()

	watchNamed(ctx, NewClient(server.URL, server.Client(), ""), "/api/v1/namespaces/test/configmaps",
		"tracked", "the test ConfigMap", func(*ConfigMap) {})

	mu.Lock()
	defer mu.Unlock()
	if lists != 2 {
		t.Errorf("the loop listed %d times, want a second listing at once after the 410", lists)
	}
}
