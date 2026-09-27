package main

// These tests cover how every collection watch recovers when its stream
// ends: which ends resume from the last version, which list again, and
// how long each one waits first.

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// One watch that ended: how it ended, and how long it ran.
type watchEnd struct {
	outcome watchOutcome
	lived   time.Duration
}

// The recovery after a run of watch ends. The base backoff is one second
// here, so each wait reads in whole seconds, and a watch that ran for a
// second or longer counts as one that lived.
func TestTheWatchRecoveryAfterEachEnd(t *testing.T) {
	short, long := 100*time.Millisecond, 5*time.Second
	cases := []struct {
		name string
		ends []watchEnd
		want watchStep
	}{
		{name: "a watch that lived and closed resumes at once",
			ends: []watchEnd{{watchClosed, long}},
			want: watchStep{}},
		{name: "a short watch that closed waits, and resumes",
			ends: []watchEnd{{watchClosed, short}},
			want: watchStep{wait: time.Second}},
		{name: "a second short close waits twice as long",
			ends: []watchEnd{{watchClosed, short}, {watchClosed, short}},
			want: watchStep{wait: 2 * time.Second}},
		{name: "the first 410 lists at once",
			ends: []watchEnd{{watchGone, short}},
			want: watchStep{relist: true}},
		{name: "a 410 on the watch from the fresh list waits, then lists",
			ends: []watchEnd{{watchGone, short}, {watchGone, short}},
			want: watchStep{wait: time.Second, relist: true}},
		{name: "a 410 after a watch that ended otherwise lists at once again",
			ends: []watchEnd{{watchGone, short}, {watchClosed, long}, {watchGone, short}},
			want: watchStep{relist: true}},
		{name: "any other error waits, then lists",
			ends: []watchEnd{{watchFailed, short}},
			want: watchStep{wait: time.Second, relist: true}},
		{name: "errors in a row wait longer each time",
			ends: []watchEnd{{watchFailed, short}, {watchFailed, short}, {watchFailed, short}},
			want: watchStep{wait: 4 * time.Second, relist: true}},
		{name: "a watch that lived resets the wait, even when it ended on an error",
			ends: []watchEnd{{watchFailed, short}, {watchFailed, short}, {watchFailed, long}},
			want: watchStep{wait: time.Second, relist: true}},
		{name: "a 410 that ends a watch that lived counts as a first 410",
			ends: []watchEnd{{watchGone, short}, {watchGone, long}},
			want: watchStep{relist: true}},
		{name: "the wait stops at its cap",
			ends: []watchEnd{{watchFailed, short}, {watchFailed, short}, {watchFailed, short},
				{watchFailed, short}, {watchFailed, short}, {watchFailed, short}, {watchFailed, short}},
			want: watchStep{wait: watchRetryCap, relist: true}},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			pauseWas := watchRetryPause.get()
			t.Cleanup(func() { watchRetryPause.set(pauseWas) })
			watchRetryPause.set(time.Second)
			var backoff watchBackoff

			var step watchStep
			for _, end := range testCase.ends {
				step = backoff.after(end.outcome, end.lived)
			}

			if step != testCase.want {
				t.Errorf("step = %+v, want %+v", step, testCase.want)
			}
		})
	}
}

// A watch that closes opens again from the last version it delivered,
// with no list: the version names the point the next watch resumes
// from, and a list would cost a read of the whole collection.
func TestAClosedWatchResumesFromItsLastVersion(t *testing.T) {
	useWatchRetryPause(t)
	api := newWatchAPI()
	api.answersWatches(watchTurn{events: []string{watchEvent("MODIFIED", "50")}})

	wake := startWatch(t, api, watchLibraries, "42")

	nextWatchRequest(t, api)
	waitForWatchWake(t, wake)
	if got := nextWatchRequest(t, api).Get("resourceVersion"); got != "50" {
		t.Errorf("the second watch resumed from %q, want the delivered 50", got)
	}
	select {
	case path := <-api.listed:
		t.Errorf("the watcher listed %q after a watch that closed", path)
	default:
	}
}

// Each case ends a watch in a way that loses the resume point, and the
// watcher lists the collection, wakes the loop, and watches from the
// list's version. A 410 arrives as a response or as an ERROR event. Any
// other ERROR event, and an event whose object does not decode, count as
// errors too.
func TestTheWatcherListsAfterAnEndThatLosesItsVersion(t *testing.T) {
	cases := []struct {
		name string
		turn watchTurn
	}{
		{name: "a 410 response", turn: watchTurn{status: http.StatusGone}},
		{name: "a 410 in the stream", turn: watchTurn{events: []string{
			`{"type":"ERROR","object":{"kind":"Status","code":410}}`}}},
		{name: "another error in the stream", turn: watchTurn{events: []string{
			`{"type":"ERROR","object":{"kind":"Status","code":500}}`}}},
		{name: "an object that does not decode", turn: watchTurn{events: []string{
			`{"type":"MODIFIED","object":{"metadata":{"resourceVersion":7}}}`}}},
		{name: "a refused watch", turn: watchTurn{status: http.StatusInternalServerError}},
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

// A server can hold a watch open for minutes after an error event, and
// every event in that time is lost to a watcher that reads on. The
// watcher closes the stream at once on each bad event, and lists.
func TestTheWatcherClosesTheStreamAtOnceOnABadEvent(t *testing.T) {
	cases := []struct {
		name  string
		event string
	}{
		{name: "a 410 in the stream", event: `{"type":"ERROR","object":{"kind":"Status","code":410}}`},
		{name: "another error in the stream", event: `{"type":"ERROR","object":{"kind":"Status","code":500}}`},
		{name: "an object that does not decode",
			event: `{"type":"MODIFIED","object":{"metadata":{"resourceVersion":7}}}`},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			useWatchRetryPause(t)
			api := newWatchAPI()
			held := make(chan struct{})
			t.Cleanup(func() { close(held) })
			api.answersWatches(watchTurn{events: []string{testCase.event}, hold: held})
			api.answersLists(listTurn{version: "150"})

			startWatch(t, api, watchLibraries, "42")

			nextWatchRequest(t, api)
			if got := nextListRequest(t, api); got != librariesPath {
				t.Errorf("listed %q, want %q", got, librariesPath)
			}
		})
	}
}

// A watch lives from the moment its 200 arrives. The dial and the wait
// for the answer's headers do not count, so a server that refuses slowly,
// or never answers, counts as a failure and the backoff grows.
func TestAWatchLivesFromItsAnswer(t *testing.T) {
	const slow = 100 * time.Millisecond
	cases := []struct {
		name   string
		status int
	}{
		{name: "a slow refusal", status: http.StatusForbidden},
		{name: "a slow 200 that ends at once", status: http.StatusOK},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				time.Sleep(slow)
				w.WriteHeader(testCase.status)
			}))
			t.Cleanup(server.Close)
			client := NewClient(server.URL, server.Client(), "")

			_, _, lived := openWatch(client, librariesPath+"?", "42", make(chan struct{}, 1))

			if lived >= slow {
				t.Errorf("lived = %s, want less than the %s the answer took", lived, slow)
			}
		})
	}
}

// A server that cannot be reached answers nothing, and its watch lived
// for no time at all.
func TestAWatchThatGotNoAnswerLivedNoTime(t *testing.T) {
	server := httptest.NewServer(http.NotFoundHandler())
	address := server.URL
	server.Close()
	client := NewClient(address, http.DefaultClient, "")

	outcome, _, lived := openWatch(client, librariesPath+"?", "42", make(chan struct{}, 1))

	if outcome != watchFailed || lived != 0 {
		t.Errorf("outcome = %v, lived = %s, want a failure that lived no time", outcome, lived)
	}
}
