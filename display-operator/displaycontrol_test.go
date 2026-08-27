package main

// One fixture holds both ends of the operator: an API server that
// stores Displays and a panel that answers DDC/CI, and both write
// one journal, so a test reads the order the two were touched in.
// The order is the whole of the capture rule: the captured value
// commits before the panel goes dark.

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// A panel of the lab drill, answering the capability string it
// answered there and holding a value for every core control it
// declares.
func drillPanel(t *testing.T, name string) *fakeMonitor {
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

// One panel of the bench: the connector it is on, the monitor
// the connector reads, and the panel that answers DDC/CI behind it.
type wiredPanel struct {
	Connector string
	Monitor   EDID
	Panel     *fakeMonitor
	// What the card says about this connector: the modes it
	// offers, in the form spec.mode states, and the mode it drives
	// when the fixture starts.
	Modes   []string
	Current string
}

type displayFixture struct {
	t        *testing.T
	client   *Client
	control  *displayControl
	panel    *fakeMonitor
	bench    *panelBench
	wired    []wiredPanel
	displays map[string]*Display
	journal  *journal
	refuse   int
	lists    int
	present  map[string]bool
	version  int
	// The restore's own goroutine reaches these, so the count
	// is atomic and the channel is what a test waits on to know the
	// restore is between attempts.
	waits   atomic.Int64
	waiting chan struct{}
	// A restore that never lands. The wait holds until the
	// operator's context ends, which is what a panel that answers
	// nothing costs a real operator.
	stall bool
	// The clock both the controller and the probe cache read.
	at time.Time
	// The screens' side of the card: what each connector drives
	// now, what the prepared claims hold, the mode writes the
	// controller ordered, and the compositor restarts it ordered.
	current  map[string]string
	held     map[string]bool
	modeSets []string
	restarts int
	// What the compositor reports it serves on each connector,
	// which is the other half of the mode status reports. It is empty
	// on a bench with no compositor answering.
	serving map[string]string
}

func (f *displayFixture) clock() time.Time { return f.at }

// The clock moves, which is how a test reaches the far side of
// the window that holds a second probe back.
func (f *displayFixture) advance(waited time.Duration) { f.at = f.at.Add(waited) }

// The panel of the drill on one connector, the API server that
// holds its resource, and the controller between them.
func newDisplayFixture(t *testing.T, panel *fakeMonitor) *displayFixture {
	t.Helper()
	return newDisplayBench(t, wiredPanel{Connector: "HDMI-A-1", Monitor: labMonitor(), Panel: panel})
}

// The same bench with more than one panel on the card, for the
// tests that prove one panel does not hold up another.
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
	fixture.client = testClient(t, fixture.handler())
	fixture.control = newDisplayControl(fixture.client, "liken-1", controls, fixture.outputs)
	fixture.at = time.Unix(0, 0).UTC()
	fixture.control.now = fixture.clock
	// The probe cache reads the same clock, because the window
	// that rate-limits a second ask is measured on it.
	controls.now = fixture.clock
	fixture.control.prepared = func() (map[string]bool, error) { return fixture.held, nil }
	// What the compositor reports, which the standing Wayland
	// connection carries on the machine. A bench states one session,
	// because nothing here restarts a compositor.
	fixture.control.served = func() servedOutputs {
		return servedOutputs{session: 1, modes: maps.Clone(fixture.serving)}
	}
	// The mode seam stands for the prepare path's whole switch,
	// so the fixture records what was ordered and moves the screen to
	// it, the way the compositor's restart does.
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
		output := litOutput(one.Connector, one.Monitor)
		output.Connected = f.present[one.Connector]
		output.OfferedModes = one.Modes
		output.CurrentMode = f.current[one.Connector]
		outputs = append(outputs, output)
	}
	return outputs
}

// The wake the restore raises when it lands or gives up, which
// is what brings the pass back to clear the capture.
func (f *displayFixture) awaitRestore() {
	f.t.Helper()
	select {
	case <-f.control.wakes:
	case <-time.After(5 * time.Second):
		f.t.Fatal("the restore raised no wake")
	}
}

// The name the drill's ultrawide publishes under, built the
// same way the slice's pairing attribute is.
func labDisplayName() string {
	return monitorID(labMonitor())
}

func (f *displayFixture) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
			f.t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
	})
}

// The status write, and the refusal a test asks for. A refused
// write is the API server that was restarting, and the operator must
// leave the panel as it is.
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
	held.Status = written.Status
	f.store(held)
	f.journal.add("status %s", captureLine(held.Status))
	_ = json.NewEncoder(w).Encode(held)
}

// What one status write says about the captured block, which is
// what the ordering test reads.
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

// A resource a person or a machine writer already wrote.
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

// The resource of a panel that stands with a capture and no
// override, which is the state an operator that restarted comes back
// to.
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

// One resource by name, for a bench with more than one panel
// on the card.
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

// What the panel holds now for one control.
func (f *displayFixture) holds(code byte) uint16 {
	return f.panel.holds(code)
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
	fixture := newDisplayFixture(t, drillPanel(t, "lg-hdr-wqhd"))

	if err := fixture.pass(); err != nil {
		t.Fatal(err)
	}

	display := fixture.display()
	if display.Status.Node != "liken-1" || display.Status.Connector != "HDMI-A-1" {
		t.Errorf("status places the panel on %s/%s, want liken-1/HDMI-A-1",
			display.Status.Node, display.Status.Connector)
	}
	// The capability list is the panel's own, in plain names.
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
	// The probe is a read, so the values it read publish.
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

// The failure that plan 17 could not survive. The value that
// brings the panel back is durable before the panel goes dark.
func TestTheCaptureCommitsBeforeThePanelGoesDark(t *testing.T) {
	fixture := newDisplayFixture(t, drillPanel(t, "lg-hdr-wqhd"))
	fixture.declare(DisplaySpec{Override: &DisplayOverride{Backlight: overrideOff}})

	if err := fixture.pass(); err != nil {
		t.Fatal(err)
	}

	journal := fixture.lines()
	captured := slices.Index(journal, "status captured=brightness 50")
	blanked := slices.Index(journal, "set brightness=0")
	if captured < 0 || blanked < 0 || captured > blanked {
		t.Fatalf("journal = %q, want the captured brightness written before the panel went dark", journal)
	}
	if fixture.holds(vcpBrightness) != 0 {
		t.Errorf("the panel holds a brightness of %d, want 0", fixture.holds(vcpBrightness))
	}
}

// A status write that fails leaves the panel lit, because a
// panel that went dark with no captured value stays dark.
func TestAFailedCaptureLeavesThePanelLit(t *testing.T) {
	fixture := newDisplayFixture(t, drillPanel(t, "lg-hdr-wqhd"))
	fixture.declare(DisplaySpec{Override: &DisplayOverride{Backlight: overrideOff}})
	fixture.refuse = 1

	if err := fixture.pass(); err == nil {
		t.Fatal("the pass reported no failure, and the status write failed")
	}

	if journal := fixture.lines(); slices.Contains(journal, "set brightness=0") {
		t.Errorf("journal = %q, want no write to the panel", journal)
	}
	if fixture.holds(vcpBrightness) != 50 {
		t.Errorf("the panel holds a brightness of %d, want the 50 it was lit at", fixture.holds(vcpBrightness))
	}
	if captured := fixture.display().Status.Captured; !captured.empty() {
		t.Errorf("captured = %+v, want nothing captured", captured)
	}
}

// An override that stands over a restart of the operator finds
// the value in status and blanks nothing twice.
func TestAStandingOverrideCapturesOnce(t *testing.T) {
	fixture := newDisplayFixture(t, drillPanel(t, "lg-hdr-wqhd"))
	fixture.declare(DisplaySpec{Override: &DisplayOverride{Backlight: overrideOff}})

	if err := fixture.pass(); err != nil {
		t.Fatal(err)
	}
	writes := len(fixture.panel.took(vcpBrightness))
	if err := fixture.pass(); err != nil {
		t.Fatal(err)
	}

	if got := len(fixture.panel.took(vcpBrightness)); got != writes {
		t.Errorf("the second pass wrote the brightness %d more times, want none", got-writes)
	}
	if brightness := fixture.display().Status.Captured.Brightness; brightness == nil || *brightness != 50 {
		t.Errorf("captured brightness = %v, want the 50 the first pass saved", brightness)
	}
}

func TestTheOverrideLiftsToTheRightValue(t *testing.T) {
	cases := []struct {
		name string
		spec DisplaySpec
		want uint16
	}{
		{
			// With no declaration, the panel goes back to what
			// stood before the override.
			name: "the captured value",
			spec: DisplaySpec{},
			want: 50,
		},
		{
			// The declaration is what the panel rests at, so it
			// wins over the value the override displaced.
			name: "the resting declaration over the captured value",
			spec: DisplaySpec{Brightness: intOf(40)},
			want: 40,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			fixture := newDisplayFixture(t, drillPanel(t, "lg-hdr-wqhd"))
			held := c.spec
			held.Override = &DisplayOverride{Backlight: overrideOff}
			display := fixture.declare(held)
			if err := fixture.pass(); err != nil {
				t.Fatal(err)
			}

			display.Spec.Override = nil
			if err := fixture.pass(); err != nil {
				t.Fatal(err)
			}
			fixture.awaitRestore()
			if err := fixture.pass(); err != nil {
				t.Fatal(err)
			}

			if got := fixture.holds(vcpBrightness); got != c.want {
				t.Errorf("the panel holds a brightness of %d, want %d", got, c.want)
			}
			if captured := fixture.display().Status.Captured; !captured.empty() {
				t.Errorf("captured = %+v, want it cleared after the restore", captured)
			}
		})
	}
}

// A panel that is waking answers slowly, and the restore
// repeats until the readback matches. This is what replaced the
// consumer's wake ladder.
func TestTheRestoreRepeatsUntilTheReadbackMatches(t *testing.T) {
	panel := drillPanel(t, "lg-hdr-wqhd")
	fixture := newDisplayFixture(t, panel)
	display := fixture.declare(DisplaySpec{Override: &DisplayOverride{Backlight: overrideOff}})
	if err := fixture.pass(); err != nil {
		t.Fatal(err)
	}

	panel.stubborn[vcpBrightness] = 2
	display.Spec.Override = nil
	if err := fixture.pass(); err != nil {
		t.Fatal(err)
	}
	fixture.awaitRestore()

	if got := fixture.waits.Load(); got != 2 {
		t.Errorf("the restore waited %d times, want 2", got)
	}
	if got := fixture.holds(vcpBrightness); got != 50 {
		t.Errorf("the panel holds a brightness of %d, want the 50 it was lit at", got)
	}
}

// The override that powers the panel down, and the capture that
// records the state to bring it back to.
func TestThePowerOverrideCapturesThePowerState(t *testing.T) {
	fixture := newDisplayFixture(t, drillPanel(t, "lg-hdr-wqhd"))
	display := fixture.declare(DisplaySpec{Override: &DisplayOverride{Power: overrideOff}})

	if err := fixture.pass(); err != nil {
		t.Fatal(err)
	}

	journal := fixture.lines()
	captured := slices.Index(journal, "status captured=power on")
	powered := slices.Index(journal, "set power=4")
	if captured < 0 || powered < 0 || captured > powered {
		t.Fatalf("journal = %q, want the captured power written before the panel went down", journal)
	}

	display.Spec.Override = nil
	if err := fixture.pass(); err != nil {
		t.Fatal(err)
	}
	fixture.awaitRestore()
	if got := fixture.holds(vcpPowerMode); got != powerModeOn {
		t.Errorf("the panel holds the power mode %#02x, want %#02x", got, powerModeOn)
	}
}

// The parameters-only rule. An empty spec states nothing, so
// the operator writes nothing.
func TestAnEmptySpecWritesNothing(t *testing.T) {
	fixture := newDisplayFixture(t, drillPanel(t, "lg-hdr-wqhd"))

	if err := fixture.pass(); err != nil {
		t.Fatal(err)
	}

	if len(fixture.panel.writes()) != 0 {
		t.Errorf("the operator wrote %v to a panel no spec states anything about", fixture.panel.writes())
	}
}

// The resting layer is reconciled by writing where the panel
// diverges from the declaration, and not otherwise. A DDC read wakes
// some panels, so a steady pass must send nothing.
func TestTheRestingSpecWritesOnlyOnDivergence(t *testing.T) {
	fixture := newDisplayFixture(t, drillPanel(t, "lg-hdr-wqhd"))
	fixture.declare(DisplaySpec{Brightness: intOf(30), Input: stringOf("HDMI-1")})

	if err := fixture.pass(); err != nil {
		t.Fatal(err)
	}
	if got := fixture.holds(vcpBrightness); got != 30 {
		t.Errorf("the panel holds a brightness of %d, want 30", got)
	}
	if got := fixture.holds(vcpInput); got != 0x11 {
		t.Errorf("the panel holds the input %#02x, want %#02x", got, 0x11)
	}

	opens, writes := fixture.bench.opened(), len(fixture.panel.writes())
	if err := fixture.pass(); err != nil {
		t.Fatal(err)
	}
	if len(fixture.panel.writes()) != writes {
		t.Errorf("the second pass wrote %v, want nothing", fixture.panel.writes()[writes:])
	}
	if fixture.bench.opened() != opens {
		t.Errorf("the second pass opened the wire %d times, want none", fixture.bench.opened()-opens)
	}
}

// A value the panel does not carry is reported and never
// written, because the capability list is what says a control exists.
func TestTheRestingSpecIsJudgedAgainstTheCapabilityList(t *testing.T) {
	cases := []struct {
		name string
		spec DisplaySpec
		says string
	}{
		{
			name: "a control the panel does not carry",
			spec: DisplaySpec{Sharpness: intOf(3)},
			says: "no sharpness control",
		},
		{
			name: "a number above the panel's maximum",
			spec: DisplaySpec{Brightness: intOf(120)},
			says: "accepts up to 100",
		},
		{
			name: "a value outside the panel's list",
			spec: DisplaySpec{Input: stringOf("VGA-1")},
			says: "accepts [DP-1 DP-2 HDMI-1 HDMI-2]",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			fixture := newDisplayFixture(t, drillPanel(t, "lg-hdr-wqhd"))
			fixture.declare(c.spec)

			err := fixture.pass()
			if err == nil || !strings.Contains(err.Error(), c.says) {
				t.Fatalf("the pass reported %v, want a failure that says %q", err, c.says)
			}
			if len(fixture.panel.writes()) != 0 {
				t.Errorf("the operator wrote %v to the panel", fixture.panel.writes())
			}
		})
	}
}

// The lab's other panel refuses the protocol outright, and the
// resource reports it rather than publishing an empty control list
// with nothing to explain it.
func TestAPanelThatAnswersNothingIsNotResponsive(t *testing.T) {
	fixture := newDisplayFixture(t, deafMonitor())

	if err := fixture.pass(); err != nil {
		t.Fatal(err)
	}

	display := fixture.display()
	responsive := condition(display, ResponsiveCondition)
	if responsive.Status != conditionFalse || responsive.Reason != NoDDCReplyReason {
		t.Errorf("Responsive = %q/%q, want %q/%q",
			responsive.Status, responsive.Reason, conditionFalse, NoDDCReplyReason)
	}
	if got := condition(display, ConnectedCondition).Status; got != conditionTrue {
		t.Errorf("Connected = %q, want %q: the panel is on the wire", got, conditionTrue)
	}
	if len(display.Status.Capabilities) != 0 {
		t.Errorf("capabilities = %v, want none", display.Status.Capabilities)
	}
}

// A person at the panel's own buttons is the one writer this
// operator cannot see. The poll is what finds the change, and this is
// the pass that publishes it.
func TestAPanelsOwnMenuReachesTheObservedValues(t *testing.T) {
	panel := drillPanel(t, "lg-hdr-wqhd")
	fixture := newDisplayFixture(t, panel)
	if err := fixture.pass(); err != nil {
		t.Fatal(err)
	}

	panel.turnedTo(vcpBrightness, 80)
	fixture.advance(pollInterval)
	if err := fixture.pass(); err != nil {
		t.Fatal(err)
	}

	observed := fixture.display().Status.Observed
	if observed == nil || observed.Brightness == nil || *observed.Brightness != 80 {
		t.Errorf("observed = %+v, want the 80 a person set at the panel", observed)
	}
}

// The point of the poll. A declared resting value is what the
// panel rests at, so a change at the panel's own buttons is a
// divergence, and the pass that reads it writes it back.
func TestADeclaredValueHealsAfterAChangeAtThePanel(t *testing.T) {
	panel := drillPanel(t, "lg-hdr-wqhd")
	fixture := newDisplayFixture(t, panel)
	fixture.declare(DisplaySpec{Brightness: intOf(30)})
	if err := fixture.pass(); err != nil {
		t.Fatal(err)
	}
	if got := fixture.holds(vcpBrightness); got != 30 {
		t.Fatalf("the panel holds a brightness of %d, want the 30 its spec states", got)
	}

	panel.turnedTo(vcpBrightness, 80)
	fixture.advance(pollInterval)
	if err := fixture.pass(); err != nil {
		t.Fatal(err)
	}

	if got := fixture.holds(vcpBrightness); got != 30 {
		t.Errorf("the panel holds a brightness of %d, want the declared 30 written back", got)
	}
}

// The guards. A read is a wake stimulus on some panels, so the
// poll never touches a panel that is held dark, believed asleep, or
// powered down.
func TestThePollLeavesAPanelAlone(t *testing.T) {
	cases := []struct {
		name  string
		spec  DisplaySpec
		power uint16
	}{
		{
			name:  "a panel held dark by an override",
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
			panel := drillPanel(t, "lg-hdr-wqhd")
			panel.turnedTo(vcpPowerMode, c.power)
			fixture := newDisplayFixture(t, panel)
			fixture.declare(c.spec)
			if err := fixture.pass(); err != nil {
				t.Fatal(err)
			}

			opens := fixture.bench.opened()
			panel.turnedTo(vcpBrightness, 80)
			fixture.advance(pollInterval)
			if err := fixture.pass(); err != nil {
				t.Fatal(err)
			}

			if got := fixture.bench.opened(); got != opens {
				t.Errorf("the pass opened the wire %d more times, want none", got-opens)
			}
		})
	}
}

// The window, for the reason the refusal's window exists: a
// cable's uevents arrive in a burst, and every one of them is a pass.
func TestThePollReadsOncePerWindow(t *testing.T) {
	fixture := newDisplayFixture(t, drillPanel(t, "lg-hdr-wqhd"))
	if err := fixture.pass(); err != nil {
		t.Fatal(err)
	}

	opens := fixture.bench.opened()
	fixture.advance(pollInterval)
	for range 3 {
		if err := fixture.pass(); err != nil {
			t.Fatal(err)
		}
	}

	if got := fixture.bench.opened() - opens; got != 1 {
		t.Errorf("three passes in one window read the wire %d times, want 1", got)
	}
}

// A panel that stops answering between the probe and the poll
// is not a panel that answers no DDC/CI. The condition reports what
// the probe found, and the next window asks again.
func TestAPollThatFailsLeavesThePanelResponsive(t *testing.T) {
	panel := drillPanel(t, "lg-hdr-wqhd")
	fixture := newDisplayFixture(t, panel)
	if err := fixture.pass(); err != nil {
		t.Fatal(err)
	}

	panel.silence()
	fixture.advance(pollInterval)
	if err := fixture.pass(); err != nil {
		t.Fatal(err)
	}

	display := fixture.display()
	if got := condition(display, ResponsiveCondition).Status; got != conditionTrue {
		t.Errorf("Responsive = %q, want %q", got, conditionTrue)
	}
	if observed := display.Status.Observed; observed == nil || *observed.Brightness != 50 {
		t.Errorf("observed = %+v, want the values the probe read", observed)
	}
}

// The panel that refused DDC/CI starts answering, and the next
// pass past the window publishes what it carries. Nothing about the
// connector or the EDID changed, so nothing else could have told the
// operator to look again.
func TestAPanelThatStartsAnsweringBecomesResponsive(t *testing.T) {
	panel := drillPanel(t, "lg-hdr-wqhd")
	panel.silent = true
	fixture := newDisplayFixture(t, panel)

	if err := fixture.pass(); err != nil {
		t.Fatal(err)
	}
	if got := condition(fixture.display(), ResponsiveCondition).Status; got != conditionFalse {
		t.Fatalf("Responsive = %q, want %q from a panel that answers nothing", got, conditionFalse)
	}

	panel.answers()
	fixture.advance(probeRetryInterval)
	if err := fixture.pass(); err != nil {
		t.Fatal(err)
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

// The resource holds the captured state, so it outlives the
// panel's absence and reports it.
func TestADisplayOutlivesItsPanel(t *testing.T) {
	fixture := newDisplayFixture(t, drillPanel(t, "lg-hdr-wqhd"))
	if err := fixture.pass(); err != nil {
		t.Fatal(err)
	}

	fixture.present["HDMI-A-1"] = false
	if err := fixture.pass(); err != nil {
		t.Fatal(err)
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

// A panel that answers no capability string is asked for the
// core codes one by one, and what it answers publishes.
func TestAPanelWithNoCapabilityStringIsAsked(t *testing.T) {
	fixture := newDisplayFixture(t, newFakeMonitor())

	if err := fixture.pass(); err != nil {
		t.Fatal(err)
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
	fixture := newDisplayFixture(t, drillPanel(t, "lg-hdr-wqhd"))
	if err := fixture.pass(); err != nil {
		t.Fatal(err)
	}
	first := condition(fixture.display(), ConnectedCondition).LastTransitionTime

	fixture.control.now = func() time.Time { return time.Unix(3600, 0).UTC() }
	writes := len(fixture.lines())
	if err := fixture.pass(); err != nil {
		t.Fatal(err)
	}

	if got := condition(fixture.display(), ConnectedCondition).LastTransitionTime; got != first {
		t.Errorf("the timestamp moved to %s, and nothing about the condition changed", got)
	}
	if journal := fixture.lines(); len(journal) != writes {
		t.Errorf("the second pass wrote %q, want nothing", journal[writes:])
	}
}

// The second panel of the bench, so a test can prove one
// panel's restore holds up nothing on the other.
func portableMonitor() EDID {
	return EDID{Manufacturer: "BOE", ProductCode: 0x095f, ModelName: "Portable"}
}

// A restore waits on the panel, and the pass must not wait on
// the restore. A panel that never answers would otherwise stop every
// other panel's reconcile and the whole watch behind it.
func TestARestoreThatWaitsHoldsUpNoOtherPanel(t *testing.T) {
	stuck, other := drillPanel(t, "lg-hdr-wqhd"), drillPanel(t, "portable-display")
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
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the pass did not return while a restore was still waiting on its panel")
	}

	<-fixture.waiting
	if got := other.holds(vcpBrightness); got != 30 {
		t.Errorf("the other panel holds a brightness of %d, want the 30 its spec states", got)
	}
}

// One restore per connector. A pass that finds one running
// leaves it alone, so a panel is never written by two restores.
func TestTwoPassesRunOneRestoreForOneConnector(t *testing.T) {
	panel := drillPanel(t, "lg-hdr-wqhd")
	panel.stubborn[vcpBrightness] = 1000
	fixture := newDisplayFixture(t, panel)
	fixture.stall = true
	fixture.captured(labDisplayName(), "HDMI-A-1", DisplayValues{Brightness: intOf(70)})

	if err := fixture.pass(); err != nil {
		t.Fatal(err)
	}
	<-fixture.waiting
	if err := fixture.pass(); err != nil {
		t.Fatal(err)
	}

	// A second restore would take the wire and wait on it too,
	// so the wait of one is the proof that only one runs.
	select {
	case <-fixture.waiting:
		t.Error("a second restore started for a connector that already had one")
	case <-time.After(200 * time.Millisecond):
	}
	if took := panel.took(vcpBrightness); len(took) != 1 {
		t.Errorf("the panel took %d writes, want the one write of a single restore", len(took))
	}
	if captured := fixture.display().Status.Captured; captured.empty() {
		t.Error("the capture was cleared while the restore had not landed")
	}
}

// The restore writes no status. It wakes the pass, and the pass
// is what clears the capture, so one goroutine writes status.
func TestARestoreThatLandsWakesThePassThatClearsTheCapture(t *testing.T) {
	fixture := newDisplayFixture(t, drillPanel(t, "lg-hdr-wqhd"))
	display := fixture.declare(DisplaySpec{Override: &DisplayOverride{Backlight: overrideOff}})
	if err := fixture.pass(); err != nil {
		t.Fatal(err)
	}

	display.Spec.Override = nil
	if err := fixture.pass(); err != nil {
		t.Fatal(err)
	}
	if captured := fixture.display().Status.Captured; captured.empty() {
		t.Fatal("the pass that started the restore cleared the capture")
	}

	fixture.awaitRestore()
	if err := fixture.pass(); err != nil {
		t.Fatal(err)
	}
	if captured := fixture.display().Status.Captured; !captured.empty() {
		t.Errorf("captured = %+v, want the woken pass to have cleared it", captured)
	}
	if got := fixture.holds(vcpBrightness); got != 50 {
		t.Errorf("the panel holds a brightness of %d, want the 50 it was lit at", got)
	}
}

// The failure the plan proves on the bench: the operator's pod
// is deleted while the override stands, and the panel still comes back
// when the override is deleted. The captured value is in the resource,
// and a panel that is powered down answers nothing until the restore
// wakes it.
func TestARestoreWakesAPanelThatAnswersNothingYet(t *testing.T) {
	panel := drillPanel(t, "lg-hdr-wqhd")
	panel.values[vcpPowerMode] = powerModeOff
	panel.silent = true
	panel.wakesAfter = 2
	fixture := newDisplayFixture(t, panel)
	fixture.captured(labDisplayName(), "HDMI-A-1", DisplayValues{Power: stringOf("on")})

	if err := fixture.pass(); err != nil {
		t.Fatal(err)
	}
	fixture.awaitRestore()
	if err := fixture.pass(); err != nil {
		t.Fatal(err)
	}

	if got := fixture.holds(vcpPowerMode); got != powerModeOn {
		t.Errorf("the panel holds the power mode %#02x, want %#02x", got, powerModeOn)
	}
	if fixture.waits.Load() == 0 {
		t.Error("the restore matched on its first write, and the panel was answering nothing")
	}
	if captured := fixture.display().Status.Captured; !captured.empty() {
		t.Errorf("captured = %+v, want it cleared after the restore", captured)
	}
}

// The mute is a boolean in the spec and two values on the
// wire, and this is where the two meet.
func TestTheSpecMutesThePanelsSpeakers(t *testing.T) {
	fixture := newDisplayFixture(t, drillPanel(t, "lg-hdr-wqhd"))
	fixture.declare(DisplaySpec{AudioMute: boolOf(true)})

	if err := fixture.pass(); err != nil {
		t.Fatal(err)
	}

	if got := fixture.holds(vcpAudioMute); got != 0x01 {
		t.Errorf("the panel holds the mute value %#02x, want %#02x", got, 0x01)
	}
	if muted := fixture.display().Status.Observed.AudioMute; muted == nil || !*muted {
		t.Errorf("observed mute = %v, want true", muted)
	}
}

// The loop takes its passes from the wakes, which is what the
// watch and the slice publisher raise.
func TestTheLoopPassesOnEveryWake(t *testing.T) {
	fixture := newDisplayFixture(t, drillPanel(t, "lg-hdr-wqhd"))
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

// The loop's own tick is what brings a poll window that came
// due to a pass. Nothing wakes this loop: the panel changed at its own
// buttons, and no event says so anywhere.
func TestTheLoopPassesOnItsTick(t *testing.T) {
	fixture := newDisplayFixture(t, drillPanel(t, "lg-hdr-wqhd"))
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

// The sweep for panels that left is the pass's one API listing,
// and it keeps the slower cadence: a panel that leaves raises a uevent
// that wakes this loop, so nothing has to be found by listing on the
// poll's cadence.
func TestTheSweepKeepsTheSlowerCadence(t *testing.T) {
	fixture := newDisplayFixture(t, drillPanel(t, "lg-hdr-wqhd"))
	if err := fixture.pass(); err != nil {
		t.Fatal(err)
	}

	for range 3 {
		fixture.advance(pollInterval)
		if err := fixture.pass(); err != nil {
			t.Fatal(err)
		}
	}
	if fixture.lists != 1 {
		t.Errorf("the poll's passes listed the displays %d times, want the one listing of the first pass",
			fixture.lists)
	}

	fixture.advance(backstopInterval)
	if err := fixture.pass(); err != nil {
		t.Fatal(err)
	}
	if fixture.lists != 2 {
		t.Errorf("the displays were listed %d times, want a listing on the slower cadence", fixture.lists)
	}
}

func intOf(value int) *int { return &value }

func boolOf(value bool) *bool { return &value }

func stringOf(value string) *string { return &value }

// The watch turns each event into one wake, which is all the
// pass needs from it.
func TestTheWatchWakesOnEveryEvent(t *testing.T) {
	events := 2
	client := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("watch") != "true" {
			t.Errorf("the operator opened %s, want a watch", r.URL)
		}
		for i := 0; i < events; i++ {
			fmt.Fprintf(w, `{"type":"MODIFIED","object":{"metadata":{"name":"panel-%d"}}}`, i)
		}
	}))

	wakes := 0
	if err := streamDisplays(t.Context(), client, func() { wakes++ }); err != nil {
		t.Fatal(err)
	}
	if wakes != events {
		t.Errorf("the watch woke the loop %d times, want %d", wakes, events)
	}
}

// The monitor of the lab drill as its own EDID states it, read
// from the fixture the slice tests read, so the identity and the size
// this resource publishes are proven against a real monitor's bytes
// and not against a struct written by hand.
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

// Every mode the fixture's connector offers, in the form the
// card reports them, one refresh each.
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
		Panel:     drillPanel(t, "lg-hdr-wqhd"),
		Modes:     offeredModes(screen),
		Current:   "3840x1600@60",
	})

	if err := fixture.pass(); err != nil {
		t.Fatal(err)
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

// The card syncing a mode on a connector and the compositor
// serving canvases at that mode are two facts. A client draws at the
// second one, so the Display reports both and a person reads the
// gap.
func TestStatusReportsBothWitnessesOfTheMode(t *testing.T) {
	screen := labScreen(t, "HDMI-A-1")
	fixture := newDisplayBench(t, wiredPanel{
		Connector: "HDMI-A-1",
		Monitor:   screen.Monitor,
		Panel:     drillPanel(t, "lg-hdr-wqhd"),
		Modes:     offeredModes(screen),
		Current:   "1920x1080@60",
	})
	fixture.serving["HDMI-A-1"] = "3840x1600@60"

	if err := fixture.pass(); err != nil {
		t.Fatal(err)
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

// A compositor the operator holds no connection to states
// nothing, and an absent value is honest where a carried-over one
// would be a guess.
func TestStatusReportsNoCanvasWhileNoCompositorAnswers(t *testing.T) {
	screen := labScreen(t, "HDMI-A-1")
	fixture := newDisplayBench(t, wiredPanel{
		Connector: "HDMI-A-1",
		Monitor:   screen.Monitor,
		Panel:     drillPanel(t, "lg-hdr-wqhd"),
		Modes:     offeredModes(screen),
		Current:   "3840x1600@60",
	})

	if err := fixture.pass(); err != nil {
		t.Fatal(err)
	}

	mode := fixture.displayNamed(monitorID(screen.Monitor)).Status.Mode
	if mode == nil || mode.Kernel != "3840x1600@60" {
		t.Fatalf("mode = %+v, want the kernel at the mode the card drives", mode)
	}
	if mode.Weston != "" {
		t.Errorf("mode.weston = %q, want nothing while no compositor answers", mode.Weston)
	}
}

// status has no attribute-length limit, so the list it carries
// is the card's own and not the cut the slice publishes. The lab's
// ultrawide offers more modes than the attribute can hold, which is
// what makes the two lists differ.
func TestTheFullModeListOutgrowsTheSliceAttribute(t *testing.T) {
	screen := labScreen(t, "HDMI-A-1")
	offered := offeredModes(screen)
	fixture := newDisplayBench(t, wiredPanel{
		Connector: "HDMI-A-1",
		Monitor:   screen.Monitor,
		Panel:     drillPanel(t, "lg-hdr-wqhd"),
		Modes:     offered,
	})

	if err := fixture.pass(); err != nil {
		t.Fatal(err)
	}

	published := fixture.displayNamed(monitorID(screen.Monitor)).Status.Modes
	if !slices.Equal(published, offered) {
		t.Errorf("modes = %q, want the card's whole list %q", published, offered)
	}
	attribute := strings.Fields(attributeList(screen.Modes))
	if len(attribute) >= len(published) {
		t.Fatalf("the slice attribute carries %d of the %d modes, so this panel proves no truncation",
			len(attribute), len(published))
	}
}

func TestTheRestingModeFollowsTheClaim(t *testing.T) {
	cases := []struct {
		name    string
		mode    *string
		current string
		held    map[string]bool
		want    []string
		says    string
	}{
		{
			name:    "a declared mode on a free screen",
			mode:    stringOf("1920x1080@60"),
			current: "3840x1600@60",
			want:    []string{"HDMI-A-1=1920x1080@60"},
		},
		{
			// write-on-divergence, the rule every other
			// declared value follows.
			name:    "a declared mode the screen already runs",
			mode:    stringOf("1920x1080@60"),
			current: "1920x1080@60",
		},
		{
			// The claim's own mode wins for its lifetime, so
			// an edit made during a claim waits for the claim to end.
			name:    "a declared mode while a claim holds the screen",
			mode:    stringOf("1920x1080@60"),
			current: "3840x1600@60",
			held:    map[string]bool{"hdmi-a-1": true},
		},
		{
			// A draw claim shares the screen and owns no mode,
			// so it holds nothing back.
			name:    "a declared mode while a draw claim shares the screen",
			mode:    stringOf("1920x1080@60"),
			current: "3840x1600@60",
			held:    map[string]bool{"hdmi-a-2": true},
			want:    []string{"HDMI-A-1=1920x1080@60"},
		},
		{
			name:    "a mode the card does not offer",
			mode:    stringOf("800x600@60"),
			current: "3840x1600@60",
			says:    "offers",
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
				Panel:     drillPanel(t, "lg-hdr-wqhd"),
				Modes:     []string{"3840x1600@60", "1920x1080@60"},
				Current:   c.current,
			})
			fixture.held = c.held
			fixture.declare(DisplaySpec{Mode: c.mode})

			err := fixture.pass()
			if c.says == "" && err != nil {
				t.Fatal(err)
			}
			if c.says != "" && (err == nil || !strings.Contains(err.Error(), c.says)) {
				t.Fatalf("the pass reported %v, want a failure that says %q", err, c.says)
			}
			if !slices.Equal(fixture.modeSets, c.want) {
				t.Errorf("the controller set %q, want %q", fixture.modeSets, c.want)
			}
		})
	}
}

// The claim ends, the screen is free, and the pass that finds
// it free puts the resting mode back. This is the restore the plan
// asks for, and it happens through the same mode switch a prepare
// makes.
func TestTheRestingModeReturnsWhenTheClaimEnds(t *testing.T) {
	fixture := newDisplayBench(t, wiredPanel{
		Connector: "HDMI-A-1",
		Monitor:   labMonitor(),
		Panel:     drillPanel(t, "lg-hdr-wqhd"),
		Modes:     []string{"3840x1600@60", "1920x1080@60"},
		Current:   "3840x1600@60",
	})
	fixture.held = map[string]bool{"hdmi-a-1": true}
	fixture.declare(DisplaySpec{Mode: stringOf("1920x1080@60")})
	if err := fixture.pass(); err != nil {
		t.Fatal(err)
	}
	if len(fixture.modeSets) != 0 {
		t.Fatalf("the controller set %q while a claim held the screen", fixture.modeSets)
	}

	fixture.held = nil
	if err := fixture.pass(); err != nil {
		t.Fatal(err)
	}

	if want := []string{"HDMI-A-1=1920x1080@60"}; !slices.Equal(fixture.modeSets, want) {
		t.Errorf("the controller set %q, want %q", fixture.modeSets, want)
	}
	// The screen runs the declaration now, so a later pass
	// finds no divergence and restarts nothing.
	if err := fixture.pass(); err != nil {
		t.Fatal(err)
	}
	if len(fixture.modeSets) != 1 {
		t.Errorf("the controller set %q, want the one switch", fixture.modeSets)
	}
}

// The flap the drill saw on the metal: an output destroyed and
// re-created leaves the clients on the surviving screens with a canvas
// sized for the output that went. A fresh compositor lays every canvas
// out again.
//
// The compositor reports the flap on the operator's standing
// Wayland connection, so the fixture reports it the same way.
// Nothing about the connectors sysfs lists has to change: the
// monitor that slept and woke is the same monitor on the same
// connector.
func (f *displayFixture) flap(t *testing.T) {
	t.Helper()
	f.control.outputsMoved(true)
	if err := f.pass(); err != nil {
		t.Fatal(err)
	}
}

func TestTheHealRestartsTheCompositorAfterAFlap(t *testing.T) {
	fixture := newDisplayFixture(t, drillPanel(t, "lg-hdr-wqhd"))
	if err := fixture.pass(); err != nil {
		t.Fatal(err)
	}

	fixture.flap(t)
	if fixture.restarts != 0 {
		t.Fatalf("the compositor restarted %d times inside the flap", fixture.restarts)
	}

	fixture.advance(canvasSettleWindow)
	if err := fixture.pass(); err != nil {
		t.Fatal(err)
	}
	if fixture.restarts != 1 {
		t.Errorf("the compositor restarted %d times, want 1", fixture.restarts)
	}

	// The debt is paid, so the passes that follow restart
	// nothing.
	fixture.advance(canvasSettleWindow)
	if err := fixture.pass(); err != nil {
		t.Fatal(err)
	}
	if fixture.restarts != 1 {
		t.Errorf("the compositor restarted %d times, want the one restart of one flap", fixture.restarts)
	}
}

// A restart would end the film. The debt stands across the
// claim and the restart happens after the last unprepare.
func TestTheHealWaitsForTheClaimToEnd(t *testing.T) {
	fixture := newDisplayFixture(t, drillPanel(t, "lg-hdr-wqhd"))
	fixture.held = map[string]bool{"hdmi-a-1": true}
	if err := fixture.pass(); err != nil {
		t.Fatal(err)
	}

	fixture.flap(t)
	fixture.advance(canvasSettleWindow)
	if err := fixture.pass(); err != nil {
		t.Fatal(err)
	}
	if fixture.restarts != 0 {
		t.Fatalf("the compositor restarted %d times while a claim held a screen", fixture.restarts)
	}

	fixture.held = nil
	if err := fixture.pass(); err != nil {
		t.Fatal(err)
	}

	if fixture.restarts != 1 {
		t.Errorf("the compositor restarted %d times after the claim ended, want 1", fixture.restarts)
	}
}

// An operator that starts finds every output for the first
// time, and a compositor that just started has every canvas right.
func TestTheFirstPassOwesNoRestart(t *testing.T) {
	fixture := newDisplayFixture(t, drillPanel(t, "lg-hdr-wqhd"))

	if err := fixture.pass(); err != nil {
		t.Fatal(err)
	}
	fixture.advance(canvasSettleWindow)
	if err := fixture.pass(); err != nil {
		t.Fatal(err)
	}

	if fixture.restarts != 0 {
		t.Errorf("the compositor restarted %d times with no output re-created", fixture.restarts)
	}
}

// The connectors sysfs lists state nothing about what the
// compositor did with them. A monitor that leaves and returns
// between two passes is a monitor the compositor may never have
// destroyed an output for, and a monitor that never moved may have
// had its output re-created under it. The compositor is the only
// source of the debt.
func TestOnlyTheCompositorSaysAnOutputWasReCreated(t *testing.T) {
	fixture := newDisplayFixture(t, drillPanel(t, "lg-hdr-wqhd"))
	if err := fixture.pass(); err != nil {
		t.Fatal(err)
	}

	fixture.present["HDMI-A-1"] = false
	if err := fixture.pass(); err != nil {
		t.Fatal(err)
	}
	fixture.present["HDMI-A-1"] = true
	if err := fixture.pass(); err != nil {
		t.Fatal(err)
	}
	fixture.advance(canvasSettleWindow)
	if err := fixture.pass(); err != nil {
		t.Fatal(err)
	}

	if fixture.restarts != 0 {
		t.Errorf("the compositor restarted %d times on a monitor that moved with no word from the compositor", fixture.restarts)
	}
}

// The two repairs are on two wires, the compositor's and the
// panel's, and they do not fight. The heal still waits for a restore:
// a panel on its way back from an override has enough to do, and the
// debt costs nothing to hold one more pass.
func TestTheHealWaitsForARestore(t *testing.T) {
	panel := drillPanel(t, "lg-hdr-wqhd")
	panel.stubborn[vcpBrightness] = 1000
	fixture := newDisplayFixture(t, panel)
	fixture.stall = true
	fixture.captured(labDisplayName(), "HDMI-A-1", DisplayValues{Brightness: intOf(70)})
	if err := fixture.pass(); err != nil {
		t.Fatal(err)
	}
	<-fixture.waiting

	fixture.flap(t)
	fixture.advance(canvasSettleWindow)
	if err := fixture.pass(); err != nil {
		t.Fatal(err)
	}

	if fixture.restarts != 0 {
		t.Errorf("the compositor restarted %d times while a restore was still writing", fixture.restarts)
	}
}

// The film ends, and the screen goes back to what it rests at
// without waiting for the loop's own tick. The unprepare raises the
// same wake the slice pass raises, and the pass it wakes finds the
// screen free because the claim's spec file is already gone.
func TestAnUnprepareRestoresTheRestingMode(t *testing.T) {
	restoreCDIDir := cdiDir
	t.Cleanup(func() { cdiDir = restoreCDIDir })
	// The plugin's own fixture owns the spec directory, so the
	// claim this test prepares by hand is the claim its unprepare
	// takes away.
	plugin, _ := labPluginWithConfig(t, screenRequest(), "")
	if err := writeCDISpec("film", []cdiDevice{{
		Name:           "film-hdmi-a-1",
		ContainerEdits: outputEdits("/var/run/display.liken.sh", "wayland-0", "hdmi-a-1"),
	}}); err != nil {
		t.Fatal(err)
	}

	fixture := newDisplayBench(t, wiredPanel{
		Connector: "HDMI-A-1",
		Monitor:   labMonitor(),
		Panel:     drillPanel(t, "lg-hdr-wqhd"),
		Modes:     []string{"3840x1600@60", "1920x1080@60"},
		Current:   "3840x1600@60",
	})
	fixture.control.prepared = preparedOutputs
	fixture.declare(DisplaySpec{Mode: stringOf("1920x1080@60")})
	if err := fixture.pass(); err != nil {
		t.Fatal(err)
	}
	if len(fixture.modeSets) != 0 {
		t.Fatalf("the controller set %q while the film held the screen", fixture.modeSets)
	}

	// What the claims held at the moment of the wake, which is
	// what says the spec was gone before the wake went out.
	var heldAtWake map[string]bool
	plugin.republish = func() {
		heldAtWake, _ = preparedOutputs()
		fixture.control.wake()
	}
	if err := plugin.unprepareClaim("film"); err != nil {
		t.Fatal(err)
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
		t.Fatal(err)
	}

	if want := []string{"HDMI-A-1=1920x1080@60"}; !slices.Equal(fixture.modeSets, want) {
		t.Errorf("the controller set %q on the woken pass, want %q", fixture.modeSets, want)
	}
}

// The values the lab's ultrawide declares, so the guard is
// proven against the panel that failed: this machine's cable is on
// HDMI-2 and the panel was showing the laptop on DP-1.
const (
	labAttachedInput = "HDMI-2"
	labOtherInput    = "DP-1"
)

func TestADarkeningOverrideRespectsTheAttachedInput(t *testing.T) {
	cases := []struct {
		name     string
		attached *string
		override *DisplayOverride
		shown    uint16
		garbles  bool
		blanks   bool
		says     string
	}{
		{
			// The failure of 2026-08-27, and the whole reason
			// for the field.
			name:     "a panel showing another machine's input",
			attached: stringOf(labAttachedInput),
			override: &DisplayOverride{Backlight: overrideOff},
			shown:    0x0f,
		},
		{
			// The failure the metal drill found. The
			// ultrawide answers the input query with a reply that
			// parses wrong while it shows another source, and a
			// panel that cannot say what it shows is treated as
			// showing somebody else.
			name:     "a panel that answers the input query with nonsense",
			attached: stringOf(labAttachedInput),
			override: &DisplayOverride{Backlight: overrideOff},
			shown:    0x12,
			garbles:  true,
		},
		{
			name:     "a panel showing this machine's input",
			attached: stringOf(labAttachedInput),
			override: &DisplayOverride{Backlight: overrideOff},
			shown:    0x12,
			blanks:   true,
		},
		{
			// Every single-input panel and every panel nobody
			// shares stays out of this entirely.
			name:     "a panel with no declaration",
			override: &DisplayOverride{Backlight: overrideOff},
			shown:    0x0f,
			blanks:   true,
		},
		{
			name:     "a power override while another input is shown",
			attached: stringOf(labAttachedInput),
			override: &DisplayOverride{Power: overrideOff},
			shown:    0x0f,
		},
		{
			name:     "an attached input the panel does not carry",
			attached: stringOf("VGA-1"),
			override: &DisplayOverride{Backlight: overrideOff},
			shown:    0x0f,
			says:     "the panel accepts",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			panel := drillPanel(t, "lg-hdr-wqhd")
			fixture := newDisplayFixture(t, panel)
			display := fixture.declare(DisplaySpec{AttachedInput: c.attached})
			// The panel answers everything at the probe, the
			// way the ultrawide does while it shows this machine, and
			// the switch to another source comes after: the input it
			// reports changes, or it stops reporting one at all.
			_ = fixture.pass()
			panel.turnedTo(vcpInput, c.shown)
			panel.garble(vcpInput, c.garbles)
			display.Spec.Override = c.override

			err := fixture.pass()
			if c.says == "" && err != nil {
				t.Fatal(err)
			}
			if c.says != "" && (err == nil || !strings.Contains(err.Error(), c.says)) {
				t.Fatalf("the pass reported %v, want a failure that says %q", err, c.says)
			}

			blanked := fixture.holds(vcpBrightness) == 0 || fixture.holds(vcpPowerMode) != powerModeOn
			if blanked != c.blanks {
				t.Errorf("the panel holds brightness %d and power %#02x, want blanked=%v",
					fixture.holds(vcpBrightness), fixture.holds(vcpPowerMode), c.blanks)
			}
			// nothing is saved until the blank lands, so a
			// deferred override leaves the captured block empty.
			if captured := fixture.display().Status.Captured; captured.empty() == c.blanks {
				t.Errorf("captured = %+v with blanked=%v", captured, c.blanks)
			}
		})
	}
}

// The deferral is a state, not a failure, so it is reported
// once and the panel is left alone until the input comes back.
func TestADeferredDarkeningIsReportedOnce(t *testing.T) {
	panel := drillPanel(t, "lg-hdr-wqhd")
	panel.turnedTo(vcpInput, 0x0f)
	fixture := newDisplayFixture(t, panel)
	fixture.declare(DisplaySpec{
		AttachedInput: stringOf(labAttachedInput),
		Override:      &DisplayOverride{Backlight: overrideOff},
	})

	for range 3 {
		fixture.advance(pollInterval)
		if err := fixture.pass(); err != nil {
			t.Fatal(err)
		}
	}

	if len(fixture.control.darkFaults) != 1 {
		t.Errorf("the deferrals standing are %v, want the one this connector holds", fixture.control.darkFaults)
	}
	if got := fixture.holds(vcpBrightness); got != 50 {
		t.Errorf("the panel holds a brightness of %d, want the 50 the other machine is watching", got)
	}
}

// The poll is what lifts the deferral. The panel comes back to
// this machine's input, the pass that reads it obeys the override that
// was waiting, and the capture runs first, as it does for every blank.
func TestADeferredDarkeningLandsWhenTheInputReturns(t *testing.T) {
	panel := drillPanel(t, "lg-hdr-wqhd")
	panel.turnedTo(vcpInput, 0x0f)
	fixture := newDisplayFixture(t, panel)
	fixture.declare(DisplaySpec{
		AttachedInput: stringOf(labAttachedInput),
		Override:      &DisplayOverride{Backlight: overrideOff},
	})
	if err := fixture.pass(); err != nil {
		t.Fatal(err)
	}

	// No clock moves here. The guard reads the panel itself, so
	// the blank lands on the very pass whose read answers this
	// machine's input, and not a poll window later.
	panel.turnedTo(vcpInput, 0x12)
	if err := fixture.pass(); err != nil {
		t.Fatal(err)
	}

	journal := fixture.lines()
	captured := slices.Index(journal, "status captured=brightness 50")
	blanked := slices.Index(journal, "set brightness=0")
	if captured < 0 || blanked < 0 || captured > blanked {
		t.Fatalf("journal = %q, want the capture before the blank once the input returned", journal)
	}
	if got := fixture.holds(vcpBrightness); got != 0 {
		t.Errorf("the panel holds a brightness of %d, want 0", got)
	}
	if len(fixture.control.darkFaults) != 0 {
		t.Errorf("the deferral %v still stands after the override acted", fixture.control.darkFaults)
	}
}

// A lift always obeys. The captured value was only ever saved
// while the panel showed this machine's input, so restoring it can
// surprise no other viewer.
func TestALiftObeysWhileAnotherInputIsShown(t *testing.T) {
	panel := drillPanel(t, "lg-hdr-wqhd")
	panel.turnedTo(vcpInput, 0x0f)
	panel.turnedTo(vcpBrightness, 0)
	fixture := newDisplayFixture(t, panel)
	display := fixture.captured(labDisplayName(), "HDMI-A-1", DisplayValues{Brightness: intOf(70)})
	display.Spec.AttachedInput = stringOf(labAttachedInput)

	if err := fixture.pass(); err != nil {
		t.Fatal(err)
	}
	fixture.awaitRestore()
	if err := fixture.pass(); err != nil {
		t.Fatal(err)
	}

	if got := fixture.holds(vcpBrightness); got != 70 {
		t.Errorf("the panel holds a brightness of %d, want the 70 the capture saved", got)
	}
	if captured := fixture.display().Status.Captured; !captured.empty() {
		t.Errorf("captured = %+v, want it cleared after the restore", captured)
	}
}

// The panel's own EDID answers which of its ports this cable is
// in, so the guard protects a shared panel that nobody declared
// anything about. The derivation publishes either way, so a person can
// read it against the cabling.
func TestTheAttachedInputIsDerivedFromTheEDID(t *testing.T) {
	cases := []struct {
		name      string
		connector string
		monitor   EDID
		derived   string
	}{
		{
			// The lab's ultrawide, on this machine's HDMI 2.
			name:      "the port the ultrawide's EDID names",
			connector: "HDMI-A-1",
			monitor:   labScreenEDID(t, "HDMI-A-1"),
			derived:   "HDMI-2",
		},
		{
			// An address in a DisplayPort EDID describes the
			// sink's HDMI topology, not the port this cable is in.
			name:      "a DisplayPort cable into the same panel",
			connector: "DP-1",
			monitor:   labScreenEDID(t, "HDMI-A-1"),
		},
		{
			name:      "a panel whose EDID names no port",
			connector: "HDMI-A-1",
			monitor:   labMonitor(),
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			fixture := newDisplayBench(t, wiredPanel{
				Connector: c.connector,
				Monitor:   c.monitor,
				Panel:     drillPanel(t, "lg-hdr-wqhd"),
			})

			if err := fixture.pass(); err != nil {
				t.Fatal(err)
			}

			status := fixture.displayNamed(monitorID(c.monitor)).Status
			if status.AttachedInput != c.derived {
				t.Errorf("status.attachedInput = %q, want %q", status.AttachedInput, c.derived)
			}
		})
	}
}

func labScreenEDID(t *testing.T, connector string) EDID {
	t.Helper()
	return labScreen(t, connector).Monitor
}

// A panel the EDID speaks for needs no declaration: the guard
// defers the blank while the panel shows another machine, exactly as
// it does for a declared one.
func TestADerivedInputGuardsTheDarkening(t *testing.T) {
	panel := drillPanel(t, "lg-hdr-wqhd")
	panel.turnedTo(vcpInput, 0x0f)
	fixture := newDisplayBench(t, wiredPanel{
		Connector: "HDMI-A-1",
		Monitor:   labScreenEDID(t, "HDMI-A-1"),
		Panel:     panel,
	})
	name := monitorID(labScreenEDID(t, "HDMI-A-1"))
	fixture.declareFor(name, DisplaySpec{Override: &DisplayOverride{Backlight: overrideOff}})

	if err := fixture.pass(); err != nil {
		t.Fatal(err)
	}

	if got := panel.holds(vcpBrightness); got != 50 {
		t.Errorf("the panel holds a brightness of %d, want the 50 the other input is watching", got)
	}
	if captured := fixture.displayNamed(name).Status.Captured; !captured.empty() {
		t.Errorf("captured = %+v, want nothing saved on a deferred override", captured)
	}

	// The panel comes back to this machine's port and the
	// deferred blank lands, capture first.
	panel.turnedTo(vcpInput, 0x12)
	fixture.advance(pollInterval)
	if err := fixture.pass(); err != nil {
		t.Fatal(err)
	}
	if got := panel.holds(vcpBrightness); got != 0 {
		t.Errorf("the panel holds a brightness of %d, want 0", got)
	}
}

// The owner's declaration wins over the derivation, because the
// person who plugged the cable in knows what the EDID cannot say: a
// panel that serves the same address on every port, or a cable moved
// since the sink was built.
func TestADeclarationWinsOverTheDerivation(t *testing.T) {
	panel := drillPanel(t, "lg-hdr-wqhd")
	panel.turnedTo(vcpInput, 0x0f)
	screen := labScreenEDID(t, "HDMI-A-1")
	fixture := newDisplayBench(t, wiredPanel{
		Connector: "HDMI-A-1",
		Monitor:   screen,
		Panel:     panel,
	})
	// The EDID derives HDMI-2, and the owner says the cable is
	// on the input the panel is showing now.
	fixture.declareFor(monitorID(screen), DisplaySpec{
		AttachedInput: stringOf(labOtherInput),
		Override:      &DisplayOverride{Backlight: overrideOff},
	})

	if err := fixture.pass(); err != nil {
		t.Fatal(err)
	}

	if got := panel.holds(vcpBrightness); got != 0 {
		t.Errorf("the panel holds a brightness of %d, want the blank the declaration allowed", got)
	}
	if status := fixture.displayNamed(monitorID(screen)).Status; status.AttachedInput != "HDMI-2" {
		t.Errorf("status.attachedInput = %q, want the derivation to publish beside the declaration",
			status.AttachedInput)
	}
}

// The guard's read is a read like any other, so what it learns
// reaches the resource. A person looking at a deferred override can
// see which input the panel answered with.
func TestTheGuardsReadReachesTheObservedInput(t *testing.T) {
	panel := drillPanel(t, "lg-hdr-wqhd")
	fixture := newDisplayFixture(t, panel)
	display := fixture.declare(DisplaySpec{AttachedInput: stringOf(labAttachedInput)})
	if err := fixture.pass(); err != nil {
		t.Fatal(err)
	}

	// The panel moves to a third input, and no poll window is
	// open, so the guard's own read is the only thing that could learn
	// it.
	panel.turnedTo(vcpInput, 0x11)
	display.Spec.Override = &DisplayOverride{Backlight: overrideOff}
	if err := fixture.pass(); err != nil {
		t.Fatal(err)
	}

	observed := fixture.display().Status.Observed
	if observed == nil || observed.Input == nil || *observed.Input != "HDMI-1" {
		t.Errorf("observed.input = %+v, want the HDMI-1 the guard read", observed)
	}
	if got := fixture.holds(vcpBrightness); got != 50 {
		t.Errorf("the panel holds a brightness of %d, want the 50 the other input is watching", got)
	}
}

// The darkening that landed asks nothing more of the panel. A
// DDC read is a wake stimulus, so a panel held dark is not read again
// on every pass to check an input it cannot change while it is dark.
func TestAnObeyedOverrideReadsThePanelNoFurther(t *testing.T) {
	panel := drillPanel(t, "lg-hdr-wqhd")
	panel.turnedTo(vcpInput, 0x12)
	fixture := newDisplayFixture(t, panel)
	fixture.declare(DisplaySpec{
		AttachedInput: stringOf(labAttachedInput),
		Override:      &DisplayOverride{Backlight: overrideOff},
	})
	if err := fixture.pass(); err != nil {
		t.Fatal(err)
	}
	if got := fixture.holds(vcpBrightness); got != 0 {
		t.Fatalf("the panel holds a brightness of %d, want the blank", got)
	}

	opens := fixture.bench.opened()
	for range 3 {
		if err := fixture.pass(); err != nil {
			t.Fatal(err)
		}
	}

	if got := fixture.bench.opened(); got != opens {
		t.Errorf("the passes after the blank opened the wire %d more times, want none", got-opens)
	}
}
