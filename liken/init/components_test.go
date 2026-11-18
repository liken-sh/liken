package main

// Each test runs in a synctest bubble, so the real restart delays
// pass on the fake clock and a test can measure them.

import (
	"context"
	"errors"
	"testing"
	"testing/synctest"
	"time"
)

// testPlane returns a machine plane that shuts down when the test
// ends, so that no component goroutine outlives the bubble.
func testPlane(t *testing.T) *machinePlane {
	t.Helper()
	p := newMachinePlane()
	t.Cleanup(func() { p.shutdown(time.Second) })
	return p
}

// runTimes waits for the duration by, then returns the start time of
// each component run that was sent on ran.
func runTimes(ran chan time.Time, by time.Duration) []time.Time {
	time.Sleep(by)
	synctest.Wait()
	var times []time.Time
	for len(ran) > 0 {
		times = append(times, <-ran)
	}
	return times
}

func TestAComponentThatFinishesIsNotRestarted(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		p := testPlane(t)
		ran := make(chan time.Time, 8)
		p.start("finisher", func(ctx context.Context) error {
			ran <- time.Now()
			return nil
		})

		if runs := runTimes(ran, 2*componentMaxBackoff); len(runs) != 1 {
			t.Fatalf("a component that returned nil ran %d times, want once", len(runs))
		}
	})
}

// The first restart comes after twice componentBackoff, because the
// delay doubles before the first wait, and each delay has up to 50%
// jitter.
func TestAComponentThatFailsIsRestarted(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		p := testPlane(t)
		ran := make(chan time.Time, 8)
		p.start("failer", func(ctx context.Context) error {
			ran <- time.Now()
			return errors.New("transient trouble")
		})

		runs := runTimes(ran, 9*componentBackoff)
		if len(runs) != 3 {
			t.Fatalf("the component ran %d times in 9s, want 3", len(runs))
		}
		if first := runs[1].Sub(runs[0]); first < 2*componentBackoff || first >= 3*componentBackoff {
			t.Errorf("the first restart came after %s, want 2s to 3s", first)
		}
		if second := runs[2].Sub(runs[1]); second < 4*componentBackoff || second >= 6*componentBackoff {
			t.Errorf("the second restart came after %s, want 4s to 6s", second)
		}
	})
}

func TestAComponentThatPanicsIsRestarted(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		p := testPlane(t)
		ran := make(chan time.Time, 8)
		p.start("panicker", func(ctx context.Context) error {
			ran <- time.Now()
			panic("a bug, not a reboot")
		})

		if runs := runTimes(ran, 9*componentBackoff); len(runs) != 3 {
			t.Fatalf("the component ran %d times in 9s, want 3", len(runs))
		}
	})
}

func TestShutdownStopsAWellBehavedComponent(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		p := newMachinePlane()
		stopped := make(chan struct{})
		p.start("listener", func(ctx context.Context) error {
			<-ctx.Done()
			close(stopped)
			return nil
		})

		begin := time.Now()
		p.shutdown(time.Second)

		select {
		case <-stopped:
		default:
			t.Fatal("the component did not receive the cancellation, want its context cancelled")
		}
		if elapsed := time.Since(begin); elapsed != 0 {
			t.Errorf("the shutdown took %s, want no wait for a component that stops", elapsed)
		}
	})
}

func TestShutdownInterruptsARestartBackoff(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		p := newMachinePlane()
		ran := make(chan time.Time, 8)
		p.start("failer", func(ctx context.Context) error {
			ran <- time.Now()
			return errors.New("transient trouble")
		})
		if runs := runTimes(ran, 0); len(runs) != 1 {
			t.Fatalf("the component ran %d times, want once before the restart wait", len(runs))
		}

		begin := time.Now()
		p.shutdown(10 * time.Second)

		if elapsed := time.Since(begin); elapsed != 0 {
			t.Errorf("the shutdown took %s, want no wait for the restart delay", elapsed)
		}
	})
}

func TestShutdownIsBoundedWhenAComponentIsStuck(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		p := newMachinePlane()
		forever := make(chan struct{})
		p.start("stuck", func(ctx context.Context) error {
			<-forever // ignores ctx on purpose
			return nil
		})

		begin := time.Now()
		p.shutdown(20 * time.Second)

		if elapsed := time.Since(begin); elapsed != 20*time.Second {
			t.Errorf("the shutdown took %s, want its timeout of 20s", elapsed)
		}
		close(forever)
	})
}

func TestSleepUnlessCancelledReturnsFalseWhenCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if sleepUnlessCancelled(ctx, time.Hour) {
		t.Error("the sleep returned true, want false after the context was cancelled")
	}
}

func TestSleepUnlessCancelledWakesNormally(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		begin := time.Now()
		if !sleepUnlessCancelled(t.Context(), time.Hour) {
			t.Error("the sleep returned false, want true when the context is not cancelled")
		}
		if elapsed := time.Since(begin); elapsed != time.Hour {
			t.Errorf("the sleep took %s, want an hour", elapsed)
		}
	})
}
