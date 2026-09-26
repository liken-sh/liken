package main

// Tests for the serio walk, run against the fake machine that
// seriolines_test.go builds.

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"golang.org/x/sys/unix"

	"github.com/liken-sh/liken/machine"
)

// declaredSerio builds a registry over the fake opener with the entries
// declared, the way the boot declares the manifest's list.
func declaredSerio(ttys *fakeTTYs, entries ...machine.SerioAttachment) *serioRegistry {
	r := newSerioRegistry(ttys.open)
	r.declare(entries)
	return r
}

// walkOnce runs one walk and returns its report.
func walkOnce(r *serioRegistry) []machine.SerioStatus {
	return r.walk()
}

// waitEnded waits until the holder on a tty has ended.
func waitEnded(t *testing.T, r *serioRegistry, tty string) {
	t.Helper()
	r.mu.Lock()
	h := r.holders[tty]
	r.mu.Unlock()
	if h == nil {
		t.Fatalf("no holder on %s", tty)
	}
	waitDone(t, h)
}

func TestAWalkAttachesAMatchedLine(t *testing.T) {
	fakeSerioMachine(t)
	loadSerioModules(t, "serport", "pulse8_cec")
	pulse := adapter{port: "1-4", tty: "ttyACM0"}
	pulse.plug(t)
	pulse.registerPort(t)
	ttys := newFakeTTYs(t)
	r := declaredSerio(ttys, pulse8Entry)

	got := walkOnce(r)
	again := walkOnce(r)

	want := []machine.SerioStatus{{
		Protocol: "pulse8-cec", USB: pulse8Entry.USB,
		TTY: "ttyACM0", Port: "serio0", State: machine.SerioAttached,
		Nodes: []string{"/dev/cec0", "/dev/input/event13"},
	}}
	if !slices.EqualFunc(got, want, serioStatusEqual) || !slices.EqualFunc(again, want, serioStatusEqual) {
		t.Errorf("walks = %+v, then %+v", got, again)
	}
	if ttys.opens[0] != filepath.Join(devRoot, "ttyACM0") || ttys.openCount() != 1 {
		t.Errorf("opens = %v; one holder holds the line across walks", ttys.opens)
	}
}

func TestAWalkReportsAMissingAdapter(t *testing.T) {
	withSerial := pulse8Entry
	withSerial.USB.Serial = "A1"
	tests := []struct {
		name    string
		entry   machine.SerioAttachment
		plugged *adapter
		want    string
	}{
		{"nothing plugged in", pulse8Entry, nil, "no USB device 2548:1002 is plugged in"},
		{"an adapter with no line", pulse8Entry, &adapter{port: "1-4"},
			"USB device 2548:1002 has no serial line; declare cdc_acm in spec.modules"},
		{"another unit", withSerial, &adapter{port: "1-4", serial: "A2", tty: "ttyACM0"},
			"no USB device 2548:1002 with serial A1 is plugged in"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fakeSerioMachine(t)
			loadSerioModules(t, "serport", "pulse8_cec")
			if test.plugged != nil {
				test.plugged.plug(t)
			}
			ttys := newFakeTTYs(t)

			got := walkOnce(declaredSerio(ttys, test.entry))

			if len(got) != 1 || got[0].State != machine.SerioMissing || got[0].Message != test.want || got[0].TTY != "" {
				t.Errorf("walk = %+v", got)
			}
			if ttys.openCount() != 0 {
				t.Errorf("opens = %v", ttys.opens)
			}
		})
	}
}

// The modules are checked before the open, so a machine missing one
// never touches the line, and the message names the fix.
func TestAWalkNamesAMissingModule(t *testing.T) {
	tests := []struct {
		name   string
		loaded []string
		want   string
	}{
		{"neither", nil, "declare serport and pulse8_cec in spec.modules"},
		{"the driver", []string{"serport"}, "declare pulse8_cec in spec.modules"},
		{"the discipline", []string{"pulse8_cec"}, "declare serport in spec.modules"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fakeSerioMachine(t)
			loadSerioModules(t, test.loaded...)
			adapter{port: "1-4", tty: "ttyACM0"}.plug(t)
			ttys := newFakeTTYs(t)

			got := walkOnce(declaredSerio(ttys, pulse8Entry))

			if len(got) != 1 || got[0].State != machine.SerioRefused || got[0].Message != test.want || got[0].TTY != "ttyACM0" {
				t.Errorf("walk = %+v", got)
			}
			if ttys.openCount() != 0 {
				t.Errorf("opens = %v", ttys.opens)
			}
		})
	}
}

// A refusal stands until the hardware or the spec changes, because
// opening the line on every uevent would toggle the adapter's modem
// lines and get the same refusal again.
func TestARefusedLineIsRetriedOnlyAfterItLeaves(t *testing.T) {
	fakeSerioMachine(t)
	loadSerioModules(t, "serport", "pulse8_cec")
	pulse := adapter{port: "1-4", tty: "ttyACM0"}
	pulse.plug(t)
	ttys := newFakeTTYs(t, &fakeLine{disciplineErr: unix.EPERM})
	r := declaredSerio(ttys, pulse8Entry)

	first := walkOnce(r)
	second := walkOnce(r)
	pulse.unplug(t)
	unplugged := walkOnce(r)
	pulse.plug(t)
	replugged := walkOnce(r)

	for _, got := range [][]machine.SerioStatus{first, second} {
		if len(got) != 1 || got[0].State != machine.SerioRefused || got[0].Message != "TIOCSETD: operation not permitted" {
			t.Errorf("walk = %+v", got)
		}
	}
	if unplugged[0].State != machine.SerioMissing || replugged[0].State != machine.SerioAttached {
		t.Errorf("after the unplug %+v, after the plug %+v", unplugged, replugged)
	}
	if ttys.openCount() != 2 {
		t.Errorf("opens = %v", ttys.opens)
	}
}

func TestDeclaringAgainRetriesARefusedLine(t *testing.T) {
	fakeSerioMachine(t)
	loadSerioModules(t, "serport", "pulse8_cec")
	adapter{port: "1-4", tty: "ttyACM0"}.plug(t)
	ttys := newFakeTTYs(t, &fakeLine{disciplineErr: unix.EPERM})
	r := declaredSerio(ttys, pulse8Entry)
	walkOnce(r)

	r.declare([]machine.SerioAttachment{pulse8Entry})
	got := walkOnce(r)

	if got[0].State != machine.SerioAttached || ttys.openCount() != 2 {
		t.Errorf("walk = %+v after %d opens", got, ttys.openCount())
	}
}

// An unplug ends the read, the next walk finds no line, and the plug
// that follows starts a new holder on the new line.
func TestAnUnplugEndsTheHolderAndAPlugStartsANewOne(t *testing.T) {
	fakeSerioMachine(t)
	loadSerioModules(t, "serport", "pulse8_cec")
	pulse := adapter{port: "1-4", tty: "ttyACM0"}
	pulse.plug(t)
	line := holdingLine()
	ttys := newFakeTTYs(t, line)
	r := declaredSerio(ttys, pulse8Entry)
	walkOnce(r)

	pulse.unplug(t)
	close(line.release)
	waitEnded(t, r, "ttyACM0")
	unplugged := walkOnce(r)
	pulse.plug(t)
	replugged := walkOnce(r)

	if unplugged[0].State != machine.SerioMissing || replugged[0].State != machine.SerioAttached {
		t.Errorf("after the unplug %+v, after the plug %+v", unplugged, replugged)
	}
	if !line.closed || ttys.openCount() != 2 {
		t.Errorf("closed %v, opens %v", line.closed, ttys.opens)
	}
}

func TestAReadThatEndsWhileTheTTYStaysIsRefused(t *testing.T) {
	fakeSerioMachine(t)
	loadSerioModules(t, "serport", "pulse8_cec")
	adapter{port: "1-4", tty: "ttyACM0"}.plug(t)
	line := holdingLine()
	ttys := newFakeTTYs(t, line)
	r := declaredSerio(ttys, pulse8Entry)
	walkOnce(r)

	close(line.release)
	waitEnded(t, r, "ttyACM0")
	got := walkOnce(r)
	again := walkOnce(r)

	want := "read: the kernel ended the attachment while the tty stayed"
	if got[0].State != machine.SerioRefused || got[0].Message != want || again[0].Message != want {
		t.Errorf("walks = %+v, then %+v", got, again)
	}
	if ttys.openCount() != 1 {
		t.Errorf("opens = %v", ttys.opens)
	}
}

func TestAnEntryWithNoSerialAttachesEveryMatchingLine(t *testing.T) {
	withSerial := pulse8Entry
	withSerial.USB.Serial = "B2"
	tests := []struct {
		name  string
		entry machine.SerioAttachment
		want  []string
	}{
		{"every unit", pulse8Entry, []string{"ttyACM0", "ttyACM1"}},
		{"one unit", withSerial, []string{"ttyACM1"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fakeSerioMachine(t)
			loadSerioModules(t, "serport", "pulse8_cec")
			adapter{port: "1-4", serial: "A1", tty: "ttyACM0"}.plug(t)
			adapter{port: "1-5", serial: "B2", tty: "ttyACM1"}.plug(t)

			got := walkOnce(declaredSerio(newFakeTTYs(t), test.entry))

			var ttys []string
			for _, s := range got {
				if s.State != machine.SerioAttached {
					t.Errorf("status = %+v", s)
				}
				ttys = append(ttys, s.TTY)
			}
			if !slices.Equal(ttys, test.want) {
				t.Errorf("attached %v", ttys)
			}
		})
	}
}

func TestAWalkRefusesAProtocolTheReleaseDoesNotKnow(t *testing.T) {
	fakeSerioMachine(t)
	entry := pulse8Entry
	entry.Protocol = "pulse9-cec"

	got := walkOnce(declaredSerio(newFakeTTYs(t), entry))

	if len(got) != 1 || got[0].State != machine.SerioRefused || got[0].Message == "" {
		t.Errorf("walk = %+v", got)
	}
}

func TestAWalkWithNothingDeclaredReportsNothing(t *testing.T) {
	fakeSerioMachine(t)
	adapter{port: "1-4", tty: "ttyACM0"}.plug(t)
	ttys := newFakeTTYs(t)

	if got := walkOnce(declaredSerio(ttys)); got != nil || ttys.openCount() != 0 {
		t.Errorf("walk = %+v, opens %v", got, ttys.opens)
	}
}

// A driver that blocks the open cannot stall the walk. The walk
// reports the wait, and the walk after the open returns finds the
// holder attached.
func TestAWalkBoundsTheWaitForTheAttachCalls(t *testing.T) {
	fakeSerioMachine(t)
	loadSerioModules(t, "serport", "pulse8_cec")
	adapter{port: "1-4", tty: "ttyACM0"}.plug(t)
	old := serioSettleTimeout
	serioSettleTimeout = 10 * time.Millisecond
	t.Cleanup(func() { serioSettleTimeout = old })
	ttys := newFakeTTYs(t)
	ttys.gate = make(chan struct{})
	r := declaredSerio(ttys, pulse8Entry)

	waiting := walkOnce(r)
	close(ttys.gate)
	serioSettleTimeout = 5 * time.Second
	attached := walkOnce(r)

	if waiting[0].State != machine.SerioRefused || attached[0].State != machine.SerioAttached || ttys.openCount() != 1 {
		t.Errorf("walks = %+v, then %+v", waiting, attached)
	}
}

// publish writes serio/ only when the report changes, so an unchanged
// walk on every uevent costs no write.
func TestPublishWritesTheFactsWhenTheReportChanges(t *testing.T) {
	fakeSerioMachine(t)
	loadSerioModules(t, "serport", "pulse8_cec")
	pulse := adapter{port: "1-4", tty: "ttyACM0"}
	pulse.plug(t)
	tree := machine.FactsTree{Dir: t.TempDir()}
	r := declaredSerio(newFakeTTYs(t), pulse8Entry)

	r.publish(tree)
	facts, err := tree.Read()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(tree.Dir, "serio", "0")); err != nil {
		t.Fatal(err)
	}
	r.publish(tree)
	_, unchanged := os.Stat(filepath.Join(tree.Dir, "serio", "0"))
	pulse.registerPort(t)
	r.publish(tree)
	withPort, err := tree.Read()
	if err != nil {
		t.Fatal(err)
	}

	if len(facts.Serio) != 1 || facts.Serio[0].State != machine.SerioAttached {
		t.Errorf("first publish = %+v", facts.Serio)
	}
	if unchanged == nil {
		t.Error("an unchanged report must not be written again")
	}
	if len(withPort.Serio) != 1 || withPort.Serio[0].Port != "serio0" || len(withPort.Serio[0].Nodes) != 2 {
		t.Errorf("publish after the port = %+v", withPort.Serio)
	}
}

// A write that fails leaves the report unpublished, and the next walk
// writes it again.
func TestPublishRetriesAFailedWrite(t *testing.T) {
	fakeSerioMachine(t)
	loadSerioModules(t, "serport", "pulse8_cec")
	adapter{port: "1-4", tty: "ttyACM0"}.plug(t)
	blocked := filepath.Join(t.TempDir(), "facts")
	writeSysfs(t, filepath.Dir(blocked), "facts", "a file where the tree's directory belongs\n")
	r := declaredSerio(newFakeTTYs(t), pulse8Entry)

	r.publish(machine.FactsTree{Dir: blocked})
	if err := os.Remove(blocked); err != nil {
		t.Fatal(err)
	}
	tree := machine.FactsTree{Dir: blocked}
	r.publish(tree)
	facts, err := tree.Read()

	if err != nil || len(facts.Serio) != 1 {
		t.Errorf("facts = %+v, %v", facts, err)
	}
}

func TestDescribeSerioNamesTheEntryAndTheOutcome(t *testing.T) {
	tests := []struct {
		name   string
		status machine.SerioStatus
		want   string
	}{
		{"attached", machine.SerioStatus{Protocol: "pulse8-cec", USB: pulse8Entry.USB, TTY: "ttyACM0", Port: "serio0",
			State: machine.SerioAttached, Nodes: []string{"/dev/cec0"}},
			"liken: serio: pulse8-cec 2548:1002 attached on ttyACM0 as serio0 (/dev/cec0)"},
		{"missing", machine.SerioStatus{Protocol: "pulse8-cec", USB: pulse8Entry.USB, State: machine.SerioMissing,
			Message: "no USB device 2548:1002 is plugged in"},
			"liken: serio: pulse8-cec 2548:1002 is missing: no USB device 2548:1002 is plugged in"},
		{"refused on a line", machine.SerioStatus{Protocol: "pulse8-cec", USB: pulse8Entry.USB, TTY: "ttyACM0",
			State: machine.SerioRefused, Message: "TIOCSETD: operation not permitted"},
			"liken: serio: pulse8-cec 2548:1002 on ttyACM0 refused: TIOCSETD: operation not permitted"},
		{"refused with no line", machine.SerioStatus{Protocol: "pulse9-cec", USB: pulse8Entry.USB,
			State: machine.SerioRefused, Message: "no such protocol"},
			"liken: serio: pulse9-cec 2548:1002 refused: no such protocol"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := describeSerio(test.status); got != test.want {
				t.Errorf("got %q", got)
			}
		})
	}
}

func TestDeclaredEntriesIsACopy(t *testing.T) {
	r := declaredSerio(newFakeTTYs(t), pulse8Entry)
	entries := r.declaredEntries()
	entries[0].Protocol = "changed"

	if r.declaredEntries()[0].Protocol != "pulse8-cec" {
		t.Error("the hardware watch must not change the declared list")
	}
}

// The most specific entry wins: an entry that names a serial claims its
// line first, whatever the declaration order, and an entry without a
// serial takes only the lines no serial-specific entry took.
func TestTheMostSpecificEntryClaimsItsLineFirst(t *testing.T) {
	specific := pulse8Entry
	specific.USB.Serial = "A1"
	tests := []struct {
		name    string
		entries []machine.SerioAttachment
		plugged []adapter
		want    []machine.SerioStatus
	}{
		{"the serial-less entry first", []machine.SerioAttachment{pulse8Entry, specific},
			[]adapter{{port: "1-4", serial: "A1", tty: "ttyACM0"}, {port: "1-5", serial: "B2", tty: "ttyACM1"}},
			[]machine.SerioStatus{
				{Protocol: "pulse8-cec", USB: pulse8Entry.USB, TTY: "ttyACM1", State: machine.SerioAttached},
				{Protocol: "pulse8-cec", USB: specific.USB, TTY: "ttyACM0", State: machine.SerioAttached},
			}},
		{"the specific entry first", []machine.SerioAttachment{specific, pulse8Entry},
			[]adapter{{port: "1-4", serial: "A1", tty: "ttyACM0"}, {port: "1-5", serial: "B2", tty: "ttyACM1"}},
			[]machine.SerioStatus{
				{Protocol: "pulse8-cec", USB: specific.USB, TTY: "ttyACM0", State: machine.SerioAttached},
				{Protocol: "pulse8-cec", USB: pulse8Entry.USB, TTY: "ttyACM1", State: machine.SerioAttached},
			}},
		{"one adapter, the serial-less entry first", []machine.SerioAttachment{pulse8Entry, specific},
			[]adapter{{port: "1-4", serial: "A1", tty: "ttyACM0"}},
			[]machine.SerioStatus{
				{Protocol: "pulse8-cec", USB: pulse8Entry.USB, State: machine.SerioMissing,
					Message: "every serial line of USB device 2548:1002 is held for another spec.serio entry"},
				{Protocol: "pulse8-cec", USB: specific.USB, TTY: "ttyACM0", State: machine.SerioAttached},
			}},
		{"one adapter, the specific entry first", []machine.SerioAttachment{specific, pulse8Entry},
			[]adapter{{port: "1-4", serial: "A1", tty: "ttyACM0"}},
			[]machine.SerioStatus{
				{Protocol: "pulse8-cec", USB: specific.USB, TTY: "ttyACM0", State: machine.SerioAttached},
				{Protocol: "pulse8-cec", USB: pulse8Entry.USB, State: machine.SerioMissing,
					Message: "every serial line of USB device 2548:1002 is held for another spec.serio entry"},
			}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fakeSerioMachine(t)
			loadSerioModules(t, "serport", "pulse8_cec")
			for _, a := range test.plugged {
				a.plug(t)
			}

			got := walkOnce(declaredSerio(newFakeTTYs(t), test.entries...))

			if !slices.EqualFunc(got, test.want, serioStatusEqual) {
				t.Errorf("walk = %+v", got)
			}
		})
	}
}
