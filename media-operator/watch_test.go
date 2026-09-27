package main

// These tests cover what the watcher does with each kind of event,
// and how it comes back from a stream that ends and from a server
// that refuses the watch.

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
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

// A backoff in milliseconds instead of seconds, restored when the test
// ends. Every watch counts as one that ran, so a stream that a test
// ends opens again at once, the way a watch that ran for minutes does.
func useWatchRetryPause(t *testing.T) {
	t.Helper()
	startWas, maxWas, lifeWas := watchBackoffStart, watchBackoffMax, watchMinLife
	t.Cleanup(func() { watchBackoffStart, watchBackoffMax, watchMinLife = startWas, maxWas, lifeWas })
	watchBackoffStart, watchBackoffMax, watchMinLife = 5*time.Millisecond, 5*time.Millisecond, 0
}

// One watch request's answer: a status other than 200, or the event
// lines the stream carries before it ends. A turn with a hold
// channel keeps the stream open until the test closes the channel.
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
	turns        chan watchTurn
	lists        chan listTurn
	watched      chan url.Values
	watchedPaths chan string
	listed       chan string
	parked       chan struct{}
}

func newWatchAPI() *watchAPI {
	return &watchAPI{
		turns:        make(chan watchTurn, 8),
		lists:        make(chan listTurn, 8),
		watched:      make(chan url.Values, 8),
		watchedPaths: make(chan string, 8),
		listed:       make(chan string, 8),
		parked:       make(chan struct{}),
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

// The two requests the watcher makes against one path, told apart by
// the watch parameter the way the API server tells them apart.
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
// rest of the run, which leaves the watcher in the request instead
// of reconnecting.
func (a *watchAPI) serveWatch(w http.ResponseWriter, r *http.Request) {
	a.watched <- r.URL.Query()
	a.watchedPaths <- r.URL.Path
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
	_ = json.NewEncoder(w).Encode(PlayList{Metadata: ListMeta{ResourceVersion: turn.version}})
}

// The server outlives the test on purpose. watchPlays has no stop,
// so every test ends with its watcher held in a watch request; a
// server that closed would set that watcher reconnecting for the rest
// of the run.
func startWatch(t *testing.T, api *watchAPI, from string) chan struct{} {
	t.Helper()
	server := httptest.NewServer(api.handler())
	wake := make(chan struct{}, 1)
	go watchPlays(NewClient(server.URL, server.Client(), ""), from, wake, nil)
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

// One line of a pod watch stream, carrying the phase the pod reports so
// the reader can tell a failed pod from a running one.
func podWatchEvent(kind, version, phase string) string {
	return `{"type":"` + kind + `","object":{"metadata":{"resourceVersion":"` + version +
		`"},"status":{"phase":"` + phase + `"}}}`
}

func startPodWatch(t *testing.T, api *watchAPI, from string) chan struct{} {
	t.Helper()
	server := httptest.NewServer(api.handler())
	wake := make(chan struct{}, 1)
	go watchPods(NewClient(server.URL, server.Client(), ""), from, wake, nil)
	return wake
}

// A device-taint eviction or a lost node deletes the playback pod, and the
// pod watch wakes the loop for the pass that recreates it. The watch
// resumes from its version and narrows to the operator's own playback
// pods by label.
func TestThePodWatchWakesTheLoopOnADelete(t *testing.T) {
	useWatchRetryPause(t)
	api := newWatchAPI()
	api.answersWatches(watchTurn{events: []string{podWatchEvent("DELETED", "50", "Running")}, hold: api.parked})

	wake := startPodWatch(t, api, "42")

	first := nextWatchRequest(t, api)
	if got := first.Get("resourceVersion"); got != "42" {
		t.Errorf("the first watch resumed from %q, want 42", got)
	}
	if got := first.Get("labelSelector"); got != "media.liken.sh/component=playback" {
		t.Errorf("labelSelector = %q, want the playback selector", got)
	}
	waitForWatchWake(t, wake)
}

// A failed pod is a crashed player, which the operator recreates, so it
// wakes the loop. A running pod's routine update needs no pass, so it
// wakes nothing and the backstop tick is the only thing that would read
// it.
func TestThePodWatchWakesOnFailedButNotOnRunning(t *testing.T) {
	cases := []struct {
		name  string
		phase string
		wakes bool
	}{
		{name: "a running update wakes nothing", phase: "Running", wakes: false},
		{name: "a failed update wakes the loop", phase: "Failed", wakes: true},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			useWatchRetryPause(t)
			api := newWatchAPI()
			api.answersWatches(watchTurn{
				events: []string{podWatchEvent("MODIFIED", "50", one.phase)},
				hold:   api.parked,
			})

			wake := startPodWatch(t, api, "42")
			nextWatchRequest(t, api)
			if one.wakes {
				waitForWatchWake(t, wake)
			} else {
				expectNoWatchWake(t, wake)
			}
		})
	}
}

func TestAChangedPlayOnTheStreamWakesTheLoopOnce(t *testing.T) {
	useWatchRetryPause(t)
	api := newWatchAPI()
	api.answersWatches(watchTurn{events: []string{watchEvent("MODIFIED", "50")}, hold: api.parked})

	wake := startWatch(t, api, "42")

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

// A bookmark carries a resourceVersion and nothing to reconcile, so
// it moves the resume point and wakes nothing. The next watch opens
// from it with no list.
func TestABookmarkMovesTheResumePointAndWakesNothing(t *testing.T) {
	useWatchRetryPause(t)
	api := newWatchAPI()
	api.answersWatches(watchTurn{events: []string{watchEvent("BOOKMARK", "99")}})

	wake := startWatch(t, api, "42")

	mustMatch(t, nextWatchRequest(t, api).Get("resourceVersion"), "42")
	mustMatch(t, nextWatchRequest(t, api).Get("resourceVersion"), "99")
	expectNoWatchWake(t, wake)
	expectNoList(t, api)
}

// expectNoList fails the test when the watcher lists in a short
// spell.
func expectNoList(t *testing.T, api *watchAPI) {
	t.Helper()
	select {
	case path := <-api.listed:
		t.Fatalf("the watcher listed %s", path)
	case <-time.After(watchQuietSpell):
	}
}

// A watch that ends, and a watch the server refuses, open again from
// the last version delivered. Neither one lists, because the version
// is still good, and a 410 says when it is not.
func TestTheWatcherResumesWithNoListAfterAWatchEnds(t *testing.T) {
	cases := []struct {
		name  string
		first watchTurn
		want  string
	}{
		{name: "the stream ends", first: watchTurn{events: []string{watchEvent("MODIFIED", "50")}}, want: "50"},
		{name: "the server refuses the watch", first: watchTurn{status: http.StatusInternalServerError}, want: "42"},
	}
	for _, each := range cases {
		t.Run(each.name, func(t *testing.T) {
			useWatchRetryPause(t)
			api := newWatchAPI()
			api.answersWatches(each.first)

			startWatch(t, api, "42")

			mustMatch(t, nextWatchRequest(t, api).Get("resourceVersion"), "42")
			mustMatch(t, nextWatchRequest(t, api).Get("resourceVersion"), each.want)
			expectNoList(t, api)
		})
	}
}

// A 410 and an error event both list the collection, wake the loop for
// the pass that reads it, and open the next watch from the list's
// version. So does an event whose object does not decode, because a
// watch from the same version would deliver it again.
func TestTheWatcherListsAndWakesAfterAWatchFails(t *testing.T) {
	cases := []struct {
		name  string
		first watchTurn
	}{
		{name: "a 410 response", first: watchTurn{status: http.StatusGone}},
		{name: "a 410 event", first: watchTurn{events: []string{`{"type":"ERROR","object":{"code":410}}`}}},
		{name: "an error event", first: watchTurn{events: []string{`{"type":"ERROR","object":{"code":500}}`}}},
		{name: "an event that does not decode", first: watchTurn{events: []string{`{"type":"MODIFIED","object":{"metadata":7}}`}}},
	}
	for _, each := range cases {
		t.Run(each.name, func(t *testing.T) {
			useWatchRetryPause(t)
			api := newWatchAPI()
			api.answersWatches(each.first)
			api.answersLists(listTurn{version: "150"})

			wake := startWatch(t, api, "42")

			mustMatch(t, nextWatchRequest(t, api).Get("resourceVersion"), "42")
			mustMatch(t, nextListRequest(t, api), playsPath)
			waitForWatchWake(t, wake)
			mustMatch(t, nextWatchRequest(t, api).Get("resourceVersion"), "150")
		})
	}
}

// media_watch_restarts_total counts a watch that ended and that this
// loop opened again, so the callback runs once per reconnect and not
// on the loop's first connection.
func TestAWatchRestartCountsEachReconnectAndNotTheFirstConnect(t *testing.T) {
	useWatchRetryPause(t)
	api := newWatchAPI()
	api.answersWatches(watchTurn{}, watchTurn{})

	server := httptest.NewServer(api.handler())
	wake := make(chan struct{}, 1)
	var restarts atomic.Int32
	go watchPlays(NewClient(server.URL, server.Client(), ""), "1", wake, func() { restarts.Add(1) })

	nextWatchRequest(t, api)
	nextWatchRequest(t, api)
	nextWatchRequest(t, api)

	mustMatch(t, restarts.Load(), int32(2))
}
