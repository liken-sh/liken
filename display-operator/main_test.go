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

	// One monitor plugged in produces a burst of uevents, and one
	// write must cover the whole burst. Every ResourceSlice write
	// wakes every DRA-pending pod in the cluster.
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

	// A cable that flaps faster than the quiet window would restart
	// the wait forever. The limit keeps the loop publishing.
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

func TestWakesCarriesAnEventThrough(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	events := make(chan drmEvent, 1)
	out := wakes(ctx, events, nil)

	events <- drmEvent{Action: "change", DevPath: "/devices/pci0000:00/0000:00:02.0/drm/card1"}
	waitForWake(t, out, time.Second)
}

func TestWakesCarriesARetryThrough(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	retries := make(chan struct{}, 1)
	out := wakes(ctx, nil, retries)

	// A write that failed schedules one more pass through the same
	// channel every other source uses, so the retry never blocks the
	// loop that watches the compositor.
	retries <- struct{}{}
	waitForWake(t, out, time.Second)
}

func TestEventsEnded(t *testing.T) {
	// A closed wake channel means nothing while the process is
	// stopping, which is how every shutdown ends.
	stopping, cancel := context.WithCancel(context.Background())
	cancel()
	if err := eventsEnded(stopping); err != nil {
		t.Errorf("a shutdown reported an error: %v", err)
	}
	// Any other time the kernel's uevent socket closed under a running
	// operator, and an operator that kept going would publish only on
	// the backstop tick, minutes after a monitor moved.
	if err := eventsEnded(context.Background()); err == nil {
		t.Error("the event stream ended under a running operator with no error")
	}
}

func TestEnvOr(t *testing.T) {
	if got := envOr("DISPLAY_OPERATOR_UNSET_IN_TESTS", "fallback"); got != "fallback" {
		t.Fatalf("envOr = %q", got)
	}
	t.Setenv("DISPLAY_OPERATOR_SET_IN_TESTS", "value")
	if got := envOr("DISPLAY_OPERATOR_SET_IN_TESTS", "fallback"); got != "value" {
		t.Fatalf("envOr = %q", got)
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
