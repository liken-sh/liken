package main

// The settle and wake tests run in a synctest bubble at the production
// durations. The fake clock advances only when every goroutine waits,
// so each test checks the exact moment a wake arrives, and a scheduler
// that runs late cannot move it.

import (
	"context"
	"testing"
	"testing/synctest"
	"time"
)

func TestSettleCollapsesABurst(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		in := make(chan struct{}, 16)
		out := settle(t.Context(), in, settleWindow, settleLimit)

		// A monitor that a person plugs in produces a burst of jack
		// events, and one write must cover the whole burst.
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

		// A cable that somebody wiggles changes the jack faster than the
		// quiet window, which would restart the wait forever. The limit
		// keeps the loop publishing.
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
		// The first event arrives one tick after the start, and the limit
		// runs from that event.
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
			t.Fatal("settle emitted after its context ended")
		}
	})
}

// waitForWake waits for one wake. In a bubble, a wake that never comes
// leaves every goroutine blocked, and synctest fails the test.
func waitForWake(t *testing.T, out <-chan struct{}) {
	t.Helper()
	if _, ok := <-out; !ok {
		t.Fatal("the channel closed instead of emitting")
	}
}

// assertQuiet checks that no second wake arrives, however long the
// test waits.
func assertQuiet(t *testing.T, out <-chan struct{}) {
	t.Helper()
	time.Sleep(time.Hour)
	synctest.Wait()
	select {
	case <-out:
		t.Fatal("settle emitted a second time for one burst")
	default:
	}
}

// Every event source ends in one wake, and one pass covers whatever
// woke it. A control a person turned on the card, a monitor plugged
// into an HDMI pin, and a spec somebody edited all arrive here. The
// backstop tick wakes the loop with no event at all.
func TestWakesCarriesEverySource(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cards := make(chan controlEvent, 1)
		pokes := make(chan struct{}, 1)
		out := wakes(t.Context(), nil, cards, pokes)

		cards <- controlEvent{Card: 0, Name: "Master Playback Volume", Mask: ctlEventMaskValue}
		waitForWake(t, out)

		cards <- controlEvent{Card: 0, Name: "HDMI/DP,pcm=3 Jack", Mask: ctlEventMaskValue}
		waitForWake(t, out)

		pokes <- struct{}{}
		waitForWake(t, out)

		start := time.Now()
		waitForWake(t, out)
		if waited := time.Since(start); waited != backstopInterval {
			t.Errorf("the backstop woke the loop after %v, want %v", waited, backstopInterval)
		}
	})
}

// A source that closes ends the merge, so the operator stops rather
// than run on with no way to notice a monitor again.
func TestWakesEndsWhenACardWatcherCloses(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cards := make(chan controlEvent)
		out := wakes(t.Context(), nil, cards, make(chan struct{}))
		close(cards)

		if _, open := <-out; open {
			t.Fatal("the merge emitted a wake after its source closed")
		}
	})
}
