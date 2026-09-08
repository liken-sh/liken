package main

// These tests cover the one decision the link history makes: which
// connectors carry the disconnected taint on this pass. They read the
// answer off the devices the slice publishes, because the taint is what
// ends a consumer's pod.

import (
	"testing"
	"time"
)

// The history under test, with a clock the test moves.
type linkBench struct {
	history *linkHistory
	clock   time.Time
}

func newLinkBench() *linkBench {
	bench := &linkBench{
		history: newLinkHistory(),
		clock:   time.Date(2026, 9, 7, 12, 0, 0, 0, time.UTC),
	}
	bench.history.now = func() time.Time { return bench.clock }
	return bench
}

func (b *linkBench) advance(interval time.Duration) {
	b.clock = b.clock.Add(interval)
}

// One pass over the card, and what it publishes: each device name, and
// whether it carries a taint.
func (b *linkBench) pass(outputs ...Output) map[string]bool {
	tainted := map[string]bool{}
	for _, device := range sliceDevices(withLinks(outputs, b.history)) {
		tainted[device.Name] = len(device.Taints) > 0
	}
	return tainted
}

// One connector with a monitor on it, running a mode.
func linkedPanel(connector string, monitor EDID, mode string) Output {
	output := litOutput(connector, monitor)
	output.CurrentMode = mode
	return output
}

// The same connector with nothing the kernel can detect on it.
func darkConnector(connector string) Output {
	return Output{Connector: connector}
}

// The measured case. An A/V receiver switches its input, the HDMI link
// goes down, and the panel is still on the wire, so the pass that finds
// the connector dark taints nothing.
func TestAConnectorThatJustWentDarkCarriesNoTaint(t *testing.T) {
	bench := newLinkBench()
	bench.pass(linkedPanel("HDMI-A-2", labMonitor(), "3840x1600@60"))

	bench.advance(time.Second)
	tainted := bench.pass(darkConnector("HDMI-A-2"))

	for _, device := range []string{"hdmi-a-2", "hdmi-a-2-draw"} {
		if tainted[device] {
			t.Errorf("%s taints on the first pass that found the connector dark", device)
		}
	}
}

// A cable that is really unplugged stays unplugged. The grace is the
// whole of the delay this rule adds, and the taint after it is the one
// a consumer tolerates with tolerationSeconds.
func TestAConnectorThatStaysDarkTaintsAfterTheGrace(t *testing.T) {
	bench := newLinkBench()
	bench.pass(linkedPanel("HDMI-A-2", labMonitor(), "3840x1600@60"))
	bench.pass(darkConnector("HDMI-A-2"))

	bench.advance(disconnectGrace + time.Second)
	tainted := bench.pass(darkConnector("HDMI-A-2"))

	for _, device := range []string{"hdmi-a-2", "hdmi-a-2-draw"} {
		if !tainted[device] {
			t.Errorf("%s carries no taint after %s dark", device, disconnectGrace+time.Second)
		}
	}
}

// The same monitor back on the connector is the same screen. Nothing
// the claim asked for changed, so no pass of the relink taints and no
// client ends.
func TestAConnectorThatComesBackTheSamePanelNeverTaints(t *testing.T) {
	bench := newLinkBench()
	lit := linkedPanel("HDMI-A-2", labMonitor(), "3840x1600@60")
	bench.pass(lit)

	bench.advance(time.Second)
	bench.pass(darkConnector("HDMI-A-2"))
	bench.advance(2 * time.Second)
	back := bench.pass(lit)
	bench.advance(disconnectGrace + time.Second)
	after := bench.pass(lit)

	for _, device := range []string{"hdmi-a-2", "hdmi-a-2-draw"} {
		if back[device] {
			t.Errorf("%s taints on the pass the panel came back", device)
		}
		if after[device] {
			t.Errorf("%s taints after the panel came back", device)
		}
	}
}

// A different monitor on the connector is a different screen, and the
// claim on it asked for the one that left. The taint is what ends the
// pod that was drawing on it.
func TestAConnectorThatComesBackAnotherMonitorTaints(t *testing.T) {
	bench := newLinkBench()
	bench.pass(linkedPanel("HDMI-A-2", labMonitor(), "3840x1600@60"))

	bench.advance(time.Second)
	bench.pass(darkConnector("HDMI-A-2"))
	bench.advance(2 * time.Second)
	other := EDID{Manufacturer: "BOE", ProductCode: 0x1080, ModelName: "DISPLAY"}
	swapped := bench.pass(linkedPanel("HDMI-A-2", other, "3840x1600@60"))

	for _, device := range []string{"hdmi-a-2", "hdmi-a-2-draw"} {
		if !swapped[device] {
			t.Errorf("%s carries no taint with another monitor on the connector", device)
		}
	}

	// The taint stands for the grace, which is the window the eviction
	// controller needs to act on it.
	bench.advance(time.Second)
	standing := bench.pass(linkedPanel("HDMI-A-2", other, "3840x1600@60"))
	for _, device := range []string{"hdmi-a-2", "hdmi-a-2-draw"} {
		if !standing[device] {
			t.Errorf("%s lost the taint one second into the grace", device)
		}
	}

	bench.advance(disconnectGrace + time.Second)
	settled := bench.pass(linkedPanel("HDMI-A-2", other, "3840x1600@60"))
	for _, device := range []string{"hdmi-a-2", "hdmi-a-2-draw"} {
		if settled[device] {
			t.Errorf("%s still taints once the new monitor has settled", device)
		}
	}
}

// A connector that was dark when the operator started earned no grace.
// Nothing was drawing on it, and a device a claim can take while no
// monitor answers strands the pod that takes it.
func TestAConnectorThatWasDarkBeforeTheOperatorStartedTaintsAtOnce(t *testing.T) {
	bench := newLinkBench()

	tainted := bench.pass(darkConnector("DP-1"))

	for _, device := range []string{"dp-1", "dp-1-draw"} {
		if !tainted[device] {
			t.Errorf("%s carries no taint on a connector that has never had a monitor", device)
		}
	}
}

// The mode is no part of the identity the taint reads. The link often
// comes back up at another mode, because the card renegotiates one, and
// it is the same screen the claim asked for.
func TestAConnectorThatComesBackAtAnotherModeNeverTaints(t *testing.T) {
	bench := newLinkBench()
	bench.pass(linkedPanel("HDMI-A-2", labMonitor(), "3840x1600@60"))

	bench.advance(time.Second)
	bench.pass(darkConnector("HDMI-A-2"))
	bench.advance(2 * time.Second)
	tainted := bench.pass(linkedPanel("HDMI-A-2", labMonitor(), "1920x1080@60"))

	for _, device := range []string{"hdmi-a-2", "hdmi-a-2-draw"} {
		if tainted[device] {
			t.Errorf("%s taints on the same monitor back at another mode", device)
		}
	}
}

// A monitor that answers no EDID decides nothing. A connector that
// comes back with no monitor id reads as the one that left, because a
// taint on a guess ends a client for nothing.
func TestAConnectorThatComesBackWithNoMonitorIDDoesNotTaint(t *testing.T) {
	bench := newLinkBench()
	bench.pass(linkedPanel("HDMI-A-2", labMonitor(), "3840x1600@60"))

	bench.advance(time.Second)
	bench.pass(darkConnector("HDMI-A-2"))
	bench.advance(2 * time.Second)
	tainted := bench.pass(linkedPanel("HDMI-A-2", EDID{}, "3840x1600@60"))

	for _, device := range []string{"hdmi-a-2", "hdmi-a-2-draw"} {
		if tainted[device] {
			t.Errorf("%s taints on a connector whose monitor answered no EDID", device)
		}
	}
}
