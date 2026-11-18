package main

// A write that a device did not confirm is made once per spec
// generation. The record in status stops a restarted operator from
// writing it again.

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"
)

// restartOperator replaces the controller with a new one that has no
// in-memory state, over the same API server, panels, and screens.
func (f *displayFixture) restartOperator() {
	f.t.Helper()
	panels := map[string]*fakeMonitor{}
	for _, one := range f.wired {
		panels[one.Connector] = one.Panel
	}
	controls, bench := benchPanels(f.t, f.t.TempDir(), "card1", panels)
	controls.now = f.clock
	previous := f.control
	f.control = newDisplayControl(f.client, "liken-1", controls, f.outputs)
	f.control.displays.recorder = f.recorder
	f.control.now = previous.now
	f.control.prepared = previous.prepared
	f.control.allocated = previous.allocated
	f.control.served = previous.served
	f.control.setMode = previous.setMode
	f.control.restart = previous.restart
	f.control.wait = previous.wait
	f.bench = bench
}

// edit changes spec and raises the Display's generation, as the API
// server does on a spec edit.
func (f *displayFixture) edit(change func(spec *DisplaySpec)) {
	display := f.display()
	change(&display.Spec)
	display.Metadata.Generation++
}

func (f *displayFixture) passes(count int) {
	f.t.Helper()
	for range count {
		f.advance(pollInterval)
		// A pass that makes an unconfirmed write returns an error.
		// The tests check the outcome through the panel and status
		// instead.
		_ = f.pass()
	}
}

func unconfirmedControls(display *Display) []string {
	var controls []string
	for _, entry := range display.Status.Unconfirmed {
		controls = append(controls, fmt.Sprintf("%s=%s gen %d", entry.Control, entry.Value, entry.Generation))
	}
	return controls
}

func TestARestartAgainstAMatchingPanelWritesNothing(t *testing.T) {
	cases := []struct {
		name string
		spec DisplaySpec
	}{
		{"a brightness", DisplaySpec{Brightness: intOf(30)}},
		{"an input", DisplaySpec{Input: stringOf("HDMI-1")}},
		{"both", DisplaySpec{Brightness: intOf(30), Input: stringOf("HDMI-1")}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			panel := recordedPanel(t, "lg-hdr-wqhd")
			panel.values[vcpBrightness] = 30
			panel.values[vcpInput] = 0x11
			fixture := newDisplayFixture(t, panel)
			fixture.declare(c.spec)

			fixture.restartOperator()
			fixture.passes(3)

			if got := panel.writes(); len(got) != 0 {
				t.Errorf("the panel received the writes %v, want nothing", got)
			}
		})
	}
}

// The panel accepts the input write and reads back a different input.
// The operator writes once in this generation, a restarted operator
// does not write again, and an edit to spec allows one more write.
func TestAnInputThePanelReadsBackDifferentlyIsWrittenOncePerGeneration(t *testing.T) {
	panel := recordedPanel(t, "lg-hdr-wqhd")
	panel.clamps[vcpInput] = 0x0f
	fixture := newDisplayFixture(t, panel)
	fixture.declare(DisplaySpec{Input: stringOf("HDMI-1")})

	if err := fixture.pass(); err == nil {
		t.Fatal("the pass reported nothing for a write the panel did not confirm")
	}
	fixture.passes(5)
	fixture.restartOperator()
	fixture.passes(5)

	if got := panel.writesTo(vcpInput); !slices.Equal(got, []uint16{0x11}) {
		t.Errorf("the panel received the writes %v, want one write of the input", got)
	}
	entries := fixture.display().Status.Unconfirmed
	if len(entries) != 1 || entries[0].Control != inputControl || entries[0].Value != "HDMI-1" ||
		entries[0].Readback != "DP-1" || entries[0].Message == "" {
		t.Fatalf("unconfirmed = %+v, want the input, the value, the readback, and the message", entries)
	}

	fixture.edit(func(spec *DisplaySpec) { spec.Brightness = intOf(40) })
	fixture.passes(3)

	if got := panel.writesTo(vcpInput); !slices.Equal(got, []uint16{0x11, 0x11}) {
		t.Errorf("the panel received the writes %v, want one more write of the input after the edit", got)
	}
	if got := unconfirmedControls(fixture.display()); !slices.Equal(got, []string{"input=HDMI-1 gen 1"}) {
		t.Errorf("unconfirmed = %v, want the one record of the new generation", got)
	}
}

func TestTheRecordClearsWhenThePanelHoldsTheValue(t *testing.T) {
	panel := recordedPanel(t, "lg-hdr-wqhd")
	panel.clamps[vcpInput] = 0x0f
	fixture := newDisplayFixture(t, panel)
	fixture.declare(DisplaySpec{Input: stringOf("HDMI-1")})
	fixture.passes(1)

	panel.setWithButtons(vcpInput, 0x11)
	fixture.passes(1)

	if got := fixture.display().Status.Unconfirmed; len(got) != 0 {
		t.Errorf("unconfirmed = %+v, want nothing once the panel holds the value", got)
	}
}

// A restore that the panel never confirms stops after restoreAttempts
// writes. status.captured keeps the value to restore, status.unconfirmed
// names the control, and neither a later pass nor a restarted operator
// writes again.
func TestARestoreThePanelNeverConfirmsStops(t *testing.T) {
	panel := recordedPanel(t, "lg-hdr-wqhd")
	fixture := newDisplayFixture(t, panel)
	display := fixture.declare(DisplaySpec{Override: &DisplayOverride{Backlight: overrideOff}})
	if err := fixture.pass(); err != nil {
		t.Fatalf("running a pass: %v", err)
	}

	panel.stubborn[vcpBrightness] = 1000
	display.Spec.Override = nil
	display.Metadata.Generation++
	if err := fixture.pass(); err != nil {
		t.Fatalf("running a pass: %v", err)
	}
	fixture.awaitRestore()
	fixture.passes(3)
	fixture.restartOperator()
	fixture.passes(3)

	if got := panel.writesTo(vcpBrightness); len(got) != 1+restoreAttempts {
		t.Errorf("the panel received the writes %v, want the override and %d restore writes", got, restoreAttempts)
	}
	if got := unconfirmedControls(fixture.display()); !slices.Equal(got, []string{"brightness=50 gen 1"}) {
		t.Errorf("unconfirmed = %v, want the brightness the restore did not land", got)
	}
	if fixture.display().Status.Captured.empty() {
		t.Error("the capture was cleared, and it is the only record of the value to restore")
	}
}

// The compositor does not serve the declared mode. The operator
// restarts it once in this generation, a restarted operator does not
// restart it again, and an edit to spec allows one more restart.
func TestAModeTheCompositorDeclinesRestartsItOncePerGeneration(t *testing.T) {
	fixture := newDisplayBench(t, wiredPanel{
		Connector: "HDMI-A-1",
		Monitor:   labMonitor(),
		Panel:     recordedPanel(t, "lg-hdr-wqhd"),
		Modes:     []string{"3840x1600@60", "1920x1080@60"},
		Current:   "3840x1600@60",
	})
	fixture.control.setMode = func(_ context.Context, output Output, mode string) error {
		fixture.modeSets = append(fixture.modeSets, output.Connector+"="+mode)
		return fmt.Errorf("%w: %s did not report the mode %s", errModeDeclined, output.Connector, mode)
	}
	fixture.declare(DisplaySpec{Mode: stringOf("1920x1080@60")})

	fixture.passes(3)
	fixture.restartOperator()
	fixture.passes(3)

	if want := []string{"HDMI-A-1=1920x1080@60"}; !slices.Equal(fixture.modeSets, want) {
		t.Errorf("the controller set %q, want %q", fixture.modeSets, want)
	}
	entries := fixture.display().Status.Unconfirmed
	if len(entries) != 1 || entries[0].Control != modeControl || entries[0].Readback != "3840x1600@60" {
		t.Fatalf("unconfirmed = %+v, want the mode and what the screen runs", entries)
	}

	fixture.edit(func(spec *DisplaySpec) { spec.Brightness = intOf(40) })
	fixture.passes(3)

	if len(fixture.modeSets) != 2 {
		t.Errorf("the controller set %q, want one more switch after the edit", fixture.modeSets)
	}
}

// A mode switch that fails for another reason, such as a compositor
// that cannot be stopped, is not recorded as unconfirmed, so the
// operator tries it again.
func TestAModeSwitchThatFailedIsNotADecline(t *testing.T) {
	fixture := newDisplayBench(t, wiredPanel{
		Connector: "HDMI-A-1",
		Monitor:   labMonitor(),
		Panel:     recordedPanel(t, "lg-hdr-wqhd"),
		Modes:     []string{"3840x1600@60", "1920x1080@60"},
		Current:   "3840x1600@60",
	})
	fixture.control.setMode = func(_ context.Context, output Output, mode string) error {
		fixture.modeSets = append(fixture.modeSets, output.Connector+"="+mode)
		return fmt.Errorf("ending the compositor: no process runs weston")
	}
	fixture.declare(DisplaySpec{Mode: stringOf("1920x1080@60")})

	fixture.passes(2)

	if len(fixture.modeSets) != 2 {
		t.Errorf("the controller set %q, want a switch on each pass", fixture.modeSets)
	}
	if got := fixture.display().Status.Unconfirmed; len(got) != 0 {
		t.Errorf("unconfirmed = %+v, want nothing", got)
	}
}

// A screen that already runs the declared mode gets no switch, before
// or after an operator restart. This is the case for a new pod, because
// the declare container starts its compositor in the declared mode.
func TestAScreenThatRunsTheDeclaredModeGetsNoSwitch(t *testing.T) {
	fixture := newDisplayBench(t, wiredPanel{
		Connector: "HDMI-A-1",
		Monitor:   labMonitor(),
		Panel:     recordedPanel(t, "lg-hdr-wqhd"),
		Modes:     []string{"3840x1600@60", "1920x1080@60"},
		Current:   "1920x1080@60",
	})
	fixture.declare(DisplaySpec{Mode: stringOf("1920x1080@60")})

	fixture.passes(2)
	fixture.restartOperator()
	fixture.passes(2)

	if len(fixture.modeSets) != 0 {
		t.Errorf("the controller set %q, want nothing", fixture.modeSets)
	}
}

func TestRecordsOfEarlierGenerationsAreDropped(t *testing.T) {
	display := &Display{
		Metadata: DisplayMeta{Generation: 4},
		Status: DisplayStatus{Unconfirmed: []DisplayUnconfirmed{
			{Control: inputControl, Value: "HDMI-1", Generation: 3},
			{Control: modeControl, Value: "1920x1080@60", Generation: 4},
		}},
	}

	records := writeRecordsOf(display)

	if records.isUnconfirmed(inputControl, "HDMI-1") {
		t.Error("a record of an earlier generation still blocks the write")
	}
	if !records.isUnconfirmed(modeControl, "1920x1080@60") {
		t.Error("the record of this generation does not block the switch")
	}
	records.record(modeControl, "1280x720@60", "", nil, time.Unix(0, 0))
	if got := records.publishedUnconfirmed(); len(got) != 1 || got[0].Value != "1280x720@60" {
		t.Errorf("published = %+v, want one record per control", got)
	}
}

// A panel confirms the input write and then switches input itself, as
// a panel does when it moves to the input that has a signal. The
// operator writes the input, writes it back writeBackLimit times, then
// stops and records why. A restarted operator stays stopped, and an
// edit to spec resets the count.
func TestAPanelThatChangesAValueByItselfGetsThreeWritesBack(t *testing.T) {
	panel := recordedPanel(t, "lg-hdr-wqhd")
	fixture := newDisplayFixture(t, panel)
	fixture.declare(DisplaySpec{Input: stringOf("HDMI-1")})
	switchesAway := func(count int) {
		for range count {
			fixture.passes(1)
			panel.setWithButtons(vcpInput, 0x0f)
		}
	}

	switchesAway(6)
	fixture.restartOperator()
	switchesAway(3)

	if got := panel.writesTo(vcpInput); len(got) != 1+writeBackLimit {
		t.Errorf("the panel received the writes %v, want the first write and %d writes back", got, writeBackLimit)
	}
	entries := fixture.display().Status.Unconfirmed
	if len(entries) != 1 || entries[0].Control != inputControl || entries[0].Readback != "DP-1" ||
		!strings.Contains(entries[0].Message, "by itself") {
		t.Fatalf("unconfirmed = %+v, want the input the panel keeps changing", entries)
	}

	fixture.edit(func(spec *DisplaySpec) { spec.Brightness = intOf(40) })
	switchesAway(6)

	if got := panel.writesTo(vcpInput); len(got) != 2*(1+writeBackLimit) {
		t.Errorf("the panel received %d input writes, want %d after the edit started the count again",
			len(got), 2*(1+writeBackLimit))
	}
}

// A person changes a value at the panel's buttons once. The operator
// writes the declared value back, and status counts both writes.
func TestOneChangeAtThePanelIsWrittenBackAndCounted(t *testing.T) {
	panel := recordedPanel(t, "lg-hdr-wqhd")
	fixture := newDisplayFixture(t, panel)
	fixture.declare(DisplaySpec{Brightness: intOf(30)})
	fixture.passes(1)

	panel.setWithButtons(vcpBrightness, 80)
	fixture.passes(3)

	if got := panel.writesTo(vcpBrightness); !slices.Equal(got, []uint16{30, 30}) {
		t.Errorf("the panel received the writes %v, want the write and one write back", got)
	}
	written := fixture.display().Status.Written
	if len(written) != 1 || written[0].Control != brightnessControl || written[0].Count != 2 {
		t.Errorf("written = %+v, want two confirmed writes of the brightness", written)
	}
	if got := fixture.display().Status.Unconfirmed; len(got) != 0 {
		t.Errorf("unconfirmed = %+v, want nothing", got)
	}
}
