package main

// A monitor's own controls: what a claim's brightness and power
// parameters become on the wire.
//
// Brightness and power live in the panel, not in the graphics card.
// The card can blank its signal, but only the panel can dim its
// backlight or shut itself down, and DDC/CI over the connector's i2c
// wire is the one channel a host has into those settings (ddc.go
// speaks the protocol). This file turns a claim's parameters into
// those messages: it probes each panel for what it carries, publishes
// the answers as device attributes, and sets what a claim states,
// with a readback after every write.
//
// The probe is cached because discoverOutputs runs on every prepare
// and every slice publish, and a panel that carries no DDC/CI costs
// three 40ms attempts per code before it answers nothing. The cache
// key is the connector plus the monitor's own EDID, so a different
// monitor on the same wire is probed again and the same monitor never
// is.
//
// Capability is per panel, and the lab measured it: of the two
// monitors on the same card, one answers these codes and the other
// refuses the protocol outright. Every path here treats a refusal as
// a fact to publish, and a panel that answers nothing gets no
// attribute, no parameter, and no traffic.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"
)

// The two VCP codes this operator reads and writes. The MCCS
// specification calls 0x10 Luminance and 0xD6 Power mode; ddcutil and
// every monitor's menu call the first one brightness, and so does the
// claim parameter.
const (
	vcpBrightness = 0x10
	vcpPowerMode  = 0xd6
)

// The power mode's values are the DPM and DPMS states written as one
// byte: 0x01 on, 0x02 standby, 0x03 suspend, 0x04 off, 0x05 a
// write-only off (VESA MCCS 2.2a, table 8-9). A display implements
// the subset of a non-continuous code that it chooses, and the
// subsets differ on real panels: the lab's BOE panel lists 01, 04,
// and 05 in its capability string and refuses standby outright. So a
// power-down writes standby first, reads the mode back, and writes
// off when the panel kept running. Standby is the gentler state, and
// off is the one this common subset carries.
const (
	powerModeOn      = 0x01
	powerModeStandby = 0x02
	powerModeOff     = 0x04
)

// The two keys this driver reads beside mode. A brightness is a
// percentage of the panel's own maximum, because the raw scale is the
// panel's and differs between models. A power states what the claim
// asks of the panel's power, in the vocabulary below.
const (
	brightnessParameter = "brightness"
	powerParameter      = "power"
)

// The two values the power parameter takes. Both power the panel on
// at prepare. Only onWhileClaimed powers it back down when the claim
// ends. The two exist apart because a Deployment that replaces its
// pod ends one claim and makes another, and an unconditional
// power-off at claim end would blink every screen on every rollout.
// A claim that states neither leaves the panel's power alone in both
// directions.
const (
	powerOn             = "on"
	powerOnWhileClaimed = "onWhileClaimed"
)

// Every parameter this driver reads. Two parsers walk the same opaque
// block, each taking the keys it acts on and skipping the others, so
// this list is what both use to tell a typo from a key the other
// parser reads. A key outside the list fails the claim from either
// side.
var claimParameterNames = []string{modeParameter, brightnessParameter, powerParameter}

// The record of the panels a claim must put back to standby. It
// shares the mode record's volume for the mode record's reason: the
// file outlives a restart of the operator's container and dies with
// the pod, so a fresh pod that prepared no such claim owes no panel a
// power-down.
var powerRecordPath = "/etc/weston/power.json"

// The failure both parsers report for a key this driver does not
// read. An unknown key fails the claim rather than being skipped,
// because a skipped typo would prepare the claim with the parameter
// silently unapplied, and the person who wrote it would learn nothing
// until the screen looked wrong.
func unknownParameter(key string) error {
	return fmt.Errorf("the claim's %s parameters name %q, and this driver reads %s",
		DriverName, key, strings.Join(claimParameterNames, ", "))
}

// One config entry's statement of a parameter, and where it came
// from. This is modeChoice's shape over any value type, because the
// precedence rule is the same for every parameter and only the
// value's type changes.
type choice[T comparable] struct {
	Value     T
	FromClaim bool
}

// One parameter's resolved statements across a claim: what each block
// that names its requests stated, and what a block with no requests
// stated, which applies to every request in the claim. A value's zero
// is how this type says "nothing was stated", which is why brightness
// wraps its number in a struct with a flag.
type selection[T comparable] struct {
	requests map[string]choice[T]
	every    choice[T]
}

// State records one block's statement under modeSelection.state's
// rule: a later entry of the same source overwrites an earlier one,
// and an entry from the class never overwrites one from the claim,
// whatever order the resolved list carries them in.
func (s *selection[T]) state(request string, stated choice[T]) {
	var unstated T
	current := s.every
	if request != "" {
		current = s.requests[request]
	}
	if current.Value != unstated && current.FromClaim && !stated.FromClaim {
		return
	}
	if request == "" {
		s.every = stated
		return
	}
	if s.requests == nil {
		s.requests = map[string]choice[T]{}
	}
	s.requests[request] = stated
}

// StateAll records one block under every request it names. A block
// that names none applies to every request in the claim, which is
// what the empty request string stands for in the map.
func (s *selection[T]) stateAll(requests []string, stated choice[T]) {
	if len(requests) == 0 {
		s.state("", stated)
		return
	}
	for _, request := range requests {
		s.state(request, stated)
	}
}

// ForRequest resolves what one request gets, by two rules in order:
// the claim's own choice beats the class's, and within one source a
// block that names the request beats a block that names none.
func (s selection[T]) forRequest(request string) T {
	var unstated T
	named := s.requests[request]
	if named.Value == unstated {
		return s.every.Value
	}
	if s.every.Value != unstated && s.every.FromClaim && !named.FromClaim {
		return s.every.Value
	}
	return named.Value
}

// A brightness carries a flag beside its number because zero percent
// is a brightness a claim can ask for, so the number alone cannot say
// whether the claim stated one.
type brightness struct {
	Percent int
	Stated  bool
}

// RequestedControls is what one request asks of the panel itself. The
// mode is the request's ask of the card and the compositor, and it
// resolves separately in modes.go.
type requestedControls struct {
	Brightness brightness
	Power      string
}

// Each parameter resolves on its own, because a claim can state a
// brightness for one request and a power for all of them, and the two
// must not shadow each other.
type controlSelection struct {
	brightness selection[brightness]
	power      selection[string]
}

// ForRequest answers one allocation result with both controls
// resolved.
func (s controlSelection) forRequest(request string) requestedControls {
	return requestedControls{
		Brightness: s.brightness.forRequest(request),
		Power:      s.power.forRequest(request),
	}
}

// ClaimControls reads the same resolved config claimModes reads and
// takes the control parameters out of it. Another driver's block is
// not this driver's to judge, so it is skipped, keys and all.
func claimControls(config []AllocatedConfig) (controlSelection, error) {
	controls := controlSelection{}
	for _, entry := range config {
		if entry.Opaque == nil || entry.Opaque.Driver != DriverName {
			continue
		}
		stated, err := controlParameters(entry.Opaque.Parameters)
		if err != nil {
			return controlSelection{}, err
		}
		fromClaim := entry.Source == configFromClaim
		if stated.Brightness.Stated {
			controls.brightness.stateAll(entry.Requests, choice[brightness]{
				Value: stated.Brightness, FromClaim: fromClaim,
			})
		}
		if stated.Power != "" {
			controls.power.stateAll(entry.Requests, choice[string]{
				Value: stated.Power, FromClaim: fromClaim,
			})
		}
	}
	return controls, nil
}

// ControlParameters reads one opaque block and refuses what no panel
// can take: a brightness outside 0 to 100 or not a whole number, and
// a power outside the two-word vocabulary. The refusal is here rather
// than at the wire because the scheduler copies an opaque block
// through unread, so this parse is the first and only code that
// judges it.
func controlParameters(raw json.RawMessage) (requestedControls, error) {
	stated := requestedControls{}
	if len(raw) == 0 {
		return stated, nil
	}
	var parameters map[string]json.RawMessage
	if err := json.Unmarshal(raw, &parameters); err != nil {
		return stated, fmt.Errorf("the claim's %s parameters are not an object: %w", DriverName, err)
	}
	for key, value := range parameters {
		if !slices.Contains(claimParameterNames, key) {
			return requestedControls{}, unknownParameter(key)
		}
		switch key {
		case brightnessParameter:
			percent := 0
			if err := json.Unmarshal(value, &percent); err != nil {
				return requestedControls{}, fmt.Errorf(
					"the claim's %s parameter is not a whole number: %s", brightnessParameter, value)
			}
			if percent < 0 || percent > 100 {
				return requestedControls{}, fmt.Errorf(
					"the claim's %s parameter is %d, and a brightness is a percentage from 0 to 100",
					brightnessParameter, percent)
			}
			stated.Brightness = brightness{Percent: percent, Stated: true}
		case powerParameter:
			power := ""
			if err := json.Unmarshal(value, &power); err != nil {
				return requestedControls{}, fmt.Errorf(
					"the claim's %s parameter is not a string: %s", powerParameter, value)
			}
			if power != powerOn && power != powerOnWhileClaimed {
				return requestedControls{}, fmt.Errorf("the claim's %s parameter is %q, and this driver takes %q or %q",
					powerParameter, power, powerOn, powerOnWhileClaimed)
			}
			stated.Power = power
		}
	}
	return stated, nil
}

// SupportedControls is the probe's answer: whether the panel on this
// connector carries each control. A panel that answered nothing and a
// panel that named the code unsupported both read false, because
// neither one can be set.
type supportedControls struct {
	Brightness bool
	Power      bool
}

// A controlBus is one i2c-dev node, opened around a single exchange
// and closed after it. The nodes arrive in the operator's own pod
// with its claim on the card's i2c companion device, the wires
// request in the claim template. liken publishes those wires apart
// from the card node, exclusively, because raw wire access has one
// writer, and this operator is that writer.
type controlBus interface {
	i2cBus
	Close() error
}

// A panelControls answers for one card's panels: the sysfs tree that
// maps a connector to its bus, the seam that opens the bus, and the
// probe cache. One instance serves the slice publisher and the
// prepare path, which is what makes the cache one cache. The sleep is
// a field for the same reason DDC's is: a test runs the protocol
// without waiting out its timing.
type panelControls struct {
	mu      sync.Mutex
	sysRoot string
	card    string
	open    func(path string) (controlBus, error)
	sleep   func(time.Duration)
	probed  map[string]probedPanel
	// The clock the retry window is measured on, a field for
	// the reason sleep is one.
	now func() time.Time
}

// The clock a cache with no clock wired reads.
func (c *panelControls) clock() time.Time {
	if c.now == nil {
		return time.Now()
	}
	return c.now()
}

// One cache entry holds the answer and the monitor it answered for.
// A connector whose EDID reads differently now carries a different
// panel, and this answer is not that panel's.
//
// The entry holds a pointer because the operator's later reads
// and writes of the panel record their values in it, and the entry is
// what the Display's observed values are built from.
type probedPanel struct {
	monitor EDID
	facts   *panelFacts
	// When the probe ran, which is what the retry window of a
	// refusal is measured from.
	asked time.Time
	// when the operator last read the carried controls of a
	// panel that answers. It is its own timestamp because it measures
	// its own window: the refusal is asked again to find a panel that
	// started answering, and this one finds a value a person changed
	// at the panel.
	polled time.Time
}

// NewPanelControls wires the real i2c-dev nodes. A test supplies its
// own open function instead.
func newPanelControls(sysRoot, card string) *panelControls {
	return &panelControls{
		sysRoot: sysRoot,
		card:    card,
		open:    func(path string) (controlBus, error) { return openI2C(path) },
		probed:  map[string]probedPanel{},
	}
}

// Of answers the two booleans the slice publishes, out of the
// panel facts the probe read. The facts carry the whole capability
// list, and the slice publishes the two controls a claim parameter
// states.
func (c *panelControls) of(output Output) supportedControls {
	return c.factsFor(output).controls()
}

// WithControls puts each panel's answer beside what sysfs said, the
// way withCurrentModes puts the card's readback there. The two reads
// answer different questions and fail apart: a card that cannot
// answer the mode ioctl still has panels that answer DDC/CI, and the
// other way around.
func withControls(outputs []Output, controls *panelControls) []Output {
	out := make([]Output, len(outputs))
	for i, output := range outputs {
		out[i] = output
		out[i].Controls = controls.of(output)
	}
	return out
}

// BusFor opens the connector's channel. No channel is a failure to a
// caller that must set a control, and no failure at all to the probe,
// which reports it and publishes nothing.
func (c *panelControls) busFor(connector string) (controlBus, error) {
	path := connectorBus(c.sysRoot, c.card, connector)
	if path == "" {
		return nil, fmt.Errorf("%s has no DDC/CI channel", connector)
	}
	bus, err := c.open(path)
	if err != nil {
		return nil, fmt.Errorf("opening %s for %s: %w", path, connector, err)
	}
	return bus, nil
}

// Client builds the protocol speaker for one open bus. The sleep the
// tests replace is the protocol's own timing and nothing else.
func (c *panelControls) client(bus controlBus) *DDC {
	ddc := newDDC(bus)
	if c.sleep != nil {
		ddc.sleep = c.sleep
	}
	return ddc
}

// Set applies what one request stated, both controls on one open bus.
// Power goes first, because a panel in standby must be awake before a
// brightness can land on it.
func (c *panelControls) set(connector string, want requestedControls) error {
	bus, err := c.busFor(connector)
	if err != nil {
		return err
	}
	defer bus.Close()

	ddc := c.client(bus)
	if want.Power != "" {
		if err := setPower(ddc, connector, powerModeOn); err != nil {
			return err
		}
	}
	if want.Brightness.Stated {
		if err := setBrightness(ddc, connector, want.Brightness.Percent); err != nil {
			return err
		}
	}
	return nil
}

// SetBrightness turns the claim's percentage into the panel's own
// scale and writes it. The claim states a percentage because the raw
// scale is the panel's: one panel counts to 100, another to 255, and
// a Set above the maximum is refused or clamped, so the maximum has
// to be read first.
//
// The readback is what proves the control moved. A display
// acknowledges the write on the wire whether it takes the value or
// not, so a Set with no Get after it proves nothing.
func setBrightness(ddc *DDC, connector string, percent int) error {
	_, max, err := ddc.GetVCP(vcpBrightness)
	if err != nil {
		return fmt.Errorf("reading the brightness range of %s: %w", connector, err)
	}
	if max == 0 {
		return fmt.Errorf("%s reports a brightness range of zero, so %d%% names no value", connector, percent)
	}
	value := brightnessValue(percent, max)
	if err := ddc.SetVCP(vcpBrightness, value); err != nil {
		return fmt.Errorf("setting the brightness of %s: %w", connector, err)
	}
	current, _, err := ddc.GetVCP(vcpBrightness)
	if err != nil {
		return fmt.Errorf("reading back the brightness of %s: %w", connector, err)
	}
	if current != value {
		return fmt.Errorf("%s holds a brightness of %d after the claim set %d, which is %d%% of %d",
			connector, current, value, percent, max)
	}
	return nil
}

// BrightnessValue rounds the percentage to the nearest step of the
// panel's own scale. A panel whose maximum is under a hundred steps
// lands on the closest step rather than truncating toward zero, so 35
// percent of a ten-step scale is 4, not 3.
func brightnessValue(percent int, max uint16) uint16 {
	return uint16((percent*int(max) + 50) / 100)
}

// SetPower writes one power mode and reads it back, like the
// brightness. A readback that disagrees means the panel carries the
// code and refuses this value, which the standard allows: a display
// implements the subset of a non-continuous code that it chooses.
func setPower(ddc *DDC, connector string, mode uint16) error {
	if err := ddc.SetVCP(vcpPowerMode, mode); err != nil {
		return fmt.Errorf("setting the power mode of %s: %w", connector, err)
	}
	current, _, err := ddc.GetVCP(vcpPowerMode)
	if err != nil {
		return fmt.Errorf("reading back the power mode of %s: %w", connector, err)
	}
	if current != mode {
		return fmt.Errorf("%s holds the power mode %#02x after the claim set %#02x", connector, current, mode)
	}
	return nil
}

// standby powers a panel down at the end of a claim, in two steps
// because the panels disagree about how. The write of standby comes
// first. Then a readback sorts the panels into three cases: a panel
// that stopped answering went dark and is done, a panel that reads
// back standby took the value, and a panel that reads back anything
// else refused it, which is what a panel whose 0xD6 subset omits
// standby does. That last panel gets off instead. The off write reads
// nothing back, because the panel is leaving the state where it
// answers, and a readback that failed would report a failure for a
// panel that did as it was told.
func (c *panelControls) standby(connector string) error {
	bus, err := c.busFor(connector)
	if err != nil {
		return err
	}
	defer bus.Close()

	ddc := c.client(bus)
	if err := ddc.SetVCP(vcpPowerMode, powerModeStandby); err != nil {
		return err
	}
	current, _, err := ddc.GetVCP(vcpPowerMode)
	if err != nil || current == powerModeStandby {
		return nil
	}
	return ddc.SetVCP(vcpPowerMode, powerModeOff)
}

// ApplyControls is the backstop for a claim that names a control the
// panel does not carry. The scheduler never reads an opaque block, so
// no selector can keep such a claim off this device on its own; the
// manual tells a person to select on the attribute the probe
// publishes, and this failure is what catches the claim that did not.
//
// A claim that states neither control returns before any of that, so
// a prepare on a panel that speaks no DDC/CI opens no bus and costs
// nothing.
func (p *draPlugin) applyControls(output Output, want requestedControls) error {
	if want.Power == "" && !want.Brightness.Stated {
		return nil
	}
	carried := p.controls.of(output)
	if want.Brightness.Stated && !carried.Brightness {
		return fmt.Errorf("the claim states a brightness, and %s answers no brightness control over DDC/CI,"+
			" so the device publishes no controlsBrightness attribute", output.Connector)
	}
	if want.Power != "" && !carried.Power {
		return fmt.Errorf("the claim states power %q, and %s answers no power control over DDC/CI,"+
			" so the device publishes no controlsPower attribute", want.Power, output.Connector)
	}
	if err := p.controls.set(output.Connector, want); err != nil {
		return err
	}
	return p.recordPower(output.Connector, want.Power)
}

// RecordPower writes down which panels a claim promised to put back.
// An operator container that restarts holds no memory of the claims
// it prepared, and the panel still owes a power-down when the claim
// ends, so the promise has to live in a file.
//
// The write happens on divergence only. Every prepare of every claim
// passes through here, most state nothing about power, and a record
// that never changes is not worth rewriting.
func (p *draPlugin) recordPower(connector, power string) error {
	p.powerRecords.Lock()
	defer p.powerRecords.Unlock()

	record, err := readPowerRecord(p.powerPath)
	if err != nil {
		return err
	}
	if record[connector] == power {
		return nil
	}
	if power == powerOnWhileClaimed {
		record[connector] = power
	} else {
		delete(record, connector)
	}
	return writePowerRecord(p.powerPath, record)
}

// ReleasePower powers down the panels this claim promised to put
// back. A failure on the wire is reported to stderr and not returned,
// because the kubelet repeats an unprepare it has no answer for, and
// a panel that will not go down would hold the claim open with no
// end. The entry leaves the record whether the panel answered or not,
// for the same reason: the retry would write the same value to the
// same silence.
func (p *draPlugin) releasePower(devices []string) {
	p.powerRecords.Lock()
	defer p.powerRecords.Unlock()

	record, err := readPowerRecord(p.powerPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "reading the power record: %v\n", err)
		return
	}
	released := false
	for connector := range record {
		if !slices.Contains(devices, deviceName(connector)) {
			continue
		}
		delete(record, connector)
		released = true
		if err := p.controls.standby(connector); err != nil {
			fmt.Fprintf(os.Stderr, "putting %s to standby: %v\n", connector, err)
		}
	}
	if !released {
		return
	}
	if err := writePowerRecord(p.powerPath, record); err != nil {
		fmt.Fprintf(os.Stderr, "writing the power record: %v\n", err)
	}
}

// ReadPowerRecord treats a file that is not there as an empty record,
// because a pod that prepared no such claim owes no panel anything. A
// file that will not parse is an error, because this operator is its
// only writer and garbage in it means something went wrong.
func readPowerRecord(path string) (map[string]string, error) {
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return map[string]string{}, nil
	}
	if err != nil {
		return nil, err
	}
	record := map[string]string{}
	if err := json.Unmarshal(raw, &record); err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}
	return record, nil
}

// WritePowerRecord replaces the file atomically. The operator's
// container can end between a truncate and a write, and the file that
// is left is the only thing an unprepare after the restart reads.
func writePowerRecord(path string, record map[string]string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	raw, err := json.Marshal(record)
	if err != nil {
		return err
	}
	temporary := path + ".tmp"
	if err := os.WriteFile(temporary, raw, 0o644); err != nil {
		return err
	}
	return os.Rename(temporary, path)
}
