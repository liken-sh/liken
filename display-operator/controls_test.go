package main

// These tests stand on a fake panel that answers the same DDC/CI
// packets a real one answers, so the probe, the scaling, and the
// readback are proven against the protocol itself and not against a
// stand-in for the code that speaks it.

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

// The two builders modes_test uses, named here for a block that
// states a control.
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
			want:    requestedControls{Brightness: brightness{Percent: 40, Stated: true}},
		},
		{
			// Zero percent is a brightness a claim can ask for, and
			// the flag beside the number is what tells it from a claim
			// that stated none.
			name:    "a brightness of zero",
			config:  claimControl(`{"brightness": 0}`),
			request: "screen",
			want:    requestedControls{Brightness: brightness{Percent: 0, Stated: true}},
		},
		{
			name:    "the power a claim states",
			config:  claimControl(`{"power": "onWhileClaimed"}`),
			request: "screen",
			want:    requestedControls{Power: powerOnWhileClaimed},
		},
		{
			name:    "both controls and a mode in one block",
			config:  claimControl(`{"mode": "1280x720", "brightness": 75, "power": "on"}`),
			request: "screen",
			want: requestedControls{
				Brightness: brightness{Percent: 75, Stated: true},
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
			want:    requestedControls{Brightness: brightness{Percent: 90, Stated: true}},
		},
		{
			name:    "the class's brightness, with none in the claim",
			config:  classControl(`{"brightness": 30}`),
			request: "screen",
			want:    requestedControls{Brightness: brightness{Percent: 30, Stated: true}},
		},
		{
			name:    "the claim's brightness over the class's",
			config:  classControl(`{"brightness": 30}`) + "," + claimControl(`{"brightness": 90}`),
			request: "screen",
			want:    requestedControls{Brightness: brightness{Percent: 90, Stated: true}},
		},
		{
			name:    "the claim's brightness, listed before the class's",
			config:  claimControl(`{"brightness": 90}`) + "," + classControl(`{"brightness": 30}`),
			request: "screen",
			want:    requestedControls{Brightness: brightness{Percent: 90, Stated: true}},
		},
		{
			// Each control resolves on its own, so a claim that
			// states one of them leaves the other to cluster policy.
			name:    "the claim's brightness beside the class's power",
			config:  classControl(`{"power": "on"}`) + "," + claimControl(`{"brightness": 90}`),
			request: "screen",
			want: requestedControls{
				Brightness: brightness{Percent: 90, Stated: true},
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
				t.Fatal(err)
			}
			if got := controls.forRequest(c.request); got != c.want {
				t.Errorf("controls = %+v, want %+v", got, c.want)
			}
		})
	}
}

// The parse is the only judge of these values. The scheduler copies
// an opaque block through unread, so a value nobody refuses here
// reaches a panel.
func TestClaimControlsRefusesParametersItCannotRead(t *testing.T) {
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
			name:   "a power state this driver does not carry",
			config: claimControl(`{"power": "off"}`),
			says:   `"onWhileClaimed"`,
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
				t.Fatal("the parse accepted parameters it cannot read")
			}
			if !strings.Contains(err.Error(), c.says) {
				t.Errorf("error = %q, want it to say %q", err, c.says)
			}
		})
	}
}

// The two parsers read one block and take different keys out of it,
// so neither one may refuse the other's.
func TestEachParserReadsPastTheOthersParameters(t *testing.T) {
	config := resolvedConfig(t, claimControl(`{"mode": "1280x720", "brightness": 75, "power": "on"}`))

	modes, err := claimModes(config)
	if err != nil {
		t.Fatal(err)
	}
	if got := modes.forRequest("screen"); got != "1280x720" {
		t.Errorf("mode = %q, want 1280x720", got)
	}
	controls, err := claimControls(config)
	if err != nil {
		t.Fatal(err)
	}
	want := requestedControls{Brightness: brightness{Percent: 75, Stated: true}, Power: powerOn}
	if got := controls.forRequest("screen"); got != want {
		t.Errorf("controls = %+v, want %+v", got, want)
	}
}

// The claim states a percentage and the panel holds a scale of its
// own, so the rounding must land on the nearest step that scale
// carries.
func TestBrightnessValueScalesToThePanelsOwnRange(t *testing.T) {
	cases := []struct {
		name    string
		percent int
		max     uint16
		want    uint16
	}{
		{name: "a scale of a hundred steps", percent: 40, max: 100, want: 40},
		{name: "the whole scale", percent: 100, max: 100, want: 100},
		{name: "a dark panel", percent: 0, max: 100, want: 0},
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

// A fakeMonitor is the panel's own half of the protocol, holding one
// value and one maximum for each code it carries. A code it does not
// hold answers the way a real display answers a control it has none
// of: a well-formed reply whose result byte says unsupported.
type fakeMonitor struct {
	values  map[byte]uint16
	maxima  map[byte]uint16
	clamps  map[byte]uint16
	silent  bool
	sets    []monitorSet
	opens   int
	closes  int
	pending []byte
	// The capability string this panel declares, and nothing
	// when it declares none. A panel that declares none is the panel
	// the probe falls back to asking, code by code.
	capabilities string
	// The writes this panel drops before it starts taking them,
	// which is what a panel that is waking does.
	stubborn map[byte]int
	// How many writes a silent panel takes before it answers
	// again.
	wakesAfter int
	// The codes this panel answers with a reply that parses
	// wrong. The lab's ultrawide does this to the input query while it
	// shows another source: not silence, an invalid response.
	garbles map[byte]bool
	// The shared record of what reached the panel and what
	// reached the API server, so a test reads the two in the order
	// they happened.
	journal *journal
	// A restore runs on its own goroutine, so the panel it
	// writes to is reached from two goroutines at once.
	mu sync.Mutex
}

// The shared record. Both the panel and the API server write
// it, from goroutines of their own, and a test reads the whole of it.
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

// A monitorSet is one write the panel took, so a test reads what
// reached the wire rather than what the code meant to send.
type monitorSet struct {
	Code  byte
	Value uint16
}

// The default panel carries a brightness of fifty out of a hundred
// and a power mode of standby, which is the state a claim's power-on
// finds a screen in.
func newFakeMonitor() *fakeMonitor {
	return &fakeMonitor{
		values: map[byte]uint16{vcpBrightness: 50, vcpPowerMode: powerModeStandby},
		maxima: map[byte]uint16{vcpBrightness: 100, vcpPowerMode: 5},
		clamps: map[byte]uint16{},
	}
}

// The lab measured a panel like this beside the one that answers: it
// answers nothing at the DDC/CI address, and its undriven wire reads
// as all ones.
func deafMonitor() *fakeMonitor {
	monitor := newFakeMonitor()
	monitor.silent = true
	return monitor
}

// The panel starts answering, which is what a person switching
// the monitor's input or turning DDC/CI on in its menu does. Neither
// fires a uevent and neither changes the EDID.
func (m *fakeMonitor) answers() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.silent = false
}

// The panel stops answering, which a monitor does when it goes
// to sleep or when its menu is open.
func (m *fakeMonitor) silence() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.silent = true
}

// The panel starts, or stops, answering one code with a reply
// that parses wrong. This is what the lab's ultrawide does to the
// input query while it shows another source: not silence, a reply the
// protocol cannot read.
func (m *fakeMonitor) garble(code byte, garbles bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.garbles == nil {
		m.garbles = map[byte]bool{}
	}
	m.garbles[code] = garbles
}

// A person turns one control at the panel's own buttons. No
// message reaches the host, which is the whole reason the operator
// polls.
func (m *fakeMonitor) turnedTo(code byte, value uint16) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.values[code] = value
}

// A panel that carries one control and not the other is the ordinary
// case, because a display implements the subset of the standard it
// chooses.
func monitorWithout(code byte) *fakeMonitor {
	monitor := newFakeMonitor()
	delete(monitor.values, code)
	return monitor
}

func (m *fakeMonitor) Write(request []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.silent {
		m.pending = bytes.Repeat([]byte{0xff}, getReplyLength)
		// A panel that is waking takes this many writes before
		// it answers again, which is what a panel coming out of a
		// power-down does.
		if request[2] == vcpSetRequest && m.wakesAfter > 0 {
			m.wakesAfter--
			m.silent = m.wakesAfter > 0
		}
		return nil
	}
	code := request[3]
	switch request[2] {
	case vcpGetRequest:
		if m.garbles[code] {
			m.pending = corrupt(getReply(code, m.values[code], m.maxima[code]), 0, ddcHostAddress)
			return nil
		}
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

// One fragment of this panel's capability string, padded to the
// length a host reads.
func capabilitiesFragmentOf(capabilities string, offset int) []byte {
	if offset > len(capabilities) {
		offset = len(capabilities)
	}
	end := min(offset+capabilitiesFragmentBytes, len(capabilities))
	return paddedCapabilitiesReply(uint16(offset), capabilities[offset:end])
}

// One line of the shared record, for the fixtures that keep
// one.
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
	return nil
}

// One open of this panel's node, counted under the same lock
// as everything else the panel records.
func (m *fakeMonitor) opened() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.opens++
}

// What one control holds now.
func (m *fakeMonitor) holds(code byte) uint16 {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.values[code]
}

// Every write the panel took, in order.
func (m *fakeMonitor) writes() []monitorSet {
	m.mu.Lock()
	defer m.mu.Unlock()
	return slices.Clone(m.sets)
}

// Took lists what the panel took for one code, in order, so a test
// reads the values and the count together.
func (m *fakeMonitor) took(code byte) []uint16 {
	var values []uint16
	for _, set := range m.writes() {
		if set.Code == code {
			values = append(values, set.Value)
		}
	}
	return values
}

// The i2c node behind each of the fixture's connectors. The numbering
// is the fixture's own: on a real machine the kernel assigns it, and
// nothing outside the ddc links may name one.
var labAdapters = map[string]string{
	"HDMI-A-1": "i2c-1",
	"HDMI-A-2": "i2c-2",
	"DP-1":     "i2c-3",
}

// A panelBench is the /dev end of the fixture: it maps the node a ddc
// link names to the panel behind it, and it counts the opens, because
// the probe cache is measured in opens.
type panelBench struct {
	monitors map[string]*fakeMonitor
	opens    atomic.Int64
}

func (b *panelBench) open(path string) (controlBus, error) {
	b.opens.Add(1)
	monitor, wired := b.monitors[path]
	if !wired {
		return nil, fmt.Errorf("nothing answers on %s", path)
	}
	monitor.opened()
	return monitor, nil
}

// How many times the bench handed out a bus.
func (b *panelBench) opened() int {
	return int(b.opens.Load())
}

// BenchPanels writes the ddc symlink the kernel writes beside a
// connector's other files, and wires the panel that answers on the
// node it names. A connector left out of the map has no link, which
// is what a DisplayPort connector behind an MST hub has.
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
			t.Fatal(err)
		}
		if err := os.Symlink(adapter, filepath.Join(dir, "ddc")); err != nil {
			t.Fatal(err)
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

// LitOutput is one connector with a monitor on it, which is all the
// probe reads out of an Output.
func litOutput(connector string, monitor EDID) Output {
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
			name:    "a panel that carries both controls",
			monitor: newFakeMonitor(),
			want:    supportedControls{Brightness: true, Power: true},
		},
		{
			name:    "a panel that carries no power control",
			monitor: monitorWithout(vcpPowerMode),
			want:    supportedControls{Brightness: true},
		},
		{
			name:    "a panel that carries no brightness control",
			monitor: monitorWithout(vcpBrightness),
			want:    supportedControls{Power: true},
		},
		{
			// The lab's second panel is one of these, and its silence
			// publishes nothing rather than publishing a false.
			name:    "a panel that answers no DDC/CI at all",
			monitor: deafMonitor(),
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			controls, _ := benchPanels(t, t.TempDir(), "card1", map[string]*fakeMonitor{"HDMI-A-1": c.monitor})

			got := controls.of(litOutput("HDMI-A-1", labMonitor()))
			if got != c.want {
				t.Errorf("controls = %+v, want %+v", got, c.want)
			}
		})
	}
}

func TestProbeReadsNoWireItCannotReach(t *testing.T) {
	cases := []struct {
		name   string
		output Output
	}{
		{
			// A connector with no ddc link carries its DDC inside the
			// AUX stream, where no i2c-dev node reaches it.
			name:   "a connector with no DDC channel",
			output: litOutput("DP-1", labMonitor()),
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

// The cache is what keeps the walk cheap: it runs on every prepare
// and every slice publish, a refusing panel costs three attempts for
// each code, and the monitor's own EDID is what says the cached
// answer still holds.
func TestProbeAsksOncePerMonitor(t *testing.T) {
	panel := newFakeMonitor()
	controls, bench := benchPanels(t, t.TempDir(), "card1", map[string]*fakeMonitor{"HDMI-A-1": panel})
	output := litOutput("HDMI-A-1", labMonitor())

	controls.of(output)
	controls.of(output)
	controls.of(output)

	if bench.opened() != 1 {
		t.Errorf("the probe opened the bus %d times for one monitor, want 1", bench.opened())
	}
	if panel.closes != 1 {
		t.Errorf("the probe left %d of its opens unclosed", bench.opened()-panel.closes)
	}
}

// A bench whose clock the test moves, for the window that
// rate-limits the second ask.
func benchAtTime(t *testing.T, monitors map[string]*fakeMonitor) (*panelControls, *panelBench, *time.Time) {
	t.Helper()
	controls, bench := benchPanels(t, t.TempDir(), "card1", monitors)
	at := time.Unix(0, 0).UTC()
	controls.now = func() time.Time { return at }
	return controls, bench, &at
}

// A panel that refused DDC/CI is asked again, because a refusal
// is a state a person changes at the monitor's own menu, with no
// uevent and no change of EDID to say so. The lab's LG did exactly
// this and stayed unresponsive in the cluster until it was asked
// again.
func TestARefusalIsAskedAgainAfterTheWindow(t *testing.T) {
	panel := deafMonitor()
	controls, bench, at := benchAtTime(t, map[string]*fakeMonitor{"HDMI-A-1": panel})
	output := litOutput("HDMI-A-1", labMonitor())

	if got := controls.of(output); got != (supportedControls{}) {
		t.Fatalf("controls = %+v, want none from a panel that answers nothing", got)
	}

	panel.answers()
	*at = at.Add(probeRetryInterval)
	got := controls.of(output)

	if !got.Brightness || !got.Power {
		t.Errorf("controls = %+v, want the answers of the panel that is talking now", got)
	}
	if bench.opened() != 2 {
		t.Errorf("the wire was opened %d times, want one probe and one ask again", bench.opened())
	}
}

// The window is what keeps a burst of passes off the wire. The
// uevents of one cable arrive in a burst, and each one is a pass.
//
// The passes now arrive at the poll's cadence, and a refusal
// keeps its own slower window: a probe of a silent panel spends the
// whole retry ladder on every code, which is the cost this window
// holds back.
func TestARefusalIsAskedAgainOncePerWindow(t *testing.T) {
	controls, bench, at := benchAtTime(t, map[string]*fakeMonitor{"HDMI-A-1": deafMonitor()})
	output := litOutput("HDMI-A-1", labMonitor())

	controls.of(output)
	for waited := pollInterval; waited < probeRetryInterval; waited += pollInterval {
		*at = at.Add(pollInterval)
		controls.of(output)
	}
	if bench.opened() != 1 {
		t.Errorf("the wire was opened %d times inside one window, want 1", bench.opened())
	}

	*at = at.Add(pollInterval)
	controls.of(output)

	if bench.opened() != 2 {
		t.Errorf("the wire was opened %d times, want one probe and one ask again", bench.opened())
	}
}

// The invariant the cache exists for. A panel that answers is
// never asked twice, so a steady pass over answering hardware sends
// nothing on any wire.
func TestAnAnsweringPanelIsNeverAskedAgain(t *testing.T) {
	controls, bench, at := benchAtTime(t, map[string]*fakeMonitor{"HDMI-A-1": newFakeMonitor()})
	output := litOutput("HDMI-A-1", labMonitor())

	controls.of(output)
	*at = at.Add(10 * probeRetryInterval)
	controls.of(output)
	controls.of(output)

	if bench.opened() != 1 {
		t.Errorf("the wire was opened %d times for one answering panel, want 1", bench.opened())
	}
}

func TestProbeAsksAgainWhenTheMonitorChanges(t *testing.T) {
	controls, bench := benchPanels(t, t.TempDir(), "card1",
		map[string]*fakeMonitor{"HDMI-A-1": newFakeMonitor()})

	controls.of(litOutput("HDMI-A-1", labMonitor()))
	other := EDID{Manufacturer: "BOE", ProductCode: 0x095f}
	got := controls.of(litOutput("HDMI-A-1", other))

	if bench.opened() != 2 {
		t.Errorf("the probe opened the bus %d times for two monitors, want 2", bench.opened())
	}
	if !got.Brightness {
		t.Errorf("controls = %+v, want the answer of the monitor there now", got)
	}
}

// LabPluginWithPanels is the lab machine with its wires: the portable
// panel on HDMI-A-2 answers DDC/CI and the ultrawide on HDMI-A-1
// refuses it. The record of the panels a claim must put back lives in
// the plugin's own temporary volume, like the mode record does.
func labPluginWithPanels(t *testing.T, config string, monitors map[string]*fakeMonitor) (*draPlugin, *panelBench) {
	t.Helper()
	plugin, _ := labPluginWithConfig(t, screenRequest(), config)
	controls, bench := benchPanels(t, plugin.sysRoot, plugin.card, monitors)
	plugin.controls = controls
	plugin.powerPath = filepath.Join(t.TempDir(), "power.json")
	return plugin, bench
}

// ClaimedPanel wires the panel a claim in these tests holds, because
// screenRequest allocates hdmi-a-2.
func claimedPanel(monitor *fakeMonitor) map[string]*fakeMonitor {
	return map[string]*fakeMonitor{"HDMI-A-2": monitor}
}

func powerRecord(t *testing.T, plugin *draPlugin) map[string]string {
	t.Helper()
	record, err := readPowerRecord(plugin.powerPath)
	if err != nil {
		t.Fatal(err)
	}
	return record
}

func TestPrepareSetsTheBrightnessTheClaimStates(t *testing.T) {
	panel := newFakeMonitor()
	plugin, _ := labPluginWithPanels(t, claimControl(`{"brightness": 40}`), claimedPanel(panel))

	claim := prepare(t, plugin)

	if claim.Error != "" {
		t.Fatalf("prepare refused a panel that carries the control: %s", claim.Error)
	}
	// The claim states a percentage of the panel's own maximum, and
	// this panel counts to a hundred.
	if got := panel.took(vcpBrightness); len(got) != 1 || got[0] != 40 {
		t.Errorf("the panel took %v, want one write of 40", got)
	}
}

func TestPrepareScalesTheBrightnessToThePanel(t *testing.T) {
	panel := newFakeMonitor()
	panel.maxima[vcpBrightness] = 255
	plugin, _ := labPluginWithPanels(t, claimControl(`{"brightness": 40}`), claimedPanel(panel))

	if claim := prepare(t, plugin); claim.Error != "" {
		t.Fatal(claim.Error)
	}

	if got := panel.took(vcpBrightness); len(got) != 1 || got[0] != 102 {
		t.Errorf("the panel took %v, want one write of 102, which is 40%% of 255", got)
	}
}

// A display acknowledges a write on the wire whether it takes the
// value or not, so the readback is the only proof, and a claim that
// cannot get the brightness it stated is a failed claim.
func TestPrepareRefusesABrightnessThePanelDoesNotTake(t *testing.T) {
	panel := newFakeMonitor()
	panel.clamps[vcpBrightness] = 80
	plugin, _ := labPluginWithPanels(t, claimControl(`{"brightness": 40}`), claimedPanel(panel))

	claim := prepare(t, plugin)

	if claim.Error == "" {
		t.Fatal("prepare delivered a screen that never took the brightness")
	}
	if !strings.Contains(claim.Error, "80") {
		t.Errorf("error = %q, want it to name the brightness the panel holds", claim.Error)
	}
}

func TestPreparePowersThePanelOn(t *testing.T) {
	panel := newFakeMonitor()
	plugin, _ := labPluginWithPanels(t, claimControl(`{"power": "on"}`), claimedPanel(panel))

	claim := prepare(t, plugin)

	if claim.Error != "" {
		t.Fatalf("prepare refused a panel that carries the control: %s", claim.Error)
	}
	if got := panel.took(vcpPowerMode); len(got) != 1 || got[0] != powerModeOn {
		t.Errorf("the panel took %v, want one write of %#02x", got, powerModeOn)
	}
	// A claim that stated on owes the panel nothing at the end, so
	// nothing is recorded and no rollout blinks the screen.
	if got := powerRecord(t, plugin); len(got) != 0 {
		t.Errorf("record = %v, want nothing written", got)
	}
}

func TestPrepareRecordsThePanelItMustPutBack(t *testing.T) {
	panel := newFakeMonitor()
	plugin, _ := labPluginWithPanels(t, claimControl(`{"power": "onWhileClaimed"}`), claimedPanel(panel))

	if claim := prepare(t, plugin); claim.Error != "" {
		t.Fatal(claim.Error)
	}

	// The record outlives a restart of the operator's container, which
	// is what makes the power-down happen at all.
	if got := powerRecord(t, plugin); got["HDMI-A-2"] != powerOnWhileClaimed {
		t.Errorf("record = %v, want HDMI-A-2 held to %s", got, powerOnWhileClaimed)
	}
	if got := panel.took(vcpPowerMode); len(got) != 1 || got[0] != powerModeOn {
		t.Errorf("the panel took %v, want one write of %#02x", got, powerModeOn)
	}
}

// A claim that names a control on a panel that carries none fails
// with the capability named, because the scheduler reads no opaque
// parameter and no selector could have kept the claim off this
// screen on its own.
func TestPrepareRefusesAControlThePanelDoesNotCarry(t *testing.T) {
	cases := []struct {
		name    string
		config  string
		monitor *fakeMonitor
		says    string
	}{
		{
			name:    "a brightness on a panel that answers no DDC/CI",
			config:  claimControl(`{"brightness": 40}`),
			monitor: deafMonitor(),
			says:    "controlsBrightness",
		},
		{
			name:    "a power state on a panel that answers no DDC/CI",
			config:  claimControl(`{"power": "on"}`),
			monitor: deafMonitor(),
			says:    "controlsPower",
		},
		{
			name:    "a brightness on a panel that carries only the power control",
			config:  claimControl(`{"brightness": 40}`),
			monitor: monitorWithout(vcpBrightness),
			says:    "controlsBrightness",
		},
		{
			name:    "a power state on a panel that carries only the brightness",
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
				t.Fatal("prepare delivered a control the panel does not carry")
			}
			if !strings.Contains(claim.Error, c.says) {
				t.Errorf("error = %q, want it to name the %s attribute", claim.Error, c.says)
			}
		})
	}
}

// A claim that states neither control puts nothing on the wire,
// which is what keeps a prepare free on every machine where no claim
// states one.
func TestPrepareWithNoControlOpensNoBus(t *testing.T) {
	panel := newFakeMonitor()
	plugin, bench := labPluginWithPanels(t, claimMode(`{"mode": "1920x1080"}`), claimedPanel(panel))

	if claim := prepare(t, plugin); claim.Error != "" {
		t.Fatal(claim.Error)
	}

	if bench.opened() != 0 {
		t.Errorf("prepare opened %d buses for a claim that states no control", bench.opened())
	}
}

func TestUnprepareStandsThePanelDown(t *testing.T) {
	panel := newFakeMonitor()
	plugin, _ := labPluginWithPanels(t, claimControl(`{"power": "onWhileClaimed"}`), claimedPanel(panel))
	if claim := prepare(t, plugin); claim.Error != "" {
		t.Fatal(claim.Error)
	}

	unprepare(t, plugin)

	want := []uint16{powerModeOn, powerModeStandby}
	if got := panel.took(vcpPowerMode); len(got) != 2 || got[1] != powerModeStandby {
		t.Errorf("the panel took %v, want %v", got, want)
	}
	if got := powerRecord(t, plugin); len(got) != 0 {
		t.Errorf("record = %v, want nothing left", got)
	}
}

// The lab's BOE panel answers the power code and lists no standby in
// its 0xD6 subset, so a write of standby leaves it running. The
// readback is what discovers that, and off is the value that lands.
// The clamp is how the fake panel refuses: it takes the write and
// keeps its own value, which is exactly what the real panel did.
func TestUnprepareTurnsOffAPanelThatRefusesStandby(t *testing.T) {
	panel := newFakeMonitor()
	panel.clamps[vcpPowerMode] = powerModeOn
	plugin, _ := labPluginWithPanels(t, claimControl(`{"power": "onWhileClaimed"}`), claimedPanel(panel))
	if claim := prepare(t, plugin); claim.Error != "" {
		t.Fatal(claim.Error)
	}

	unprepare(t, plugin)

	want := []uint16{powerModeOn, powerModeStandby, powerModeOff}
	if got := panel.took(vcpPowerMode); !slices.Equal(got, want) {
		t.Errorf("the panel took %v, want %v", got, want)
	}
}

// This case is the whole reason the two power values are apart: a
// Deployment that replaces its pod ends one claim and makes another,
// and a screen that went dark between them would blink on every
// rollout.
func TestUnprepareLeavesAPanelTheClaimOnlyPoweredOn(t *testing.T) {
	panel := newFakeMonitor()
	plugin, _ := labPluginWithPanels(t, claimControl(`{"power": "on"}`), claimedPanel(panel))
	if claim := prepare(t, plugin); claim.Error != "" {
		t.Fatal(claim.Error)
	}

	unprepare(t, plugin)

	if got := panel.took(vcpPowerMode); len(got) != 1 || got[0] != powerModeOn {
		t.Errorf("the panel took %v, want the power-on and nothing after it", got)
	}
}

// The kubelet repeats an unprepare it has no answer for, so a second
// call must find the record already empty and put nothing on the
// wire.
func TestUnprepareStandsThePanelDownOnce(t *testing.T) {
	panel := newFakeMonitor()
	plugin, _ := labPluginWithPanels(t, claimControl(`{"power": "onWhileClaimed"}`), claimedPanel(panel))
	if claim := prepare(t, plugin); claim.Error != "" {
		t.Fatal(claim.Error)
	}

	unprepare(t, plugin)
	unprepare(t, plugin)

	if got := panel.took(vcpPowerMode); len(got) != 2 {
		t.Errorf("the panel took %v, want the power-on and one standby", got)
	}
}

// A panel that will not go down must not hold the claim open,
// because the kubelet repeats a failed unprepare with no end, and the
// pod would never finish terminating.
func TestUnprepareEndsTheClaimWhenThePanelWillNotAnswer(t *testing.T) {
	panel := newFakeMonitor()
	plugin, _ := labPluginWithPanels(t, claimControl(`{"power": "onWhileClaimed"}`), claimedPanel(panel))
	if claim := prepare(t, plugin); claim.Error != "" {
		t.Fatal(claim.Error)
	}
	panel.silent = true

	unprepare(t, plugin)

	if got := powerRecord(t, plugin); len(got) != 0 {
		t.Errorf("record = %v, want the entry gone whether the panel answered or not", got)
	}
	if got := specFiles(t); len(got) != 0 {
		t.Errorf("unprepare left %v behind", got)
	}
}

// The slice is what a selector reads, and the attribute is present
// and true or absent, never present and false.
func TestSliceDevicesPublishesTheControlsAPanelCarries(t *testing.T) {
	cases := []struct {
		name      string
		controls  supportedControls
		attribute string
		published bool
	}{
		{
			name:      "the brightness of a panel that carries it",
			controls:  supportedControls{Brightness: true, Power: true},
			attribute: "controlsBrightness",
			published: true,
		},
		{
			name:      "the power of a panel that carries it",
			controls:  supportedControls{Brightness: true, Power: true},
			attribute: "controlsPower",
			published: true,
		},
		{
			name:      "the power of a panel that carries only the brightness",
			controls:  supportedControls{Brightness: true},
			attribute: "controlsPower",
		},
		{
			name:      "the brightness of a panel that answers no DDC/CI",
			controls:  supportedControls{},
			attribute: "controlsBrightness",
		},
		{
			name:      "the power of a panel that answers no DDC/CI",
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

// A dark connector publishes no control attribute for the same
// reason it publishes no EDID fact: the panel that answered is gone.
func TestSliceDevicesPublishesNoControlOfADarkConnector(t *testing.T) {
	devices := sliceDevices([]Output{{
		Connector: "DP-1",
		Controls:  supportedControls{Brightness: true, Power: true},
	}})

	for _, name := range []string{"controlsBrightness", "controlsPower"} {
		if _, published := devices[0].Attributes[name]; published {
			t.Errorf("%s publishes on a connector with nothing on it", name)
		}
	}
}
