package main

// Tests for the status of a line whose holder is reading.

import (
	"strings"
	"testing"
	"time"

	"github.com/liken-sh/liken/liken/machine"
)

// A line is not attached until the kernel has registered its serio
// port and a driver has bound to it, because the CEC device does not
// exist before that.
func TestALineWithNoPortYetIsNotAttached(t *testing.T) {
	fakeSerioMachine(t)
	loadSerioModules(t, "serport", "pulse8_cec")
	adapter{port: "1-4", tty: "ttyACM0", portless: true}.plug(t)

	got := walkOnce(declaredSerio(newFakeTTYs(t), pulse8Entry))

	want := "attaching: the kernel has not registered a serio port on ttyACM0 yet"
	if got[0].State != machine.SerioRefused || got[0].Message != want {
		t.Errorf("the walk returned %+v, want Refused with message %q", got, want)
	}
}

// The status reports a driver failure only when the port is still
// unbound after serioBindGrace. No uevent arrives when the grace ends,
// so the walk must schedule its own wake-up.
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
		t.Errorf("the walk inside the grace returned %+v, want Refused and waiting for pulse8_cec to bind serio0", probing)
	}
	if !waking || wait != serioBindGrace {
		t.Errorf("the next retry is in %s (scheduled %v), want %s", wait, waking, serioBindGrace)
	}
	want := "serio0 on ttyACM0 has no driver because pulse8_cec did not bind it. The kernel log has the reason"
	if failed[0].Message != want {
		t.Errorf("the walk after the grace returned %+v, want message %q", failed, want)
	}
}

// A holder can end after the walk prunes the holders and before it
// reports their lines. Its line is refused, not attached.
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
		t.Errorf("the status is %+v, want Refused with a message that starts with read:", got)
	}
}

// When a tty registers again while the old holder is still reading,
// the walk opens the new tty with a new holder.
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
		t.Errorf("the walk returned %+v after %d opens, want Attached after 2", got, ttys.openCount())
	}
}

func TestTheBindGraceIsSeconds(t *testing.T) {
	if serioBindGrace < time.Second || serioBindGrace > 10*time.Second {
		t.Errorf("serioBindGrace is %s, want between 1s and 10s", serioBindGrace)
	}
}
