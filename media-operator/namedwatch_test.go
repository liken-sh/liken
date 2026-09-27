package main

// These tests cover the watch on one named object: it opens the
// collection under a field selector that names the object, hands each
// event's object to its owner, lists the object again after a 410
// Gone, and backs off a watch the API server refuses.

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

// watchingAPI wraps the core API with a watch: it records each watch
// request's URL and writes the events the test sends until the
// channel closes.
func watchingAPI(api *coreAPI, events <-chan string, watched chan<- *url.URL) http.Handler {
	answer := api.handler()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("watch") != "true" {
			answer.ServeHTTP(w, r)
			return
		}
		watched <- r.URL
		w.(http.Flusher).Flush()
		for event := range events {
			_, _ = io.WriteString(w, event)
			w.(http.Flusher).Flush()
		}
	})
}

// until waits for an outcome with a deadline, because the watch is
// another goroutine and a test asserts what the owner holds, not when
// the goroutine ran.
func until(t *testing.T, complaint string, done func() bool) {
	t.Helper()
	deadline := time.Now().Add(watchTimeout)
	for !done() {
		if time.Now().After(deadline) {
			t.Fatal(complaint)
		}
		time.Sleep(watchQuietSpell / 10)
	}
}

func objectEvent(t *testing.T, kind string, object any) string {
	t.Helper()
	body, err := json.Marshal(map[string]any{"type": kind, "object": object})
	mustSucceed(t, err)
	return string(body)
}

// goneEvent is how the API server says it no longer holds the version
// a watch asked for.
func goneEvent(t *testing.T) string {
	t.Helper()
	return objectEvent(t, "ERROR", map[string]any{"kind": "Status", "code": http.StatusGone})
}

// heldValue is what a test owner holds: the data value of the last
// ConfigMap the watch handed it, or "" after a delete.
type heldValue struct {
	mu    sync.Mutex
	value string
}

func (h *heldValue) changed(object json.RawMessage) error {
	var configMap ConfigMap
	if err := json.Unmarshal(object, &configMap); err != nil {
		return err
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.value = configMap.Data["value"]
	return nil
}

func (h *heldValue) removed() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.value = ""
}

func (h *heldValue) is(value string) func() bool {
	return func() bool {
		h.mu.Lock()
		defer h.mu.Unlock()
		return h.value == value
	}
}

func valueConfigMap(value, version string) *ConfigMap {
	return &ConfigMap{
		Metadata: ObjectMeta{Name: "anchor", Namespace: "liken-system", ResourceVersion: version},
		Data:     map[string]string{"value": value},
	}
}

// The watch asks for the collection under a field selector, because
// that is the request RBAC authorizes against a Role's resourceNames
// and the one the API server streams.
func TestANamedWatchOpensTheCollectionForOneName(t *testing.T) {
	api := newCoreAPI()
	events := make(chan string)
	defer close(events)
	watched := make(chan *url.URL, 1)
	held := &heldValue{}
	watch := newNamedWatch(testAPIClient(t, watchingAPI(api, events, watched)),
		"liken-system", "configmaps", "anchor", held.changed, held.removed)

	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	go watch.follow(ctx, "11")

	opened := <-watched
	mustMatch(t, opened.Path, "/api/v1/namespaces/liken-system/configmaps")
	mustMatch(t, opened.Query().Get("fieldSelector"), "metadata.name=anchor")
	mustMatch(t, opened.Query().Get("resourceVersion"), "11")
}

// Each event reaches the owner as it arrives, with no read and no
// clock between the change and the owner.
func TestANamedWatchHandsEachEventToItsOwner(t *testing.T) {
	api := newCoreAPI()
	events := make(chan string)
	defer close(events)
	watched := make(chan *url.URL, 1)
	held := &heldValue{}
	watch := newNamedWatch(testAPIClient(t, watchingAPI(api, events, watched)),
		"liken-system", "configmaps", "anchor", held.changed, held.removed)

	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	go watch.follow(ctx, "")
	<-watched

	events <- objectEvent(t, "ADDED", valueConfigMap("first", "12"))
	until(t, "the owner never took up the added object", held.is("first"))

	events <- objectEvent(t, "MODIFIED", valueConfigMap("second", "13"))
	until(t, "the owner never took up the modified object", held.is("second"))

	events <- objectEvent(t, "DELETED", valueConfigMap("second", "14"))
	until(t, "the owner kept an object the watch deleted", held.is(""))
}

// The read lists the collection under the name and answers the list's
// resourceVersion, not the object's. An object that has not changed in
// a while carries a version older than the API server's watch window,
// and a watch from it answers 410 Gone at once.
func TestANamedWatchReadAnswersTheListsVersion(t *testing.T) {
	api := newCoreAPI()
	api.seed(t, "configmaps/anchor", valueConfigMap("current", "16"))
	held := &heldValue{}
	watch := newNamedWatch(testAPIClient(t, api.handler()),
		"liken-system", "configmaps", "anchor", held.changed, held.removed)

	version, err := watch.read()

	mustSucceed(t, err)
	mustMatch(t, version, "900")
	mustMatch(t, held.is("current")(), true)
}

// A 410 Gone means the API server no longer holds the version, so the
// watch lists again and resumes from the list's version. The object's
// own version is older than the window, and a watch from it would
// answer 410 again. The list also delivers a change the lost window
// held.
func TestANamedWatchListsAgainAfterAGone(t *testing.T) {
	api := newCoreAPI()
	api.seed(t, "configmaps/anchor", valueConfigMap("current", "16"))
	events := make(chan string)
	defer close(events)
	watched := make(chan *url.URL, 2)
	held := &heldValue{}
	watch := newNamedWatch(testAPIClient(t, watchingAPI(api, events, watched)),
		"liken-system", "configmaps", "anchor", held.changed, held.removed)

	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	go watch.follow(ctx, "11")
	<-watched

	events <- goneEvent(t)
	resumed := <-watched
	mustMatch(t, resumed.Query().Get("resourceVersion"), "900")
	until(t, "the list after the gone never reached the owner", held.is("current"))
}

// A list that finds no object runs removed and answers no error,
// because an absent object is a state the owner handles, not a fault.
// It still answers the list's version, so the watch that follows
// starts where the list ended and a later create arrives as an ADDED
// event.
func TestANamedWatchReadOfAnAbsentObjectRemovesIt(t *testing.T) {
	held := &heldValue{value: "stale"}
	watch := newNamedWatch(testAPIClient(t, newCoreAPI().handler()),
		"liken-system", "configmaps", "anchor", held.changed, held.removed)

	version, err := watch.read()

	mustSucceed(t, err)
	mustMatch(t, version, "900")
	mustMatch(t, held.is("")(), true)
}

// A watch the API server refuses waits longer after each refusal, up
// to a minute, and says so once. It never sends a request and a line
// several times a second.
func TestANamedWatchBacksOffARefusal(t *testing.T) {
	refusing := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = io.WriteString(w, "the role names no such object")
	})
	held := &heldValue{}
	watch := newNamedWatch(testAPIClient(t, refusing),
		"liken-system", "configmaps", "anchor", held.changed, held.removed)

	waits := make(chan time.Duration, 8)
	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	var lines []string
	watch.report = func(line string) { lines = append(lines, line) }
	watch.pause = func(_ context.Context, wait time.Duration) bool {
		select {
		case waits <- wait:
			return true
		default:
			stop()
			return false
		}
	}

	watch.follow(ctx, "11")

	close(waits)
	var stepped []time.Duration
	for wait := range waits {
		stepped = append(stepped, wait)
	}
	mustMatchAll(t, stepped, []time.Duration{
		time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second,
		16 * time.Second, 32 * time.Second, time.Minute, time.Minute,
	})
	mustMatch(t, len(lines), 1)
	mustMatch(t, strings.Contains(lines[0], "403 Forbidden"), true)
	mustMatch(t, strings.Contains(lines[0], "the role names no such object"), true)
}

// A watch that opens after a refusal says so once. The watch that
// opened closes at once, which is a failure of its own, so the wait
// goes on growing: only a watch that ran a second or longer resets it.
func TestANamedWatchReportsItsReturn(t *testing.T) {
	var refusing sync.Mutex
	refused := true
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		refusing.Lock()
		defer refusing.Unlock()
		if refused {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		w.WriteHeader(http.StatusOK)
	})
	held := &heldValue{}
	watch := newNamedWatch(testAPIClient(t, handler),
		"liken-system", "configmaps", "anchor", held.changed, held.removed)

	var lines []string
	var waits []time.Duration
	watch.report = func(line string) { lines = append(lines, line) }
	watch.pause = func(_ context.Context, wait time.Duration) bool {
		waits = append(waits, wait)
		if len(waits) == 2 {
			refusing.Lock()
			refused = false
			refusing.Unlock()
		}
		return len(waits) < 3
	}

	watch.follow(context.Background(), "11")

	mustMatch(t, len(lines), 3)
	mustMatch(t, strings.Contains(lines[0], "403 Forbidden"), true)
	mustMatch(t, lines[1], "watching configmap liken-system/anchor again")
	mustMatch(t, lines[2],
		"watching configmap liken-system/anchor: the watch closed less than a second after it opened")
	mustMatchAll(t, waits, []time.Duration{time.Second, 2 * time.Second, 4 * time.Second})
}

// A stream that ends is opened again from the version of its last
// event, so no change between the two streams is lost and no read is
// spent.
func TestANamedWatchResumesAnEndedStream(t *testing.T) {
	watched := make(chan *url.URL, 4)
	var opens sync.Mutex
	count := 0
	ending := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		opens.Lock()
		count++
		first := count == 1
		opens.Unlock()
		if r.URL.Query().Get("watch") != "true" {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		watched <- r.URL
		if first {
			_, _ = io.WriteString(w, objectEvent(t, "MODIFIED", valueConfigMap("latest", "21")))
			return
		}
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	})
	held := &heldValue{}
	watch := newNamedWatch(testAPIClient(t, ending),
		"liken-system", "configmaps", "anchor", held.changed, held.removed)
	watch.backoffStart = time.Millisecond

	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	go watch.follow(ctx, "20")

	mustMatch(t, (<-watched).Query().Get("resourceVersion"), "20")
	mustMatch(t, (<-watched).Query().Get("resourceVersion"), "21")
	mustMatch(t, held.is("latest")(), true)
}

// An object the owner cannot use is the owner's report, and the watch
// still opens from the list's version. A list that failed on it would
// list again on a growing wait and never watch, so the next version,
// which can be valid, would not arrive.
func TestANamedWatchOpensAfterAReadTheOwnerCannotUse(t *testing.T) {
	api := newCoreAPI()
	api.seed(t, "configmaps/anchor", valueConfigMap("unusable", "16"))
	events := make(chan string)
	defer close(events)
	watched := make(chan *url.URL, 1)
	watch := newNamedWatch(testAPIClient(t, watchingAPI(api, events, watched)),
		"liken-system", "configmaps", "anchor",
		func(json.RawMessage) error { return errors.New("the value is unusable") }, func() {})
	lines := make(chan string, 4)
	watch.report = func(line string) { lines <- line }

	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	go watch.follow(ctx, "")

	mustMatch(t, within(t, watched).Query().Get("resourceVersion"), "900")
	mustMatch(t, within(t, lines), "reading configmap liken-system/anchor: the value is unusable")
}

// within receives one value with a deadline, so a watch that never
// arrives fails the test instead of holding it.
func within[T any](t *testing.T, values <-chan T) T {
	t.Helper()
	select {
	case value := <-values:
		return value
	case <-time.After(watchTimeout):
		t.Fatal("nothing arrived")
		var zero T
		return zero
	}
}
