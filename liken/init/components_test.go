package main

// Tests for the machine plane's contract with its components: a
// finished component stays finished, the code restarts a failed or
// panicking component, and shutdown is prompt for a well-behaved
// component and bounded for a stuck one. Each test runs in a synctest
// bubble, so the restart pacing runs at its real delays on the fake
// clock, and a test checks each delay.

import (
	"context"
	"errors"
	"testing"
	"testing/synctest"
	"time"
)

// testPlane builds a machine plane that shuts down when the test ends,
// so no component goroutine outlives the bubble.
func testPlane(t *testing.T) *machinePlane {
	t.Helper()
	p := newMachinePlane()
	t.Cleanup(func() { p.shutdown(time.Second) })
	return p
}

// runTimes waits until the plane has done all it can by the given time
// from now, and answers the time of each run the component recorded.
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

// A failed component restarts after a delay that doubles each time,
// from twice the initial backoff, plus up to half again of jitter.
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
			t.Fatal("the component never saw the cancellation")
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
			t.Errorf("the shutdown took %s; it waited out a backoff instead of interrupting it", elapsed)
		}
	})
}

func TestShutdownIsBoundedWhenAComponentIsStuck(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		p := newMachinePlane()
		forever := make(chan struct{})
		p.start("stuck", func(ctx context.Context) error {
			<-forever // ignores ctx, the misbehavior under test
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

func TestSleepUnlessCancelledHearsTheShutdown(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if sleepUnlessCancelled(ctx, time.Hour) {
		t.Error("a cancelled context must interrupt the sleep")
	}
}

func TestSleepUnlessCancelledWakesNormally(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		begin := time.Now()
		if !sleepUnlessCancelled(t.Context(), time.Hour) {
			t.Error("an undisturbed sleep reports true")
		}
		if elapsed := time.Since(begin); elapsed != time.Hour {
			t.Errorf("the sleep took %s, want an hour", elapsed)
		}
	})
}
