package main

// What the operator holds about one panel: whether it answers DDC/CI
// at all, the controls it declares, and the last value the operator
// read or wrote for each control. The probe runs once per monitor,
// and every later value comes from an actuation, because a DDC read
// wakes some panels and a periodic read would light a dark screen.

import (
	"errors"
	"fmt"
	"os"
)

// What one probe answered. Responsive is the panel's answer to
// the protocol itself, apart from the controls it carries: a panel
// that names every code unsupported still answers.
type panelFacts struct {
	Responsive   bool
	Capabilities map[string]panelCapability
	Observed     map[byte]uint16
}

// The two booleans the slice publishes, out of the capability
// list.
func (f panelFacts) controls() supportedControls {
	_, brightness := f.Capabilities[brightnessControl]
	_, power := f.Capabilities[powerControl]
	return supportedControls{Brightness: brightness, Power: power}
}

// FactsFor answers what one output's panel carries, from the cache when the
// same monitor was probed before and from the wire otherwise. A nil
// panelControls answers no controls and opens nothing, which is what
// the tests that publish slices with no probe wired rely on.
//
// A connector with nothing on it is never probed. A dark connector
// can still hold the last EDID its driver read, and there is no panel
// behind it to answer.
func (c *panelControls) factsFor(output Output) panelFacts {
	if c == nil || !output.Connected {
		return panelFacts{}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if probed, known := c.probed[output.Connector]; known && probed.monitor == output.Monitor && !c.askAgain(probed) {
		return probed.facts.copy()
	}
	facts := c.probe(output.Connector)
	if c.probed == nil {
		c.probed = map[string]probedPanel{}
	}
	// The probe just read every carried control, so the poll's
	// window starts here too.
	asked := c.clock()
	c.probed[output.Connector] = probedPanel{
		monitor: output.Monitor, facts: &facts, asked: asked, polled: asked,
	}
	return facts.copy()
}

// how long the operator leaves a panel alone between reads of
// what it holds. One window per backstop tick: a person at the panel's
// buttons is found within a minute, and a burst of passes costs one
// read.
const pollInterval = backstopInterval

// Whether this connector's window has passed, and the stamp
// that opens the next one. The check and the stamp are one step under
// the lock, so two goroutines that pass at once read the wire once.
func (c *panelControls) pollDue(connector string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	probed, known := c.probed[connector]
	if !known || probed.facts == nil {
		return false
	}
	if c.clock().Before(probed.polled.Add(pollInterval)) {
		return false
	}
	probed.polled = c.clock()
	c.probed[connector] = probed
	return true
}

// The read itself: every carried core control on one open bus,
// with each answer recorded the way an actuation's readback is. A code
// that fails to answer is reported and leaves its last value standing,
// because one control that went quiet says nothing about the others.
func (c *panelControls) pollControls(connector string) error {
	facts, known := c.cached(connector)
	if !known {
		return nil
	}
	bus, err := c.busFor(connector)
	if err != nil {
		return err
	}
	defer bus.Close()

	ddc := c.client(bus)
	var failures []error
	for _, control := range coreControls {
		if _, carried := facts.Capabilities[control.Name]; !carried {
			continue
		}
		current, _, err := ddc.GetVCP(control.Code)
		if err != nil {
			failures = append(failures, fmt.Errorf("reading the %s of %s: %w", control.Name, connector, err))
			continue
		}
		c.observe(connector, control.Code, current)
	}
	return errors.Join(failures...)
}

// The cached facts of one connector, without the probe that
// factsFor would run. The poll holds no lock while it reads the wire.
func (c *panelControls) cached(connector string) (panelFacts, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	probed, known := c.probed[connector]
	if !known || probed.facts == nil {
		return panelFacts{}, false
	}
	return probed.facts.copy(), true
}

// How long a refusal is held before the panel is asked again.
// One window per backstop tick, because a cable's uevents arrive in
// bursts and each burst is a pass, and a probe of a silent panel
// spends its full delay on every code.
const probeRetryInterval = backstopInterval

// Whether a cached answer is asked again. Only a refusal is:
// DDC/CI is a state the monitor's own menu turns on, and an input
// switch fires no uevent and changes no EDID, so nothing but this
// window would ever tell the operator to look. A panel that answered
// is never asked twice, which is what keeps a steady pass off the
// wire.
func (c *panelControls) askAgain(probed probedPanel) bool {
	if probed.facts == nil || probed.facts.Responsive {
		return false
	}
	return !c.clock().Before(probed.asked.Add(probeRetryInterval))
}

// The caller gets its own values, because the entry behind them
// changes on every write the operator makes.
func (f panelFacts) copy() panelFacts {
	observed := make(map[byte]uint16, len(f.Observed))
	for code, value := range f.Observed {
		observed[code] = value
	}
	return panelFacts{Responsive: f.Responsive, Capabilities: f.Capabilities, Observed: observed}
}

// The probe. It reads the capability string first, because that
// is the panel's own statement of what it carries, and asks each
// declared core control for its value and its maximum. A panel that
// answers no capability string is asked for the core codes one by one,
// and a first code that answers nothing ends the probe, because a
// silent wire answers every other code the same way.
func (c *panelControls) probe(connector string) panelFacts {
	facts := panelFacts{Observed: map[byte]uint16{}}
	bus, err := c.busFor(connector)
	if err != nil {
		fmt.Fprintf(os.Stderr, "reading the controls %s carries: %v\n", connector, err)
		return facts
	}
	defer bus.Close()

	ddc := c.client(bus)
	declared := declaredCodes(ddc)
	facts.Responsive = declared != nil
	capabilities := map[string]panelCapability{}
	for _, control := range coreControls {
		values, carried := declared[control.Code]
		if declared != nil && !carried {
			continue
		}
		current, max, err := ddc.GetVCP(control.Code)
		if err != nil {
			if errors.Is(err, ErrUnsupportedVCP) {
				facts.Responsive = true
				continue
			}
			if declared == nil && !facts.Responsive {
				break
			}
			continue
		}
		facts.Responsive = true
		facts.Observed[control.Code] = current
		capabilities[control.Name] = capabilityOf(control.Code, values, max)
	}
	if len(capabilities) > 0 {
		facts.Capabilities = capabilities
	}
	return facts
}

// One entry of the published list. A control the panel declared
// values for publishes them, and a continuous control publishes the
// maximum the panel answered, which is what turns a declared number
// into a value the panel accepts.
func capabilityOf(code byte, values []uint16, max uint16) panelCapability {
	capability := panelCapability{}
	for _, value := range values {
		capability.Values = append(capability.Values, valueName(code, value))
	}
	if len(values) == 0 {
		capability.Max = int(max)
	}
	return capability
}

// The codes the panel declares, and nothing when it declares no
// capability string. A refusal here is an ordinary state: the string is
// an optional part of the protocol and the probe falls back to asking.
func declaredCodes(ddc *DDC) map[byte][]uint16 {
	text, err := ddc.Capabilities()
	if err != nil {
		return nil
	}
	codes := capabilityCodes(text)
	if len(codes) == 0 {
		return nil
	}
	return codes
}

// What the operator last saw of one control, recorded in the
// cache entry so that the next pass writes only on divergence and the
// resource reports a value that no read went out to fetch.
func (c *panelControls) observe(connector string, code byte, raw uint16) {
	c.mu.Lock()
	defer c.mu.Unlock()
	probed, known := c.probed[connector]
	if !known || probed.facts == nil {
		return
	}
	if probed.facts.Observed == nil {
		probed.facts.Observed = map[byte]uint16{}
	}
	probed.facts.Observed[code] = raw
}

// One read of one control, with what it read recorded. This is
// the read the capture makes before the panel goes dark.
func (c *panelControls) readControl(connector string, code byte) (uint16, uint16, error) {
	bus, err := c.busFor(connector)
	if err != nil {
		return 0, 0, err
	}
	defer bus.Close()

	current, max, err := c.client(bus).GetVCP(code)
	if err != nil {
		return 0, 0, fmt.Errorf("reading the %s of %s: %w", capabilityName(code), connector, err)
	}
	c.observe(connector, code, current)
	return current, max, nil
}

// One write, and the readback that proves the control moved. A
// display acknowledges a write on the wire whether it takes the value
// or not.
func (c *panelControls) writeControl(connector string, code byte, raw uint16) error {
	bus, err := c.busFor(connector)
	if err != nil {
		return err
	}
	defer bus.Close()

	ddc := c.client(bus)
	if err := ddc.SetVCP(code, raw); err != nil {
		return fmt.Errorf("setting the %s of %s: %w", capabilityName(code), connector, err)
	}
	current, _, err := ddc.GetVCP(code)
	if err != nil {
		return fmt.Errorf("reading back the %s of %s: %w", capabilityName(code), connector, err)
	}
	c.observe(connector, code, current)
	if current != raw {
		return fmt.Errorf("%s holds a %s of %d after the operator set %d",
			connector, capabilityName(code), current, raw)
	}
	return nil
}

// The write with no readback, for the power-down. The panel is
// leaving the state where it answers, so a readback that failed would
// report a failure for a panel that did as it was told.
func (c *panelControls) writeControlBlind(connector string, code byte, raw uint16) error {
	bus, err := c.busFor(connector)
	if err != nil {
		return err
	}
	defer bus.Close()

	if err := c.client(bus).SetVCP(code, raw); err != nil {
		return fmt.Errorf("setting the %s of %s: %w", capabilityName(code), connector, err)
	}
	c.observe(connector, code, raw)
	return nil
}
