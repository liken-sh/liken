package main

// These tests cover the faults in the organization's list of watch-loop
// scenarios that watch_test.go does not: a refused grant, a server that
// does not answer the dial, a slow 200, and a 410 that arrives as the
// HTTP response instead of an event.

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// A watch the API server refuses with a 403 backs off, and the backoff
// grows while the refusal lasts. The refusal is not a gap in the stream,
// so the watch that the grant finally lets through starts at the list's
// version, with no second list. The first refusal waits one second and
// the second waits two.
func TestARefusedWatchBacksOffUntilTheGrantArrives(t *testing.T) {
	server := newWatchServer("/things", "[]", []string{forbid}, []string{forbid}, []string{holdOpen})
	stop := runWatcher(t, server)
	server.awaitWatches(t, 3)
	stop()

	lists, versions := server.seen()
	if lists != 1 || fmt.Sprint(versions) != "[list-1 list-1 list-1]" {
		t.Fatalf("the watcher listed %d times and watched from %v, want 1 list and [list-1 list-1 list-1]", lists, versions)
	}
	server.mu.Lock()
	gap := server.watchTimes[2].Sub(server.watchTimes[1])
	server.mu.Unlock()
	if want := 2 * watchRetry; gap < want-300*time.Millisecond {
		t.Fatalf("the third watch came %s after the second, want about %s", gap, want)
	}
}

// failingTransport fails every request the way a dial to a dead API
// server fails, and records when each request came.
type failingTransport struct {
	mu    sync.Mutex
	times []time.Time
	tried chan struct{}
}

func (f *failingTransport) RoundTrip(*http.Request) (*http.Response, error) {
	f.mu.Lock()
	f.times = append(f.times, time.Now())
	f.mu.Unlock()
	f.tried <- struct{}{}
	return nil, errors.New("dial tcp 10.43.0.1:443: connect: connection refused")
}

// A dead API server fails every list, and the wait between the
// attempts doubles: one second, then two.
func TestADeadServerGrowsTheBackoff(t *testing.T) {
	transport := &failingTransport{tried: make(chan struct{}, 8)}
	credentials := t.TempDir()
	if err := os.WriteFile(filepath.Join(credentials, "token"), []byte("test-token"), 0o600); err != nil {
		t.Fatal(err)
	}
	client := NewClient("https://10.43.0.1:443", &http.Client{Transport: transport}, credentials)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		listThenWatch(ctx, client, "/things", "the things", func([]ObjectMeta) {}, func(string, ObjectMeta) {})
	}()
	for range 3 {
		select {
		case <-transport.tried:
		case <-time.After(5 * time.Second):
			t.Fatal("the watcher stopped trying the dead server")
		}
	}
	cancel()
	<-done

	transport.mu.Lock()
	first, second := transport.times[1].Sub(transport.times[0]), transport.times[2].Sub(transport.times[1])
	transport.mu.Unlock()
	if first < watchRetry-300*time.Millisecond || second < 2*watchRetry-300*time.Millisecond {
		t.Fatalf("the waits were %s and %s, want about %s and %s", first, second, watchRetry, 2*watchRetry)
	}
}

// The wait doubles after each failure until it reaches watchRetryLimit,
// and stays there.
func TestTheBackoffStopsAtItsCap(t *testing.T) {
	cases := []struct {
		delay time.Duration
		want  time.Duration
	}{
		{delay: watchRetry, want: 2 * watchRetry},
		{delay: 40 * time.Second, want: watchRetryLimit},
		{delay: watchRetryLimit, want: watchRetryLimit},
	}
	ended, cancel := context.WithCancel(context.Background())
	cancel()
	for _, c := range cases {
		t.Run(c.delay.String(), func(t *testing.T) {
			if got := pauseWatch(ended, c.delay); got != c.want {
				t.Fatalf("the wait after %s is %s, want %s", c.delay, got, c.want)
			}
		})
	}
}

// A 200 that arrives late is still a watch that runs, and every event
// on its stream reaches the watcher. The stream then runs past
// shortWatch and closes cleanly, so the next watch opens from the last
// version, with no list.
func TestASlowAcceptDeliversEveryEvent(t *testing.T) {
	server := newWatchServer("/things", "[]", []string{
		acceptSlowly,
		`{"type":"ADDED","object":{"metadata":{"resourceVersion":"2"},"name":"first"}}`,
		`{"type":"ADDED","object":{"metadata":{"resourceVersion":"3"},"name":"second"}}`,
		linger,
	}, []string{holdOpen})
	client := testClient(t, server)
	var mu sync.Mutex
	var names []string
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		listThenWatch(ctx, client, "/things", "the things", func([]ObjectMeta) {}, func(_ string, meta ObjectMeta) {
			mu.Lock()
			defer mu.Unlock()
			names = append(names, meta.Name)
		})
	}()
	server.awaitWatches(t, 2)
	cancel()
	<-done

	lists, versions := server.seen()
	mu.Lock()
	delivered := fmt.Sprint(names)
	mu.Unlock()
	if delivered != "[first second]" || lists != 1 || fmt.Sprint(versions) != "[list-1 3]" {
		t.Fatalf("the watcher delivered %s, listed %d times, and watched from %v; want [first second], 1, and [list-1 3]",
			delivered, lists, versions)
	}
}

// A 410 that arrives as the HTTP response, and not as an event, lists
// again at once. The watch from that list works, so the watcher lists
// once more and no more.
func TestA410ResponseListsAgainAtOnce(t *testing.T) {
	server := newWatchServer("/things", "[]", []string{expire}, []string{holdOpen})
	stop := runWatcher(t, server)
	server.awaitWatches(t, 2)
	stop()

	lists, versions := server.seen()
	if lists != 2 || fmt.Sprint(versions) != "[list-1 list-2]" {
		t.Fatalf("the watcher listed %d times and watched from %v, want 2 lists and [list-1 list-2]", lists, versions)
	}
	server.mu.Lock()
	gap := server.listTimes[1].Sub(server.listTimes[0])
	server.mu.Unlock()
	if gap > 800*time.Millisecond {
		t.Fatalf("the second list came %s after the first, want at once", gap)
	}
}

// A 410 response that takes longer than shortWatch is still a watch
// that never ran. The first 410 lists at once, the second waits one
// second, and the third waits two. A backoff that the slow answer reset
// would make the last two waits the same.
func TestASlow410StillGrowsTheBackoff(t *testing.T) {
	scripts := append(repeat(3, expireSlowly), []string{holdOpen})
	server := newWatchServer("/things", "[]", scripts...)
	stop := runWatcher(t, server)
	server.awaitWatches(t, 4)
	stop()

	server.mu.Lock()
	second, third := server.listTimes[2].Sub(server.listTimes[1]), server.listTimes[3].Sub(server.listTimes[2])
	server.mu.Unlock()
	slow := shortWatch + 200*time.Millisecond
	if second < slow+watchRetry-300*time.Millisecond || third < slow+2*watchRetry-300*time.Millisecond {
		t.Fatalf("the lists came %s and %s apart, want about %s and %s",
			second, third, slow+watchRetry, slow+2*watchRetry)
	}
}
