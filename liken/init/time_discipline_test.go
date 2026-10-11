package main

// Tests for the discipline loop that runs after boot: when it
// publishes the standing, when it writes the reading, when it slews,
// when it reports that the sources are gone, and when it writes the
// hardware clock. Each test runs the loop in a
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
// the boot step's status, the way main starts it. The loop writes its
// reading to the returned readingDir. The returned stop function
// cancels the loop and waits for it to return.
func runDiscipline(t *testing.T, initial machine.TimeStatus) (tree machine.FactsTree, readingDir string, stop func()) {
	t.Helper()
	tree = machine.FactsTree{Dir: t.TempDir()}
	readingDir = t.TempDir()
	if err := tree.WriteTime(initial); err != nil {
		t.Fatal(err)
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
		tree, _, stop := runDiscipline(t, timeStatus(nil, []string{"10.10.0.1"}))

		time.Sleep(timePollInterval + time.Second)
		synctest.Wait()

		got := publishedTime(t, tree)
		if got.State != machine.TimeSynchronized || got.Source != "10.10.0.1" || got.Stratum != 3 || got.Offset != "±5ms" {
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
		tree, _, stop := runDiscipline(t, timeStatus(nil, []string{"10.10.0.1"}))
		defer stop()

		time.Sleep(timePollInterval + time.Second)
		synctest.Wait()

		if got := publishedTime(t, tree); got.State != machine.TimeSynchronized {
			t.Errorf("the measurement is published: %+v", got)
		}
	})
}

// A worse bound publishes at the poll that measures it. A better
// bound publishes only at the third poll in a row inside it, so a
// clock near a boundary does not write status at each crossing.
func TestDisciplineClockPublishesAWorseBoundAtOnceAndABetterOneAfterThreePolls(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		// Each answer has a shorter round trip than the last, so the
		// filter selects each one as it arrives.
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
			t.Errorf("a worse bound publishes at once, got %q", got)
		}

		time.Sleep(2 * timePollInterval)
		synctest.Wait()
		if got := publishedTime(t, tree).Offset; got != "±1s" {
			t.Errorf("two better polls publish nothing, got %q", got)
		}

		time.Sleep(timePollInterval)
		synctest.Wait()
		if got := publishedTime(t, tree).Offset; got != "±5ms" {
			t.Errorf("the third better poll publishes, got %q", got)
		}
	})
}

// Over wifi, the samples with a long round trip swing by tens of
// milliseconds. The filter keeps the short one, so the clock slews by
// its offset once, and the bound holds.
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
			t.Errorf("noise on slow round trips moved the bound to %q", got)
		}
		if _, _, slews, _ := f.recorded(); !slices.Equal(slews, []time.Duration{-2 * time.Millisecond, 0, 0}) {
			t.Errorf("the clock slews by the short round trip, and then by nothing: %v", slews)
		}
	})
}

// A machine that boots 37ms off slews by the shortest round trip, and
// once the slew has applied, that sample reports a clock that is right.
// The bound falls at the third poll after the correction, not after the
// sample ages out of the filter eight polls later.
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
			t.Errorf("the bound after the slew is %q", got)
		}
		if got, _ := machine.ReadClockReading(readingDir); got.Offset != 0 {
			t.Errorf("the reading after the slew is %v", got.Offset)
		}
	})
}

// Every answered poll writes the reading, the exact filtered offset
// that Prometheus scrapes, even when the standing does not change.
func TestDisciplineClockWritesTheReadingAtEveryAnsweredPoll(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		installFakeClock(t, timeAnswer(1200*time.Microsecond), timeAnswerOver(1300*time.Microsecond, 500*time.Microsecond))
		_, readingDir, stop := runDiscipline(t, bootSynchronized("±5ms"))
		defer stop()

		time.Sleep(2*timePollInterval + time.Second)
		synctest.Wait()

		got, err := machine.ReadClockReading(readingDir)
		if err != nil {
			t.Fatal(err)
		}
		want := machine.ClockReading{
			Offset: 1300 * time.Microsecond, Delay: 500 * time.Microsecond,
			Source: "10.10.0.1", At: time.Now().Add(-time.Second),
		}
		if !got.At.Equal(want.At) || got.Offset != want.Offset || got.Delay != want.Delay || got.Source != want.Source {
			t.Errorf("got %+v, want %+v", got, want)
		}
	})
}

// A steady clock rewrites nothing, however long it holds. The test
// changes the published standing under the loop, and a loop that
// republished would write its own standing back over it.
func TestDisciplineClockRewritesNothingWhileTheClockHoldsSteady(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		installFakeClock(t, slices.Repeat([]pollAnswer{timeAnswer(time.Millisecond)}, 12)...)
		tree, _, stop := runDiscipline(t, bootSynchronized("±1ms"))
		defer stop()
		marked := bootSynchronized("±1ms")
		marked.Source = "marker"
		if err := tree.WriteTime(marked); err != nil {
			t.Fatal(err)
		}

		time.Sleep(12*timePollInterval + time.Second)
		synctest.Wait()

		if got := publishedTime(t, tree).Source; got != "marker" {
			t.Errorf("twelve steady polls rewrote the standing, source %q", got)
		}
	})
}

// A machine keeps reporting Synchronized through three missed polls,
// and reports Unsynchronized at stratum 16 at the fourth. A source
// that misses one poll is busy; a source that misses three is gone.
func TestDisciplineClockReportsLostSourcesAfterThreeMissedPolls(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		installFakeClock(t)
		tree, _, stop := runDiscipline(t, bootSynchronized("±1ms"))
		defer stop()

		time.Sleep(3*timePollInterval + time.Second)
		synctest.Wait()
		if got := publishedTime(t, tree).State; got != machine.TimeSynchronized {
			t.Errorf("three missed polls are inside the window, got %q", got)
		}

		time.Sleep(timePollInterval)
		synctest.Wait()
		got := publishedTime(t, tree)
		if got.State != machine.TimeUnsynchronized || got.Stratum != stratumUnsynchronized || got.Offset != "" {
			t.Errorf("the fourth missed poll reports the clock on its own, with no bound: %+v", got)
		}
	})
}

// A machine that never had good time leaves the hardware clock alone
// at shutdown. Writing the system clock into the RTC then would only
// copy the RTC's own error back into it.
func TestDisciplineClockLeavesTheRTCAloneWithoutGoodTime(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := installFakeClock(t)
		_, _, stop := runDiscipline(t, timeStatus(nil, []string{"10.10.0.1"}))

		time.Sleep(3 * timePollInterval)
		stop()

		if _, _, _, rtcWrites := f.recorded(); rtcWrites != 0 {
			t.Errorf("no sync means no RTC write, got %d", rtcWrites)
		}
	})
}
