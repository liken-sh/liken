package main

// Tests for the machine plane's contract with its components: a
// finished component stays finished, the code restarts a failed or
// panicking component, and shutdown is prompt for a well-behaved
// component and bounded for a stuck one.

import (
	"context"
	"errors"
	"testing"
	"time"
)

// testPlane builds a machine plane with restart delays measured in
// milliseconds, so a test can exercise several restarts without
// slowing the suite down.
func testPlane(t *testing.T) *machinePlane {
	t.Helper()
	p := newMachinePlane()
	p.backoff = time.Millisecond
	p.maxBackoff = 4 * time.Millisecond
	t.Cleanup(func() { p.shutdown(time.Second) })
	return p
}

// awaitRuns fails the test unless the counter channel delivers n runs
// before the deadline.
func awaitRuns(t *testing.T, ran <-chan struct{}, n int) {
	t.Helper()
	for i := range n {
		select {
		case <-ran:
		case <-time.After(2 * time.Second):
			t.Fatalf("saw only %d of %d runs", i, n)
		}
	}
}

func TestAComponentThatFinishesIsNotRestarted(t *testing.T) {
	p := testPlane(t)
	ran := make(chan struct{}, 8)
	p.start("finisher", func(ctx context.Context) error {
		ran <- struct{}{}
		return nil
	})
	awaitRuns(t, ran, 1)

	select {
	case <-ran:
		t.Fatal("a component that returned nil was restarted")
	case <-time.After(50 * time.Millisecond):
	}
}

func TestAComponentThatFailsIsRestarted(t *testing.T) {
	p := testPlane(t)
	ran := make(chan struct{}, 8)
	p.start("failer", func(ctx context.Context) error {
		ran <- struct{}{}
		return errors.New("transient trouble")
	})
	awaitRuns(t, ran, 3)
}

func TestAComponentThatPanicsIsRestarted(t *testing.T) {
	p := testPlane(t)
	ran := make(chan struct{}, 8)
	p.start("panicker", func(ctx context.Context) error {
		ran <- struct{}{}
		panic("a bug, not a reboot")
	})
	awaitRuns(t, ran, 3)
}

func TestShutdownStopsAWellBehavedComponent(t *testing.T) {
	p := newMachinePlane()
	stopped := make(chan struct{})
	p.start("listener", func(ctx context.Context) error {
		<-ctx.Done()
		close(stopped)
		return nil
	})

	p.shutdown(time.Second)

	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("the component never saw the cancellation")
	}
}

func TestShutdownInterruptsARestartBackoff(t *testing.T) {
	p := newMachinePlane()
	p.backoff = time.Hour // shutdown must cut this restart wait short
	p.maxBackoff = time.Hour
	ran := make(chan struct{}, 8)
	p.start("failer", func(ctx context.Context) error {
		ran <- struct{}{}
		return errors.New("transient trouble")
	})
	awaitRuns(t, ran, 1)

	done := make(chan struct{})
	go func() {
		p.shutdown(10 * time.Second)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("shutdown waited out a backoff instead of interrupting it")
	}
}

func TestShutdownIsBoundedWhenAComponentIsStuck(t *testing.T) {
	p := newMachinePlane()
	forever := make(chan struct{})
	p.start("stuck", func(ctx context.Context) error {
		<-forever // ignores ctx, the misbehavior under test
		return nil
	})

	done := make(chan struct{})
	go func() {
		p.shutdown(20 * time.Millisecond)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("shutdown hung on a component that ignored cancellation")
	}
	close(forever)
}

func TestSleepUnlessCancelledHearsTheShutdown(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if sleepUnlessCancelled(ctx, time.Hour) {
		t.Error("a cancelled context must interrupt the sleep")
	}
}

func TestSleepUnlessCancelledWakesNormally(t *testing.T) {
	if !sleepUnlessCancelled(context.Background(), time.Millisecond) {
		t.Error("an undisturbed sleep reports true")
	}
}
