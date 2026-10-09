package hardware

import (
	"context"
	"testing"
	"testing/synctest"
	"time"
)

// noisyChannel sends signals continuously, faster than any quiet
// interval, until the test ends. This is the pattern of a node whose
// containers are starting and stopping constantly.
func noisyChannel(t *testing.T) chan struct{} {
	t.Helper()
	ch := make(chan struct{}, 1)
	done := make(chan struct{})
	t.Cleanup(func() { <-done })
	go func() {
		defer close(done)
		for {
			select {
			case <-t.Context().Done():
				return
			case ch <- struct{}{}:
			default:
			}
			time.Sleep(5 * time.Millisecond)
		}
	}()
	return ch
}

func TestSettleReturnsAtTheCeilingUnderConstantNoise(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		started := time.Now()
		Settle(t.Context(), noisyChannel(t), time.Second, 5*time.Second)
		if elapsed := time.Since(started); elapsed != 5*time.Second {
			t.Errorf("Settle returned after %s, want the 5s ceiling", elapsed)
		}
	})
}

func TestSettleReturnsAtOnceWhenTheListenerStops(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ch := make(chan struct{})
		close(ch)
		started := time.Now()
		Settle(t.Context(), ch, time.Second, 5*time.Second)
		if elapsed := time.Since(started); elapsed != 0 {
			t.Errorf("Settle returned after %s, want at once for a closed channel", elapsed)
		}
	})
}

func TestSettleReturnsAtQuietWhenTheStreamStops(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ch := make(chan struct{}, 1)
		started := time.Now()
		Settle(t.Context(), ch, time.Second, 5*time.Second)
		if elapsed := time.Since(started); elapsed != time.Second {
			t.Errorf("Settle returned after %s, want the 1s quiet interval", elapsed)
		}
	})
}

// Settle returns when its context ends, even while the stream is still
// noisy, so a component that is shutting down does not wait out the
// ceiling.
func TestSettleReturnsWhenTheContextEnds(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
		defer cancel()
		started := time.Now()
		Settle(ctx, noisyChannel(t), time.Second, 5*time.Second)
		if elapsed := time.Since(started); elapsed != 2*time.Second {
			t.Errorf("Settle returned after %s, want 2s, when the context ended", elapsed)
		}
	})
}
