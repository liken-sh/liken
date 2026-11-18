package main

// The settle and wake tests run in a synctest bubble with the
// production durations, so a test can check the exact time that a wake
// arrives.

import (
	"context"
	"slices"
	"testing"
	"testing/synctest"
	"time"
)

func TestSettleCollapsesABurst(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		in := make(chan struct{}, 16)
		out := settle(t.Context(), in, settleWindow, settleLimit)

		// Plugging in a monitor produces a burst of jack events, and
		// settle must send once, a full window after the last event.
		for range 8 {
			in <- struct{}{}
			time.Sleep(settleWindow / 4)
		}
		last := time.Now().Add(-settleWindow / 4)
		waitForWake(t, out)
		if waited := time.Since(last); waited != settleWindow {
			t.Errorf("settle emitted %v after the last event, want %v", waited, settleWindow)
		}
		assertQuiet(t, out)
	})
}

func TestSettleWaitsForQuiet(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		in := make(chan struct{}, 16)
		out := settle(t.Context(), in, settleWindow, settleLimit)

		start := time.Now()
		in <- struct{}{}
		waitForWake(t, out)
		if waited := time.Since(start); waited != settleWindow {
			t.Errorf("settle emitted after %v, want %v", waited, settleWindow)
		}
	})
}

func TestSettleEmitsUnderAConstantFlap(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		in := make(chan struct{})
		out := settle(t.Context(), in, settleWindow, settleLimit)

		// Events that arrive faster than the quiet window would restart
		// the wait forever, so settle sends when the limit passes.
		go func() {
			tick := time.NewTicker(settleWindow / 2)
			defer tick.Stop()
			for {
				select {
				case <-t.Context().Done():
					return
				case <-tick.C:
					select {
					case in <- struct{}{}:
					case <-t.Context().Done():
						return
					}
				}
			}
		}()

		start := time.Now()
		waitForWake(t, out)
		// The limit counts from the first event, which arrives one tick
		// after the start.
		if waited := time.Since(start); waited != settleWindow/2+settleLimit {
			t.Errorf("settle emitted after %v, want %v", waited, settleWindow/2+settleLimit)
		}
	})
}

func TestSettleStopsWithItsContext(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		in := make(chan struct{}, 1)
		out := settle(ctx, in, settleWindow, settleLimit)

		cancel()
		if _, ok := <-out; ok {
			t.Fatal("settle emitted after its context ended, want a closed channel")
		}
	})
}

// waitForWake receives one wake. In a bubble, a wake that never comes
// leaves every goroutine blocked, and synctest fails the test.
func waitForWake(t *testing.T, out <-chan struct{}) {
	t.Helper()
	if _, ok := <-out; !ok {
		t.Fatal("the channel closed instead of emitting")
	}
}

// assertQuiet fails the test when a second wake arrives within an hour
// of fake time.
func assertQuiet(t *testing.T, out <-chan struct{}) {
	t.Helper()
	time.Sleep(time.Hour)
	synctest.Wait()
	select {
	case <-out:
		t.Fatal("settle emitted a second time for one burst, want one wake")
	default:
	}
}

// The backstop tick wakes the loop with no event at all.
func TestWakesCarriesEverySource(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cards := make(chan controlEvent, 1)
		wakeRequests := make(chan struct{}, 1)
		out := wakes(t.Context(), nil, cards, wakeRequests, nil)

		cards <- controlEvent{Card: 0, Name: "Master Playback Volume", Mask: ctlEventMaskValue}
		waitForWake(t, out)

		cards <- controlEvent{Card: 0, Name: "HDMI/DP,pcm=3 Jack", Mask: ctlEventMaskValue}
		waitForWake(t, out)

		wakeRequests <- struct{}{}
		waitForWake(t, out)

		start := time.Now()
		waitForWake(t, out)
		if waited := time.Since(start); waited != backstopInterval {
			t.Errorf("the backstop woke the loop after %v, want %v", waited, backstopInterval)
		}
	})
}

// The pass that reads the card after an ELD change must find the
// change counted by the monitor hold.
func TestWakesHandsEachEventToTheHoldFirst(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cards := make(chan controlEvent, 1)
		var seenByHold []string
		out := wakes(t.Context(), nil, cards, make(chan struct{}), func(event controlEvent) {
			seenByHold = append(seenByHold, event.Name)
		})

		cards <- controlEvent{Card: 0, Device: 3, Name: eldElementName, Mask: ctlEventMaskValue}
		waitForWake(t, out)

		if !slices.Equal(seenByHold, []string{eldElementName}) {
			t.Errorf("the monitor hold received %q before the wake, want the ELD change", seenByHold)
		}
	})
}

// The operator exits when the merge ends, instead of running on with no
// way to notice a monitor change.
func TestWakesEndsWhenACardWatcherCloses(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cards := make(chan controlEvent)
		out := wakes(t.Context(), nil, cards, make(chan struct{}), nil)
		close(cards)

		if _, open := <-out; open {
			t.Fatal("the merge emitted a wake after its source closed, want a closed channel")
		}
	})
}
