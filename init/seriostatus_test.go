package main

// Tests for what the serio walk reports for a line whose holder is in
// its read: the port the kernel registers, the driver that binds it,
// and a holder that no longer describes the line.

import (
	"strings"
	"testing"
	"time"

	"github.com/liken-sh/liken/machine"
)

// A holder in its read with no port yet has no CEC device, so the line
// is not attached until the port is there and its driver bound it.
func TestALineWithNoPortYetIsNotAttached(t *testing.T) {
	fakeSerioMachine(t)
	loadSerioModules(t, "serport", "pulse8_cec")
	adapter{port: "1-4", tty: "ttyACM0", portless: true}.plug(t)

	got := walkOnce(declaredSerio(newFakeTTYs(t), pulse8Entry))

	want := "attaching: the kernel has not registered a serio port on ttyACM0 yet"
	if got[0].State != machine.SerioRefused || got[0].Message != want {
		t.Errorf("walk = %+v", got)
	}
}

// pulse8_connect talks to the adapter before it binds, so an unbound
// port is the ordinary state of a probe for a moment. The status blames
// the driver only after the port stays unbound past the grace, and the
// walk wakes when the grace ends, because no uevent marks it.
func TestAnUnboundPortIsBlamedOnlyAfterTheGrace(t *testing.T) {
	fakeSerioMachine(t)
	loadSerioModules(t, "serport", "pulse8_cec")
	pulse := adapter{port: "1-4", tty: "ttyACM0", portless: true}
	pulse.plug(t)
	pulse.registerUnboundPort(t)
	r, clock := clocked(newFakeTTYs(t), pulse8Entry)

	probing := walkOnce(r)
	wait, waking := r.nextRetry()
	clock.now = clock.now.Add(serioBindGrace)
	failed := walkOnce(r)

	if probing[0].State != machine.SerioRefused || probing[0].Message != "attaching: waiting for pulse8_cec to bind serio0" {
		t.Errorf("walk in the grace = %+v", probing)
	}
	if !waking || wait != serioBindGrace {
		t.Errorf("the walk wakes in %s (%v)", wait, waking)
	}
	want := "serio0 on ttyACM0 has no driver: pulse8_cec did not bind it; the kernel log names the cause"
	if failed[0].Message != want {
		t.Errorf("walk after the grace = %+v", failed)
	}
}

// A holder can end between the prune and the report, and a holder that
// ended does not hold the port.
func TestAHolderThatEndedIsNotReportedAttached(t *testing.T) {
	fakeSerioMachine(t)
	loadSerioModules(t, "serport", "pulse8_cec")
	pulse := adapter{port: "1-4", tty: "ttyACM0"}
	pulse.plug(t)
	line := holdingLine()
	r := declaredSerio(newFakeTTYs(t, line), pulse8Entry)
	walkOnce(r)
	close(line.release)
	waitEnded(t, r, "ttyACM0")

	lines := discoverSerialLines()
	got := r.settledStatus(machine.SerioStatus{TTY: "ttyACM0"}, pulse8Protocol, lines[0], r.holders["ttyACM0"])

	if got.State != machine.SerioRefused || !strings.HasPrefix(got.Message, "read:") {
		t.Errorf("status = %+v", got)
	}
}

// A tty that registers again while the old holder is still in its
// read gets a holder of its own, and the old holder does not describe
// it.
func TestAHolderOfAnOlderTTYIsStale(t *testing.T) {
	fakeSerioMachine(t)
	loadSerioModules(t, "serport", "pulse8_cec")
	pulse := adapter{port: "1-4", tty: "ttyACM0"}
	pulse.plug(t)
	ttys := newFakeTTYs(t)
	r, _ := clocked(ttys, pulse8Entry)
	walkOnce(r)

	pulse.unplug(t)
	pulse.plug(t)
	got := walkOnce(r)

	if got[0].State != machine.SerioAttached || ttys.openCount() != 2 {
		t.Errorf("walk = %+v after %d opens", got, ttys.openCount())
	}
}

func TestTheBindGraceIsSeconds(t *testing.T) {
	if serioBindGrace < time.Second || serioBindGrace > 10*time.Second {
		t.Errorf("grace = %s", serioBindGrace)
	}
}
