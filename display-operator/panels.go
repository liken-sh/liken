package main

// panelControls caches, for each connector, whether its panel responds
// to DDC/CI, which controls it declares, and the last value read or
// written for each control. A monitor is probed once, and a probe that
// got no response is retried after probeRetryInterval. After the probe
// the operator reads a panel only to confirm a write, to save a value
// before an override, and in the poll. The poll skips a panel last
// seen in standby or off, because a DDC read wakes some panels.

import (
	"errors"
	"fmt"
	"os"
	"time"
)

// panelFacts is the result of one probe. Responsive is true for any
// panel that responds to DDC/CI, including one that reports every
// control as unsupported.
type panelFacts struct {
	Responsive   bool
	Capabilities map[string]panelCapability
	Observed     map[byte]uint16
}

// controls returns which of the controls that the Display status
// publishes the panel has.
func (f panelFacts) controls() supportedControls {
	_, brightness := f.Capabilities[brightnessControl]
	_, power := f.Capabilities[powerControl]
	return supportedControls{Brightness: brightness, Power: power}
}

// factsFor returns the cached facts when the same monitor on the same
// connector was probed before, and otherwise probes the panel. A nil
// panelControls returns no facts and opens no bus, which tests without
// a probe rely on. A disconnected connector is never probed, because
// the driver can still report the last EDID it read.
func (c *panelControls) factsFor(output Output) panelFacts {
	if c == nil || !output.Connected {
		return panelFacts{}
	}
	// The cache lock is released during the probe, because the probe
	// waits for the bus, and the goroutine that has the bus calls
	// observe, which takes the cache lock. A second caller for the same
	// connector waits on c.probing and then reads the cache.
	for {
		c.mu.Lock()
		if probed, known := c.probed[output.Connector]; known && probed.monitor == output.Monitor && !c.probeAgain(probed) {
			// Copy under the lock, because observe writes to the same
			// Observed map.
			facts := probed.facts.copy()
			c.mu.Unlock()
			return facts
		}
		running, probing := c.probing[output.Connector]
		if !probing {
			break
		}
		c.mu.Unlock()
		<-running
	}
	if c.probing == nil {
		c.probing = map[string]chan struct{}{}
	}
	done := make(chan struct{})
	c.probing[output.Connector] = done
	c.mu.Unlock()

	facts := c.probe(output.Connector)

	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.probing, output.Connector)
	close(done)
	if c.probed == nil {
		c.probed = map[string]probedPanel{}
	}
	// The probe just read every control, so the first poll is due
	// pollInterval from now.
	asked := c.clock()
	c.probed[output.Connector] = probedPanel{
		monitor: output.Monitor, facts: &facts, probed: asked, polled: asked,
	}
	return facts.copy()
}

// pollInterval is the minimum time between two polls of one panel's
// control values. The poll finds a value that a person changed with
// the panel's own buttons. The control loop also ticks at pollInterval,
// so a panel is polled every 10 to 20 seconds.
const pollInterval = 10 * time.Second

// pollDue reports whether pollInterval has passed since the last poll
// of this connector, and if so records the time. It checks and
// records under one lock, so two goroutines that call it at once cause
// one read of the panel.
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

// pollControls reads and records every core control that the panel
// declares. A control that fails keeps its last recorded value, and
// the read goes on to the next control. It returns the names of the
// controls that failed with the joined error, so the caller can tell
// one failing control from a panel that stopped responding.
func (c *panelControls) pollControls(connector string) ([]string, error) {
	facts, known := c.cached(connector)
	if !known {
		return nil, nil
	}
	bus, err := c.busFor(connector)
	if err != nil {
		return nil, err
	}
	defer bus.Close()

	ddc := c.client(bus)
	var quiet []string
	var failures []error
	for _, control := range coreControls {
		if _, carried := facts.Capabilities[control.Name]; !carried {
			continue
		}
		current, _, err := ddc.GetVCP(control.Code)
		if err != nil {
			quiet = append(quiet, control.Name)
			failures = append(failures, fmt.Errorf("reading the %s of %s: %w", control.Name, connector, err))
			continue
		}
		c.observe(connector, control.Code, current)
	}
	return quiet, errors.Join(failures...)
}

// cached returns a copy of the cached facts for a connector and never
// probes.
func (c *panelControls) cached(connector string) (panelFacts, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	probed, known := c.probed[connector]
	if !known || probed.facts == nil {
		return panelFacts{}, false
	}
	return probed.facts.copy(), true
}

// probeRetryInterval is the wait before a panel that did not respond
// to the probe is probed again. A probe of such a panel waits out
// every DDC retry before it fails, and one cable change sends a burst
// of uevents that each cause a pass, so a shorter wait would repeat
// that delay on every pass of the burst.
const probeRetryInterval = backstopInterval

// probeAgain reports whether a panel that did not respond to its probe
// is due for another. A person can turn DDC/CI on in the monitor's own
// menu, which raises no uevent and does not change the EDID, so the
// retry is the only way the operator finds out. A panel that responded
// is never probed again.
func (c *panelControls) probeAgain(probed probedPanel) bool {
	if probed.facts == nil || probed.facts.Responsive {
		return false
	}
	return !c.clock().Before(probed.probed.Add(probeRetryInterval))
}

// copy returns facts with their own Observed map, because observe
// changes the cached map. Capabilities is shared, because nothing
// writes to it after the probe.
func (f panelFacts) copy() panelFacts {
	observed := make(map[byte]uint16, len(f.Observed))
	for code, value := range f.Observed {
		observed[code] = value
	}
	return panelFacts{Responsive: f.Responsive, Capabilities: f.Capabilities, Observed: observed}
}

// probe reads the panel's DDC/CI capability string, which lists its
// controls, and then the value and maximum of each declared core
// control. With no capability string, probe reads each core code in
// turn. It stops when the first read gets no reply, because a bus with
// no device on it gives the same result for every code.
func (c *panelControls) probe(connector string) panelFacts {
	facts := panelFacts{Observed: map[byte]uint16{}}
	bus, err := c.busFor(connector)
	if err != nil {
		fmt.Fprintf(os.Stderr, "reading the controls that %s supports: %v\n", connector, err)
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

// capabilityOf builds the published capability of one control. A
// control with declared values publishes their names. A control with
// none is continuous and publishes the maximum that the panel returned.
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

// declaredCodes returns the controls in the panel's capability
// string, or nil when there is none. The string is optional in
// DDC/CI, so a failed read is not an error.
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

// observe records the last value read or written for a control, so
// the next pass writes only when the panel differs from the wanted
// value, and the Display status reports the value without another read.
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

// readControl reads one control and records its value.
// displayControl.capture uses it to save a value before an override.
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

// writeControl sets a control and reads it back to confirm the new
// value, because a display acknowledges a write on the bus even when
// it ignores the value.
func (c *panelControls) writeControl(connector string, code byte, raw uint16) error {
	bus, err := c.busFor(connector)
	if err != nil {
		return err
	}
	defer bus.Close()

	ddc := c.client(bus)
	c.announce(connector, code, raw)
	if err := ddc.SetVCP(code, raw); err != nil {
		return fmt.Errorf("setting the %s of %s: %w", capabilityName(code), connector, err)
	}
	current, _, err := ddc.GetVCP(code)
	if err != nil {
		return fmt.Errorf("reading back the %s of %s: %w", capabilityName(code), connector, err)
	}
	c.observe(connector, code, current)
	if current != raw {
		return fmt.Errorf("the %s of %s reads %d after the operator set it to %d",
			capabilityName(code), connector, current, raw)
	}
	return nil
}

// writeControlBlind sets a control without reading it back, for the
// power-down. A panel that is turning off stops responding, so a
// readback would fail for a write that worked.
func (c *panelControls) writeControlBlind(connector string, code byte, raw uint16) error {
	bus, err := c.busFor(connector)
	if err != nil {
		return err
	}
	defer bus.Close()

	c.announce(connector, code, raw)
	if err := c.client(bus).SetVCP(code, raw); err != nil {
		return fmt.Errorf("setting the %s of %s: %w", capabilityName(code), connector, err)
	}
	c.observe(connector, code, raw)
	return nil
}

// announce logs each write to a panel with the last observed value
// and the new one, so a person who saw the screen change can find the
// cause in the log.
func (c *panelControls) announce(connector string, code byte, raw uint16) {
	facts, _ := c.cached(connector)
	from, known := facts.Observed[code]
	announceWrite(connector, code, from, known, raw)
}

// announceWrite prints the log line for a write. A caller that
// already has the previous value calls it directly.
func announceWrite(connector string, code byte, from uint16, known bool, to uint16) {
	name := capabilityName(code)
	if !known {
		fmt.Printf("%s: the %s goes to %s\n", connector, name, statusValue(code, to))
		return
	}
	fmt.Printf("%s: the %s goes from %s to %s\n", connector, name, statusValue(code, from), statusValue(code, to))
}
