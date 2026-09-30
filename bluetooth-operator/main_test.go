package main

import (
	"context"
	"testing"
	"testing/synctest"
	"time"
)

// The settle tests run in synctest bubbles at the production window and
// limit, so each one checks the exact moment that settle emits.

// emitted answers whether settle has emitted, and takes the wake. A
// test calls it after synctest.Wait, so a timer that is due has fired.
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

// A controller connecting produces a burst of uevents and a burst of
// D-Bus signals, and one write covers the whole burst. settle emits one
// window after the last event of the burst.
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

// One event emits one window later, and not before.
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

// A controller that reconnects faster than the quiet window would
// restart the wait forever. The limit keeps the loop publishing: settle
// emits at the limit after the first event.
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

// wakes merges five sources, and each of them closing must end the
// merge. A source that closed and stayed in the select would spin the
// loop on a channel that is always ready to receive.
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
				out := wakes(t.Context(), uevents, bluez, retries, requests, edits)

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
		out := wakes(t.Context(), uevents, bluez, retries, requests, edits)

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

// With no event, the backstop wakes the loop once each interval, and
// not before.
func TestWakesRunsTheBackstopEachInterval(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		quiet := make(chan struct{})
		out := wakes(t.Context(), make(chan kernelEvent), quiet, quiet, quiet, quiet)

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

// The loop prints what woke it. A HID event names its controller, a
// power supply change names none, and a lost datagram says so.
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
		{
			name:  "a lost datagram",
			event: kernelEvent{Lost: true},
			want:  "kernel: uevents were lost; reading the whole state",
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
