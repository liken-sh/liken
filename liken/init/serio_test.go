package main

// Tests for the serio walk, on the fake machine that seriolines_test.go
// builds.

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"golang.org/x/sys/unix"

	"github.com/liken-sh/liken/liken/machine"
)

// declaredSerio returns a registry that opens lines through ttys and
// has entries declared, as the boot does with the manifest's list.
func declaredSerio(ttys *fakeTTYs, entries ...machine.SerioAttachment) *serioRegistry {
	r := newSerioRegistry(ttys.open)
	r.declare(entries)
	return r
}

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
		t.Fatalf("the registry has no holder on %s, want one", tty)
	}
	waitDone(t, h)
}

func TestAWalkAttachesAMatchedLine(t *testing.T) {
	fakeSerioMachine(t)
	loadSerioModules(t, "serport", "pulse8_cec")
	adapter{port: "1-4", tty: "ttyACM0"}.plug(t)
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
		t.Errorf("the first walk reports %+v and the second %+v, want %+v for both", got, again, want)
	}
	if ttys.opens[0] != filepath.Join(devRoot, "ttyACM0") || ttys.openCount() != 1 {
		t.Errorf("the walks opened %v, want one open of ttyACM0 held across both walks", ttys.opens)
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
			"USB device 2548:1002 has no serial line. Declare cdc_acm in spec.modules"},
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
				t.Errorf("the walk reports %+v, want one Missing entry with no tty and the message %q", got, test.want)
			}
			if ttys.openCount() != 0 {
				t.Errorf("the walk opened %v, want no open", ttys.opens)
			}
		})
	}
}

// A walk checks that the modules are loaded before it opens the line,
// so a machine that lacks one never touches the line, and the refusal
// message says which modules to declare.
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
				t.Errorf("the walk reports %+v, want one Refused entry on ttyACM0 with the message %q", got, test.want)
			}
			if ttys.openCount() != 0 {
				t.Errorf("the walk opened %v, want no open", ttys.opens)
			}
		})
	}
}

// A walk does not open a refused line again while the same tty stays
// and the backoff runs, because each open toggles the adapter's modem
// lines. An unplug and a plug give a new tty, which a walk opens at
// once.
func TestARefusedLineIsNotOpenedAgainUntilItsTTYChanges(t *testing.T) {
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
		if len(got) != 1 || got[0].State != machine.SerioRefused || !strings.Contains(got[0].Message, "TIOCSETD") {
			t.Errorf("a walk reports %+v, want one Refused entry with the TIOCSETD error", got)
		}
	}
	if unplugged[0].State != machine.SerioMissing || replugged[0].State != machine.SerioAttached {
		t.Errorf("after the unplug the walk reports %+v and after the plug %+v, want Missing and then Attached", unplugged, replugged)
	}
	if ttys.openCount() != 2 {
		t.Errorf("the walks opened %v, want 2 opens: the first plug and the second plug", ttys.opens)
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
		t.Errorf("after declaring again the walk reports %+v with %d opens, want Attached with 2 opens", got, ttys.openCount())
	}
}

// An unplug ends the holder's read and the next walk finds no line.
// After a plug, the next walk starts a new holder on the new line.
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
		t.Errorf("after the unplug the walk reports %+v and after the plug %+v, want Missing and then Attached", unplugged, replugged)
	}
	if !line.closed || ttys.openCount() != 2 {
		t.Errorf("the first line is closed: %v, and the walks opened %v, want it closed and 2 opens", line.closed, ttys.opens)
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
		t.Errorf("the first walk reports %+v and the second %+v, want %+v for both", got, again, want)
	}
	if ttys.openCount() != 1 {
		t.Errorf("the walks opened %v, want 1 open", ttys.opens)
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
					t.Errorf("the walk reports %+v, want State Attached", s)
				}
				ttys = append(ttys, s.TTY)
			}
			if !slices.Equal(ttys, test.want) {
				t.Errorf("the walk attached %v, want %v", ttys, test.want)
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
		t.Errorf("the walk reports %+v, want one Refused entry with a message", got)
	}
}

func TestAWalkWithNothingDeclaredReportsNothing(t *testing.T) {
	fakeSerioMachine(t)
	adapter{port: "1-4", tty: "ttyACM0"}.plug(t)
	ttys := newFakeTTYs(t)

	if got := walkOnce(declaredSerio(ttys)); got != nil || ttys.openCount() != 0 {
		t.Errorf("the walk reports %+v and opened %v, want no entries and no open", got, ttys.opens)
	}
}

// A walk waits no longer than serioSettleTimeout for a driver that
// blocks the open. It reports the line as refused, and the next walk
// after the open returns reports it attached.
func TestAWalkBoundsTheWaitForTheAttachCalls(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		fakeSerioMachine(t)
		loadSerioModules(t, "serport", "pulse8_cec")
		adapter{port: "1-4", tty: "ttyACM0"}.plug(t)
		ttys := newFakeTTYs(t)
		ttys.gate = make(chan struct{})
		r := declaredSerio(ttys, pulse8Entry)

		waiting := walkOnce(r)
		close(ttys.gate)
		// The next walk does not wait for a holder that an earlier walk
		// started, so the test waits for the attach calls that the gate
		// released.
		r.mu.Lock()
		h := r.holders["ttyACM0"]
		r.mu.Unlock()
		select {
		case <-h.settled:
		case <-time.After(5 * time.Second):
			t.Fatal("the holder did not finish its attach calls within 5 seconds, want them finished")
		}
		attached := walkOnce(r)

		if waiting[0].State != machine.SerioRefused || attached[0].State != machine.SerioAttached || ttys.openCount() != 1 {
			t.Errorf("the walks report %+v, then %+v, want Refused, then Attached with 1 open", waiting, attached)
		}
	})
}

// publish writes serio/ only when the report changes, so a walk that
// finds no change writes nothing.
func TestPublishWritesTheFactsWhenTheReportChanges(t *testing.T) {
	fakeSerioMachine(t)
	loadSerioModules(t, "serport", "pulse8_cec")
	pulse := adapter{port: "1-4", tty: "ttyACM0", portless: true}
	pulse.plug(t)
	tree := machine.FactsTree{Dir: t.TempDir()}
	r := declaredSerio(newFakeTTYs(t), pulse8Entry)

	r.publish(tree)
	facts, err := tree.Read()
	if err != nil {
		t.Fatalf("reading the facts tree after the first publish: %v", err)
	}
	if err := os.Remove(filepath.Join(tree.Dir, "serio", "0")); err != nil {
		t.Fatalf("removing the serio/0 fact: %v", err)
	}
	r.publish(tree)
	_, unchanged := os.Stat(filepath.Join(tree.Dir, "serio", "0"))
	pulse.registerPort(t)
	r.publish(tree)
	withPort, err := tree.Read()
	if err != nil {
		t.Fatalf("reading the facts tree after the port registered: %v", err)
	}

	if len(facts.Serio) != 1 || facts.Serio[0].State != machine.SerioRefused {
		t.Errorf("the first publish wrote %+v, want one Refused entry", facts.Serio)
	}
	if unchanged == nil {
		t.Error("the second publish wrote serio/0 again, want no write because the report did not change")
	}
	if len(withPort.Serio) != 1 || withPort.Serio[0].State != machine.SerioAttached ||
		withPort.Serio[0].Port != "serio0" || len(withPort.Serio[0].Nodes) != 2 {
		t.Errorf("the publish after the port registered wrote %+v, want one Attached entry on serio0 with 2 nodes", withPort.Serio)
	}
}

// When a write fails, the report counts as unpublished, so the next
// walk writes it again.
func TestPublishRetriesAFailedWrite(t *testing.T) {
	fakeSerioMachine(t)
	loadSerioModules(t, "serport", "pulse8_cec")
	adapter{port: "1-4", tty: "ttyACM0"}.plug(t)
	blocked := filepath.Join(t.TempDir(), "facts")
	writeSysfs(t, filepath.Dir(blocked), "facts", "a file where the tree's directory belongs\n")
	r := declaredSerio(newFakeTTYs(t), pulse8Entry)

	r.publish(machine.FactsTree{Dir: blocked})
	if err := os.Remove(blocked); err != nil {
		t.Fatalf("removing the file that blocks the facts directory: %v", err)
	}
	tree := machine.FactsTree{Dir: blocked}
	r.publish(tree)
	facts, err := tree.Read()

	if err != nil || len(facts.Serio) != 1 {
		t.Errorf("reading the facts after the retry gives %+v and error %v, want one entry and no error", facts, err)
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
			State: machine.SerioRefused, Message: "setting the serport line discipline (TIOCSETD): operation not permitted"},
			"liken: serio: pulse8-cec 2548:1002 on ttyACM0 refused: setting the serport line discipline (TIOCSETD): operation not permitted"},
		{"refused with no line", machine.SerioStatus{Protocol: "pulse9-cec", USB: pulse8Entry.USB,
			State: machine.SerioRefused, Message: "no such protocol"},
			"liken: serio: pulse9-cec 2548:1002 refused: no such protocol"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := describeSerio(test.status); got != test.want {
				t.Errorf("describeSerio = %q, want %q", got, test.want)
			}
		})
	}
}

func TestDeclaredEntriesIsACopy(t *testing.T) {
	r := declaredSerio(newFakeTTYs(t), pulse8Entry)
	entries := r.declaredEntries()
	entries[0].Protocol = "changed"

	if r.declaredEntries()[0].Protocol != "pulse8-cec" {
		t.Error("changing a returned entry changed the declared list, want declaredEntries to return a copy")
	}
}

// attachedNodes are the device nodes that the fake attached adapter
// creates.
var attachedNodes = []string{"/dev/cec0", "/dev/input/event13"}

// An entry with a serial claims its line first, whatever the
// declaration order. An entry without a serial gets only the lines
// that no entry with a serial took.
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
				{Protocol: "pulse8-cec", USB: pulse8Entry.USB, TTY: "ttyACM1", State: machine.SerioAttached,
					Port: "serio0", Nodes: attachedNodes},
				{Protocol: "pulse8-cec", USB: specific.USB, TTY: "ttyACM0", State: machine.SerioAttached,
					Port: "serio0", Nodes: attachedNodes},
			}},
		{"the specific entry first", []machine.SerioAttachment{specific, pulse8Entry},
			[]adapter{{port: "1-4", serial: "A1", tty: "ttyACM0"}, {port: "1-5", serial: "B2", tty: "ttyACM1"}},
			[]machine.SerioStatus{
				{Protocol: "pulse8-cec", USB: specific.USB, TTY: "ttyACM0", State: machine.SerioAttached,
					Port: "serio0", Nodes: attachedNodes},
				{Protocol: "pulse8-cec", USB: pulse8Entry.USB, TTY: "ttyACM1", State: machine.SerioAttached,
					Port: "serio0", Nodes: attachedNodes},
			}},
		{"one adapter, the serial-less entry first", []machine.SerioAttachment{pulse8Entry, specific},
			[]adapter{{port: "1-4", serial: "A1", tty: "ttyACM0"}},
			[]machine.SerioStatus{
				{Protocol: "pulse8-cec", USB: pulse8Entry.USB, State: machine.SerioMissing,
					Message: "every serial line of USB device 2548:1002 is in use by another spec.serio entry"},
				{Protocol: "pulse8-cec", USB: specific.USB, TTY: "ttyACM0", State: machine.SerioAttached,
					Port: "serio0", Nodes: attachedNodes},
			}},
		{"one adapter, the specific entry first", []machine.SerioAttachment{specific, pulse8Entry},
			[]adapter{{port: "1-4", serial: "A1", tty: "ttyACM0"}},
			[]machine.SerioStatus{
				{Protocol: "pulse8-cec", USB: specific.USB, TTY: "ttyACM0", State: machine.SerioAttached,
					Port: "serio0", Nodes: attachedNodes},
				{Protocol: "pulse8-cec", USB: pulse8Entry.USB, State: machine.SerioMissing,
					Message: "every serial line of USB device 2548:1002 is in use by another spec.serio entry"},
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
				t.Errorf("the walk reports %+v, want %+v", got, test.want)
			}
		})
	}
}
