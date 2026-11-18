package main

// Tests for the loop that disciplines the clock after boot. Each test
// runs the loop in a synctest bubble, so a poll interval passes in no
// real time, and the time sources answer from the script of
// fakeClockActions.

import (
	"context"
	"slices"
	"testing"
	"testing/synctest"
	"time"

	"golang.org/x/sys/unix"

	"github.com/liken-sh/liken/liken/machine"
)

// runDiscipline starts the loop as main does, on a facts tree that
// already holds the time status from the boot step. stop cancels the
// loop and waits for it to return.
func runDiscipline(t *testing.T, initial machine.TimeStatus) (tree machine.FactsTree, readingDir string, stop func()) {
	t.Helper()
	tree = machine.FactsTree{Dir: t.TempDir()}
	readingDir = t.TempDir()
	if err := tree.WriteTime(initial); err != nil {
		t.Fatalf("writing the boot time status: %v", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	clk := newClock([]string{"10.10.0.1"})
	if initial.State == machine.TimeSynchronized {
		clk.record(&timeSync{source: initial.Source, stratum: initial.Stratum - 1, at: time.Now()})
	}
	go func() { done <- disciplineClock(clk, tree, readingDir, initial)(ctx) }()
	return tree, readingDir, func() {
		cancel()
		if err := <-done; err != nil {
			t.Errorf("the loop returned %v at shutdown, want nil", err)
		}
	}
}

func publishedTime(t *testing.T, tree machine.FactsTree) machine.TimeStatus {
	t.Helper()
	status, err := tree.Read()
	if err != nil {
		t.Fatalf("reading the facts tree: %v", err)
	}
	return status.Time
}

// bootSynchronized is the time status that the boot step publishes
// after a good measurement at the start of the bubble.
func bootSynchronized(offset string) machine.TimeStatus {
	return machine.TimeStatus{
		State:   machine.TimeSynchronized,
		Source:  "10.10.0.1",
		Stratum: 3,
		Offset:  offset,
	}
}

// A machine that booted with no answer from a time source publishes
// Synchronized at the first poll that gets an answer. At that poll it
// also slews the clock by the measured offset and writes the hardware
// clock. A shutdown writes the hardware clock again, so the next boot
// starts from the best time that this boot had.
func TestDisciplineClockPublishesTheFirstSync(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := installFakeClock(t, timeAnswer(3*time.Millisecond))
		tree, _, stop := runDiscipline(t, timeStatus(nil, []string{"10.10.0.1"}))

		time.Sleep(timePollInterval + time.Second)
		synctest.Wait()

		got := publishedTime(t, tree)
		if got.State != machine.TimeSynchronized || got.Source != "10.10.0.1" || got.Stratum != 3 || got.Offset != "±5ms" {
			t.Errorf("the published time is %+v, want Synchronized from 10.10.0.1 at stratum 3 with offset ±5ms", got)
		}
		_, _, slews, rtcWrites := f.recorded()
		if !slices.Equal(slews, []time.Duration{3 * time.Millisecond}) {
			t.Errorf("the slews are %v, want one of 3ms", slews)
		}
		if rtcWrites != 1 {
			t.Errorf("the hardware clock was written %d times after the first sync, want 1", rtcWrites)
		}

		stop()
		if _, _, _, rtcWrites := f.recorded(); rtcWrites != 2 {
			t.Errorf("the hardware clock was written %d times after shutdown, want 2", rtcWrites)
		}
	})
}

// A failed slew does not stop the loop from publishing the
// measurement.
func TestDisciplineClockPublishesDespiteAFailedSlew(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := installFakeClock(t, timeAnswer(3*time.Millisecond))
		f.slewErr = unix.EPERM
		tree, _, stop := runDiscipline(t, timeStatus(nil, []string{"10.10.0.1"}))
		defer stop()

		time.Sleep(timePollInterval + time.Second)
		synctest.Wait()

		if got := publishedTime(t, tree); got.State != machine.TimeSynchronized {
			t.Errorf("the published time is %+v, want Synchronized", got)
		}
	})
}

// A larger offset bound is published at the poll that measures it. A
// smaller bound is published only after three polls in a row measure
// inside it, so a clock near the boundary between two bounds does not
// rewrite the status each time it crosses.
func TestDisciplineClockPublishesAWorseBoundAtOnceAndABetterOneAfterThreePolls(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		// Each answer has a shorter round trip than the one before, so
		// the filter selects each new answer.
		installFakeClock(t,
			timeAnswerOver(200*time.Millisecond, 9*time.Millisecond),
			timeAnswerOver(3*time.Millisecond, 8*time.Millisecond),
			timeAnswerOver(3*time.Millisecond, 7*time.Millisecond),
			timeAnswerOver(3*time.Millisecond, 6*time.Millisecond))
		tree, _, stop := runDiscipline(t, bootSynchronized("±5ms"))
		defer stop()

		time.Sleep(timePollInterval + time.Second)
		synctest.Wait()
		if got := publishedTime(t, tree).Offset; got != "±1s" {
			t.Errorf("the published offset is %q after a worse bound, want ±1s", got)
		}

		time.Sleep(2 * timePollInterval)
		synctest.Wait()
		if got := publishedTime(t, tree).Offset; got != "±1s" {
			t.Errorf("the published offset is %q after two better polls, want ±1s", got)
		}

		time.Sleep(timePollInterval)
		synctest.Wait()
		if got := publishedTime(t, tree).Offset; got != "±5ms" {
			t.Errorf("the published offset is %q after three better polls, want ±5ms", got)
		}
	})
}

// Over wifi, samples with a long round trip vary by tens of
// milliseconds. The filter selects the sample with the shortest round
// trip, so the clock slews by that sample's offset once and the
// published bound does not change.
func TestDisciplineClockFiltersOutSlowRoundTrips(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := installFakeClock(t,
			timeAnswerOver(-2*time.Millisecond, 4*time.Millisecond),
			timeAnswerOver(-30*time.Millisecond, 40*time.Millisecond),
			timeAnswerOver(37*time.Millisecond, 45*time.Millisecond))
		tree, _, stop := runDiscipline(t, bootSynchronized("±5ms"))
		defer stop()

		time.Sleep(3*timePollInterval + time.Second)
		synctest.Wait()

		if got := publishedTime(t, tree).Offset; got != "±5ms" {
			t.Errorf("the published offset is %q after slow round trips, want ±5ms", got)
		}
		if _, _, slews, _ := f.recorded(); !slices.Equal(slews, []time.Duration{-2 * time.Millisecond, 0, 0}) {
			t.Errorf("the slews are %v, want -2ms, 0, 0", slews)
		}
	})
}

// A machine that boots 37ms off slews by 37ms. The filter subtracts the
// slew from the offset of the sample measured before it, so the
// published bound falls at the third poll after the correction instead
// of waiting for that sample to leave the filter. As the drift bound of
// the old sample grows, the clock reading follows the newer samples.
func TestDisciplineClockReportsTheErrorThatRemainsAfterASlew(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		installFakeClock(t,
			timeAnswerOver(37*time.Millisecond, time.Millisecond),
			timeAnswerOver(500*time.Microsecond, 20*time.Millisecond),
			timeAnswerOver(500*time.Microsecond, 20*time.Millisecond),
			timeAnswerOver(500*time.Microsecond, 20*time.Millisecond))
		tree, readingDir, stop := runDiscipline(t, bootSynchronized("±128ms"))
		defer stop()

		time.Sleep(4*timePollInterval + time.Second)
		synctest.Wait()

		if got := publishedTime(t, tree).Offset; got != "±1ms" {
			t.Errorf("the published offset is %q after the slew, want ±1ms", got)
		}
		if got, _ := machine.ReadClockReading(readingDir); got.Offset != 500*time.Microsecond {
			t.Errorf("the clock reading offset is %v after the slew, want 500µs", got.Offset)
		}
	})
}

// A clock that drifts 3ms between polls on a quiet wired path is
// slewed by 3ms at every poll. An older sample with the same round trip
// measured the clock before that drift, so the filter must not select
// it.
func TestDisciplineClockCorrectsDriftAtEveryPoll(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := installFakeClock(t, timeAnswer(3*time.Millisecond), timeAnswer(3*time.Millisecond), timeAnswer(3*time.Millisecond))
		_, _, stop := runDiscipline(t, bootSynchronized("±5ms"))
		defer stop()

		time.Sleep(3*timePollInterval + time.Second)
		synctest.Wait()

		want := []time.Duration{3 * time.Millisecond, 3 * time.Millisecond, 3 * time.Millisecond}
		if _, _, slews, _ := f.recorded(); !slices.Equal(slews, want) {
			t.Errorf("slews %v, want %v", slews, want)
		}
	})
}

// Every answered poll writes the clock reading, which machine-operator
// serves to Prometheus, even when the published time status does not
// change.
func TestDisciplineClockWritesTheReadingAtEveryAnsweredPoll(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		installFakeClock(t, timeAnswer(1200*time.Microsecond), timeAnswerOver(1300*time.Microsecond, 500*time.Microsecond))
		_, readingDir, stop := runDiscipline(t, bootSynchronized("±5ms"))
		defer stop()

		time.Sleep(2*timePollInterval + time.Second)
		synctest.Wait()

		got, err := machine.ReadClockReading(readingDir)
		if err != nil {
			t.Fatalf("reading the clock reading: %v", err)
		}
		want := machine.ClockReading{
			Offset: 1300 * time.Microsecond, Delay: 500 * time.Microsecond,
			Source: "10.10.0.1", At: time.Now().Add(-time.Second),
		}
		if !got.At.Equal(want.At) || got.Offset != want.Offset || got.Delay != want.Delay || got.Source != want.Source {
			t.Errorf("the clock reading is %+v, want %+v", got, want)
		}
	})
}

// A clock that stays steady does not rewrite the time status. The test
// changes the published status while the loop runs, and a loop that
// republished would overwrite that change.
func TestDisciplineClockRewritesNothingWhileTheClockHoldsSteady(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		installFakeClock(t, slices.Repeat([]pollAnswer{timeAnswer(time.Millisecond)}, 12)...)
		tree, _, stop := runDiscipline(t, bootSynchronized("±1ms"))
		defer stop()
		marked := bootSynchronized("±1ms")
		marked.Source = "marker"
		if err := tree.WriteTime(marked); err != nil {
			t.Fatalf("writing the marked time status: %v", err)
		}

		time.Sleep(12*timePollInterval + time.Second)
		synctest.Wait()

		if got := publishedTime(t, tree).Source; got != "marker" {
			t.Errorf("the published source is %q after twelve steady polls, want marker", got)
		}
	})
}

// A machine keeps reporting Synchronized through three missed polls,
// and reports Unsynchronized at stratum 16 at the fourth, because
// syncStaleAfter is three poll intervals.
func TestDisciplineClockReportsLostSourcesAfterThreeMissedPolls(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		installFakeClock(t)
		tree, _, stop := runDiscipline(t, bootSynchronized("±1ms"))
		defer stop()

		time.Sleep(3*timePollInterval + time.Second)
		synctest.Wait()
		if got := publishedTime(t, tree).State; got != machine.TimeSynchronized {
			t.Errorf("the published state is %q after three missed polls, want Synchronized", got)
		}

		time.Sleep(timePollInterval)
		synctest.Wait()
		got := publishedTime(t, tree)
		if got.State != machine.TimeUnsynchronized || got.Stratum != stratumUnsynchronized || got.Offset != "" {
			t.Errorf("the published time is %+v after four missed polls, want Unsynchronized at stratum 16 with no offset", got)
		}
	})
}

// A machine that never had a good time does not write the hardware
// clock at shutdown, because writing the system clock to the RTC would
// only copy the RTC's own error back into it.
func TestDisciplineClockLeavesTheRTCAloneWithoutGoodTime(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := installFakeClock(t)
		_, _, stop := runDiscipline(t, timeStatus(nil, []string{"10.10.0.1"}))

		time.Sleep(3 * timePollInterval)
		stop()

		if _, _, _, rtcWrites := f.recorded(); rtcWrites != 0 {
			t.Errorf("the hardware clock was written %d times without a sync, want 0", rtcWrites)
		}
	})
}
