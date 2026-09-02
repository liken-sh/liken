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

	// A controller connecting produces a burst of uevents and a burst
	// of D-Bus signals, and one write covers the whole burst.
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

	// A controller that reconnects faster than the quiet window would
	// restart the wait forever. The limit keeps the loop publishing.
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

// wakes merges four sources, and each of them closing must end the
// merge. A source that closed and stayed in the select would spin the
// loop on a channel that is always ready to receive.
func TestWakesEndsWhenAnySourceCloses(t *testing.T) {
	sources := []string{"uevents", "bluez", "retries", "requests"}
	for i, closing := range sources {
		t.Run(closing, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			uevents := make(chan kernelEvent)
			bluez := make(chan struct{})
			retries := make(chan struct{})
			requests := make(chan struct{})
			out := wakes(ctx, uevents, bluez, retries, requests)

			switch i {
			case 0:
				close(uevents)
			case 1:
				close(bluez)
			case 2:
				close(retries)
			case 3:
				close(requests)
			}
			select {
			case _, ok := <-out:
				if ok {
					t.Fatal("wakes emitted after its source closed")
				}
			case <-time.After(time.Second):
				t.Fatal("wakes did not end when its source closed")
			}
		})
	}
}

func TestWakesPassesEachSourceThrough(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	uevents := make(chan kernelEvent, 1)
	bluez := make(chan struct{}, 1)
	retries := make(chan struct{}, 1)
	requests := make(chan struct{}, 1)
	out := wakes(ctx, uevents, bluez, retries, requests)

	uevents <- kernelEvent{Subsystem: "hid", Action: "add", MAC: "a0:ab:51:33:b7:12"}
	waitForWake(t, out, time.Second)
	bluez <- struct{}{}
	waitForWake(t, out, time.Second)
	retries <- struct{}{}
	waitForWake(t, out, time.Second)
	requests <- struct{}{}
	waitForWake(t, out, time.Second)
}

// The loop prints what woke it. A HID event names its controller, and
// a power supply change names none.
func TestKernelEventLine(t *testing.T) {
	cases := []struct {
		name  string
		event kernelEvent
		want  string
	}{
		{
			name:  "a HID add",
			event: kernelEvent{Subsystem: "hid", Action: "add", MAC: "a0:ab:51:33:b7:12"},
			want:  "controller A0:AB:51:33:B7:12: hid add",
		},
		{
			name:  "a battery change",
			event: kernelEvent{Subsystem: "power_supply", Action: "change"},
			want:  "kernel: power_supply change",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := kernelEventLine(c.event); got != c.want {
				t.Errorf("line = %q, want %q", got, c.want)
			}
		})
	}
}
