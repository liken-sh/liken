package main

// The fixture runs the controller against two fakes: an API server that
// stores Displays, and a panel that responds to DDC/CI. Both write to one
// journal, so a test can check the order of their calls, such as that
// the captured value is in status before the panel goes dark.

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"maps"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/liken-sh/liken/kubernetes/apiclient"
	"github.com/liken-sh/liken/kubernetes/events"
	"github.com/liken-sh/liken/kubernetes/events/eventstest"
)

// recordedPanel returns a fake panel that returns the capability string
// recorded in testdata/<name>.capabilities, and has a value
// for each core control that string declares.
func recordedPanel(t *testing.T, name string) *fakeMonitor {
	t.Helper()
	panel := &fakeMonitor{
		values:       map[byte]uint16{},
		maxima:       map[byte]uint16{},
		clamps:       map[byte]uint16{},
		stubborn:     map[byte]int{},
		capabilities: labCapabilities(t, name),
	}
	for code, values := range capabilityCodes(panel.capabilities) {
		if capabilityName(code) == "" {
			continue
		}
		if len(values) == 0 {
			panel.values[code], panel.maxima[code] = 50, 100
			continue
		}
		panel.values[code], panel.maxima[code] = values[0], values[len(values)-1]
	}
	return panel
}

// wiredPanel is one connector on the fixture's card, with the EDID it
// reads and the fake panel behind it.
type wiredPanel struct {
	Connector string
	Monitor   EDID
	Panel     *fakeMonitor
	// Modes and Current are what the card reports for this connector:
	// the modes it offers, in the form spec.mode uses, and the mode it
	// drives when the fixture starts.
	Modes   []string
	Current string
}

type displayFixture struct {
	t        *testing.T
	client   *apiclient.Client
	control  *displayControl
	panel    *fakeMonitor
	bench    *panelBench
	wired    []wiredPanel
	displays map[string]*Display
	journal  *journal
	refuse   int
	lists    int
	// requests is every request the API server received, as the
	// method and the path.
	requests []string
	present  map[string]bool
	version  int
	// waits counts the restore's waits between attempts, and waiting
	// receives a value at each one. The restore goroutine writes waits,
	// so it is atomic.
	waits   atomic.Int64
	waiting chan struct{}
	// stall makes the wait block until the operator's context ends, so
	// a restore never finishes, like a restore of a panel that never
	// responds.
	stall bool
	// at is the clock that both the controller and the probe cache
	// read.
	at time.Time
	// current is the mode each connector drives, prepared is the
	// devices that the prepared claims hold, and modeSets and restarts
	// record the mode switches and compositor restarts the controller
	// ran.
	current  map[string]string
	prepared map[string]bool
	modeSets []string
	// allocated is the devices that the API server reports claims hold,
	// which includes a claim whose prepare has not finished.
	// allocations counts the reads of it.
	allocated   map[string]bool
	allocations int
	restarts    int
	// serving is the mode that the compositor reports on each
	// connector, which status reports as mode.weston.
	serving map[string]string
	// unread makes the card report no mode list, as it does in every
	// pass while the operator has no connection to a compositor.
	unread bool
	// events records the Events that the controller's recorder wrote.
	// It serves the Events API in front of handler.
	events   *eventstest.Events
	recorder *events.Recorder
}

func (f *displayFixture) clock() time.Time { return f.at }

func (f *displayFixture) advance(waited time.Duration) { f.at = f.at.Add(waited) }

// newDisplayFixture builds a fixture with one panel on HDMI-A-1, the
// fake API server that stores its Display, and the controller.
func newDisplayFixture(t *testing.T, panel *fakeMonitor) *displayFixture {
	t.Helper()
	return newDisplayBench(t, wiredPanel{Connector: "HDMI-A-1", Monitor: labMonitor(), Panel: panel})
}

// newDisplayBench builds the same fixture with several panels on the
// card, for tests that show one panel's restore does not delay another
// panel.
func newDisplayBench(t *testing.T, wired ...wiredPanel) *displayFixture {
	t.Helper()
	fixture := &displayFixture{
		t:        t,
		wired:    wired,
		panel:    wired[0].Panel,
		displays: map[string]*Display{},
		journal:  &journal{},
		present:  map[string]bool{},
		waiting:  make(chan struct{}, 8),
		serving:  map[string]string{},
	}
	panels := map[string]*fakeMonitor{}
	fixture.current = map[string]string{}
	for _, one := range wired {
		one.Panel.journal = fixture.journal
		panels[one.Connector] = one.Panel
		fixture.present[one.Connector] = true
		fixture.current[one.Connector] = one.Current
	}
	controls, bench := benchPanels(t, t.TempDir(), "card1", panels)
	fixture.bench = bench
	fixture.events = &eventstest.Events{}
	fixture.client = testClient(t, fixture.events.Around(fixture.handler()))
	fixture.recorder = events.New(t.Context(), fixture.client, componentName, events.Options{Instance: "liken-1", Log: io.Discard})
	fixture.control = newDisplayControl(fixture.client, "liken-1", controls, fixture.outputs)
	fixture.control.displays.recorder = fixture.recorder
	fixture.at = time.Unix(0, 0).UTC()
	fixture.control.now = fixture.clock
	// The probe cache reads the same clock, because the window that
	// limits a second probe is measured on it.
	controls.now = fixture.clock
	fixture.control.prepared = func() (map[string]bool, error) { return fixture.prepared, nil }
	fixture.control.allocated = func() (map[string]bool, error) {
		fixture.allocations++
		return fixture.allocated, nil
	}
	// served reports the modes that the operator's Wayland connection
	// would report. The session stays 1, because no test here restarts
	// a compositor.
	fixture.control.served = func() servedOutputs {
		return servedOutputs{session: 1, modes: maps.Clone(fixture.serving)}
	}
	// setMode replaces the prepare path's whole switch. It records the
	// switch and moves the screen to the mode.
	fixture.control.setMode = func(_ context.Context, output Output, mode string) error {
		fixture.modeSets = append(fixture.modeSets, output.Connector+"="+mode)
		fixture.current[output.Connector] = mode
		return nil
	}
	fixture.control.restart = func() error {
		fixture.restarts++
		return nil
	}
	fixture.control.wait = func(ctx context.Context, _ time.Duration) error {
		fixture.waits.Add(1)
		select {
		case fixture.waiting <- struct{}{}:
		default:
		}
		if !fixture.stall {
			return nil
		}
		<-ctx.Done()
		return ctx.Err()
	}
	return fixture
}

func (f *displayFixture) outputs() []Output {
	var outputs []Output
	for _, one := range f.wired {
		output := connectedOutput(one.Connector, one.Monitor)
		output.Connected = f.present[one.Connector]
		output.OfferedModes = one.Modes
		output.CurrentMode = f.current[one.Connector]
		output.ModesRead = !f.unread
		outputs = append(outputs, output)
	}
	return outputs
}

// awaitRestore waits for the wake that a restore raises when it
// succeeds or gives up, which brings the pass back to clear the
// capture.
func (f *displayFixture) awaitRestore() {
	f.t.Helper()
	select {
	case <-f.control.wakes:
	case <-time.After(5 * time.Second):
		f.t.Fatal("the restore raised no wake")
	}
}

// labDisplayName is the name that the Display of labMonitor publishes
// under, built the way the slice's pairing attribute is.
func labDisplayName() string {
	return monitorID(labMonitor())
}

func (f *displayFixture) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.requests = append(f.requests, r.Method+" "+r.URL.Path)
		name := strings.TrimPrefix(r.URL.Path, DisplaysPath+"/")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == DisplaysPath:
			f.lists++
			list := DisplayList{}
			for _, display := range f.displays {
				list.Items = append(list.Items, *display)
			}
			slices.SortFunc(list.Items, func(a, b Display) int {
				return strings.Compare(a.Metadata.Name, b.Metadata.Name)
			})
			_ = json.NewEncoder(w).Encode(list)
		case r.Method == http.MethodGet:
			display, held := f.displays[name]
			if !held {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			_ = json.NewEncoder(w).Encode(display)
		case r.Method == http.MethodPost:
			created := &Display{}
			_ = json.NewDecoder(r.Body).Decode(created)
			f.store(created)
			f.journal.add("create %s", created.Metadata.Name)
			_ = json.NewEncoder(w).Encode(created)
		case r.Method == http.MethodPut && strings.HasSuffix(name, "/status"):
			f.writeStatus(w, r, strings.TrimSuffix(name, "/status"))
		default:
			f.t.Errorf("the operator sent %s %s, which the fake API server does not serve", r.Method, r.URL.Path)
		}
	})
}

// writeStatus stores a status write, or fails it with a 500 while
// refuse is above zero, as an API server that is restarting does.
func (f *displayFixture) writeStatus(w http.ResponseWriter, r *http.Request, name string) {
	written := &Display{}
	_ = json.NewDecoder(r.Body).Decode(written)
	if f.refuse > 0 {
		f.refuse--
		f.journal.add("status refused")
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	held, stored := f.displays[name]
	if !stored {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	// A write from a version older than the stored one is refused with
	// a conflict, as the API server refuses it.
	if version := written.Metadata.ResourceVersion; version != "" && version != held.Metadata.ResourceVersion {
		f.journal.add("status conflict")
		w.WriteHeader(http.StatusConflict)
		return
	}
	held.Status = written.Status
	f.store(held)
	f.journal.add("status %s", captureLine(held.Status))
	_ = json.NewEncoder(w).Encode(held)
}

// captureLine describes the captured block of one status write, which
// the ordering tests read.
func captureLine(status DisplayStatus) string {
	if status.Captured.empty() {
		return "captured=none"
	}
	if status.Captured.Brightness != nil {
		return "captured=brightness " + strconv.Itoa(*status.Captured.Brightness)
	}
	if status.Captured.Power != nil {
		return "captured=power " + *status.Captured.Power
	}
	return "captured=other"
}

func (f *displayFixture) store(display *Display) {
	f.version++
	display.Metadata.ResourceVersion = strconv.Itoa(f.version)
	f.displays[display.Metadata.Name] = display
}

// declare stores a Display as a person or another writer would have
// created it.
func (f *displayFixture) declare(spec DisplaySpec) *Display {
	return f.declareFor(labDisplayName(), spec)
}

func (f *displayFixture) declareFor(name string, spec DisplaySpec) *Display {
	display := &Display{
		APIVersion: DisplayAPIVersion,
		Kind:       "Display",
		Metadata:   DisplayMeta{Name: name},
		Spec:       spec,
	}
	f.store(display)
	return display
}

// captured stores the Display of a panel that has a capture and no
// override, which is the state that an operator that restarted finds.
func (f *displayFixture) captured(name, connector string, values DisplayValues) *Display {
	display := f.declareFor(name, DisplaySpec{})
	display.Status = DisplayStatus{Node: "liken-1", Connector: connector, Captured: &values}
	return display
}

func (f *displayFixture) pass() error {
	f.t.Helper()
	return f.control.pass(f.t.Context())
}

func (f *displayFixture) display() *Display {
	f.t.Helper()
	display, held := f.displays[labDisplayName()]
	if !held {
		f.t.Fatalf("the operator published no Display; it published %v", f.names())
	}
	return display
}

func (f *displayFixture) lines() []string {
	return f.journal.read()
}

// displayNamed returns one Display by name, for a bench with more than
// one panel.
func (f *displayFixture) displayNamed(name string) *Display {
	f.t.Helper()
	display, held := f.displays[name]
	if !held {
		f.t.Fatalf("the operator published no Display named %s; it published %v", name, f.names())
	}
	return display
}

func (f *displayFixture) names() []string {
	var names []string
	for name := range f.displays {
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}

func (f *displayFixture) value(code byte) uint16 {
	return f.panel.value(code)
}

func condition(display *Display, kind string) DisplayCondition {
	for _, current := range display.Status.Conditions {
		if current.Type == kind {
			return current
		}
	}
	return DisplayCondition{}
}

func TestADisplayIsPublishedForEveryPanel(t *testing.T) {
	fixture := newDisplayFixture(t, recordedPanel(t, "lg-hdr-wqhd"))

	if err := fixture.pass(); err != nil {
		t.Fatalf("running a pass: %v", err)
	}

	display := fixture.display()
	if display.Status.Node != "liken-1" || display.Status.Connector != "HDMI-A-1" {
		t.Errorf("status places the panel on %s/%s, want liken-1/HDMI-A-1",
			display.Status.Node, display.Status.Connector)
	}
	// The capabilities use the plain control names.
	brightness, carried := display.Status.Capabilities[brightnessControl]
	if !carried || brightness.Max != 100 {
		t.Errorf("brightness = %+v, want a maximum of 100", brightness)
	}
	input := display.Status.Capabilities[inputControl]
	if want := []string{"DP-1", "DP-2", "HDMI-1", "HDMI-2"}; !slices.Equal(input.Values, want) {
		t.Errorf("input = %q, want %q", input.Values, want)
	}
	if _, carried := display.Status.Capabilities[sharpnessControl]; carried {
		t.Error("status publishes a sharpness control, and this panel declares none")
	}
	// The probe reads the values, so they are published.
	if display.Status.Observed == nil || *display.Status.Observed.Brightness != 50 {
		t.Errorf("observed = %+v, want the brightness the probe read", display.Status.Observed)
	}
	if got := condition(display, ConnectedCondition).Status; got != conditionTrue {
		t.Errorf("Connected = %q, want %q", got, conditionTrue)
	}
	if got := condition(display, ResponsiveCondition).Status; got != conditionTrue {
		t.Errorf("Responsive = %q, want %q", got, conditionTrue)
	}
}

// The saved value is what brings the panel back, so it must be in
// status before the panel goes dark.
func TestTheCaptureCommitsBeforeThePanelGoesDark(t *testing.T) {
	fixture := newDisplayFixture(t, recordedPanel(t, "lg-hdr-wqhd"))
	fixture.declare(DisplaySpec{Override: &DisplayOverride{Backlight: overrideOff}})

	if err := fixture.pass(); err != nil {
		t.Fatalf("running a pass: %v", err)
	}

	journal := fixture.lines()
	captured := slices.Index(journal, "status captured=brightness 50")
	blanked := slices.Index(journal, "set brightness=0")
	if captured < 0 || blanked < 0 || captured > blanked {
		t.Fatalf("journal = %q, want the captured brightness written before the panel turned off", journal)
	}
	if fixture.value(vcpBrightness) != 0 {
		t.Errorf("the panel reports a brightness of %d, want 0", fixture.value(vcpBrightness))
	}
}

// When the capture's status write fails, the override does not darken
// the panel, because nothing could restore it.
func TestAFailedCaptureLeavesThePanelOn(t *testing.T) {
	fixture := newDisplayFixture(t, recordedPanel(t, "lg-hdr-wqhd"))
	fixture.declare(DisplaySpec{Override: &DisplayOverride{Backlight: overrideOff}})
	fixture.refuse = 1

	if err := fixture.pass(); err == nil {
		t.Fatal("the pass reported no failure, and the status write failed")
	}

	if journal := fixture.lines(); slices.Contains(journal, "set brightness=0") {
		t.Errorf("journal = %q, want no write to the panel", journal)
	}
	if fixture.value(vcpBrightness) != 50 {
		t.Errorf("the panel reports a brightness of %d, want the 50 it had before", fixture.value(vcpBrightness))
	}
	if captured := fixture.display().Status.Captured; !captured.empty() {
		t.Errorf("captured = %+v, want nothing captured", captured)
	}
}

// A pass that finds the capture already in status, as an operator that
// restarted during an override does, neither captures again nor writes
// the panel again.
func TestAnOverrideInPlaceCapturesOnce(t *testing.T) {
	fixture := newDisplayFixture(t, recordedPanel(t, "lg-hdr-wqhd"))
	fixture.declare(DisplaySpec{Override: &DisplayOverride{Backlight: overrideOff}})

	if err := fixture.pass(); err != nil {
		t.Fatalf("running a pass: %v", err)
	}
	writes := len(fixture.panel.writesTo(vcpBrightness))
	if err := fixture.pass(); err != nil {
		t.Fatalf("running a pass: %v", err)
	}

	if got := len(fixture.panel.writesTo(vcpBrightness)); got != writes {
		t.Errorf("the second pass wrote the brightness %d more times, want none", got-writes)
	}
	if brightness := fixture.display().Status.Captured.Brightness; brightness == nil || *brightness != 50 {
		t.Errorf("captured brightness = %v, want the 50 the first pass saved", brightness)
	}
}

func TestTheEndOfAnOverrideRestoresTheRightValue(t *testing.T) {
	cases := []struct {
		name string
		spec DisplaySpec
		want uint16
	}{
		{
			// With no declared value, the panel goes back to the value
			// it had before the override.
			name: "the captured value",
			spec: DisplaySpec{},
			want: 50,
		},
		{
			// The declared value wins over the saved value.
			name: "the declared value over the captured value",
			spec: DisplaySpec{Brightness: intOf(40)},
			want: 40,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			fixture := newDisplayFixture(t, recordedPanel(t, "lg-hdr-wqhd"))
			spec := c.spec
			spec.Override = &DisplayOverride{Backlight: overrideOff}
			display := fixture.declare(spec)
			if err := fixture.pass(); err != nil {
				t.Fatalf("running a pass: %v", err)
			}

			display.Spec.Override = nil
			if err := fixture.pass(); err != nil {
				t.Fatalf("running a pass: %v", err)
			}
			fixture.awaitRestore()
			if err := fixture.pass(); err != nil {
				t.Fatalf("running a pass: %v", err)
			}

			if got := fixture.value(vcpBrightness); got != c.want {
				t.Errorf("the panel reports a brightness of %d, want %d", got, c.want)
			}
			if captured := fixture.display().Status.Captured; !captured.empty() {
				t.Errorf("captured = %+v, want it cleared after the restore", captured)
			}
		})
	}
}

// A panel that is waking can ignore the first writes, so the restore
// repeats until the readback matches.
func TestTheRestoreRepeatsUntilTheReadbackMatches(t *testing.T) {
	panel := recordedPanel(t, "lg-hdr-wqhd")
	fixture := newDisplayFixture(t, panel)
	display := fixture.declare(DisplaySpec{Override: &DisplayOverride{Backlight: overrideOff}})
	if err := fixture.pass(); err != nil {
		t.Fatalf("running a pass: %v", err)
	}

	panel.stubborn[vcpBrightness] = 2
	display.Spec.Override = nil
	if err := fixture.pass(); err != nil {
		t.Fatalf("running a pass: %v", err)
	}
	fixture.awaitRestore()

	if got := fixture.waits.Load(); got != 2 {
		t.Errorf("the restore waited %d times, want 2", got)
	}
	if got := fixture.value(vcpBrightness); got != 50 {
		t.Errorf("the panel reports a brightness of %d, want the 50 it had before", got)
	}
}

func TestThePowerOverrideCapturesThePowerState(t *testing.T) {
	fixture := newDisplayFixture(t, recordedPanel(t, "lg-hdr-wqhd"))
	display := fixture.declare(DisplaySpec{Override: &DisplayOverride{Power: overrideOff}})

	if err := fixture.pass(); err != nil {
		t.Fatalf("running a pass: %v", err)
	}

	journal := fixture.lines()
	captured := slices.Index(journal, "status captured=power On")
	powered := slices.Index(journal, "set power=4")
	if captured < 0 || powered < 0 || captured > powered {
		t.Fatalf("journal = %q, want the captured power written before the panel went down", journal)
	}

	display.Spec.Override = nil
	if err := fixture.pass(); err != nil {
		t.Fatalf("running a pass: %v", err)
	}
	fixture.awaitRestore()
	if got := fixture.value(vcpPowerMode); got != powerModeOn {
		t.Errorf("the panel reports the power mode %#02x, want %#02x", got, powerModeOn)
	}
}

func TestAnEmptySpecWritesNothing(t *testing.T) {
	fixture := newDisplayFixture(t, recordedPanel(t, "lg-hdr-wqhd"))

	if err := fixture.pass(); err != nil {
		t.Fatalf("running a pass: %v", err)
	}

	if len(fixture.panel.writes()) != 0 {
		t.Errorf("the operator wrote %v to a panel whose spec sets nothing", fixture.panel.writes())
	}
}

// A DDC read wakes some panels, so a pass that finds the panel at the
// declared values opens no bus.
func TestDeclaredValuesAreWrittenOnlyWhereThePanelDiffers(t *testing.T) {
	fixture := newDisplayFixture(t, recordedPanel(t, "lg-hdr-wqhd"))
	fixture.declare(DisplaySpec{Brightness: intOf(30), Input: stringOf("HDMI-1")})

	if err := fixture.pass(); err != nil {
		t.Fatalf("running a pass: %v", err)
	}
	if got := fixture.value(vcpBrightness); got != 30 {
		t.Errorf("the panel reports a brightness of %d, want 30", got)
	}
	if got := fixture.value(vcpInput); got != 0x11 {
		t.Errorf("the panel reports the input %#02x, want %#02x", got, 0x11)
	}

	opens, writes := fixture.bench.opened(), len(fixture.panel.writes())
	if err := fixture.pass(); err != nil {
		t.Fatalf("running a pass: %v", err)
	}
	if len(fixture.panel.writes()) != writes {
		t.Errorf("the second pass wrote %v, want nothing", fixture.panel.writes()[writes:])
	}
	if fixture.bench.opened() != opens {
		t.Errorf("the second pass opened the wire %d times, want none", fixture.bench.opened()-opens)
	}
}

func TestADeclaredValueIsCheckedAgainstTheCapabilityList(t *testing.T) {
	cases := []struct {
		name      string
		spec      DisplaySpec
		wantError string
	}{
		{
			name:      "a control the panel does not support",
			spec:      DisplaySpec{Sharpness: intOf(3)},
			wantError: "no sharpness control",
		},
		{
			name:      "a number above the panel's maximum",
			spec:      DisplaySpec{Brightness: intOf(120)},
			wantError: "accepts up to 100",
		},
		{
			name:      "a value outside the panel's list",
			spec:      DisplaySpec{Input: stringOf("VGA-1")},
			wantError: "accepts [DP-1 DP-2 HDMI-1 HDMI-2]",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			fixture := newDisplayFixture(t, recordedPanel(t, "lg-hdr-wqhd"))
			fixture.declare(c.spec)

			err := fixture.pass()
			if err == nil || !strings.Contains(err.Error(), c.wantError) {
				t.Fatalf("the pass reported %v, want an error that contains %q", err, c.wantError)
			}
			if len(fixture.panel.writes()) != 0 {
				t.Errorf("the operator wrote %v to the panel", fixture.panel.writes())
			}
		})
	}
}

// The message names standby, because a panel in standby does not
// respond either, and a person told only about the menu setting would
// look in the wrong place.
func TestAPanelThatAnswersNothingIsNotResponsive(t *testing.T) {
	fixture := newDisplayFixture(t, deafMonitor())

	if err := fixture.pass(); err != nil {
		t.Fatalf("running a pass: %v", err)
	}

	display := fixture.display()
	responsive := condition(display, ResponsiveCondition)
	if responsive.Status != conditionFalse || responsive.Reason != NoDDCReplyReason {
		t.Errorf("Responsive = %q/%q, want %q/%q",
			responsive.Status, responsive.Reason, conditionFalse, NoDDCReplyReason)
	}
	if !strings.Contains(responsive.Message, "standby") {
		t.Errorf("Responsive message = %q, want it to name standby", responsive.Message)
	}
	if got := condition(display, ConnectedCondition).Status; got != conditionTrue {
		t.Errorf("Connected = %q, want %q: the panel is connected", got, conditionTrue)
	}
	if len(display.Status.Capabilities) != 0 {
		t.Errorf("capabilities = %v, want none", display.Status.Capabilities)
	}
}

// DDC/CI sends no event when a person changes a value at the panel's
// buttons, so the poll finds the change.
func TestAPanelsOwnMenuReachesTheObservedValues(t *testing.T) {
	panel := recordedPanel(t, "lg-hdr-wqhd")
	fixture := newDisplayFixture(t, panel)
	if err := fixture.pass(); err != nil {
		t.Fatalf("running a pass: %v", err)
	}

	panel.setWithButtons(vcpBrightness, 80)
	fixture.advance(pollInterval)
	if err := fixture.pass(); err != nil {
		t.Fatalf("running a pass: %v", err)
	}

	observed := fixture.display().Status.Observed
	if observed == nil || observed.Brightness == nil || *observed.Brightness != 80 {
		t.Errorf("observed = %+v, want the 80 a person set at the panel", observed)
	}
}

func TestADeclaredValueIsWrittenBackAfterAChangeAtThePanel(t *testing.T) {
	panel := recordedPanel(t, "lg-hdr-wqhd")
	fixture := newDisplayFixture(t, panel)
	fixture.declare(DisplaySpec{Brightness: intOf(30)})
	if err := fixture.pass(); err != nil {
		t.Fatalf("running a pass: %v", err)
	}
	if got := fixture.value(vcpBrightness); got != 30 {
		t.Fatalf("the panel reports a brightness of %d, want the 30 its spec sets", got)
	}

	panel.setWithButtons(vcpBrightness, 80)
	fixture.advance(pollInterval)
	if err := fixture.pass(); err != nil {
		t.Fatalf("running a pass: %v", err)
	}

	if got := fixture.value(vcpBrightness); got != 30 {
		t.Errorf("the panel reports a brightness of %d, want the declared 30 written back", got)
	}
}

// A DDC read wakes some panels, so the poll skips a panel that an
// override keeps dark, that is in standby, or that is powered off.
func TestThePollLeavesAPanelAlone(t *testing.T) {
	cases := []struct {
		name  string
		spec  DisplaySpec
		power uint16
	}{
		{
			name:  "a panel that an override turns off",
			spec:  DisplaySpec{Override: &DisplayOverride{Backlight: overrideOff}},
			power: powerModeOn,
		},
		{
			name:  "a panel in standby",
			power: powerModeStandby,
		},
		{
			name:  "a panel that is powered off",
			power: powerModeOff,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			panel := recordedPanel(t, "lg-hdr-wqhd")
			panel.setWithButtons(vcpPowerMode, c.power)
			fixture := newDisplayFixture(t, panel)
			fixture.declare(c.spec)
			if err := fixture.pass(); err != nil {
				t.Fatalf("running a pass: %v", err)
			}

			opens := fixture.bench.opened()
			panel.setWithButtons(vcpBrightness, 80)
			fixture.advance(pollInterval)
			if err := fixture.pass(); err != nil {
				t.Fatalf("running a pass: %v", err)
			}

			if got := fixture.bench.opened(); got != opens {
				t.Errorf("the pass opened the wire %d more times, want none", got-opens)
			}
		})
	}
}

// A cable's uevents arrive in a burst and each one runs a pass, so the
// poll window limits those passes to one read.
func TestThePollReadsOncePerWindow(t *testing.T) {
	fixture := newDisplayFixture(t, recordedPanel(t, "lg-hdr-wqhd"))
	if err := fixture.pass(); err != nil {
		t.Fatalf("running a pass: %v", err)
	}

	opens := fixture.bench.opened()
	fixture.advance(pollInterval)
	for range 3 {
		if err := fixture.pass(); err != nil {
			t.Fatalf("running a pass: %v", err)
		}
	}

	if got := fixture.bench.opened() - opens; got != 1 {
		t.Errorf("three passes in one window read the wire %d times, want 1", got)
	}
}

// A panel that stops responding after the probe keeps Responsive true,
// because the condition reports what the probe found.
func TestAPollThatFailsLeavesThePanelResponsive(t *testing.T) {
	panel := recordedPanel(t, "lg-hdr-wqhd")
	fixture := newDisplayFixture(t, panel)
	if err := fixture.pass(); err != nil {
		t.Fatalf("running a pass: %v", err)
	}

	panel.silence()
	fixture.advance(pollInterval)
	if err := fixture.pass(); err != nil {
		t.Fatalf("running a pass: %v", err)
	}

	display := fixture.display()
	if got := condition(display, ResponsiveCondition).Status; got != conditionTrue {
		t.Errorf("Responsive = %q, want %q", got, conditionTrue)
	}
	if observed := display.Status.Observed; observed == nil || *observed.Brightness != 50 {
		t.Errorf("observed = %+v, want the values the probe read", observed)
	}
}

// A sleeping panel can return different noise on each read. The log
// reports once that the panel went quiet, and again only after the
// panel responded in between.
func TestANoisyPanelReportsOnceEachTimeItGoesQuiet(t *testing.T) {
	fixture := newDisplayFixture(t, recordedPanel(t, "lg-hdr-wqhd"))
	faults := fixture.countFaults()

	fixture.panel.garbles()
	fixture.pollTimes(3)
	if got := faults.lines(); got != 1 {
		t.Errorf("three polls of a noisy panel printed %d fault lines, want 1:\n%s", got, faults)
	}

	fixture.panel.startResponding()
	fixture.pollTimes(1)
	fixture.panel.garbles()
	fixture.pollTimes(2)
	if got := faults.lines(); got != 2 {
		t.Errorf("a panel that responded and went quiet again printed %d fault lines, want 2:\n%s", got, faults)
	}
}

// One control that stops responding is a different fault from the
// whole panel going quiet, so each logs its own line.
func TestAPanelThatGoesQuietAfterOneControlDidReportsAgain(t *testing.T) {
	fixture := newDisplayFixture(t, recordedPanel(t, "lg-hdr-wqhd"))
	faults := fixture.countFaults()

	fixture.panel.drops(vcpBrightness)
	fixture.pollTimes(2)
	fixture.panel.garbles()
	fixture.pollTimes(2)
	if got := faults.lines(); got != 2 {
		t.Errorf("one control and then the whole panel going quiet printed %d fault lines, want 2:\n%s", got, faults)
	}
}

// countFaults runs a first pass, which probes the panel, and then
// counts the poll fault lines that later passes log.
type faultLog struct{ bytes.Buffer }

func (f *faultLog) lines() int { return strings.Count(f.String(), "polling the controls of HDMI-A-1") }

func (f *displayFixture) countFaults() *faultLog {
	f.t.Helper()
	faults := &faultLog{}
	f.control.faults = faults
	if err := f.pass(); err != nil {
		f.t.Fatalf("running a pass: %v", err)
	}
	return faults
}

// pollTimes runs passes one poll window apart.
func (f *displayFixture) pollTimes(times int) {
	f.t.Helper()
	for range times {
		f.advance(pollInterval)
		if err := f.pass(); err != nil {
			f.t.Fatalf("running a pass: %v", err)
		}
	}
}

// A panel that refused DDC/CI and then turns it on raises no event,
// because its connector and EDID do not change. The probe retry finds
// it after probeRetryInterval.
func TestAPanelThatStartsAnsweringBecomesResponsive(t *testing.T) {
	panel := recordedPanel(t, "lg-hdr-wqhd")
	panel.silent = true
	fixture := newDisplayFixture(t, panel)

	if err := fixture.pass(); err != nil {
		t.Fatalf("running a pass: %v", err)
	}
	if got := condition(fixture.display(), ResponsiveCondition).Status; got != conditionFalse {
		t.Fatalf("Responsive = %q, want %q from a panel that does not respond", got, conditionFalse)
	}

	panel.startResponding()
	fixture.advance(probeRetryInterval)
	if err := fixture.pass(); err != nil {
		t.Fatalf("running a pass: %v", err)
	}

	display := fixture.display()
	responsive := condition(display, ResponsiveCondition)
	if responsive.Status != conditionTrue {
		t.Errorf("Responsive = %q/%q, want %q", responsive.Status, responsive.Reason, conditionTrue)
	}
	if _, carried := display.Status.Capabilities[brightnessControl]; !carried {
		t.Errorf("capabilities = %v, want what the panel declares now", display.Status.Capabilities)
	}
}

// The Display keeps the captured values, so it stays after its panel
// leaves.
func TestADisplayOutlivesItsPanel(t *testing.T) {
	fixture := newDisplayFixture(t, recordedPanel(t, "lg-hdr-wqhd"))
	if err := fixture.pass(); err != nil {
		t.Fatalf("running a pass: %v", err)
	}

	fixture.present["HDMI-A-1"] = false
	if err := fixture.pass(); err != nil {
		t.Fatalf("running a pass: %v", err)
	}

	display := fixture.display()
	connected := condition(display, ConnectedCondition)
	if connected.Status != conditionFalse {
		t.Errorf("Connected = %q, want %q", connected.Status, conditionFalse)
	}
	if display.Status.Connector != "HDMI-A-1" {
		t.Errorf("connector = %q, want the connector the panel was last on", display.Status.Connector)
	}
}

// A panel with no capability string is probed one core code at a time.
func TestAPanelWithNoCapabilityStringIsAsked(t *testing.T) {
	fixture := newDisplayFixture(t, newFakeMonitor())

	if err := fixture.pass(); err != nil {
		t.Fatalf("running a pass: %v", err)
	}

	capabilities := fixture.display().Status.Capabilities
	carried := []string{}
	for name := range capabilities {
		carried = append(carried, name)
	}
	slices.Sort(carried)
	if want := []string{brightnessControl, powerControl}; !slices.Equal(carried, want) {
		t.Errorf("capabilities = %q, want %q", carried, want)
	}
}

func TestTheConditionKeepsItsTimestampWhileNothingChanges(t *testing.T) {
	fixture := newDisplayFixture(t, recordedPanel(t, "lg-hdr-wqhd"))
	if err := fixture.pass(); err != nil {
		t.Fatalf("running a pass: %v", err)
	}
	first := condition(fixture.display(), ConnectedCondition).LastTransitionTime

	fixture.control.now = func() time.Time { return time.Unix(3600, 0).UTC() }
	writes := len(fixture.lines())
	if err := fixture.pass(); err != nil {
		t.Fatalf("running a pass: %v", err)
	}

	if got := condition(fixture.display(), ConnectedCondition).LastTransitionTime; got != first {
		t.Errorf("the timestamp moved to %s, and nothing about the condition changed", got)
	}
	if journal := fixture.lines(); len(journal) != writes {
		t.Errorf("the second pass wrote %q, want nothing", journal[writes:])
	}
}

func portableMonitor() EDID {
	return EDID{Manufacturer: "BOE", ProductCode: 0x095f, ModelName: "Portable"}
}

// The pass does not wait for a restore, because a panel that never
// responds would otherwise stop the reconcile of every other panel.
func TestARestoreThatWaitsHoldsUpNoOtherPanel(t *testing.T) {
	stuck, other := recordedPanel(t, "lg-hdr-wqhd"), recordedPanel(t, "portable-display")
	stuck.stubborn[vcpBrightness] = 1000
	fixture := newDisplayBench(t,
		wiredPanel{Connector: "HDMI-A-1", Monitor: labMonitor(), Panel: stuck},
		wiredPanel{Connector: "HDMI-A-2", Monitor: portableMonitor(), Panel: other})
	fixture.stall = true
	fixture.captured(labDisplayName(), "HDMI-A-1", DisplayValues{Brightness: intOf(70)})
	fixture.declareFor(monitorID(portableMonitor()), DisplaySpec{Brightness: intOf(30)})

	passed := make(chan error, 1)
	go func() { passed <- fixture.pass() }()
	select {
	case err := <-passed:
		if err != nil {
			t.Fatalf("running a pass: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the pass did not return while a restore was still waiting on its panel")
	}

	<-fixture.waiting
	if got := other.value(vcpBrightness); got != 30 {
		t.Errorf("the other panel reports a brightness of %d, want the 30 its spec sets", got)
	}
}

func TestTwoPassesRunOneRestoreForOneConnector(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		panel := recordedPanel(t, "lg-hdr-wqhd")
		panel.stubborn[vcpBrightness] = 1000
		fixture := newDisplayFixture(t, panel)
		fixture.stall = true
		fixture.captured(labDisplayName(), "HDMI-A-1", DisplayValues{Brightness: intOf(70)})

		if err := fixture.pass(); err != nil {
			t.Fatalf("running a pass: %v", err)
		}
		<-fixture.waiting
		if err := fixture.pass(); err != nil {
			t.Fatalf("running a pass: %v", err)
		}

		// A second restore would also reach the wait, so no second
		// wait means that one restore runs.
		synctest.Wait()
		select {
		case <-fixture.waiting:
			t.Error("a second restore started for a connector that already had one")
		default:
		}
		if took := panel.writesTo(vcpBrightness); len(took) != 1 {
			t.Errorf("the panel received %d writes, want the single write of a single restore", len(took))
		}
		if captured := fixture.display().Status.Captured; captured.empty() {
			t.Error("the capture was cleared while the restore had not finished")
		}
	})
}

// The restore writes no status, so that only the pass writes status.
// It wakes the pass, and the pass clears the capture.
func TestARestoreThatLandsWakesThePassThatClearsTheCapture(t *testing.T) {
	fixture := newDisplayFixture(t, recordedPanel(t, "lg-hdr-wqhd"))
	display := fixture.declare(DisplaySpec{Override: &DisplayOverride{Backlight: overrideOff}})
	if err := fixture.pass(); err != nil {
		t.Fatalf("running a pass: %v", err)
	}

	display.Spec.Override = nil
	if err := fixture.pass(); err != nil {
		t.Fatalf("running a pass: %v", err)
	}
	if captured := fixture.display().Status.Captured; captured.empty() {
		t.Fatal("the pass that started the restore cleared the capture")
	}

	fixture.awaitRestore()
	if err := fixture.pass(); err != nil {
		t.Fatalf("running a pass: %v", err)
	}
	if captured := fixture.display().Status.Captured; !captured.empty() {
		t.Errorf("captured = %+v, want the woken pass to have cleared it", captured)
	}
	if got := fixture.value(vcpBrightness); got != 50 {
		t.Errorf("the panel reports a brightness of %d, want the 50 it had before", got)
	}
}

// An operator that restarted during a power override finds the capture
// in the Display and a panel that does not respond, because it is
// powered down. The restore must still wake it.
func TestARestoreWakesAPanelThatAnswersNothingYet(t *testing.T) {
	panel := recordedPanel(t, "lg-hdr-wqhd")
	panel.values[vcpPowerMode] = powerModeOff
	panel.silent = true
	panel.wakesAfter = 2
	fixture := newDisplayFixture(t, panel)
	fixture.captured(labDisplayName(), "HDMI-A-1", DisplayValues{Power: stringOf("on")})

	if err := fixture.pass(); err != nil {
		t.Fatalf("running a pass: %v", err)
	}
	fixture.awaitRestore()
	if err := fixture.pass(); err != nil {
		t.Fatalf("running a pass: %v", err)
	}

	if got := fixture.value(vcpPowerMode); got != powerModeOn {
		t.Errorf("the panel reports the power mode %#02x, want %#02x", got, powerModeOn)
	}
	if fixture.waits.Load() == 0 {
		t.Error("the restore matched on its first write, and the panel was not responding")
	}
	if captured := fixture.display().Status.Captured; !captured.empty() {
		t.Errorf("captured = %+v, want it cleared after the restore", captured)
	}
}

// spec.audioMute is a boolean, and the panel's mute control takes one
// of two numeric values.
func TestTheSpecMutesThePanelsSpeakers(t *testing.T) {
	fixture := newDisplayFixture(t, recordedPanel(t, "lg-hdr-wqhd"))
	fixture.declare(DisplaySpec{AudioMute: boolOf(true)})

	if err := fixture.pass(); err != nil {
		t.Fatalf("running a pass: %v", err)
	}

	if got := fixture.value(vcpAudioMute); got != 0x01 {
		t.Errorf("the panel reports the mute value %#02x, want %#02x", got, 0x01)
	}
	if muted := fixture.display().Status.Observed.AudioMute; muted == nil || !*muted {
		t.Errorf("observed mute = %v, want true", muted)
	}
}

func TestTheLoopPassesOnEveryWake(t *testing.T) {
	fixture := newDisplayFixture(t, recordedPanel(t, "lg-hdr-wqhd"))
	ctx, stop := context.WithCancel(t.Context())
	passed := make(chan struct{}, 2)
	fixture.control.outputs = func() []Output {
		passed <- struct{}{}
		return fixture.outputs()
	}

	go fixture.control.run(ctx)
	<-passed
	fixture.control.wake()
	<-passed
	stop()
}

// Nothing wakes the loop when a poll window comes due, so the tick must
// run the pass.
func TestTheLoopPassesOnItsTick(t *testing.T) {
	fixture := newDisplayFixture(t, recordedPanel(t, "lg-hdr-wqhd"))
	if fixture.control.tick != pollInterval {
		t.Errorf("the loop ticks every %s, want the poll's window of %s", fixture.control.tick, pollInterval)
	}
	fixture.control.tick = time.Millisecond
	ctx, stop := context.WithCancel(t.Context())
	defer stop()
	passed := make(chan struct{}, 4)
	fixture.control.outputs = func() []Output {
		passed <- struct{}{}
		return fixture.outputs()
	}

	go fixture.control.run(ctx)
	<-passed

	select {
	case <-passed:
	case <-time.After(5 * time.Second):
		t.Fatal("the loop made no second pass, and its tick came due")
	}
}

// A panel that leaves raises a uevent that wakes the loop, so the list
// of every Display runs once per backstop interval and not once per
// poll window.
func TestTheSweepKeepsTheSlowerCadence(t *testing.T) {
	fixture := newDisplayFixture(t, recordedPanel(t, "lg-hdr-wqhd"))
	if err := fixture.pass(); err != nil {
		t.Fatalf("running a pass: %v", err)
	}

	for range 3 {
		fixture.advance(pollInterval)
		if err := fixture.pass(); err != nil {
			t.Fatalf("running a pass: %v", err)
		}
	}
	if fixture.lists != 1 {
		t.Errorf("the poll's passes listed the displays %d times, want the one listing of the first pass",
			fixture.lists)
	}

	fixture.advance(backstopInterval)
	if err := fixture.pass(); err != nil {
		t.Fatalf("running a pass: %v", err)
	}
	if fixture.lists != 2 {
		t.Errorf("the displays were listed %d times, want a listing on the slower cadence", fixture.lists)
	}
}

func intOf(value int) *int { return &value }

func boolOf(value bool) *bool { return &value }

func stringOf(value string) *string { return &value }

// labScreen reads one output from the sysfs fixture that the slice
// tests read, so the Display's identity and size are checked against a
// recorded EDID and not a struct written by hand.
func labScreen(t *testing.T, connector string) Output {
	t.Helper()
	for _, output := range discoverOutputs(labSysfs(t), "card1") {
		if output.Connector == connector {
			return output
		}
	}
	t.Fatalf("the lab fixture wires no %s", connector)
	return Output{}
}

// offeredModes returns every mode of the connector in the form that the
// card reports, at one refresh rate each.
func offeredModes(output Output) []string {
	var modes []string
	for _, name := range output.Modes {
		modes = append(modes, name+"@60")
	}
	return modes
}

func TestStatusReportsTheScreen(t *testing.T) {
	screen := labScreen(t, "HDMI-A-1")
	fixture := newDisplayBench(t, wiredPanel{
		Connector: "HDMI-A-1",
		Monitor:   screen.Monitor,
		Panel:     recordedPanel(t, "lg-hdr-wqhd"),
		Modes:     offeredModes(screen),
		Current:   "3840x1600@60",
	})

	if err := fixture.pass(); err != nil {
		t.Fatalf("running a pass: %v", err)
	}

	status := fixture.displayNamed(monitorID(screen.Monitor)).Status
	if status.Manufacturer != screen.Monitor.Manufacturer || status.Model != screen.Monitor.ModelName {
		t.Errorf("status names %s %s, want %s %s",
			status.Manufacturer, status.Model, screen.Monitor.Manufacturer, screen.Monitor.ModelName)
	}
	if status.Serial != screen.Monitor.Serial {
		t.Errorf("serial = %q, want %q", status.Serial, screen.Monitor.Serial)
	}
	if status.WidthMillimeters != screen.Monitor.WidthMillimeters ||
		status.HeightMillimeters != screen.Monitor.HeightMillimeters {
		t.Errorf("size = %dx%dmm, want %dx%dmm", status.WidthMillimeters, status.HeightMillimeters,
			screen.Monitor.WidthMillimeters, screen.Monitor.HeightMillimeters)
	}
	if status.Mode == nil || status.Mode.Kernel != "3840x1600@60" {
		t.Errorf("mode = %+v, want the kernel at the mode the card drives", status.Mode)
	}
}

// The card's mode on a connector and the compositor's mode can differ,
// and a client draws at the compositor's mode, so the Display reports
// both.
func TestStatusReportsTheKernelModeAndTheCompositorMode(t *testing.T) {
	screen := labScreen(t, "HDMI-A-1")
	fixture := newDisplayBench(t, wiredPanel{
		Connector: "HDMI-A-1",
		Monitor:   screen.Monitor,
		Panel:     recordedPanel(t, "lg-hdr-wqhd"),
		Modes:     offeredModes(screen),
		Current:   "1920x1080@60",
	})
	fixture.serving["HDMI-A-1"] = "3840x1600@60"

	if err := fixture.pass(); err != nil {
		t.Fatalf("running a pass: %v", err)
	}

	mode := fixture.displayNamed(monitorID(screen.Monitor)).Status.Mode
	if mode == nil {
		t.Fatal("status reports no mode at all")
	}
	if mode.Kernel != "1920x1080@60" {
		t.Errorf("mode.kernel = %q, want the mode the card is synced to", mode.Kernel)
	}
	if mode.Weston != "3840x1600@60" {
		t.Errorf("mode.weston = %q, want the mode the compositor serves", mode.Weston)
	}
}

func TestStatusReportsNoCompositorModeWhileNoCompositorAnswers(t *testing.T) {
	screen := labScreen(t, "HDMI-A-1")
	fixture := newDisplayBench(t, wiredPanel{
		Connector: "HDMI-A-1",
		Monitor:   screen.Monitor,
		Panel:     recordedPanel(t, "lg-hdr-wqhd"),
		Modes:     offeredModes(screen),
		Current:   "3840x1600@60",
	})

	if err := fixture.pass(); err != nil {
		t.Fatalf("running a pass: %v", err)
	}

	mode := fixture.displayNamed(monitorID(screen.Monitor)).Status.Mode
	if mode == nil || mode.Kernel != "3840x1600@60" {
		t.Fatalf("mode = %+v, want the kernel at the mode the card drives", mode)
	}
	if mode.Weston != "" {
		t.Errorf("mode.weston = %q, want nothing while no compositor replies", mode.Weston)
	}
}

// status.modes has no length limit, so it lists every mode, where the
// slice attribute lists fewer. The recorded monitor offers more modes
// than the attribute fits.
func TestTheFullModeListOutgrowsTheSliceAttribute(t *testing.T) {
	screen := labScreen(t, "HDMI-A-1")
	offered := offeredModes(screen)
	fixture := newDisplayBench(t, wiredPanel{
		Connector: "HDMI-A-1",
		Monitor:   screen.Monitor,
		Panel:     recordedPanel(t, "lg-hdr-wqhd"),
		Modes:     offered,
	})

	if err := fixture.pass(); err != nil {
		t.Fatalf("running a pass: %v", err)
	}

	published := fixture.displayNamed(monitorID(screen.Monitor)).Status.Modes
	if !slices.Equal(published, offered) {
		t.Errorf("modes = %q, want the card's whole list %q", published, offered)
	}
	attribute := strings.Fields(attributeList(screen.Modes))
	if len(attribute) >= len(published) {
		t.Fatalf("the slice attribute has %d of the %d modes, so this panel proves no truncation",
			len(attribute), len(published))
	}
}

func TestTheDeclaredModeWaitsForAClaimToEnd(t *testing.T) {
	cases := []struct {
		name      string
		mode      *string
		current   string
		held      map[string]bool
		want      []string
		wantError string
	}{
		{
			name:    "a declared mode on a free screen",
			mode:    stringOf("1920x1080@60"),
			current: "3840x1600@60",
			want:    []string{"HDMI-A-1=1920x1080@60"},
		},
		{
			name:    "a declared mode the screen already runs",
			mode:    stringOf("1920x1080@60"),
			current: "1920x1080@60",
		},
		{
			name:    "a declared mode while a claim holds the screen",
			mode:    stringOf("1920x1080@60"),
			current: "3840x1600@60",
			held:    map[string]bool{"hdmi-a-1": true},
		},
		{
			// A claim on another connector does not hold back this
			// screen's mode.
			name:    "a declared mode while a claim holds another screen",
			mode:    stringOf("1920x1080@60"),
			current: "3840x1600@60",
			held:    map[string]bool{"hdmi-a-2": true},
			want:    []string{"HDMI-A-1=1920x1080@60"},
		},
		{
			name:      "a mode the card does not offer",
			mode:      stringOf("800x600@60"),
			current:   "3840x1600@60",
			wantError: "offers",
		},
		{
			name:    "no declaration at all",
			current: "3840x1600@60",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			fixture := newDisplayBench(t, wiredPanel{
				Connector: "HDMI-A-1",
				Monitor:   labMonitor(),
				Panel:     recordedPanel(t, "lg-hdr-wqhd"),
				Modes:     []string{"3840x1600@60", "1920x1080@60"},
				Current:   c.current,
			})
			fixture.prepared = c.held
			fixture.declare(DisplaySpec{Mode: c.mode})

			err := fixture.pass()
			if c.wantError == "" && err != nil {
				t.Fatalf("running a pass: %v", err)
			}
			if c.wantError != "" && (err == nil || !strings.Contains(err.Error(), c.wantError)) {
				t.Fatalf("the pass reported %v, want an error that contains %q", err, c.wantError)
			}
			if !slices.Equal(fixture.modeSets, c.want) {
				t.Errorf("the controller set %q, want %q", fixture.modeSets, c.want)
			}
		})
	}
}

// Without a mode list from the card, a pass can neither apply the
// declared mode nor report that the card does not offer it.
func TestTheDeclaredModeWaitsForAReadOfTheCard(t *testing.T) {
	fixture := newDisplayBench(t, wiredPanel{
		Connector: "HDMI-A-1",
		Monitor:   labMonitor(),
		Panel:     recordedPanel(t, "lg-hdr-wqhd"),
	})
	fixture.unread = true
	fixture.declare(DisplaySpec{Mode: stringOf("1920x1080@60")})

	if err := fixture.pass(); err != nil {
		t.Fatalf("running a pass: %v", err)
	}
	if len(fixture.modeSets) != 0 {
		t.Errorf("the controller set %q with no read of the card", fixture.modeSets)
	}
}

func TestTheDeclaredModeIsAppliedWhenTheClaimEnds(t *testing.T) {
	fixture := newDisplayBench(t, wiredPanel{
		Connector: "HDMI-A-1",
		Monitor:   labMonitor(),
		Panel:     recordedPanel(t, "lg-hdr-wqhd"),
		Modes:     []string{"3840x1600@60", "1920x1080@60"},
		Current:   "3840x1600@60",
	})
	fixture.prepared = map[string]bool{"hdmi-a-1": true}
	fixture.declare(DisplaySpec{Mode: stringOf("1920x1080@60")})
	if err := fixture.pass(); err != nil {
		t.Fatalf("running a pass: %v", err)
	}
	if len(fixture.modeSets) != 0 {
		t.Fatalf("the controller set %q while a claim held the screen", fixture.modeSets)
	}

	fixture.prepared = nil
	if err := fixture.pass(); err != nil {
		t.Fatalf("running a pass: %v", err)
	}

	if want := []string{"HDMI-A-1=1920x1080@60"}; !slices.Equal(fixture.modeSets, want) {
		t.Errorf("the controller set %q, want %q", fixture.modeSets, want)
	}
	// The screen now runs the declared mode, so a later pass switches
	// nothing.
	if err := fixture.pass(); err != nil {
		t.Fatalf("running a pass: %v", err)
	}
	if len(fixture.modeSets) != 1 {
		t.Errorf("the controller set %q, want the one switch", fixture.modeSets)
	}
}

// flap reports that the compositor re-created an output with another
// monitor or mode, as the Wayland watch reports it, and then runs a
// pass. The sysfs connectors do not change.
func (f *displayFixture) flap(t *testing.T) {
	t.Helper()
	f.control.outputsChanged(true)
	if err := f.pass(); err != nil {
		t.Fatalf("running a pass: %v", err)
	}
}

func TestTheCompositorRestartsAfterAnOutputIsRecreated(t *testing.T) {
	fixture := newDisplayFixture(t, recordedPanel(t, "lg-hdr-wqhd"))
	if err := fixture.pass(); err != nil {
		t.Fatalf("running a pass: %v", err)
	}

	fixture.flap(t)
	if fixture.restarts != 0 {
		t.Fatalf("the compositor restarted %d times inside the flap", fixture.restarts)
	}

	fixture.advance(outputSettleWindow)
	if err := fixture.pass(); err != nil {
		t.Fatalf("running a pass: %v", err)
	}
	if fixture.restarts != 1 {
		t.Errorf("the compositor restarted %d times, want 1", fixture.restarts)
	}

	fixture.advance(outputSettleWindow)
	if err := fixture.pass(); err != nil {
		t.Fatalf("running a pass: %v", err)
	}
	if fixture.restarts != 1 {
		t.Errorf("the compositor restarted %d times, want the one restart of one flap", fixture.restarts)
	}
}

// A restart would end the workload that draws on the claimed screen,
// so it waits until the claim ends.
func TestTheCompositorRestartWaitsForTheClaimToEnd(t *testing.T) {
	fixture := newDisplayFixture(t, recordedPanel(t, "lg-hdr-wqhd"))
	fixture.prepared = map[string]bool{"hdmi-a-1": true}
	if err := fixture.pass(); err != nil {
		t.Fatalf("running a pass: %v", err)
	}

	fixture.flap(t)
	fixture.advance(outputSettleWindow)
	if err := fixture.pass(); err != nil {
		t.Fatalf("running a pass: %v", err)
	}
	if fixture.restarts != 0 {
		t.Fatalf("the compositor restarted %d times while a claim held a screen", fixture.restarts)
	}

	fixture.prepared = nil
	if err := fixture.pass(); err != nil {
		t.Fatalf("running a pass: %v", err)
	}

	if fixture.restarts != 1 {
		t.Errorf("the compositor restarted %d times after the claim ended, want 1", fixture.restarts)
	}
}

// The outputs that an operator finds at start are not re-created
// outputs, and the compositor's surfaces already have the right size.
func TestTheFirstPassDoesNotRestartTheCompositor(t *testing.T) {
	fixture := newDisplayFixture(t, recordedPanel(t, "lg-hdr-wqhd"))

	if err := fixture.pass(); err != nil {
		t.Fatalf("running a pass: %v", err)
	}
	fixture.advance(outputSettleWindow)
	if err := fixture.pass(); err != nil {
		t.Fatalf("running a pass: %v", err)
	}

	if fixture.restarts != 0 {
		t.Errorf("the compositor restarted %d times with no output re-created", fixture.restarts)
	}
}

// A monitor that leaves and returns in sysfs may not have made the
// compositor re-create its output, so only the compositor's report
// starts a restart.
func TestOnlyTheCompositorsReportStartsARestart(t *testing.T) {
	fixture := newDisplayFixture(t, recordedPanel(t, "lg-hdr-wqhd"))
	if err := fixture.pass(); err != nil {
		t.Fatalf("running a pass: %v", err)
	}

	fixture.present["HDMI-A-1"] = false
	if err := fixture.pass(); err != nil {
		t.Fatalf("running a pass: %v", err)
	}
	fixture.present["HDMI-A-1"] = true
	if err := fixture.pass(); err != nil {
		t.Fatalf("running a pass: %v", err)
	}
	fixture.advance(outputSettleWindow)
	if err := fixture.pass(); err != nil {
		t.Fatalf("running a pass: %v", err)
	}

	if fixture.restarts != 0 {
		t.Errorf("the compositor restarted %d times on a monitor that moved with no word from the compositor", fixture.restarts)
	}
}

func TestTheCompositorRestartWaitsForAPanelRestore(t *testing.T) {
	panel := recordedPanel(t, "lg-hdr-wqhd")
	panel.stubborn[vcpBrightness] = 1000
	fixture := newDisplayFixture(t, panel)
	fixture.stall = true
	fixture.captured(labDisplayName(), "HDMI-A-1", DisplayValues{Brightness: intOf(70)})
	if err := fixture.pass(); err != nil {
		t.Fatalf("running a pass: %v", err)
	}
	<-fixture.waiting

	fixture.flap(t)
	fixture.advance(outputSettleWindow)
	if err := fixture.pass(); err != nil {
		t.Fatalf("running a pass: %v", err)
	}

	if fixture.restarts != 0 {
		t.Errorf("the compositor restarted %d times while a restore was still writing", fixture.restarts)
	}
}

// When a claim ends, the screen goes back to the declared mode without
// waiting for the tick. The unprepare wakes the loop after it deletes
// the claim's CDI spec file, so the woken pass finds the screen free.
func TestAnUnprepareAppliesTheDeclaredMode(t *testing.T) {
	restoreCDIDir := cdiDir
	t.Cleanup(func() { cdiDir = restoreCDIDir })
	// The plugin fixture sets the CDI spec directory, so the unprepare
	// removes the spec that this test writes by hand.
	plugin, _ := labPluginWithConfig(t, screenRequest(), "")
	if err := writeCDISpec("film", []cdiDevice{{
		Name:           "film-hdmi-a-1",
		ContainerEdits: outputEdits("/var/run/display.liken.sh", "wayland-0", "hdmi-a-1"),
	}}); err != nil {
		t.Fatalf("writing the CDI spec of the claim film: %v", err)
	}

	fixture := newDisplayBench(t, wiredPanel{
		Connector: "HDMI-A-1",
		Monitor:   labMonitor(),
		Panel:     recordedPanel(t, "lg-hdr-wqhd"),
		Modes:     []string{"3840x1600@60", "1920x1080@60"},
		Current:   "3840x1600@60",
	})
	fixture.control.prepared = preparedOutputs
	fixture.declare(DisplaySpec{Mode: stringOf("1920x1080@60")})
	if err := fixture.pass(); err != nil {
		t.Fatalf("running a pass: %v", err)
	}
	if len(fixture.modeSets) != 0 {
		t.Fatalf("the controller set %q while the film held the screen", fixture.modeSets)
	}

	// heldAtWake records the prepared devices at the moment of the
	// wake.
	var heldAtWake map[string]bool
	plugin.republish = func() {
		heldAtWake, _ = preparedOutputs()
		fixture.control.wake()
	}
	if err := plugin.unprepareClaim("film"); err != nil {
		t.Fatalf("unpreparing the claim film: %v", err)
	}
	if len(heldAtWake) != 0 {
		t.Errorf("the wake went out while %v still held a screen", heldAtWake)
	}

	select {
	case <-fixture.control.wakes:
	case <-time.After(5 * time.Second):
		t.Fatal("the unprepare raised no wake")
	}
	if err := fixture.pass(); err != nil {
		t.Fatalf("running a pass: %v", err)
	}

	if want := []string{"HDMI-A-1=1920x1080@60"}; !slices.Equal(fixture.modeSets, want) {
		t.Errorf("the controller set %q on the woken pass, want %q", fixture.modeSets, want)
	}
}

// A DDC read wakes some panels, so a pass does not read a panel that
// the override has darkened.
func TestAnObeyedOverrideReadsThePanelNoFurther(t *testing.T) {
	panel := recordedPanel(t, "lg-hdr-wqhd")
	fixture := newDisplayFixture(t, panel)
	fixture.declare(DisplaySpec{Override: &DisplayOverride{Backlight: overrideOff}})
	if err := fixture.pass(); err != nil {
		t.Fatalf("running a pass: %v", err)
	}
	if got := fixture.value(vcpBrightness); got != 0 {
		t.Fatalf("the panel reports a brightness of %d, want the blank", got)
	}

	opens := fixture.bench.opened()
	for range 3 {
		if err := fixture.pass(); err != nil {
			t.Fatalf("running a pass: %v", err)
		}
	}

	if got := fixture.bench.opened(); got != opens {
		t.Errorf("the passes after the blank opened the wire %d more times, want none", got-opens)
	}
}
