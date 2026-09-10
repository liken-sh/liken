package main

// The watch as the loop reads it: one connection's events, and the
// relist that resumes after the stream ends.

import (
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

// watchStream is a watch stream as the API server writes it.
func watchStream(events ...string) *http.Response {
	return &http.Response{Body: io.NopCloser(strings.NewReader(strings.Join(events, "\n")))}
}

func TestReadWatchStreamMovesTheResumeVersion(t *testing.T) {
	const (
		added    = `{"type":"ADDED","object":{"metadata":{"name":"theater","resourceVersion":"10"}}}`
		bookmark = `{"type":"BOOKMARK","object":{"metadata":{"resourceVersion":"11"}}}`
		failure  = `{"type":"ERROR","object":{"metadata":{"resourceVersion":"12"}}}`
		later    = `{"type":"MODIFIED","object":{"metadata":{"name":"theater","resourceVersion":"99"}}}`
	)
	cases := []struct {
		name    string
		stream  *http.Response
		version string
		wakes   int
	}{
		{"a bookmark wakes nothing", watchStream(bookmark), "11", 0},
		{"an ordinary event wakes the loop", watchStream(added), "10", 1},
		{"an error ends the stream", watchStream(added, failure, later), "10", 1},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			wake := make(chan struct{}, 1)

			mustMatch(t, readWatchStream(one.stream, "1", wake), one.version)
			mustMatch(t, len(wake), one.wakes)
		})
	}
}

// startWatch shortens watchRetryPause for the length of one test, and
// restores it only after the watch goroutine has stopped reading it.
func startWatch(t *testing.T, api *fakeAPI, resourceVersion string, wake chan struct{}) {
	t.Helper()
	was := watchRetryPause
	t.Cleanup(func() { watchRetryPause = was })
	watchRetryPause = 5 * time.Millisecond

	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		watchReceivers(t.Context(), api.client, resourceVersion, wake, testMetrics(t))
	}()
	t.Cleanup(func() {
		select {
		case <-stopped:
		case <-time.After(testTimeout):
			t.Error("the watch did not stop")
		}
	})
}

func waitForWake(t *testing.T, wake <-chan struct{}) {
	t.Helper()
	select {
	case <-wake:
	case <-time.After(testTimeout):
		t.Fatal("the watch woke nothing")
	}
}

// An event on an open stream is a wake, and the loop that answers it
// reads the whole collection.
func TestAnEventOnTheStreamWakesTheLoop(t *testing.T) {
	api := startFakeAPI(t)
	api.setReceivers()
	api.queueWatchEvents(false, `{"type":"ADDED","object":{"metadata":{"name":"theater","resourceVersion":"10"}}}`)
	wake := make(chan struct{}, 1)

	startWatch(t, api, "1", wake)

	mustMatch(t, waitForString(t, api.watched), "1")
	waitForWake(t, wake)
}

// A stream that ends re-lists, wakes the loop, and resumes the next
// watch from the version the list carried.
func TestAnEndedStreamRelistsAndResumesFromTheListsVersion(t *testing.T) {
	api := startFakeAPI(t)
	api.setReceivers()
	api.queueWatchEvents(true)
	wake := make(chan struct{}, 1)

	startWatch(t, api, "0", wake)

	mustMatch(t, waitForString(t, api.watched), "0")
	waitForWake(t, wake)
	mustMatch(t, waitForString(t, api.watched), "1")
}

// A list that fails after a stream ends is not fatal. The watch tries
// again.
func TestAFailedRelistKeepsTheWatchTrying(t *testing.T) {
	api := startFakeAPI(t)
	api.setReceivers()
	api.queueWatchEvents(true)
	api.breakTheList()
	wake := make(chan struct{}, 1)

	startWatch(t, api, "0", wake)

	mustMatch(t, waitForString(t, api.watched), "0")
	mustMatch(t, waitForString(t, api.watched), "0")
}
