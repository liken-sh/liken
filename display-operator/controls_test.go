package main

// These tests run the probe, the scaling, and the readback against a
// fake panel that replies to the same DDC/CI packets as a real panel,
// so the operator's own protocol code runs in every test.

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// The same builders as modes_test.go, named for a block that sets a
// control.
func claimControl(parameters string) string {
	return configEntry(configFromClaim, "", parameters)
}

func classControl(parameters string) string {
	return configEntry(configFromClass, "", parameters)
}

func TestClaimControlsReadsTheOpaqueBlock(t *testing.T) {
	cases := []struct {
		name    string
		config  string
		request string
		want    requestedControls
	}{
		{name: "a claim with no config block", request: "screen"},
		{
			name:    "a brightness for every request",
			config:  claimControl(`{"brightness": 40}`),
			request: "screen",
			want:    requestedControls{Brightness: brightness{Percent: 40, Set: true}},
		},
		{
			name:    "a brightness of zero",
			config:  claimControl(`{"brightness": 0}`),
			request: "screen",
			want:    requestedControls{Brightness: brightness{Percent: 0, Set: true}},
		},
		{
			name:    "the power a claim sets",
			config:  claimControl(`{"power": "onWhileClaimed"}`),
			request: "screen",
			want:    requestedControls{Power: powerOnWhileClaimed},
		},
		{
			name:    "both controls and a mode in one block",
			config:  claimControl(`{"mode": "1280x720", "brightness": 75, "power": "on"}`),
			request: "screen",
			want: requestedControls{
				Brightness: brightness{Percent: 75, Set: true},
				Power:      powerOn,
			},
		},
		{
			name: "another driver's block",
			config: `{"source": "FromClaim", "opaque": {"driver": "audio.liken.sh",
			          "parameters": {"codec": "sbc"}}}`,
			request: "screen",
		},
		{
			name:    "a block that names another request",
			config:  configEntry(configFromClaim, `"second-screen"`, `{"brightness": 40}`),
			request: "screen",
		},
		{
			name:    "a request's own block over the claim's",
			config:  claimControl(`{"brightness": 40}`) + "," + configEntry(configFromClaim, `"screen"`, `{"brightness": 90}`),
			request: "screen",
			want:    requestedControls{Brightness: brightness{Percent: 90, Set: true}},
		},
		{
			name:    "the class's brightness, with none in the claim",
			config:  classControl(`{"brightness": 30}`),
			request: "screen",
			want:    requestedControls{Brightness: brightness{Percent: 30, Set: true}},
		},
		{
			name:    "the claim's brightness over the class's",
			config:  classControl(`{"brightness": 30}`) + "," + claimControl(`{"brightness": 90}`),
			request: "screen",
			want:    requestedControls{Brightness: brightness{Percent: 90, Set: true}},
		},
		{
			name:    "the claim's brightness, listed before the class's",
			config:  claimControl(`{"brightness": 90}`) + "," + classControl(`{"brightness": 30}`),
			request: "screen",
			want:    requestedControls{Brightness: brightness{Percent: 90, Set: true}},
		},
		{
			// Each control resolves separately, so a claim that sets
			// one of them leaves the other to the class.
			name:    "the claim's brightness beside the class's power",
			config:  classControl(`{"power": "on"}`) + "," + claimControl(`{"brightness": 90}`),
			request: "screen",
			want: requestedControls{
				Brightness: brightness{Percent: 90, Set: true},
				Power:      powerOn,
			},
		},
		{
			name: "the claim's every-request block over the class's named one",
			config: configEntry(configFromClass, `"screen"`, `{"power": "onWhileClaimed"}`) + "," +
				claimControl(`{"power": "on"}`),
			request: "screen",
			want:    requestedControls{Power: powerOn},
		},
		{
			name:    "a block with empty parameters",
			config:  claimControl(`{}`),
			request: "screen",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			controls, err := claimControls(resolvedConfig(t, c.config))
			if err != nil {
				t.Fatalf("parsing the claim controls: %v", err)
			}
			if got := controls.forRequest(c.request); got != c.want {
				t.Errorf("controls = %+v, want %+v", got, c.want)
			}
		})
	}
}

// The scheduler copies opaque parameters without reading them, so a
// value that this parse accepts is sent to the panel.
func TestClaimControlsRejectsInvalidParameters(t *testing.T) {
	cases := []struct {
		name   string
		config string
		says   string
	}{
		{
			name:   "a key this driver does not read",
			config: claimControl(`{"backlight": 40}`),
			says:   `"backlight"`,
		},
		{
			name:   "a key the class does not read either",
			config: classControl(`{"backlight": 40}`),
			says:   `"backlight"`,
		},
		{
			name:   "a brightness that is not a number",
			config: claimControl(`{"brightness": "40"}`),
			says:   "not a whole number",
		},
		{
			name:   "a brightness that is not whole",
			config: claimControl(`{"brightness": 40.5}`),
			says:   "not a whole number",
		},
		{
			name:   "a brightness above the scale",
			config: claimControl(`{"brightness": 101}`),
			says:   "percentage from 0 to 100",
		},
		{
			name:   "a brightness below the scale",
			config: claimControl(`{"brightness": -1}`),
			says:   "percentage from 0 to 100",
		},
		{
			name:   "a power that is not a string",
			config: claimControl(`{"power": 1}`),
			says:   "not a string",
		},
		{
			name:   "a power state this driver does not support",
			config: claimControl(`{"power": "off"}`),
			says:   `"OnWhileClaimed"`,
		},
		{
			name:   "parameters that are not an object",
			config: claimControl(`["on"]`),
			says:   "parameters",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := claimControls(resolvedConfig(t, c.config))
			if err == nil {
				t.Fatal("claimControls returned no error, want one for parameters it cannot read")
			}
			if !strings.Contains(err.Error(), c.says) {
				t.Errorf("error is %q, want it to contain %q", err, c.says)
			}
		})
	}
}

// The mode parser and the control parser read different keys from one
// block, so neither may reject the other's keys.
func TestEachParserSkipsTheOtherParsersParameters(t *testing.T) {
	config := resolvedConfig(t, claimControl(`{"mode": "1280x720", "brightness": 75, "power": "on"}`))

	modes, err := claimModes(config)
	if err != nil {
		t.Fatalf("parsing the claim modes: %v", err)
	}
	if got := modes.forRequest("screen"); got != "1280x720" {
		t.Errorf("mode = %q, want 1280x720", got)
	}
	controls, err := claimControls(config)
	if err != nil {
		t.Fatalf("parsing the claim controls: %v", err)
	}
	want := requestedControls{Brightness: brightness{Percent: 75, Set: true}, Power: powerOn}
	if got := controls.forRequest("screen"); got != want {
		t.Errorf("controls = %+v, want %+v", got, want)
	}
}

func TestBrightnessValueScalesToThePanelsOwnRange(t *testing.T) {
	cases := []struct {
		name    string
		percent int
		max     uint16
		want    uint16
	}{
		{name: "a scale of a hundred steps", percent: 40, max: 100, want: 40},
		{name: "the whole scale", percent: 100, max: 100, want: 100},
		{name: "a panel at 0 percent", percent: 0, max: 100, want: 0},
		{name: "a scale of 255 steps", percent: 50, max: 255, want: 128},
		{name: "a scale of 255 steps at the top", percent: 100, max: 255, want: 255},
		{name: "a rounding that goes up", percent: 33, max: 10, want: 3},
		{name: "a step nearer than truncation gives", percent: 35, max: 10, want: 4},
		{name: "a wide scale", percent: 75, max: 1000, want: 750},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := brightnessValue(c.percent, c.max); got != c.want {
				t.Errorf("brightnessValue(%d, %d) = %d, want %d", c.percent, c.max, got, c.want)
			}
		})
	}
}

// fakeMonitor plays the panel's side of DDC/CI. It keeps a value and a
// maximum for each code it supports. For any other code it replies as a
// real display does, with a well-formed reply whose result byte means
// unsupported.
type fakeMonitor struct {
	values  map[byte]uint16
	maxima  map[byte]uint16
	clamps  map[byte]uint16
	silent  bool
	sets    []monitorSet
	opens   int
	closes  int
	pending []byte
	// The capability string that this panel declares, or empty when it
	// declares none. A panel that declares none is probed code by code.
	capabilities string
	// How many writes to each code the panel ignores before it accepts
	// one, as a panel that is waking does.
	stubborn map[byte]int
	// How many set requests a silent panel receives before it responds
	// again, as a panel coming out of a power-down does.
	wakesAfter int
	// A garbled panel replies to every request with bytes that are not
	// a DDC/CI reply. noise is the first byte of each reply and changes
	// on every reply.
	garbled bool
	noise   byte
	// The shared log of what was sent to the panel and to the API
	// server, so a test can read both in the order they happened.
	journal *journal
	// A restore runs on its own goroutine, so two goroutines can use
	// the panel at once.
	mu sync.Mutex
	// How many holders have the node open now, and how many times a
	// second holder opened it while another held it.
	holders  int
	overlaps int
	// When the last holder closed the node, and how many messages
	// arrived sooner than ddcBusGap after the previous exchange ended.
	closedAt time.Time
	rushed   int
}

// journal is the shared log. The panel and the API server write to it
// from their own goroutines.
type journal struct {
	mu    sync.Mutex
	lines []string
}

func (j *journal) add(format string, args ...any) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.lines = append(j.lines, fmt.Sprintf(format, args...))
}

func (j *journal) read() []string {
	j.mu.Lock()
	defer j.mu.Unlock()
	return slices.Clone(j.lines)
}

// monitorSet is one write that the panel received, so a test checks
// what was sent on the bus, not what the code meant to send.
type monitorSet struct {
	Code  byte
	Value uint16
}

// The default panel has a brightness of 50 out of 100 and a power mode
// of standby, which is the state that a power-on claim finds a screen
// in.
func newFakeMonitor() *fakeMonitor {
	return &fakeMonitor{
		values: map[byte]uint16{vcpBrightness: 50, vcpPowerMode: powerModeStandby},
		maxima: map[byte]uint16{vcpBrightness: 100, vcpPowerMode: 5},
		clamps: map[byte]uint16{},
	}
}

// A panel that does not respond at the DDC/CI address. Its undriven wire
// reads as all ones.
func deafMonitor() *fakeMonitor {
	monitor := newFakeMonitor()
	monitor.silent = true
	return monitor
}

// The panel starts responding, as it does when a person turns DDC/CI on
// in its menu. That raises no uevent and does not change the EDID.
func (m *fakeMonitor) startResponding() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.silent = false
	m.garbled = false
}

// The panel stops responding, as a monitor does when it sleeps or when
// its menu is open.
func (m *fakeMonitor) silence() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.silent = true
}

// The panel replies to every request with bytes that differ from one
// reply to the next and are not a DDC/CI reply, as a sleeping panel
// did on a real machine.
func (m *fakeMonitor) garbles() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.garbled = true
}

// One control stops responding while the others still do. A read of it
// gets the unsupported reply.
func (m *fakeMonitor) drops(code byte) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.values, code)
}

// A person changes one control with the panel's own buttons. No message
// reaches the host, which is why the operator polls.
func (m *fakeMonitor) setWithButtons(code byte, value uint16) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.values[code] = value
}

// A panel that supports one control and not the other is common,
// because a display implements only the part of MCCS that its maker
// chose.
func monitorWithout(code byte) *fakeMonitor {
	monitor := newFakeMonitor()
	delete(monitor.values, code)
	return monitor
}

func (m *fakeMonitor) Write(request []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.closedAt.IsZero() && time.Since(m.closedAt) < ddcBusGap {
		m.rushed++
	}
	if m.silent {
		m.pending = bytes.Repeat([]byte{0xff}, getReplyLength)
		if request[2] == vcpSetRequest && m.wakesAfter > 0 {
			m.wakesAfter--
			m.silent = m.wakesAfter > 0
		}
		return nil
	}
	if m.garbled {
		m.noise++
		if m.noise == ddcReplySource {
			m.noise++
		}
		m.pending = append([]byte{m.noise}, bytes.Repeat([]byte{0x2a}, getReplyLength-1)...)
		return nil
	}
	code := request[3]
	switch request[2] {
	case vcpGetRequest:
		current, carried := m.values[code]
		if !carried {
			m.pending = corrupt(getReply(code, 0, 0), 3, vcpResultUnsupported)
			return nil
		}
		m.pending = getReply(code, current, m.maxima[code])
	case vcpSetRequest:
		value := uint16(request[4])<<8 | uint16(request[5])
		m.sets = append(m.sets, monitorSet{Code: code, Value: value})
		m.record("set %s=%d", capabilityName(code), value)
		if m.stubborn[code] > 0 {
			m.stubborn[code]--
			return nil
		}
		if clamped, limited := m.clamps[code]; limited {
			value = clamped
		}
		m.values[code] = value
	case vcpCapabilitiesRequest:
		if m.capabilities == "" {
			return nil
		}
		offset := int(request[3])<<8 | int(request[4])
		m.pending = capabilitiesFragmentOf(m.capabilities, offset)
	}
	return nil
}

// capabilitiesFragmentOf returns one fragment of the panel's capability
// string, padded to the length that a host reads.
func capabilitiesFragmentOf(capabilities string, offset int) []byte {
	if offset > len(capabilities) {
		offset = len(capabilities)
	}
	end := min(offset+capabilitiesFragmentBytes, len(capabilities))
	return paddedCapabilitiesReply(uint16(offset), capabilities[offset:end])
}

// record adds one line to the journal, for the fixtures that have one.
func (m *fakeMonitor) record(format string, args ...any) {
	if m.journal == nil {
		return
	}
	m.journal.add(format, args...)
}

func (m *fakeMonitor) Read(reply []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.pending) != len(reply) {
		return errors.New("the panel has no reply of that length to give")
	}
	copy(reply, m.pending)
	return nil
}

func (m *fakeMonitor) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.closes++
	m.holders--
	m.closedAt = time.Now()
	return nil
}

func (m *fakeMonitor) opened() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.opens++
	m.holders++
	if m.holders > 1 {
		m.overlaps++
	}
}

func (m *fakeMonitor) value(code byte) uint16 {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.values[code]
}

func (m *fakeMonitor) writes() []monitorSet {
	m.mu.Lock()
	defer m.mu.Unlock()
	return slices.Clone(m.sets)
}

// writesTo returns the values written to one code, in order, so a test
// checks the values and the number of writes together.
func (m *fakeMonitor) writesTo(code byte) []uint16 {
	var values []uint16
	for _, set := range m.writes() {
		if set.Code == code {
			values = append(values, set.Value)
		}
	}
	return values
}

// The i2c node behind each connector of the fixture. On a real machine
// the kernel assigns these numbers, so the code finds a connector's
// node only through its ddc link.
var labAdapters = map[string]string{
	"HDMI-A-1": "i2c-1",
	"HDMI-A-2": "i2c-2",
	"DP-1":     "i2c-3",
}

// panelBench replaces /dev in the fixture. It maps each node path to
// its fake panel, and counts opens, because the probe cache tests
// count opens.
type panelBench struct {
	monitors map[string]*fakeMonitor
	opens    atomic.Int64
}

func (b *panelBench) open(path string) (controlBus, error) {
	b.opens.Add(1)
	monitor, wired := b.monitors[path]
	if !wired {
		return nil, fmt.Errorf("no fake monitor is wired to %s", path)
	}
	monitor.opened()
	return monitor, nil
}

func (b *panelBench) opened() int {
	return int(b.opens.Load())
}

// benchPanels writes the ddc symlink that the kernel writes in a
// connector's sysfs directory, and connects each panel to the node that
// the link points to. A connector left out of the map has no link, like
// a DisplayPort connector behind an MST hub.
func benchPanels(t *testing.T, sysRoot, card string, monitors map[string]*fakeMonitor) (*panelControls, *panelBench) {
	t.Helper()
	bench := &panelBench{monitors: map[string]*fakeMonitor{}}
	for connector, monitor := range monitors {
		adapter, named := labAdapters[connector]
		if !named {
			t.Fatalf("the fixture wires no adapter for %s", connector)
		}
		dir := filepath.Join(sysRoot, "class", "drm", card+"-"+connector)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("creating the directory of %s: %v", connector, err)
		}
		if err := os.Symlink(adapter, filepath.Join(dir, "ddc")); err != nil {
			t.Fatalf("linking the ddc adapter of %s: %v", connector, err)
		}
		bench.monitors["/dev/"+adapter] = monitor
	}
	controls := &panelControls{
		sysRoot: sysRoot,
		card:    card,
		open:    bench.open,
		sleep:   func(time.Duration) {},
		probed:  map[string]probedPanel{},
	}
	return controls, bench
}

// connectedOutput is a connected output with a monitor, which is all that the
// probe reads from an Output.
func connectedOutput(connector string, monitor EDID) Output {
	return Output{Connector: connector, Connected: true, Monitor: monitor}
}

func labMonitor() EDID {
	return EDID{Manufacturer: "GSM", ProductCode: 0x5b09, ModelName: "LG HDR WQHD"}
}

func TestProbeReportsWhatThePanelAnswers(t *testing.T) {
	cases := []struct {
		name    string
		monitor *fakeMonitor
		want    supportedControls
	}{
		{
			name:    "a panel that supports both controls",
			monitor: newFakeMonitor(),
			want:    supportedControls{Brightness: true, Power: true},
		},
		{
			name:    "a panel that supports no power control",
			monitor: monitorWithout(vcpPowerMode),
			want:    supportedControls{Brightness: true},
		},
		{
			name:    "a panel that supports no brightness control",
			monitor: monitorWithout(vcpBrightness),
			want:    supportedControls{Power: true},
		},
		{
			// A silent panel publishes no attribute, not a false one.
			name:    "a panel that does not respond to DDC/CI at all",
			monitor: deafMonitor(),
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			controls, _ := benchPanels(t, t.TempDir(), "card1", map[string]*fakeMonitor{"HDMI-A-1": c.monitor})

			got := controls.of(connectedOutput("HDMI-A-1", labMonitor()))
			if got != c.want {
				t.Errorf("controls = %+v, want %+v", got, c.want)
			}
		})
	}
}

func TestProbeOpensNoBusForAConnectorWithoutOne(t *testing.T) {
	cases := []struct {
		name   string
		output Output
	}{
		{
			// A DisplayPort connector behind an MST hub has no ddc link,
			// because it carries DDC inside the AUX channel.
			name:   "a connector with no DDC channel",
			output: connectedOutput("DP-1", labMonitor()),
		},
		{
			name:   "a connector with nothing on it",
			output: Output{Connector: "HDMI-A-1", Monitor: labMonitor()},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			controls, bench := benchPanels(t, t.TempDir(), "card1",
				map[string]*fakeMonitor{"HDMI-A-1": newFakeMonitor()})

			if got := controls.of(c.output); got != (supportedControls{}) {
				t.Errorf("controls = %+v, want none", got)
			}
			if bench.opened() != 0 {
				t.Errorf("the probe opened %d buses, want none", bench.opened())
			}
		})
	}
}

// Every prepare and every slice publish reads the probe result, so the
// cache must probe each monitor once.
func TestProbeOpensTheBusOncePerMonitor(t *testing.T) {
	panel := newFakeMonitor()
	controls, bench := benchPanels(t, t.TempDir(), "card1", map[string]*fakeMonitor{"HDMI-A-1": panel})
	output := connectedOutput("HDMI-A-1", labMonitor())

	controls.of(output)
	controls.of(output)
	controls.of(output)

	if bench.opened() != 1 {
		t.Errorf("the probe opened the bus %d times for one monitor, want 1", bench.opened())
	}
	if panel.closes != 1 {
		t.Errorf("the probe left %d buses open", bench.opened()-panel.closes)
	}
}

// benchAtTime is a bench whose clock the test moves, to test the retry
// window of a refusal.
func benchAtTime(t *testing.T, monitors map[string]*fakeMonitor) (*panelControls, *panelBench, *time.Time) {
	t.Helper()
	controls, bench := benchPanels(t, t.TempDir(), "card1", monitors)
	at := time.Unix(0, 0).UTC()
	controls.now = func() time.Time { return at }
	return controls, bench, &at
}

// A panel that refused DDC/CI is probed again, because a person can
// turn DDC/CI on in the monitor's menu, which raises no uevent and does
// not change the EDID.
func TestARefusalIsProbedAgainAfterTheRetryWindow(t *testing.T) {
	panel := deafMonitor()
	controls, bench, at := benchAtTime(t, map[string]*fakeMonitor{"HDMI-A-1": panel})
	output := connectedOutput("HDMI-A-1", labMonitor())

	if got := controls.of(output); got != (supportedControls{}) {
		t.Fatalf("controls = %+v, want none from a panel that does not respond", got)
	}

	panel.startResponding()
	*at = at.Add(probeRetryInterval)
	got := controls.of(output)

	if !got.Brightness || !got.Power {
		t.Errorf("controls = %+v, want both controls from the panel that now responds", got)
	}
	if bench.opened() != 2 {
		t.Errorf("the bus was opened %d times, want 2: one probe and one probe after the retry window", bench.opened())
	}
}

// The uevents of one cable change arrive in a burst and each starts a
// pass, and passes also run every pollInterval. A refused probe is
// retried at most once per probeRetryInterval, because probing a
// silent panel retries every code.
func TestARefusalIsProbedOnceInEachRetryWindow(t *testing.T) {
	controls, bench, at := benchAtTime(t, map[string]*fakeMonitor{"HDMI-A-1": deafMonitor()})
	output := connectedOutput("HDMI-A-1", labMonitor())

	controls.of(output)
	for waited := pollInterval; waited < probeRetryInterval; waited += pollInterval {
		*at = at.Add(pollInterval)
		controls.of(output)
	}
	if bench.opened() != 1 {
		t.Errorf("the bus was opened %d times inside one retry window, want 1", bench.opened())
	}

	*at = at.Add(pollInterval)
	controls.of(output)

	if bench.opened() != 2 {
		t.Errorf("the bus was opened %d times, want 2: one probe and one probe after the retry window", bench.opened())
	}
}

func TestAnAnsweringPanelIsNeverProbedAgain(t *testing.T) {
	controls, bench, at := benchAtTime(t, map[string]*fakeMonitor{"HDMI-A-1": newFakeMonitor()})
	output := connectedOutput("HDMI-A-1", labMonitor())

	controls.of(output)
	*at = at.Add(10 * probeRetryInterval)
	controls.of(output)
	controls.of(output)

	if bench.opened() != 1 {
		t.Errorf("the bus was opened %d times for one panel that responds, want 1", bench.opened())
	}
}

func TestProbeReadsAgainWhenTheMonitorChanges(t *testing.T) {
	controls, bench := benchPanels(t, t.TempDir(), "card1",
		map[string]*fakeMonitor{"HDMI-A-1": newFakeMonitor()})

	controls.of(connectedOutput("HDMI-A-1", labMonitor()))
	other := EDID{Manufacturer: "BOE", ProductCode: 0x095f}
	got := controls.of(connectedOutput("HDMI-A-1", other))

	if bench.opened() != 2 {
		t.Errorf("the probe opened the bus %d times for two monitors, want 2", bench.opened())
	}
	if !got.Brightness {
		t.Errorf("controls = %+v, want the controls of the monitor now connected", got)
	}
}

// labPluginWithPanels is the lab plugin with the given fake panels on
// its connectors and its power record in a temporary directory.
func labPluginWithPanels(t *testing.T, config string, monitors map[string]*fakeMonitor) (*draPlugin, *panelBench) {
	t.Helper()
	plugin, _ := labPluginWithConfig(t, screenRequest(), config)
	controls, bench := benchPanels(t, plugin.sysRoot, plugin.card, monitors)
	plugin.controls = controls
	plugin.powerPath = filepath.Join(t.TempDir(), "power.json")
	// The grace timer never fires unless a test replaces it with
	// holdGrace.
	plugin.afterGrace = func(time.Duration, func()) {}
	return plugin, bench
}

// claimedPanel connects the panel on HDMI-A-2, the connector that
// screenRequest allocates.
func claimedPanel(monitor *fakeMonitor) map[string]*fakeMonitor {
	return map[string]*fakeMonitor{"HDMI-A-2": monitor}
}

func powerRecord(t *testing.T, plugin *draPlugin) map[string]string {
	t.Helper()
	record, err := readPowerRecord(plugin.powerPath)
	if err != nil {
		t.Fatalf("reading the power record: %v", err)
	}
	return record
}

func TestPrepareSetsTheBrightnessTheClaimStates(t *testing.T) {
	panel := newFakeMonitor()
	plugin, _ := labPluginWithPanels(t, claimControl(`{"brightness": 40}`), claimedPanel(panel))

	claim := prepare(t, plugin)

	if claim.Error != "" {
		t.Fatalf("prepare returned the error %q for a panel that supports the control", claim.Error)
	}
	// This panel's maximum is 100, so 40 percent is 40.
	if got := panel.writesTo(vcpBrightness); len(got) != 1 || got[0] != 40 {
		t.Errorf("the panel received the writes %v, want one write of 40", got)
	}
}

func TestPrepareScalesTheBrightnessToThePanel(t *testing.T) {
	panel := newFakeMonitor()
	panel.maxima[vcpBrightness] = 255
	plugin, _ := labPluginWithPanels(t, claimControl(`{"brightness": 40}`), claimedPanel(panel))

	if claim := prepare(t, plugin); claim.Error != "" {
		t.Fatalf("prepare returned the error %q, want none", claim.Error)
	}

	if got := panel.writesTo(vcpBrightness); len(got) != 1 || got[0] != 102 {
		t.Errorf("the panel received the writes %v, want one write of 102, which is 40%% of 255", got)
	}
}

func TestPrepareRefusesABrightnessThePanelDoesNotTake(t *testing.T) {
	panel := newFakeMonitor()
	panel.clamps[vcpBrightness] = 80
	plugin, _ := labPluginWithPanels(t, claimControl(`{"brightness": 40}`), claimedPanel(panel))

	claim := prepare(t, plugin)

	if claim.Error == "" {
		t.Fatal("prepare returned no error for a panel that never accepted the brightness, want one")
	}
	if !strings.Contains(claim.Error, "80") {
		t.Errorf("error is %q, want it to contain the brightness 80 that the panel reports", claim.Error)
	}
}

func TestPreparePowersThePanelOn(t *testing.T) {
	panel := newFakeMonitor()
	plugin, _ := labPluginWithPanels(t, claimControl(`{"power": "on"}`), claimedPanel(panel))

	claim := prepare(t, plugin)

	if claim.Error != "" {
		t.Fatalf("prepare returned the error %q for a panel that supports the control", claim.Error)
	}
	if got := panel.writesTo(vcpPowerMode); len(got) != 1 || got[0] != powerModeOn {
		t.Errorf("the panel received the writes %v, want one write of %#02x", got, powerModeOn)
	}
	// A claim with on needs no standby at the end, so nothing is
	// recorded.
	if got := powerRecord(t, plugin); len(got) != 0 {
		t.Errorf("record = %v, want nothing written", got)
	}
}

func TestPrepareRecordsThePanelForStandbyAtUnprepare(t *testing.T) {
	panel := newFakeMonitor()
	plugin, _ := labPluginWithPanels(t, claimControl(`{"power": "onWhileClaimed"}`), claimedPanel(panel))

	if claim := prepare(t, plugin); claim.Error != "" {
		t.Fatalf("prepare returned the error %q, want none", claim.Error)
	}

	// The standby at unprepare reads this file, so it still runs after
	// an operator container restart.
	if got := powerRecord(t, plugin); got["HDMI-A-2"] != powerOnWhileClaimed {
		t.Errorf("record = %v, want HDMI-A-2 held to %s", got, powerOnWhileClaimed)
	}
	if got := panel.writesTo(vcpPowerMode); len(got) != 1 || got[0] != powerModeOn {
		t.Errorf("the panel received the writes %v, want one write of %#02x", got, powerModeOn)
	}
}

func TestPrepareRefusesAControlThePanelDoesNotCarry(t *testing.T) {
	cases := []struct {
		name    string
		config  string
		monitor *fakeMonitor
		says    string
	}{
		{
			name:    "a brightness on a panel that does not respond to DDC/CI",
			config:  claimControl(`{"brightness": 40}`),
			monitor: deafMonitor(),
			says:    "controlsBrightness",
		},
		{
			name:    "a power state on a panel that does not respond to DDC/CI",
			config:  claimControl(`{"power": "on"}`),
			monitor: deafMonitor(),
			says:    "controlsPower",
		},
		{
			name:    "a brightness on a panel that supports only the power control",
			config:  claimControl(`{"brightness": 40}`),
			monitor: monitorWithout(vcpBrightness),
			says:    "controlsBrightness",
		},
		{
			name:    "a power state on a panel that supports only the brightness",
			config:  claimControl(`{"power": "on"}`),
			monitor: monitorWithout(vcpPowerMode),
			says:    "controlsPower",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			plugin, _ := labPluginWithPanels(t, c.config, claimedPanel(c.monitor))

			claim := prepare(t, plugin)

			if claim.Error == "" {
				t.Fatal("prepare returned no error for a control that the panel does not support, want one")
			}
			if !strings.Contains(claim.Error, c.says) {
				t.Errorf("error is %q, want it to contain the %s attribute", claim.Error, c.says)
			}
		})
	}
}

func TestPrepareWithNoControlOpensNoBus(t *testing.T) {
	panel := newFakeMonitor()
	plugin, bench := labPluginWithPanels(t, claimMode(`{"mode": "1920x1080"}`), claimedPanel(panel))

	if claim := prepare(t, plugin); claim.Error != "" {
		t.Fatalf("prepare returned the error %q, want none", claim.Error)
	}

	if bench.opened() != 0 {
		t.Errorf("prepare opened %d buses for a claim that sets no control, want none", bench.opened())
	}
}

func TestUnprepareWritesStandby(t *testing.T) {
	panel := newFakeMonitor()
	plugin, _ := labPluginWithPanels(t, claimControl(`{"power": "onWhileClaimed"}`), claimedPanel(panel))
	grace := holdGrace(plugin)
	if claim := prepare(t, plugin); claim.Error != "" {
		t.Fatalf("prepare returned the error %q, want none", claim.Error)
	}

	unprepare(t, plugin)
	grace.end()

	want := []uint16{powerModeOn, powerModeStandby}
	if got := panel.writesTo(vcpPowerMode); len(got) != 2 || got[1] != powerModeStandby {
		t.Errorf("the panel received the writes %v, want %v", got, want)
	}
	if got := powerRecord(t, plugin); len(got) != 0 {
		t.Errorf("record = %v, want nothing left", got)
	}
}

// The clamp makes the fake panel accept the standby write and stay on,
// as a panel does when its power code has no standby value.
func TestUnprepareWritesOffToAPanelThatRefusesStandby(t *testing.T) {
	panel := newFakeMonitor()
	panel.clamps[vcpPowerMode] = powerModeOn
	plugin, _ := labPluginWithPanels(t, claimControl(`{"power": "onWhileClaimed"}`), claimedPanel(panel))
	grace := holdGrace(plugin)
	if claim := prepare(t, plugin); claim.Error != "" {
		t.Fatalf("prepare returned the error %q, want none", claim.Error)
	}

	unprepare(t, plugin)
	grace.end()

	want := []uint16{powerModeOn, powerModeStandby, powerModeOff}
	if got := panel.writesTo(vcpPowerMode); !slices.Equal(got, want) {
		t.Errorf("the panel received the writes %v, want %v", got, want)
	}
}

// A claim with on leaves the panel on at unprepare, for a workload
// whose screen must never be powered down.
func TestUnprepareLeavesOnAPanelThatTheClaimOnlyPoweredOn(t *testing.T) {
	panel := newFakeMonitor()
	plugin, _ := labPluginWithPanels(t, claimControl(`{"power": "on"}`), claimedPanel(panel))
	if claim := prepare(t, plugin); claim.Error != "" {
		t.Fatalf("prepare returned the error %q, want none", claim.Error)
	}

	unprepare(t, plugin)

	if got := panel.writesTo(vcpPowerMode); len(got) != 1 || got[0] != powerModeOn {
		t.Errorf("the panel received the writes %v, want the power-on and nothing after it", got)
	}
}

// The kubelet can call unprepare again for the same claim, so a second
// call must schedule no second standby.
func TestUnprepareWritesStandbyOnce(t *testing.T) {
	panel := newFakeMonitor()
	plugin, _ := labPluginWithPanels(t, claimControl(`{"power": "onWhileClaimed"}`), claimedPanel(panel))
	grace := holdGrace(plugin)
	if claim := prepare(t, plugin); claim.Error != "" {
		t.Fatalf("prepare returned the error %q, want none", claim.Error)
	}

	unprepare(t, plugin)
	unprepare(t, plugin)
	grace.end()

	if got := panel.writesTo(vcpPowerMode); len(got) != 2 {
		t.Errorf("the panel received the writes %v, want the power-on and one standby", got)
	}
}

// A panel that does not power down must not keep the claim open,
// because the kubelet repeats a failed unprepare and the pod would never
// finish terminating.
func TestUnprepareEndsTheClaimWhenThePanelWillNotAnswer(t *testing.T) {
	panel := newFakeMonitor()
	plugin, _ := labPluginWithPanels(t, claimControl(`{"power": "onWhileClaimed"}`), claimedPanel(panel))
	grace := holdGrace(plugin)
	if claim := prepare(t, plugin); claim.Error != "" {
		t.Fatalf("prepare returned the error %q, want none", claim.Error)
	}
	panel.silent = true

	unprepare(t, plugin)
	grace.end()

	if got := powerRecord(t, plugin); len(got) != 0 {
		t.Errorf("record = %v, want the entry removed whether or not the panel responded", got)
	}
	if got := specFiles(t); len(got) != 0 {
		t.Errorf("after unprepare, the spec files are %v, want none", got)
	}
}

// A control attribute is either present and true or absent, never
// present and false.
func TestSliceDevicesPublishesTheControlsAPanelCarries(t *testing.T) {
	cases := []struct {
		name      string
		controls  supportedControls
		attribute string
		published bool
	}{
		{
			name:      "the brightness of a panel that supports it",
			controls:  supportedControls{Brightness: true, Power: true},
			attribute: "controlsBrightness",
			published: true,
		},
		{
			name:      "the power of a panel that supports it",
			controls:  supportedControls{Brightness: true, Power: true},
			attribute: "controlsPower",
			published: true,
		},
		{
			name:      "the power of a panel that supports only the brightness",
			controls:  supportedControls{Brightness: true},
			attribute: "controlsPower",
		},
		{
			name:      "the brightness of a panel that does not respond to DDC/CI",
			controls:  supportedControls{},
			attribute: "controlsBrightness",
		},
		{
			name:      "the power of a panel that does not respond to DDC/CI",
			controls:  supportedControls{},
			attribute: "controlsPower",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			devices := sliceDevices([]Output{{
				Connector: "HDMI-A-1",
				Connected: true,
				Monitor:   labMonitor(),
				Controls:  c.controls,
			}})

			attribute, published := devices[0].Attributes[c.attribute]
			if published != c.published {
				t.Fatalf("%s published = %v, want %v", c.attribute, published, c.published)
			}
			if published && (attribute.Bool == nil || !*attribute.Bool) {
				t.Errorf("%s = %+v, want it true", c.attribute, attribute)
			}
		})
	}
}

// A disconnected output publishes no control attribute, because the
// panel that was probed is gone.
func TestSliceDevicesPublishesNoControlOfAnEmptyConnector(t *testing.T) {
	devices := sliceDevices([]Output{{
		Connector: "DP-1",
		Controls:  supportedControls{Brightness: true, Power: true},
	}})

	for _, name := range []string{"controlsBrightness", "controlsPower"} {
		if _, published := devices[0].Attributes[name]; published {
			t.Errorf("the device has %s on a connector with nothing on it, want no attribute", name)
		}
	}
}
