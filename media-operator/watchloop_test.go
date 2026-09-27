package main

// These tests cover the recovery every hand-written watch shares: where
// the next watch starts, when the loop lists again, and when it waits.
// The loop runs against a script of watch outcomes and a clock the
// script moves, so each case reads as the requests the loop made.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

// scriptedWatch is one watch the script answers: how long it stayed
// open, and how it ended.
type scriptedWatch struct {
	lived time.Duration
	end   watchEnd
}

// runScript runs a loop from the version given over the scripted
// watches and lists, and answers every step it took: "list", "open
// <version>", and "pause <wait>". The run ends when the script has no
// watch left to answer.
func runScript(t *testing.T, from string, lists []error, watches []scriptedWatch) []string {
	t.Helper()
	var steps []string
	clock := time.Unix(0, 0)
	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	loop := watchLoop{
		subject:      "plays",
		backoffStart: time.Second,
		backoffMax:   time.Minute,
		minLife:      time.Second,
		now:          func() time.Time { return clock },
		report:       func(string) {},
		pause: func(_ context.Context, wait time.Duration) bool {
			steps = append(steps, fmt.Sprintf("pause %s", wait))
			return true
		},
		list: func() (string, error) {
			steps = append(steps, "list")
			if len(lists) == 0 {
				return "900", nil
			}
			err := lists[0]
			lists = lists[1:]
			return "900", err
		},
		open: func(_ context.Context, version string, live func()) watchEnd {
			steps = append(steps, "open "+version)
			if len(watches) == 0 {
				stop()
				return watchEnd{version: version}
			}
			next := watches[0]
			watches = watches[1:]
			if next.end.opened {
				live()
			}
			clock = clock.Add(next.lived)
			return next.end
		},
	}
	loop.run(ctx, from)
	return steps
}

func TestAWatchLoopRecoversEachEnding(t *testing.T) {
	gone := watchEnd{version: "42", opened: true, gone: true}
	cases := []struct {
		name    string
		from    string
		lists   []error
		watches []scriptedWatch
		want    []string
	}{
		{
			name:    "a watch that ran a second or longer opens again at once from its last version",
			from:    "42",
			watches: []scriptedWatch{{lived: time.Minute, end: watchEnd{version: "50", opened: true}}},
			want:    []string{"open 42", "open 50"},
		},
		{
			name:    "a watch that closed under a second waits before it opens again from its last version",
			from:    "42",
			watches: []scriptedWatch{{lived: time.Millisecond, end: watchEnd{version: "50", opened: true}}},
			want:    []string{"open 42", "pause 1s", "open 50"},
		},
		{
			name: "short watches wait longer each time",
			from: "42",
			watches: []scriptedWatch{
				{lived: time.Millisecond, end: watchEnd{version: "42", opened: true}},
				{lived: time.Millisecond, end: watchEnd{version: "42", opened: true}},
			},
			want: []string{"open 42", "pause 1s", "open 42", "pause 2s", "open 42"},
		},
		{
			name:    "a 410 lists again at once",
			from:    "42",
			watches: []scriptedWatch{{end: gone}},
			want:    []string{"open 42", "list", "open 900"},
		},
		{
			name:    "a 410 on the watch from the fresh list waits before the next list",
			from:    "42",
			watches: []scriptedWatch{{end: gone}, {end: gone}, {end: gone}},
			want: []string{
				"open 42", "list", "open 900",
				"pause 1s", "list", "open 900",
				"pause 2s", "list", "open 900",
			},
		},
		{
			name: "a 410 after a watch that ended some other way lists again at once",
			from: "42",
			watches: []scriptedWatch{
				{end: gone},
				{lived: time.Minute, end: watchEnd{version: "901", opened: true}},
				{end: gone},
			},
			want: []string{"open 42", "list", "open 900", "open 901", "list", "open 900"},
		},
		{
			name: "a 410 that ends a watch that ran a second or longer counts as a first 410",
			from: "42",
			watches: []scriptedWatch{
				{end: gone},
				{lived: time.Minute, end: watchEnd{version: "950", opened: true, gone: true}},
			},
			want: []string{"open 42", "list", "open 900", "list", "open 900"},
		},
		{
			name:    "an error event waits before the list",
			from:    "42",
			watches: []scriptedWatch{{end: watchEnd{version: "42", opened: true, failed: true, reason: "a bad event"}}},
			want:    []string{"open 42", "pause 1s", "list", "open 900"},
		},
		{
			name:    "a refused watch waits and opens again from the same version",
			from:    "42",
			watches: []scriptedWatch{{end: watchEnd{version: "42", reason: "403 Forbidden"}}},
			want:    []string{"open 42", "pause 1s", "open 42"},
		},
		{
			name: "a watch that ran a second or longer resets the wait, even when it failed",
			from: "42",
			watches: []scriptedWatch{
				{end: watchEnd{version: "42", reason: "refused"}},
				{end: watchEnd{version: "42", reason: "refused"}},
				{lived: time.Minute, end: watchEnd{version: "60", opened: true, failed: true, reason: "a bad event"}},
			},
			want: []string{"open 42", "pause 1s", "open 42", "pause 2s", "open 42", "pause 1s", "list", "open 900"},
		},
		{
			name: "a watch that never opened does not reset the wait, however long it took",
			from: "42",
			watches: []scriptedWatch{
				{lived: 10 * time.Second, end: watchEnd{version: "42", reason: "timeout awaiting response headers"}},
				{lived: 10 * time.Second, end: watchEnd{version: "42", reason: "timeout awaiting response headers"}},
				{lived: 10 * time.Second, end: watchEnd{version: "42", reason: "timeout awaiting response headers"}},
			},
			want: []string{"open 42", "pause 1s", "open 42", "pause 2s", "open 42", "pause 4s", "open 42"},
		},
		{
			name:  "a loop with no version lists first, and a failed list waits",
			from:  "",
			lists: []error{errors.New("refused")},
			want:  []string{"list", "pause 1s", "list", "open 900"},
		},
	}
	for _, each := range cases {
		t.Run(each.name, func(t *testing.T) {
			mustMatchAll(t, runScript(t, each.from, each.lists, each.watches), each.want)
		})
	}
}

// The restart callback counts a watch the loop opened again, and not
// its first watch.
func TestAWatchLoopCountsEachReopen(t *testing.T) {
	restarts := 0
	opens := 0
	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	loop := watchLoop{
		subject:      "plays",
		backoffStart: time.Second,
		backoffMax:   time.Minute,
		minLife:      time.Second,
		now:          time.Now,
		report:       func(string) {},
		pause:        func(context.Context, time.Duration) bool { return true },
		list:         func() (string, error) { return "900", nil },
		restarted:    func() { restarts++ },
		open: func(_ context.Context, version string, _ func()) watchEnd {
			opens++
			if opens == 3 {
				stop()
			}
			return watchEnd{version: version, opened: true}
		},
	}

	loop.run(ctx, "42")

	mustMatch(t, restarts, 2)
}

// decodeEvents reads a stream body the way one watch does and answers
// how the stream ended.
func decodeEvents(body string) watchEnd {
	return readWatchEvents(strings.NewReader(body), "42", func(string, []byte) error { return nil })
}

func TestAWatchStreamEndsOnEachKindOfEvent(t *testing.T) {
	cases := []struct {
		name string
		body string
		want watchEnd
	}{
		{
			name: "a stream that closes keeps the last version it delivered",
			body: watchEvent("MODIFIED", "50") + "\n" + watchEvent("BOOKMARK", "60") + "\n",
			want: watchEnd{version: "60", opened: true},
		},
		{
			name: "a 410 in an ERROR event is gone",
			body: `{"type":"ERROR","object":{"kind":"Status","code":410}}`,
			want: watchEnd{version: "42", opened: true, gone: true},
		},
		{
			name: "any other ERROR event is a failure",
			body: `{"type":"ERROR","object":{"kind":"Status","code":500,"message":"etcd is down"}}`,
			want: watchEnd{version: "42", opened: true, failed: true, reason: "the API server sent an error: 500 etcd is down"},
		},
		{
			name: "a line that is not JSON is a failure",
			body: watchEvent("MODIFIED", "50") + "\n{not json\n",
			want: watchEnd{version: "50", opened: true, failed: true, reason: "an event did not decode"},
		},
		{
			name: "an object whose metadata does not decode is a failure",
			body: `{"type":"MODIFIED","object":{"metadata":"a string"}}`,
			want: watchEnd{version: "42", opened: true, failed: true, reason: "an event did not decode"},
		},
	}
	for _, each := range cases {
		t.Run(each.name, func(t *testing.T) {
			got := decodeEvents(each.body)
			mustMatch(t, got.version, each.want.version)
			mustMatch(t, got.opened, each.want.opened)
			mustMatch(t, got.gone, each.want.gone)
			mustMatch(t, got.failed, each.want.failed)
			mustMatch(t, strings.HasPrefix(got.reason, each.want.reason), true)
		})
	}
}

// An object the owner cannot read is a failure too, so the loop lists
// again after the backoff and does not resume past the event.
func TestAWatchStreamFailsOnAnObjectTheOwnerCannotRead(t *testing.T) {
	got := readWatchEvents(strings.NewReader(watchEvent("MODIFIED", "50")), "42",
		func(string, []byte) error { return io.ErrUnexpectedEOF })

	mustMatch(t, got.failed, true)
}

// The answers to the request itself: a 410 is gone, and any other
// status is a watch that did not open.
func TestAWatchRequestAnswersItsStatus(t *testing.T) {
	cases := []struct {
		name   string
		status int
		opened bool
		gone   bool
	}{
		{name: "gone", status: http.StatusGone, opened: true, gone: true},
		{name: "forbidden", status: http.StatusForbidden},
	}
	for _, each := range cases {
		t.Run(each.name, func(t *testing.T) {
			handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(each.status)
			})
			client := testAPIClient(t, handler)

			got := openWatch(context.Background(), client, playsPath+"?watch=true", "42", func() {},
				func(string, []byte) error { return nil })

			mustMatch(t, got.opened, each.opened)
			mustMatch(t, got.gone, each.gone)
			mustMatch(t, got.version, "42")
		})
	}
}
