package main

// This file applies a claim's brightness and power parameters to a
// monitor, and reports which of those controls the monitor supports.
//
// Only the panel can dim its backlight or power itself down, so the
// operator sends these settings to the panel over DDC/CI on the
// connector's I2C bus (ddc.go), not to the graphics card. Panels on one
// card can differ: one supports these codes and another refuses DDC/CI.
// The operator publishes each panel's supported controls as device
// attributes, and a claim that sets a control the panel does not
// support fails at prepare. panels.go caches the probe results by
// connector and monitor.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/liken-sh/liken/kubernetes/events"
)

// The VCP codes for brightness and power mode. The MCCS specification
// names 0x10 Luminance. ddcutil, monitor menus, and the claim parameter
// call it brightness.
const (
	vcpBrightness = 0x10
	vcpPowerMode  = 0xd6
)

// The values of the MCCS power mode code 0xD6 (VESA MCCS 2.2a, table
// 8-9). A panel supports only some of these values. 0x05 is an off
// value that a panel accepts only as a write.
const (
	powerModeOn      = 0x01
	powerModeStandby = 0x02
	powerModeSuspend = 0x03
	powerModeOff     = 0x04
	powerModeHardOff = 0x05
)

// The claim parameter keys that this file reads, besides modeParameter.
// A brightness is a percentage of the panel's own maximum, because the
// raw scale differs between models.
const (
	brightnessParameter = "brightness"
	powerParameter      = "power"
)

// The values of the power parameter. Both power the panel on at
// prepare. onWhileClaimed also puts the panel in standby
// powerReleaseGrace after the claim ends. A claim with no power value
// does not change the panel's power.
const (
	powerOn             = "on"
	powerOnWhileClaimed = "onWhileClaimed"
)

// A claim can spell each power value in lowercase or in PascalCase. The
// parser maps both to the lowercase value, so the rest of the code and
// the power record compare one value.
var powerSpellings = map[string]string{
	powerOn:             powerOn,
	"On":                powerOn,
	powerOnWhileClaimed: powerOnWhileClaimed,
	"OnWhileClaimed":    powerOnWhileClaimed,
}

// The power record's value for a panel whose onWhileClaimed claim has
// ended and whose standby waits for powerReleaseGrace. No claim uses
// this value.
const powerReleased = "released"

// How long a panel stays on after its onWhileClaimed claim ends. A
// Deployment that replaces its pod ends one claim and prepares the next
// one on the same connector a few seconds later, and an immediate
// standby would make the screen blink in between. A prepare on the
// connector within this time cancels the standby.
const powerReleaseGrace = 30 * time.Second

// Every parameter key that this driver accepts. The mode parser
// (modes.go) and controlParameters read different keys from the same
// opaque block, so each needs the whole list to tell a typo from a key
// that the other parser reads.
var claimParameterNames = []string{modeParameter, brightnessParameter, powerParameter}

// The file that records which panels go to standby when their claim
// ends. It is in the pod's emptyDir volume with the mode record, so it
// survives a restart of the operator container. A new pod starts with
// an empty record, so it never powers down a panel for a claim that it
// did not prepare.
var powerRecordPath = "/etc/weston/power.json"

// unknownParameter is the error that both parsers return for a key
// outside claimParameterNames. If a parser skipped a typo, the claim
// would prepare without that setting, and its author would find out
// only when the screen looked wrong.
func unknownParameter(key string) error {
	return fmt.Errorf("the claim's %s parameters have the key %q, and this driver reads only %s",
		DriverName, key, strings.Join(claimParameterNames, ", "))
}

// choice is one config entry's value for a parameter, and whether the
// entry came from the claim or from the class. It is a generic
// modeChoice, because every parameter follows the same precedence.
type choice[T comparable] struct {
	Value     T
	FromClaim bool
}

// selection holds one parameter's values across a claim's config: a
// value for each request that a block names, and the value of a block
// that names no request, which applies to every request. The zero value
// of T means that no value was set, which is why brightness is a struct
// with a Set flag.
type selection[T comparable] struct {
	requests map[string]choice[T]
	every    choice[T]
}

// set records one block's value for one request, or for every request
// when request is empty. A later entry from the same source overwrites
// an earlier one, and an entry from the class never overwrites one from
// the claim, whatever the order of the config list.
func (s *selection[T]) set(request string, value choice[T]) {
	var unset T
	current := s.every
	if request != "" {
		current = s.requests[request]
	}
	if current.Value != unset && current.FromClaim && !value.FromClaim {
		return
	}
	if request == "" {
		s.every = value
		return
	}
	if s.requests == nil {
		s.requests = map[string]choice[T]{}
	}
	s.requests[request] = value
}

// setAll records one block's value under each request it names, or
// for every request when the block names none.
func (s *selection[T]) setAll(requests []string, value choice[T]) {
	if len(requests) == 0 {
		s.set("", value)
		return
	}
	for _, request := range requests {
		s.set(request, value)
	}
}

// forRequest returns the value for one request. A value from the claim
// beats one from the class, and within one source a block that names
// the request beats a block that names none.
func (s selection[T]) forRequest(request string) T {
	var unset T
	named := s.requests[request]
	if named.Value == unset {
		return s.every.Value
	}
	if s.every.Value != unset && s.every.FromClaim && !named.FromClaim {
		return s.every.Value
	}
	return named.Value
}

// brightness has a Set flag because 0 percent is a valid brightness,
// so the number alone cannot tell whether the claim set one.
type brightness struct {
	Percent int
	Set     bool
}

// requestedControls is the brightness and power that one request sets
// on its panel. modes.go resolves the request's display mode.
type requestedControls struct {
	Brightness brightness
	Power      string
}

// Brightness and power resolve separately, so a claim can set a
// brightness for one request and a power for all requests without
// either overriding the other.
type controlSelection struct {
	brightness selection[brightness]
	power      selection[string]
}

func (s controlSelection) forRequest(request string) requestedControls {
	return requestedControls{
		Brightness: s.brightness.forRequest(request),
		Power:      s.power.forRequest(request),
	}
}

// claimControls returns the brightness and power parameters of the
// resolved config that claimModes also reads. It skips other drivers'
// blocks without checking their keys.
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
		if stated.Brightness.Set {
			controls.brightness.setAll(entry.Requests, choice[brightness]{
				Value: stated.Brightness, FromClaim: fromClaim,
			})
		}
		if stated.Power != "" {
			controls.power.setAll(entry.Requests, choice[string]{
				Value: stated.Power, FromClaim: fromClaim,
			})
		}
	}
	return controls, nil
}

// controlParameters parses one opaque block. It rejects a key outside
// claimParameterNames, a brightness that is not a whole number from 0
// to 100, and a power value outside powerSpellings. The check is here
// and not at the I2C write because the scheduler copies an opaque block
// without reading it, so this parse is the only validation.
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
					"the claim's %s parameter is %d, and it must be a percentage from 0 to 100",
					brightnessParameter, percent)
			}
			stated.Brightness = brightness{Percent: percent, Set: true}
		case powerParameter:
			power := ""
			if err := json.Unmarshal(value, &power); err != nil {
				return requestedControls{}, fmt.Errorf(
					"the claim's %s parameter is not a string: %s", powerParameter, value)
			}
			value, known := powerSpellings[power]
			if !known {
				return requestedControls{}, fmt.Errorf(
					"the claim's %s parameter is %q, and it must be %q or %q, in capitals as written or in lowercase",
					powerParameter, power, "On", "OnWhileClaimed")
			}
			stated.Power = value
		}
	}
	return stated, nil
}

// supportedControls reports whether the panel on a connector supports
// each control. A control is false both when the panel does not respond
// to DDC/CI and when it reports the code as unsupported, because the
// operator can set neither.
type supportedControls struct {
	Brightness bool
	Power      bool
}

// controlBus is one open i2c-dev node, closed after each use. The
// operator's pod gets the card's i2c-dev nodes from the wires request
// of its claim template, which claims a display-i2c device, because
// liken publishes the buses as a device separate from the card.
type controlBus interface {
	i2cBus
	Close() error
}

// panelControls probes and sets the controls of one card's panels. The
// slice publisher and the prepare path share one instance, so they
// share the probe cache. open, sleep, and now are fields so that a test
// can replace the bus, the protocol's delays, and the clock.
type panelControls struct {
	mu      sync.Mutex
	sysRoot string
	card    string
	open    func(path string) (controlBus, error)
	sleep   func(time.Duration)
	probed  map[string]probedPanel
	// The probes that are running, by connector. A caller that misses
	// the cache while another caller probes the same connector waits
	// for that probe and uses its result, so the panel is probed once.
	probing map[string]chan struct{}
	// The access lock of each bus, by node path (busturn.go).
	turnsMu sync.Mutex
	turns   map[string]*busTurn
	now     func() time.Time
}

func (c *panelControls) clock() time.Time {
	if c.now == nil {
		return time.Now()
	}
	return c.now()
}

// probedPanel is one cache entry: the probe result and the EDID of the
// monitor it was read from. A different EDID on the connector means a
// different monitor, and the result does not apply to it.
//
// facts is a pointer because later reads and writes of the panel record
// their values in it, and the Display's observed values come from it.
type probedPanel struct {
	monitor EDID
	facts   *panelFacts
	// When the probe ran. probeAgain retries a refused probe
	// probeRetryInterval after it.
	probed time.Time
	// When pollControls last read the panel's values, to find a value
	// that a person changed with the panel's buttons. It is separate
	// from probed because it runs on the shorter pollInterval.
	polled time.Time
}

func newPanelControls(sysRoot, card string) *panelControls {
	return &panelControls{
		sysRoot: sysRoot,
		card:    card,
		open:    func(path string) (controlBus, error) { return openI2C(path) },
		probed:  map[string]probedPanel{},
	}
}

// of returns the controls that the slice publishes for an output:
// brightness and power, the controls that a claim parameter can set.
func (c *panelControls) of(output Output) supportedControls {
	return c.factsFor(output).controls()
}

// withControls adds each panel's supported controls to the outputs read
// from sysfs. It is separate from withCurrentModes because the two
// reads fail independently: a card whose mode ioctl fails can still
// have panels that respond to DDC/CI, and the reverse.
func withControls(outputs []Output, controls *panelControls) []Output {
	out := make([]Output, len(outputs))
	for i, output := range outputs {
		out[i] = output
		out[i].Controls = controls.of(output)
	}
	return out
}

// busFor waits until no other caller has the connector's I2C bus open
// (busturn.go), then opens it. Closing the returned bus releases it to
// the next caller.
func (c *panelControls) busFor(connector string) (controlBus, error) {
	path := connectorBus(c.sysRoot, c.card, connector)
	if path == "" {
		return nil, fmt.Errorf("%s has no DDC/CI bus", connector)
	}
	turn := c.turn(path)
	turn.take(c.clock, c.pause)
	bus, err := c.open(path)
	if err != nil {
		turn.give(c.clock())
		return nil, fmt.Errorf("opening %s for %s: %w", path, connector, err)
	}
	return &turnBus{controlBus: bus, turn: turn, clock: c.clock}, nil
}

func (c *panelControls) client(bus controlBus) *DDC {
	ddc := newDDC(bus)
	if c.sleep != nil {
		ddc.sleep = c.sleep
	}
	return ddc
}

// set applies one request's brightness and power. It powers the panel
// on first, because a panel in standby must be awake before it accepts
// a brightness. Each control is read before it is written, and set
// writes nothing when the panel already has the value, so a new claim
// on a panel that is already on and at that brightness changes nothing
// on screen.
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
	if want.Brightness.Set {
		if err := setBrightness(ddc, connector, want.Brightness.Percent); err != nil {
			return err
		}
	}
	return nil
}

// setBrightness converts the claim's percentage to the panel's scale
// and writes it. It reads the panel's maximum first, because the scale
// differs between panels, for example 100 or 255. The same read returns
// the current value, so a panel that already has the value gets no
// write. After the write it reads the value back, because a display
// acknowledges a write whether or not it accepts the value.
func setBrightness(ddc *DDC, connector string, percent int) error {
	held, max, err := ddc.GetVCP(vcpBrightness)
	if err != nil {
		return fmt.Errorf("reading the brightness range of %s: %w", connector, err)
	}
	if max == 0 {
		return fmt.Errorf("%s reports a maximum brightness of 0, so %d%% has no value on its scale", connector, percent)
	}
	value := brightnessValue(percent, max)
	if held == value {
		return nil
	}
	announceWrite(connector, vcpBrightness, held, true, value)
	if err := ddc.SetVCP(vcpBrightness, value); err != nil {
		return fmt.Errorf("setting the brightness of %s: %w", connector, err)
	}
	current, _, err := ddc.GetVCP(vcpBrightness)
	if err != nil {
		return fmt.Errorf("reading back the brightness of %s: %w", connector, err)
	}
	if current != value {
		return fmt.Errorf("%s reports a brightness of %d after the write of %d, which is %d%% of %d",
			connector, current, value, percent, max)
	}
	return nil
}

// brightnessValue rounds to the nearest step of the panel's scale, so
// 35 percent of a maximum of 10 is 4, not 3.
func brightnessValue(percent int, max uint16) uint16 {
	return uint16((percent*int(max) + 50) / 100)
}

// setPower writes the mode when the panel is in another mode, and reads
// it back, as setBrightness does. A readback that differs means that
// the panel supports the power code but not this value, which MCCS
// allows.
//
// When the first read fails, setPower writes nothing, because the
// operator writes only to a panel that responds, and a panel in standby
// still responds to DDC/CI. The prepare fails with the read's error, and
// the kubelet's retry of the prepare reads again.
func setPower(ddc *DDC, connector string, mode uint16) error {
	held, _, err := ddc.GetVCP(vcpPowerMode)
	if err != nil {
		return fmt.Errorf("reading the power mode of %s: %w", connector, err)
	}
	if held == mode {
		return nil
	}
	announceWrite(connector, vcpPowerMode, held, true, mode)
	if err := ddc.SetVCP(vcpPowerMode, mode); err != nil {
		return fmt.Errorf("setting the power mode of %s: %w", connector, err)
	}
	current, _, err := ddc.GetVCP(vcpPowerMode)
	if err != nil {
		return fmt.Errorf("reading back the power mode of %s: %w", connector, err)
	}
	if current != mode {
		return fmt.Errorf("%s reports the power mode %#02x after the write of %#02x", connector, current, mode)
	}
	return nil
}

// standby powers a panel down when its claim ends. It writes nothing
// when the panel is not on, and returns the error when the first read
// fails.
//
// It writes standby first, because standby is the shallower power-down
// state, and reads the mode back. Some panels do not support standby
// and stay on, and then it writes off. It does not read after the off
// write, and it treats a failed read after the standby write as
// success, because a panel can stop responding as it powers down.
func (c *panelControls) standby(connector string) error {
	bus, err := c.busFor(connector)
	if err != nil {
		return err
	}
	defer bus.Close()

	ddc := c.client(bus)
	held, _, err := ddc.GetVCP(vcpPowerMode)
	if err != nil {
		return fmt.Errorf("reading the power mode of %s: %w", connector, err)
	}
	if held != powerModeOn {
		return nil
	}
	announceWrite(connector, vcpPowerMode, held, true, powerModeStandby)
	if err := ddc.SetVCP(vcpPowerMode, powerModeStandby); err != nil {
		return err
	}
	current, _, err := ddc.GetVCP(vcpPowerMode)
	if err != nil || current == powerModeStandby {
		return nil
	}
	announceWrite(connector, vcpPowerMode, current, true, powerModeOff)
	return ddc.SetVCP(vcpPowerMode, powerModeOff)
}

// applyControls sets the claim's controls on the panel at prepare. It
// fails a claim that sets a control the panel does not support. The
// scheduler does not read opaque parameters, so only a selector on the
// published attribute keeps such a claim off the device, and the manual
// tells a person to write one. A claim that sets neither control opens
// no bus.
func (p *draPlugin) applyControls(output Output, want requestedControls) error {
	// The new claim keeps the panel on, so cancel any standby that is
	// waiting for the end of an earlier claim, whatever this claim sets.
	if err := p.cancelRelease(output.Connector); err != nil {
		return err
	}
	if want.Power == "" && !want.Brightness.Set {
		return nil
	}
	carried := p.controls.of(output)
	if want.Brightness.Set && !carried.Brightness {
		return fmt.Errorf("the claim sets a brightness, and %s does not support the brightness control over DDC/CI,"+
			" so its device has no controlsBrightness attribute", output.Connector)
	}
	if want.Power != "" && !carried.Power {
		return fmt.Errorf("the claim sets power %q, and %s does not support the power control over DDC/CI,"+
			" so its device has no controlsPower attribute", want.Power, output.Connector)
	}
	if err := p.controls.set(output.Connector, want); err != nil {
		return err
	}
	return p.recordPower(output.Connector, want.Power)
}

// recordPower records a connector whose claim set onWhileClaimed, and
// removes the connector when the new claim did not. It writes the file
// only when the record changes, because every prepare calls it and
// most claims set no power.
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

// releasePower runs at unprepare. It marks each of these devices that
// the record lists as onWhileClaimed as released, and schedules its
// standby after powerReleaseGrace. The mark is in the file, so an
// operator container that restarts during the grace period starts it
// again (resumeReleases).
//
// A failure goes to stderr and does not fail the unprepare, because the
// kubelet repeats a failed unprepare, and a panel that cannot be
// written would keep the claim from finishing.
func (p *draPlugin) releasePower(devices []string) {
	p.powerRecords.Lock()
	defer p.powerRecords.Unlock()

	record, err := readPowerRecord(p.powerPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "reading the power record: %v\n", err)
		return
	}
	var released []string
	for connector, power := range record {
		if power != powerOnWhileClaimed || !slices.Contains(devices, deviceName(connector)) {
			continue
		}
		record[connector] = powerReleased
		released = append(released, connector)
	}
	if len(released) == 0 {
		return
	}
	if err := writePowerRecord(p.powerPath, record); err != nil {
		fmt.Fprintf(os.Stderr, "writing the power record: %v\n", err)
		return
	}
	for _, connector := range released {
		p.scheduleStandby(connector)
	}
}

// resumeReleases runs at startup and schedules the standby again for
// each panel that the record marks as released, because the previous
// operator container exited before that standby ran.
func (p *draPlugin) resumeReleases() {
	p.powerRecords.Lock()
	defer p.powerRecords.Unlock()

	record, err := readPowerRecord(p.powerPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "reading the power record: %v\n", err)
		return
	}
	for connector, power := range record {
		if power == powerReleased {
			p.scheduleStandby(connector)
		}
	}
}

// scheduleStandby runs standbyReleased for one connector after the
// grace period. A prepare during that period cancels the standby by
// removing the mark from the record, so the timer itself is never
// stopped.
func (p *draPlugin) scheduleStandby(connector string) {
	grace := p.releaseGrace
	after := p.afterGrace
	if after == nil {
		after = func(delay time.Duration, run func()) { time.AfterFunc(delay, run) }
	}
	after(grace, func() { p.standbyReleased(connector) })
}

// standbyReleased puts a panel in standby if its released mark is still
// in the record. It removes the mark before it tries, so a panel that
// cannot power down is tried once and not again at every restart. A
// failure posts a warning Event.
func (p *draPlugin) standbyReleased(connector string) {
	p.powerRecords.Lock()
	defer p.powerRecords.Unlock()

	record, err := readPowerRecord(p.powerPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "reading the power record: %v\n", err)
		return
	}
	if record[connector] != powerReleased {
		return
	}
	delete(record, connector)
	if err := writePowerRecord(p.powerPath, record); err != nil {
		fmt.Fprintf(os.Stderr, "writing the power record: %v\n", err)
		return
	}
	if err := p.controls.standby(connector); err != nil {
		fmt.Fprintf(os.Stderr, "putting %s to standby: %v\n", connector, err)
		p.notices.post(connector, events.TypeWarning, PanelStandbyFailedReason,
			fmt.Sprintf("The panel on %s stays on, because it did not enter standby after its last claim ended: %v", connector, err))
	}
}

// cancelRelease removes a connector's released mark, so the pending
// standbyReleased finds no mark and writes nothing.
func (p *draPlugin) cancelRelease(connector string) error {
	p.powerRecords.Lock()
	defer p.powerRecords.Unlock()

	record, err := readPowerRecord(p.powerPath)
	if err != nil {
		return err
	}
	if record[connector] != powerReleased {
		return nil
	}
	delete(record, connector)
	return writePowerRecord(p.powerPath, record)
}

// readPowerRecord returns an empty record when the file does not exist,
// because the pod has then recorded no panel. Invalid JSON is an error,
// because only this operator writes the file and a parse failure means
// that the file is damaged.
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
		return nil, fmt.Errorf("parsing the power record %s: %w", path, err)
	}
	return record, nil
}

// writePowerRecord writes a temporary file and renames it over the
// record, so a container that exits mid-write leaves the old record
// whole for the next container to read.
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
