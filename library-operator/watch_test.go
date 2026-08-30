package main

// These tests cover what a watcher does with each kind of event, and
// how it comes back from a stream that ends and from a server that
// refuses the watch.

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"
)

// The bounds on every wait in this file: long enough that a loaded
// machine still passes, short enough that a broken watcher fails in
// seconds.
const (
	watchTimeout    = 2 * time.Second
	watchQuietSpell = 50 * time.Millisecond
)

// A reconnect in milliseconds instead of seconds, restored when the
// test ends.
func useWatchRetryPause(t *testing.T) {
	t.Helper()
	pauseWas := watchRetryPause
	t.Cleanup(func() { watchRetryPause = pauseWas })
	watchRetryPause = 5 * time.Millisecond
}

// One watch request's answer: a status other than 200, or the event
// lines the stream carries before it ends. A turn with a hold channel
// keeps the stream open until the test closes the channel.
type watchTurn struct {
	status int
	events []string
	hold   chan struct{}
}

// One list request's answer: either a status other than 200 or the
// collection's resourceVersion.
type listTurn struct {
	status  int
	version string
}

// An API server for the watch: it answers each watch and each list
// from a script the test loads, and records what every request asked
// for.
type watchAPI struct {
	turns   chan watchTurn
	lists   chan listTurn
	watched chan url.Values
	listed  chan string
	parked  chan struct{}
}

func newWatchAPI() *watchAPI {
	return &watchAPI{
		turns:   make(chan watchTurn, 8),
		lists:   make(chan listTurn, 8),
		watched: make(chan url.Values, 8),
		listed:  make(chan string, 8),
		parked:  make(chan struct{}),
	}
}

func (a *watchAPI) answersWatches(turns ...watchTurn) {
	for _, turn := range turns {
		a.turns <- turn
	}
}

func (a *watchAPI) answersLists(turns ...listTurn) {
	for _, turn := range turns {
		a.lists <- turn
	}
}

// The two requests a watcher makes against one path, told apart by the
// watch parameter the way the API server tells them apart.
func (a *watchAPI) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("watch") == "true" {
			a.serveWatch(w, r)
			return
		}
		a.serveList(w, r)
	})
}

// A watch the script does not answer holds its stream open for the
// rest of the run, which leaves the watcher in the request instead of
// reconnecting.
func (a *watchAPI) serveWatch(w http.ResponseWriter, r *http.Request) {
	a.watched <- r.URL.Query()
	turn := watchTurn{hold: a.parked}
	select {
	case scripted := <-a.turns:
		turn = scripted
	default:
	}
	if turn.status != 0 {
		w.WriteHeader(turn.status)
		return
	}
	for _, event := range turn.events {
		_, _ = io.WriteString(w, event+"\n")
		w.(http.Flusher).Flush()
	}
	if turn.hold != nil {
		<-turn.hold
	}
}

// A list answers with a collection's resourceVersion and no items,
// which is all a watcher reads from one. The same body decodes as a
// LibraryList and as a PodList, so one handler serves both watchers.
func (a *watchAPI) serveList(w http.ResponseWriter, r *http.Request) {
	a.listed <- r.URL.Path
	turn := listTurn{version: "1"}
	select {
	case scripted := <-a.lists:
		turn = scripted
	default:
	}
	if turn.status != 0 {
		w.WriteHeader(turn.status)
		return
	}
	_ = json.NewEncoder(w).Encode(LibraryList{Metadata: ListMeta{ResourceVersion: turn.version}})
}

// The server outlives the test on purpose. A watcher has no stop, so
// every test ends with its watcher held in a watch request; a server
// that closed would set that watcher reconnecting, and its next read
// of watchRetryPause would race the write that restores it.
func startWatch(t *testing.T, api *watchAPI, watcher func(*Client, string, chan<- struct{}), from string) chan struct{} {
	t.Helper()
	server := httptest.NewServer(api.handler())
	wake := make(chan struct{}, 1)
	go watcher(NewClient(server.URL, server.Client(), ""), from, wake)
	return wake
}

// One line of a watch stream, in the shape the API server sends.
func watchEvent(kind, version string) string {
	return `{"type":"` + kind + `","object":{"metadata":{"resourceVersion":"` + version + `"}}}`
}

func nextWatchRequest(t *testing.T, api *watchAPI) url.Values {
	t.Helper()
	select {
	case query := <-api.watched:
		return query
	case <-time.After(watchTimeout):
		t.Fatal("no watch request arrived")
		return nil
	}
}

func nextListRequest(t *testing.T, api *watchAPI) string {
	t.Helper()
	select {
	case path := <-api.listed:
		return path
	case <-time.After(watchTimeout):
		t.Fatal("no list request arrived")
		return ""
	}
}

func waitForWatchWake(t *testing.T, wake <-chan struct{}) {
	t.Helper()
	select {
	case <-wake:
	case <-time.After(watchTimeout):
		t.Fatal("no wake reached the loop")
	}
}

func expectNoWatchWake(t *testing.T, wake <-chan struct{}) {
	t.Helper()
	select {
	case <-wake:
		t.Fatal("a wake reached the loop")
	case <-time.After(watchQuietSpell):
	}
}

// A Library that changes wakes the loop once. The pass that answers
// the wake reads the whole collection, so a second wake would say
// nothing the first does not.
func TestAChangedLibraryOnTheStreamWakesTheLoopOnce(t *testing.T) {
	useWatchRetryPause(t)
	api := newWatchAPI()
	api.answersWatches(watchTurn{events: []string{watchEvent("MODIFIED", "50")}, hold: api.parked})

	wake := startWatch(t, api, watchLibraries, "42")

	first := nextWatchRequest(t, api)
	if got := first.Get("resourceVersion"); got != "42" {
		t.Errorf("the first watch resumed from %q, want 42", got)
	}
	if got := first.Get("allowWatchBookmarks"); got != "true" {
		t.Errorf("allowWatchBookmarks = %q, want true", got)
	}
	waitForWatchWake(t, wake)
	expectNoWatchWake(t, wake)
}

// Every scanner pod event wakes the loop, because a Library is Ready
// only while its pod runs with every container ready: the update that
// turns a container ready is as much a change to report as a delete.
// The label selector keeps the stream to this operator's own pods.
func TestThePodWatchWakesOnEveryScannerPodEvent(t *testing.T) {
	cases := []struct {
		name  string
		event string
	}{
		{name: "the pod was created", event: "ADDED"},
		{name: "the pod turned ready", event: "MODIFIED"},
		{name: "the pod was removed", event: "DELETED"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			useWatchRetryPause(t)
			api := newWatchAPI()
			api.answersWatches(watchTurn{
				events: []string{watchEvent(testCase.event, "50")},
				hold:   api.parked,
			})

			wake := startWatch(t, api, watchPods, "42")

			first := nextWatchRequest(t, api)
			if got := first.Get("labelSelector"); got != "app.kubernetes.io/name=library-scanner" {
				t.Errorf("labelSelector = %q, want the scanner selector", got)
			}
			waitForWatchWake(t, wake)
		})
	}
}

// A bookmark carries a resourceVersion and nothing to reconcile, so it
// moves the resume point and wakes nothing. The list after the stream
// fails here, because a list that answered would give the watcher a
// newer version and hide what the bookmark did.
func TestABookmarkMovesTheResumePointAndWakesNothing(t *testing.T) {
	useWatchRetryPause(t)
	api := newWatchAPI()
	release := make(chan struct{})
	api.answersWatches(
		watchTurn{events: []string{watchEvent("BOOKMARK", "99")}},
		watchTurn{hold: release},
	)
	api.answersLists(
		listTurn{status: http.StatusInternalServerError},
		listTurn{version: "150"},
	)

	wake := startWatch(t, api, watchLibraries, "42")

	if got := nextWatchRequest(t, api).Get("resourceVersion"); got != "42" {
		t.Errorf("the first watch resumed from %q, want 42", got)
	}
	nextListRequest(t, api)
	if got := nextWatchRequest(t, api).Get("resourceVersion"); got != "99" {
		t.Errorf("the second watch resumed from %q, want the bookmark's 99", got)
	}
	expectNoWatchWake(t, wake)

	// The run ends on a list that answers, which is the one thing in
	// this test that wakes the loop.
	close(release)
	nextListRequest(t, api)
	waitForWatchWake(t, wake)
	if got := nextWatchRequest(t, api).Get("resourceVersion"); got != "150" {
		t.Errorf("the third watch resumed from %q, want the list's 150", got)
	}
}

// Each case ends one watch connection without a change to report, and
// the recovery is the same: list the collection, resume from its
// version, and wake the loop for the pass that reads it. An ERROR
// event is a 410 Gone in the stream, which the API server sends when
// it no longer holds the version the watch asked for.
func TestTheWatcherListsAndWakesAfterAStreamThatCarriedNothing(t *testing.T) {
	cases := []struct {
		name string
		turn watchTurn
	}{
		{name: "the server refused the watch", turn: watchTurn{status: http.StatusInternalServerError}},
		{name: "the stream ended", turn: watchTurn{}},
		{name: "the version is gone", turn: watchTurn{events: []string{watchEvent("ERROR", "0")}}},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			useWatchRetryPause(t)
			api := newWatchAPI()
			api.answersWatches(testCase.turn)
			api.answersLists(listTurn{version: "150"})

			wake := startWatch(t, api, watchLibraries, "42")

			nextWatchRequest(t, api)
			if got := nextListRequest(t, api); got != librariesPath {
				t.Errorf("listed %q, want %q", got, librariesPath)
			}
			waitForWatchWake(t, wake)
			if got := nextWatchRequest(t, api).Get("resourceVersion"); got != "150" {
				t.Errorf("the second watch resumed from %q, want the list's 150", got)
			}
		})
	}
}

// A Catalog change wakes the loop, so a Library waiting on its
// namespace's Catalog proceeds without a backstop tick's delay. The
// watcher lists the Catalogs to resume and wakes on the list.
func TestTheCatalogWatchListsAndWakes(t *testing.T) {
	useWatchRetryPause(t)
	api := newWatchAPI()
	api.answersWatches(watchTurn{})
	api.answersLists(listTurn{version: "150"})

	wake := startWatch(t, api, watchCatalogs, "42")

	nextWatchRequest(t, api)
	if got := nextListRequest(t, api); got != catalogsPath {
		t.Errorf("listed %q, want %q", got, catalogsPath)
	}
	waitForWatchWake(t, wake)
	if got := nextWatchRequest(t, api).Get("resourceVersion"); got != "150" {
		t.Errorf("the second watch resumed from %q, want the list's 150", got)
	}
}

// A list that fails leaves the resume point where it was and the
// watcher tries again, so an API server that is briefly away costs
// reconnects and no missed change.
func TestTheWatcherRetriesAfterAListThatFails(t *testing.T) {
	useWatchRetryPause(t)
	api := newWatchAPI()
	api.answersWatches(watchTurn{}, watchTurn{})
	api.answersLists(listTurn{status: http.StatusInternalServerError}, listTurn{version: "150"})

	wake := startWatch(t, api, watchPods, "42")

	nextWatchRequest(t, api)
	if got := nextListRequest(t, api); got != podsAllPath {
		t.Errorf("listed %q, want %q", got, podsAllPath)
	}
	if got := nextWatchRequest(t, api).Get("resourceVersion"); got != "42" {
		t.Errorf("the watch after the failed list resumed from %q, want the unchanged 42", got)
	}
	nextListRequest(t, api)
	waitForWatchWake(t, wake)
	if got := nextWatchRequest(t, api).Get("resourceVersion"); got != "150" {
		t.Errorf("the next watch resumed from %q, want the list's 150", got)
	}
}
