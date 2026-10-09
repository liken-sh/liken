package main

// Tests for the discipline loop that runs after boot: when it
// publishes a measurement, when it reports that the sources are gone,
// and when it writes the hardware clock. Each test runs the loop in a
// synctest bubble, so a 64-second poll takes no real time, and the
// loop measures through the scripted sources of `fakeClockActions`.

import (
	"context"
	"slices"
	"testing"
	"testing/synctest"
	"time"

	"golang.org/x/sys/unix"

	"github.com/liken-sh/liken/liken/machine"
)

// runDiscipline starts the loop over a facts tree that already holds
// the boot step's status, the way main starts it. The returned stop
// function cancels the loop and waits for it to return.
func runDiscipline(t *testing.T, initial machine.TimeStatus) (tree machine.FactsTree, stop func()) {
	t.Helper()
	tree = machine.FactsTree{Dir: t.TempDir()}
	if err := tree.WriteTime(initial); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	clk := newClock([]string{"10.10.0.1"})
	if initial.State == machine.TimeSynchronized {
		clk.record(&timeSync{source: initial.Source, stratum: initial.Stratum - 1, at: time.Now()})
	}
	go func() { done <- disciplineClock(clk, tree, initial)(ctx) }()
	return tree, func() {
		cancel()
		if err := <-done; err != nil {
			t.Errorf("the loop ends cleanly at shutdown: %v", err)
		}
	}
}

func publishedTime(t *testing.T, tree machine.FactsTree) machine.TimeStatus {
	t.Helper()
	status, err := tree.Read()
	if err != nil {
		t.Fatal(err)
	}
	return status.Time
}

// bootSynchronized is the status a boot step publishes after a good
// measurement at the bubble's start.
func bootSynchronized(offset string) machine.TimeStatus {
	return machine.TimeStatus{
		State:   machine.TimeSynchronized,
		Source:  "10.10.0.1",
		Stratum: 3,
		Offset:  offset,
	}
}

// A machine that booted with no answer publishes its first sync at
// the first poll that gets one, slews by the measured offset, and
// writes the hardware clock. A shutdown writes the hardware clock
// again, so the next boot starts from the best time this boot had.
func TestDisciplineClockPublishesTheFirstSync(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := installFakeClock(t, timeAnswer(3*time.Millisecond))
		tree, stop := runDiscipline(t, timeStatus(nil, []string{"10.10.0.1"}))

		time.Sleep(timePollInterval + time.Second)
		synctest.Wait()

		got := publishedTime(t, tree)
		if got.State != machine.TimeSynchronized || got.Source != "10.10.0.1" || got.Stratum != 3 || got.Offset != "3ms" {
			t.Errorf("the first sync is published: %+v", got)
		}
		_, _, slews, rtcWrites := f.recorded()
		if !slices.Equal(slews, []time.Duration{3 * time.Millisecond}) {
			t.Errorf("the loop slews by the measured offset: %v", slews)
		}
		if rtcWrites != 1 {
			t.Errorf("the first sync corrects the hardware clock, got %d writes", rtcWrites)
		}

		stop()
		if _, _, _, rtcWrites := f.recorded(); rtcWrites != 2 {
			t.Errorf("a shutdown writes the hardware clock again, got %d writes", rtcWrites)
		}
	})
}

// A failed slew does not hide the measurement. Status still reports
// the source that answered, because the clock follows it on the next
// poll's slew.
func TestDisciplineClockPublishesDespiteAFailedSlew(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := installFakeClock(t, timeAnswer(3*time.Millisecond))
		f.slewErr = unix.EPERM
		tree, stop := runDiscipline(t, timeStatus(nil, []string{"10.10.0.1"}))
		defer stop()

		time.Sleep(timePollInterval + time.Second)
		synctest.Wait()

		if got := publishedTime(t, tree); got.State != machine.TimeSynchronized {
			t.Errorf("the measurement is published: %+v", got)
		}
	})
}

// The loop leaves the published offset alone while it wobbles by
// microseconds, and publishes it when it moves 25ms or more from the
// published value. Each publish is a status write that costs a raft
// round on every leader.
func TestDisciplineClockPublishesDriftButNotWobble(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		installFakeClock(t, timeAnswer(1500*time.Microsecond), timeAnswer(30*time.Millisecond), timeAnswer(200*time.Millisecond))
		tree, stop := runDiscipline(t, bootSynchronized("1ms"))
		defer stop()

		time.Sleep(timePollInterval + time.Second)
		synctest.Wait()
		if got := publishedTime(t, tree).Offset; got != "1ms" {
			t.Errorf("a 0.5ms wobble is not published, got %q", got)
		}

		time.Sleep(timePollInterval)
		synctest.Wait()
		if got := publishedTime(t, tree).Offset; got != "30ms" {
			t.Errorf("a 29ms drift is published, got %q", got)
		}

		time.Sleep(timePollInterval)
		synctest.Wait()
		if got := publishedTime(t, tree).Offset; got != "200ms" {
			t.Errorf("an offset past the step threshold is published, got %q", got)
		}
	})
}

// Ten minutes of wobble rewrite nothing. The test changes the
// published offset under the loop, and a loop that republished would
// write the measured offset back over it.
func TestDisciplineClockRewritesNothingWhileTheClockHoldsSteady(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		installFakeClock(t, slices.Repeat([]pollAnswer{timeAnswer(time.Millisecond)}, 12)...)
		tree, stop := runDiscipline(t, bootSynchronized("1ms"))
		defer stop()
		marked := bootSynchronized("7ms")
		if err := tree.WriteTime(marked); err != nil {
			t.Fatal(err)
		}

		time.Sleep(12*timePollInterval + time.Second)
		synctest.Wait()

		if got := publishedTime(t, tree).Offset; got != "7ms" {
			t.Errorf("twelve polls of 1ms wobble rewrote the offset to %s", got)
		}
	})
}

// A machine keeps reporting Synchronized through three missed polls,
// and reports Unsynchronized at stratum 16 at the fourth. A source
// that misses one poll is busy; a source that misses three is gone.
func TestDisciplineClockReportsLostSourcesAfterThreeMissedPolls(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		installFakeClock(t)
		tree, stop := runDiscipline(t, bootSynchronized("1ms"))
		defer stop()

		time.Sleep(3*timePollInterval + time.Second)
		synctest.Wait()
		if got := publishedTime(t, tree).State; got != machine.TimeSynchronized {
			t.Errorf("three missed polls are inside the window, got %q", got)
		}

		time.Sleep(timePollInterval)
		synctest.Wait()
		got := publishedTime(t, tree)
		if got.State != machine.TimeUnsynchronized || got.Stratum != stratumUnsynchronized {
			t.Errorf("the fourth missed poll reports the clock on its own: %+v", got)
		}
	})
}

// A machine that never had good time leaves the hardware clock alone
// at shutdown. Writing the system clock into the RTC then would only
// copy the RTC's own error back into it.
func TestDisciplineClockLeavesTheRTCAloneWithoutGoodTime(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := installFakeClock(t)
		_, stop := runDiscipline(t, timeStatus(nil, []string{"10.10.0.1"}))

		time.Sleep(3 * timePollInterval)
		stop()

		if _, _, _, rtcWrites := f.recorded(); rtcWrites != 0 {
			t.Errorf("no sync means no RTC write, got %d", rtcWrites)
		}
	})
}
