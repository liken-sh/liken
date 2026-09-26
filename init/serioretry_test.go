package main

// Tests for how the serio walk retries: a refusal is keyed to the tty
// it happened on, a new tty under the same name is new hardware, and a
// refusal on the same tty retries after a bounded backoff.

import (
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"

	"github.com/liken-sh/liken/machine"
)

// fakeClock is the registry's clock in these tests. Only the test
// moves it.
type fakeClock struct{ now time.Time }

func (c *fakeClock) read() time.Time { return c.now }

// clocked builds a registry on the fake opener with a fake clock.
func clocked(ttys *fakeTTYs, entries ...machine.SerioAttachment) (*serioRegistry, *fakeClock) {
	clock := &fakeClock{now: time.Date(2026, 9, 26, 12, 0, 0, 0, time.UTC)}
	r := declaredSerio(ttys, entries...)
	r.now = clock.read
	return r, clock
}

// A USB reset unbinds cdc_acm and probes it again, because cdc_acm has
// no post_reset, and the new tty takes the old name within
// milliseconds. The new tty is new hardware, and it attaches at once.
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
		t.Errorf("walk = %+v after %d opens", got, ttys.openCount())
	}
}

// cdc_acm's reset_resume hangs up the tty and keeps it. The read ends
// on the same tty, and the walk tries again after the backoff, so the
// adapter comes back by itself.
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
		t.Errorf("walks before the backoff = %+v, %+v", refused, early)
	}
	if retried[0].State != machine.SerioAttached || ttys.openCount() != 2 {
		t.Errorf("walk after the backoff = %+v after %d opens", retried, ttys.openCount())
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
			t.Errorf("serioBackoff(%d) = %s", test.failures, got)
		}
	}
}

// A holder that held its port for a while and then lost it starts the
// backoff over, so an adapter that resets once a day waits a second,
// not five minutes.
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
				t.Errorf("got %d", got)
			}
		})
	}
}

// An attach that outlasts the settle wait and then fails is refused
// like any other failed attach, and waits for its backoff before the
// next open.
func TestASlowAttachThatFailsIsRefused(t *testing.T) {
	fakeSerioMachine(t)
	loadSerioModules(t, "serport", "pulse8_cec")
	adapter{port: "1-4", tty: "ttyACM0"}.plug(t)
	old := serioSettleTimeout
	serioSettleTimeout = 10 * time.Millisecond
	t.Cleanup(func() { serioSettleTimeout = old })
	ttys := newFakeTTYs(t, &fakeLine{disciplineErr: unix.EPERM})
	ttys.gate = make(chan struct{})
	r, _ := clocked(ttys, pulse8Entry)

	waiting := walkOnce(r)
	close(ttys.gate)
	waitEnded(t, r, "ttyACM0")
	refused := walkOnce(r)
	again := walkOnce(r)

	if !strings.Contains(waiting[0].Message, "did not return") {
		t.Errorf("first walk = %+v", waiting)
	}
	for _, got := range [][]machine.SerioStatus{refused, again} {
		if got[0].State != machine.SerioRefused || got[0].Message != "TIOCSETD: operation not permitted" {
			t.Errorf("walk = %+v", got)
		}
	}
	if ttys.openCount() != 1 {
		t.Errorf("opens = %v", ttys.opens)
	}
}

// The walk waits for a new holder without the registry's lock, so the
// hardware watch and the module loader never wait behind a slow
// adapter.
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
		t.Error("declaredEntries waited behind the walk")
	}
	close(ttys.gate)
	if got := <-walked; got[0].State != machine.SerioAttached {
		t.Errorf("walk = %+v", got)
	}
}

// The walk wakes at the next retry, because no uevent announces that a
// backoff ran out.
func TestNextRetryNamesTheEarliestBackoff(t *testing.T) {
	fakeSerioMachine(t)
	loadSerioModules(t, "serport", "pulse8_cec")
	adapter{port: "1-4", tty: "ttyACM0"}.plug(t)
	r, _ := clocked(newFakeTTYs(t, &fakeLine{disciplineErr: unix.EPERM}), pulse8Entry)

	_, before := r.nextRetry()
	walkOnce(r)
	wait, after := r.nextRetry()

	if before || !after || wait != serioRetryBase {
		t.Errorf("before %v, after %v in %s", before, after, wait)
	}
}

// A nudge from an ending holder waits for the uevent burst to settle,
// like a uevent does. An unplug hangs up the tty before the kernel
// removes it, and a walk in between would report a refusal for one
// walk.
func TestANudgeSettlesBeforeTheWalk(t *testing.T) {
	nudge := make(chan struct{}, 1)
	nudge <- struct{}{}
	started := time.Now()

	waitForSerioWork(t.Context(), make(chan struct{}), nudge, nil)

	if elapsed := time.Since(started); elapsed < serioQuiet {
		t.Errorf("the walk woke after %s", elapsed)
	}
}

// A port whose driver did not bind has no CEC device, whatever the
// holder does, and the status says which driver to look for.
func TestAPortWithNoDriverIsRefused(t *testing.T) {
	fakeSerioMachine(t)
	loadSerioModules(t, "serport", "pulse8_cec")
	pulse := adapter{port: "1-4", tty: "ttyACM0"}
	pulse.plug(t)
	pulse.registerUnboundPort(t)

	got := walkOnce(declaredSerio(newFakeTTYs(t), pulse8Entry))

	want := "serio0 on ttyACM0 has no driver: pulse8_cec did not bind it; the kernel log names the cause"
	if got[0].State != machine.SerioRefused || got[0].Message != want || got[0].Port != "serio0" {
		t.Errorf("walk = %+v", got)
	}
}
