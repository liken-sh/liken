package main

// These tests cover the six collection watchers other than the Play
// watch: each one watches its own collection, and each one recovers from a
// stream that ended and from a list that failed.

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// One collection watcher: the loop under test, the path it watches, and
// the path it lists to resume.
type collectionWatcher struct {
	name      string
	watch     func(c *Client, resourceVersion string, wake chan<- struct{}, onRestart func())
	watchPath string
	listPath  string
}

// Every watcher this file drives. The Play watch is not here because
// watch_test.go covers it on its own.
var collectionWatchers = []collectionWatcher{
	{name: "remotes", watch: watchRemotes, watchPath: remotesAllPath, listPath: remotesAllPath},
	{name: "players", watch: watchPlayers, watchPath: playersPath, listPath: playersPath},
	{name: "keymaps", watch: watchKeymaps, watchPath: keymapsPath, listPath: keymapsPath},
	{name: "media preferences", watch: watchMediaPreferences, watchPath: mediaPrefsPath, listPath: mediaPrefsPath},
	{name: "playback pods", watch: watchPods, watchPath: podsAllPath, listPath: podsAllPath},
	{name: "peripherals", watch: watchPeripherals, watchPath: peripheralsPath, listPath: peripheralsPath},
}

// The server outlives the test for the reason startWatch names: a
// watcher has no stop, so every run ends with one held in a watch request.
func startCollectionWatch(t *testing.T, each collectionWatcher, api *watchAPI, from string) chan struct{} {
	t.Helper()
	server := httptest.NewServer(api.handler())
	wake := make(chan struct{}, 1)
	go each.watch(NewClient(server.URL, server.Client(), ""), from, wake, nil)
	return wake
}

func nextWatchPath(t *testing.T, api *watchAPI) string {
	t.Helper()
	select {
	case path := <-api.watchedPaths:
		return path
	case <-time.After(watchTimeout):
		t.Fatal("no watch request arrived")
		return ""
	}
}

// Each watcher reads its own collection and resumes from where the
// caller said to start.
func TestEachCollectionWatchReadsItsOwnCollection(t *testing.T) {
	for _, each := range collectionWatchers {
		t.Run(each.name, func(t *testing.T) {
			useWatchRetryPause(t)
			api := newWatchAPI()

			startCollectionWatch(t, each, api, "42")

			mustMatch(t, nextWatchRequest(t, api).Get("resourceVersion"), "42")
			mustMatch(t, nextWatchPath(t, api), each.watchPath)
		})
	}
}

// A 410 lists the collection, wakes the loop for the pass that reads
// it, and resumes the watch from the list's version. A list that fails
// waits out the backoff, and the watcher lists again.
func TestEachCollectionWatchListsAndWakesAfterAGone(t *testing.T) {
	for _, each := range collectionWatchers {
		t.Run(each.name, func(t *testing.T) {
			useWatchRetryPause(t)
			api := newWatchAPI()
			api.answersWatches(watchTurn{status: http.StatusGone})
			api.answersLists(listTurn{status: http.StatusInternalServerError}, listTurn{version: "150"})

			wake := startCollectionWatch(t, each, api, "42")

			mustMatch(t, nextWatchRequest(t, api).Get("resourceVersion"), "42")
			mustMatch(t, nextWatchPath(t, api), each.watchPath)
			mustMatch(t, nextListRequest(t, api), each.listPath)
			mustMatch(t, nextListRequest(t, api), each.listPath)

			waitForWatchWake(t, wake)
			mustMatch(t, nextWatchRequest(t, api).Get("resourceVersion"), "150")
			mustMatch(t, nextWatchPath(t, api), each.watchPath)
		})
	}
}

// media_watch_restarts_total counts a reconnect on every collection
// watcher the same way, because they all run the one loop in
// watchloop.go.
func TestEachCollectionWatchCountsOneRestartPerReconnect(t *testing.T) {
	for _, each := range collectionWatchers {
		t.Run(each.name, func(t *testing.T) {
			useWatchRetryPause(t)
			api := newWatchAPI()
			api.answersWatches(watchTurn{})

			server := httptest.NewServer(api.handler())
			wake := make(chan struct{}, 1)
			var restarts atomic.Int32
			go each.watch(NewClient(server.URL, server.Client(), ""), "42", wake,
				func() { restarts.Add(1) })

			nextWatchRequest(t, api)
			nextWatchRequest(t, api)

			mustMatch(t, restarts.Load(), int32(1))
		})
	}
}

// A watch the server refuses waits out the backoff and opens again
// from the same version, with no list.
func TestEachCollectionWatchRecoversFromARefusedWatch(t *testing.T) {
	for _, each := range collectionWatchers {
		t.Run(each.name, func(t *testing.T) {
			useWatchRetryPause(t)
			api := newWatchAPI()
			api.answersWatches(watchTurn{status: http.StatusInternalServerError})

			startCollectionWatch(t, each, api, "42")

			mustMatch(t, nextWatchRequest(t, api).Get("resourceVersion"), "42")
			mustMatch(t, nextWatchRequest(t, api).Get("resourceVersion"), "42")
			expectNoList(t, api)
		})
	}
}
