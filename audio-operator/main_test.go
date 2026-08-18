package main

import (
	"context"
	"testing"
	"time"
)

// The settle tests use short windows so that the whole file runs in
// under a second. The assertions leave wide margins, because a test
// that measures a timer measures the scheduler as well.
const (
	testWindow = 40 * time.Millisecond
	testLimit  = 200 * time.Millisecond
)

func TestSettleCollapsesABurst(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	in := make(chan struct{}, 16)
	out := settle(ctx, in, testWindow, testLimit)

	// A monitor that a person plugs in produces a burst of jack
	// events, and one write must cover the whole burst.
	for range 8 {
		in <- struct{}{}
		time.Sleep(testWindow / 4)
	}
	waitForWake(t, out, testLimit)
	assertQuiet(t, out, 3*testWindow)
}

func TestSettleWaitsForQuiet(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	in := make(chan struct{}, 16)
	out := settle(ctx, in, testWindow, testLimit)

	in <- struct{}{}
	// Nothing arrives before the window passes.
	select {
	case <-out:
		t.Fatal("settle emitted before the window passed")
	case <-time.After(testWindow / 2):
	}
	waitForWake(t, out, testLimit)
}

func TestSettleEmitsUnderAConstantFlap(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	in := make(chan struct{})
	out := settle(ctx, in, testWindow, testLimit)

	// A cable that somebody wiggles changes the jack faster than the
	// quiet window, which would restart the wait forever. The limit
	// keeps the loop publishing.
	stop := make(chan struct{})
	defer close(stop)
	go func() {
		tick := time.NewTicker(testWindow / 2)
		defer tick.Stop()
		for {
			select {
			case <-stop:
				return
			case <-tick.C:
				select {
				case in <- struct{}{}:
				case <-stop:
					return
				}
			}
		}
	}()

	waitForWake(t, out, 2*testLimit)
}

func TestSettleStopsWithItsContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	in := make(chan struct{}, 1)
	out := settle(ctx, in, testWindow, testLimit)

	cancel()
	select {
	case _, ok := <-out:
		if ok {
			t.Fatal("settle emitted after its context ended")
		}
	case <-time.After(time.Second):
		t.Fatal("settle did not close its channel")
	}
}

func waitForWake(t *testing.T, out <-chan struct{}, within time.Duration) {
	t.Helper()
	select {
	case _, ok := <-out:
		if !ok {
			t.Fatal("the settle channel closed instead of emitting")
		}
	case <-time.After(within + time.Second):
		t.Fatal("settle never emitted")
	}
}

func assertQuiet(t *testing.T, out <-chan struct{}, within time.Duration) {
	t.Helper()
	select {
	case <-out:
		t.Fatal("settle emitted a second time for one burst")
	case <-time.After(within):
	}
}
