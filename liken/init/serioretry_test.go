package main

// Tests for retrying failed serio attachments. A failure is recorded
// with the tty it happened on. A new tty with the same name attaches at
// once, and a failure on the same tty is retried after a backoff.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"golang.org/x/sys/unix"

	"github.com/liken-sh/liken/liken/machine"
)

type fakeClock struct{ now time.Time }

func (c *fakeClock) read() time.Time { return c.now }

// clocked builds a registry that uses the fake opener and a fake clock.
func clocked(ttys *fakeTTYs, entries ...machine.SerioAttachment) (*serioRegistry, *fakeClock) {
	clock := &fakeClock{now: time.Date(2026, 9, 26, 12, 0, 0, 0, time.UTC)}
	r := declaredSerio(ttys, entries...)
	r.now = clock.read
	return r, clock
}

// After a USB reset, a new tty registers under the old name and
// attaches at once.
func TestAnAdapterThatResetsAttachesAgainAtOnce(t *testing.T) {
	fakeSerioMachine(t)
	loadSerioModules(t, "serport", "pulse8_cec")
	pulse := adapter{port: "1-4", tty: "ttyACM0"}
	pulse.plug(t)
	line := holdingLine()
	ttys := newFakeTTYs(t, line)
	r, _ := clocked(ttys, pulse8Entry)
	walkOnce(r)

	pulse.unplug(t)
	pulse.plug(t)
	close(line.release)
	waitEnded(t, r, "ttyACM0")
	got := walkOnce(r)

	if got[0].State != machine.SerioAttached || ttys.openCount() != 2 {
		t.Errorf("after the tty registered again, the walk is %+v after %d opens, want Attached after 2", got, ttys.openCount())
	}
}

// cdc_acm's reset_resume hangs up the tty and keeps it. The read ends
// on the same tty, and the walk attaches again after the backoff.
func TestAHangupThatKeepsTheTTYRetriesAfterTheBackoff(t *testing.T) {
	fakeSerioMachine(t)
	loadSerioModules(t, "serport", "pulse8_cec")
	adapter{port: "1-4", tty: "ttyACM0"}.plug(t)
	line := holdingLine()
	ttys := newFakeTTYs(t, line)
	r, clock := clocked(ttys, pulse8Entry)
	walkOnce(r)

	close(line.release)
	waitEnded(t, r, "ttyACM0")
	refused := walkOnce(r)
	early := walkOnce(r)
	clock.now = clock.now.Add(serioRetryBase)
	retried := walkOnce(r)

	if refused[0].State != machine.SerioRefused || early[0].State != machine.SerioRefused {
		t.Errorf("before the backoff ends, the walks are %+v and %+v, want both Refused", refused, early)
	}
	if retried[0].State != machine.SerioAttached || ttys.openCount() != 2 {
		t.Errorf("after the backoff ends, the walk is %+v after %d opens, want Attached after 2", retried, ttys.openCount())
	}
}

func TestTheRetryBackoffDoublesToItsBound(t *testing.T) {
	tests := []struct {
		failures int
		want     time.Duration
	}{
		{1, time.Second},
		{2, 2 * time.Second},
		{4, 8 * time.Second},
		{9, 256 * time.Second},
		{10, 5 * time.Minute},
		{80, 5 * time.Minute},
	}
	for _, test := range tests {
		if got := serioBackoff(test.failures); got != test.want {
			t.Errorf("serioBackoff(%d) = %s, want %s", test.failures, got, test.want)
		}
	}
}

func TestALongHoldStartsTheBackoffOver(t *testing.T) {
	tests := []struct {
		name  string
		prior int
		held  time.Duration
		want  int
	}{
		{"a first failure", 0, 0, 1},
		{"a quick end", 3, time.Second, 4},
		{"a long hold", 3, 2 * serioStableHold, 1},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := nextFailures(test.prior, test.held); got != test.want {
				t.Errorf("nextFailures(%d, %s) = %d, want %d", test.prior, test.held, got, test.want)
			}
		})
	}
}

// An attach that runs past serioSettleTimeout and then fails is
// recorded like any other failure, and the line is not opened again
// until its backoff ends.
func TestASlowAttachThatFailsIsRefused(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		fakeSerioMachine(t)
		loadSerioModules(t, "serport", "pulse8_cec")
		adapter{port: "1-4", tty: "ttyACM0"}.plug(t)
		ttys := newFakeTTYs(t, &fakeLine{disciplineErr: unix.EPERM})
		ttys.gate = make(chan struct{})
		r, _ := clocked(ttys, pulse8Entry)

		waiting := walkOnce(r)
		close(ttys.gate)
		waitEnded(t, r, "ttyACM0")
		refused := walkOnce(r)
		again := walkOnce(r)

		if !strings.Contains(waiting[0].Message, "did not return") {
			t.Errorf("the first walk is %+v, want a message that the attach did not return", waiting)
		}
		for _, got := range [][]machine.SerioStatus{refused, again} {
			if got[0].State != machine.SerioRefused || !strings.Contains(got[0].Message, "TIOCSETD") {
				t.Errorf("the walk after the failed attach is %+v, want Refused with the TIOCSETD error", got)
			}
		}
		if ttys.openCount() != 1 {
			t.Errorf("the tty was opened %v, want one open", ttys.opens)
		}
	})
}

// The walk does not hold the registry's lock while it waits for a new
// holder, so a caller of declaredEntries, such as the hardware watch,
// does not wait for a slow adapter.
func TestTheWalkHoldsNoLockWhileItWaits(t *testing.T) {
	fakeSerioMachine(t)
	loadSerioModules(t, "serport", "pulse8_cec")
	adapter{port: "1-4", tty: "ttyACM0"}.plug(t)
	ttys := newFakeTTYs(t)
	ttys.gate = make(chan struct{})
	r, _ := clocked(ttys, pulse8Entry)
	walked := make(chan []machine.SerioStatus)
	go func() { walked <- walkOnce(r) }()

	read := make(chan struct{})
	go func() {
		r.declaredEntries()
		close(read)
	}()
	select {
	case <-read:
	case <-time.After(2 * time.Second):
		t.Error("declaredEntries did not return within 2 seconds while a walk was in progress, want it to return")
	}
	close(ttys.gate)
	if got := <-walked; got[0].State != machine.SerioAttached {
		t.Errorf("the walk is %+v, want Attached", got)
	}
}

// nextRetry returns the time of the earliest backoff to end, because
// no uevent announces that a backoff ended.
func TestNextRetryReturnsTheEarliestBackoff(t *testing.T) {
	fakeSerioMachine(t)
	loadSerioModules(t, "serport", "pulse8_cec")
	adapter{port: "1-4", tty: "ttyACM0"}.plug(t)
	r, _ := clocked(newFakeTTYs(t, &fakeLine{disciplineErr: unix.EPERM}), pulse8Entry)

	_, before := r.nextRetry()
	walkOnce(r)
	wait, after := r.nextRetry()

	if before || !after || wait != serioRetryBase {
		t.Errorf("nextRetry reports %v before a walk and %v after it, with %s to wait, want false, true, and %s", before, after, wait, serioRetryBase)
	}
}

// A nudge from a holder that ended waits serioQuiet before the walk,
// as a uevent does. An unplug hangs up the tty before the kernel
// removes it, and a walk between the two would report a refusal.
func TestANudgeSettlesBeforeTheWalk(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		nudge := make(chan struct{}, 1)
		nudge <- struct{}{}
		started := time.Now()

		waitForSerioWork(t.Context(), make(chan struct{}), nudge, nil)

		if elapsed := time.Since(started); elapsed != serioQuiet {
			t.Errorf("the walk woke after %s, want the quiet interval of %s", elapsed, serioQuiet)
		}
	})
}

// When a backoff ends but a module is missing, the walk sets a new
// retryAt, so nextRetry does not return a time that has passed.
func TestARetryBlockedByAMissingModuleWaitsAgain(t *testing.T) {
	fakeSerioMachine(t)
	loadSerioModules(t, "serport", "pulse8_cec")
	adapter{port: "1-4", tty: "ttyACM0"}.plug(t)
	r, clock := clocked(newFakeTTYs(t, &fakeLine{disciplineErr: unix.EPERM}), pulse8Entry)
	walkOnce(r)
	if err := os.RemoveAll(filepath.Join(sysModuleDir, "pulse8_cec")); err != nil {
		t.Fatalf("removing the pulse8_cec module directory: %v", err)
	}

	clock.now = clock.now.Add(serioRetryBase)
	got := walkOnce(r)
	wait, ok := r.nextRetry()

	if got[0].Message != "declare pulse8_cec in spec.modules" || !ok || wait <= 0 {
		t.Errorf("the walk is %+v and the next retry is in %s (%v), want a message to declare pulse8_cec and a retry in the future", got, wait, ok)
	}
}

// An adapter that enumerates again on every attach gets a new tty each
// time. The first new tty attaches at once, and the second waits for
// the backoff, because the failure count belongs to the USB port.
func TestAnAdapterThatReenumeratesOnEveryAttachBacksOff(t *testing.T) {
	fakeSerioMachine(t)
	loadSerioModules(t, "serport", "pulse8_cec")
	pulse := adapter{port: "1-4", tty: "ttyACM0"}
	pulse.plug(t)
	first, second := holdingLine(), holdingLine()
	ttys := newFakeTTYs(t, first, second)
	r, clock := clocked(ttys, pulse8Entry)
	walkOnce(r)

	reenumerate := func(line *fakeLine) {
		pulse.unplug(t)
		pulse.plug(t)
		close(line.release)
		waitEnded(t, r, "ttyACM0")
	}
	reenumerate(first)
	once := walkOnce(r)
	reenumerate(second)
	twice := walkOnce(r)
	clock.now = clock.now.Add(serioBackoff(2))
	later := walkOnce(r)

	if once[0].TTY != "ttyACM0" || ttys.openCount() < 2 {
		t.Errorf("the walk of the first new tty is %+v after %d opens, want it on ttyACM0 after 2 or more", once, ttys.openCount())
	}
	if twice[0].State != machine.SerioRefused {
		t.Errorf("the walk of the second new tty is %+v, want Refused", twice)
	}
	if later[0].State == machine.SerioMissing || ttys.openCount() != 3 {
		t.Errorf("after the backoff, the walk is %+v after %d opens, want a present tty after 3", later, ttys.openCount())
	}
}
