package main

import (
	"context"
	"testing"
	"testing/synctest"
	"time"
)

// The settle tests run in synctest bubbles with the production window
// and limit, so each test checks the exact moment that settle emits.

// emitted reports whether settle has sent a wake, and receives it.
// Call it after synctest.Wait, so that every timer that is due has
// fired.
func emitted(t *testing.T, out <-chan struct{}) bool {
	t.Helper()
	select {
	case _, ok := <-out:
		if !ok {
			t.Fatal("the settle channel closed instead of emitting")
		}
		return true
	default:
		return false
	}
}

// A controller that connects causes a burst of uevents and D-Bus
// signals. settle sends one wake, one window after the last event of
// the burst.
func TestSettleCollapsesABurst(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		in := make(chan struct{}, 16)
		out := settle(t.Context(), in, settleWindow, settleLimit)

		for range 8 {
			in <- struct{}{}
			time.Sleep(settleWindow / 4)
		}
		time.Sleep(settleWindow - settleWindow/4 - time.Nanosecond)
		synctest.Wait()
		if emitted(t, out) {
			t.Fatal("settle emitted before the burst was quiet for a window")
		}
		time.Sleep(time.Nanosecond)
		synctest.Wait()
		if !emitted(t, out) {
			t.Fatal("settle did not emit a window after the burst")
		}
		time.Sleep(settleLimit)
		synctest.Wait()
		if emitted(t, out) {
			t.Fatal("settle emitted a second time for one burst")
		}
	})
}

// A single event produces a wake one window later, and not before.
func TestSettleWaitsForQuiet(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		in := make(chan struct{}, 16)
		out := settle(t.Context(), in, settleWindow, settleLimit)

		in <- struct{}{}
		time.Sleep(settleWindow - time.Nanosecond)
		synctest.Wait()
		if emitted(t, out) {
			t.Fatal("settle emitted before the window passed")
		}
		time.Sleep(time.Nanosecond)
		synctest.Wait()
		if !emitted(t, out) {
			t.Fatal("settle did not emit when the window passed")
		}
	})
}

// Events that arrive faster than the window keep restarting it, so
// settle sends a wake when the limit has passed since the first event.
func TestSettleEmitsUnderAConstantFlap(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		in := make(chan struct{})
		out := settle(t.Context(), in, settleWindow, settleLimit)

		go func() {
			tick := time.NewTicker(settleWindow / 2)
			defer tick.Stop()
			for {
				select {
				case <-t.Context().Done():
					return
				case in <- struct{}{}:
				}
				select {
				case <-t.Context().Done():
					return
				case <-tick.C:
				}
			}
		}()

		time.Sleep(settleLimit - time.Nanosecond)
		synctest.Wait()
		if emitted(t, out) {
			t.Fatal("settle emitted before the limit while the events went on")
		}
		time.Sleep(time.Nanosecond)
		synctest.Wait()
		if !emitted(t, out) {
			t.Fatal("settle did not emit at the limit")
		}
	})
}

func TestSettleStopsWithItsContext(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		in := make(chan struct{}, 1)
		out := settle(ctx, in, settleWindow, settleLimit)

		cancel()
		synctest.Wait()
		select {
		case _, ok := <-out:
			if ok {
				t.Fatal("settle emitted after its context ended")
			}
		default:
			t.Fatal("settle did not close its channel")
		}
	})
}

// When any source of wakes closes, wakes must close its output. A
// closed channel that stayed in the select would always be ready, and
// the goroutine would spin on it.
func TestWakesEndsWhenAnySourceCloses(t *testing.T) {
	sources := []string{"uevents", "bluez", "retries", "requests", "edits"}
	for i, closing := range sources {
		t.Run(closing, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				uevents := make(chan kernelEvent)
				bluez := make(chan struct{})
				retries := make(chan struct{})
				requests := make(chan struct{})
				edits := make(chan struct{})
				out, _ := wakes(t.Context(), uevents, bluez, retries, requests, edits)

				switch i {
				case 0:
					close(uevents)
				case 1:
					close(bluez)
				case 2:
					close(retries)
				case 3:
					close(requests)
				case 4:
					close(edits)
				}
				synctest.Wait()
				select {
				case _, ok := <-out:
					if ok {
						t.Fatal("wakes emitted after its source closed")
					}
				default:
					t.Fatal("wakes did not end when its source closed")
				}
			})
		})
	}
}

func TestWakesPassesEachSourceThrough(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		uevents := make(chan kernelEvent, 1)
		bluez := make(chan struct{}, 1)
		retries := make(chan struct{}, 1)
		requests := make(chan struct{}, 1)
		edits := make(chan struct{}, 1)
		out, _ := wakes(t.Context(), uevents, bluez, retries, requests, edits)

		uevents <- kernelEvent{Subsystem: "hid", Action: "add", MAC: "a0:ab:51:33:b7:12"}
		synctest.Wait()
		if !emitted(t, out) {
			t.Fatal("a uevent did not wake the loop")
		}
		bluez <- struct{}{}
		synctest.Wait()
		if !emitted(t, out) {
			t.Fatal("a BlueZ change did not wake the loop")
		}
		retries <- struct{}{}
		synctest.Wait()
		if !emitted(t, out) {
			t.Fatal("a retry did not wake the loop")
		}
		requests <- struct{}{}
		synctest.Wait()
		if !emitted(t, out) {
			t.Fatal("a PairingRequest did not wake the loop")
		}
		edits <- struct{}{}
		synctest.Wait()
		if !emitted(t, out) {
			t.Fatal("an edit did not wake the loop")
		}
	})
}

// With no events, wakes sends one wake at each backstop interval, and
// none before it.
func TestWakesRunsTheBackstopEachInterval(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		quiet := make(chan struct{})
		out, _ := wakes(t.Context(), make(chan kernelEvent), quiet, quiet, quiet, quiet)

		time.Sleep(backstopInterval - time.Nanosecond)
		synctest.Wait()
		if emitted(t, out) {
			t.Fatal("the backstop woke the loop before its interval")
		}
		time.Sleep(time.Nanosecond)
		synctest.Wait()
		if !emitted(t, out) {
			t.Fatal("the backstop did not wake the loop at its interval")
		}
	})
}

// The log line for a uevent includes the controller's address when the
// uevent has one.
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
			name:  "an evdev node's add",
			event: kernelEvent{Subsystem: "input", Action: "add", MAC: "a0:ab:51:33:b7:12"},
			want:  "controller A0:AB:51:33:B7:12: input add",
		},
		{
			name:  "a battery change",
			event: kernelEvent{Subsystem: "power_supply", Action: "change"},
			want:  "kernel: power_supply change",
		},
		{
			name:  "a lost datagram",
			event: kernelEvent{Lost: true},
			want:  "kernel: uevents were lost, so the operator reads the whole state",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := kernelEventLine(c.event); got != c.want {
				t.Errorf("line is %q, want %q", got, c.want)
			}
		})
	}
}
