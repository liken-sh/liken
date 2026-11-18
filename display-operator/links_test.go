package main

// These tests read the taints from the devices that sliceDevices
// publishes, because the taint is what ends the pod on a connector.

import (
	"testing"
	"time"
)

// linkBench is a linkHistory with a clock that the test advances.
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

// pass records the outputs in the history as one pass and returns, for
// each published device name, whether the device has a taint.
func (b *linkBench) pass(outputs ...Output) map[string]bool {
	tainted := map[string]bool{}
	for _, device := range sliceDevices(withLinks(outputs, b.history)) {
		tainted[device.Name] = len(device.Taints) > 0
	}
	return tainted
}

// connectedPanel is a connector with a monitor on it, running a mode.
func connectedPanel(connector string, monitor EDID, mode string) Output {
	output := connectedOutput(connector, monitor)
	output.CurrentMode = mode
	return output
}

// disconnectedOutput is a connector that reads disconnected, with
// nothing in its EDID file, as sysfs reports it during a drop.
func disconnectedOutput(connector string) Output {
	return Output{Connector: connector}
}

// An A/V receiver that switches input drops the HDMI link while the
// monitor stays attached, so the first pass that reads the connector
// disconnected must not taint it.
func TestAConnectorThatJustDisconnectedHasNoTaint(t *testing.T) {
	bench := newLinkBench()
	bench.pass(connectedPanel("HDMI-A-2", labMonitor(), "3840x1600@60"))

	bench.advance(time.Second)
	tainted := bench.pass(disconnectedOutput("HDMI-A-2"))

	for _, device := range []string{"hdmi-a-2", "hdmi-a-2-draw"} {
		if tainted[device] {
			t.Errorf("%s taints on the first pass that found the connector disconnected", device)
		}
	}
}

// A monitor that shows another input reads as disconnected until it
// switches back, so the connector keeps its last monitor and no taint
// for as long as it stays disconnected.
func TestAConnectorThatStaysDisconnectedNeverTaints(t *testing.T) {
	bench := newLinkBench()
	bench.pass(connectedPanel("HDMI-A-2", labMonitor(), "3840x1600@60"))
	bench.pass(disconnectedOutput("HDMI-A-2"))

	bench.advance(disconnectGrace + time.Hour)
	tainted := bench.pass(disconnectedOutput("HDMI-A-2"))

	for _, device := range []string{"hdmi-a-2", "hdmi-a-2-draw"} {
		if tainted[device] {
			t.Errorf("%s taints after %s disconnected, and its monitor is expected back",
				device, disconnectGrace+time.Hour)
		}
	}
}

func TestAConnectorThatReconnectsWithTheSameMonitorNeverTaints(t *testing.T) {
	bench := newLinkBench()
	panel := connectedPanel("HDMI-A-2", labMonitor(), "3840x1600@60")
	bench.pass(panel)

	bench.advance(time.Second)
	bench.pass(disconnectedOutput("HDMI-A-2"))
	bench.advance(2 * time.Second)
	back := bench.pass(panel)
	bench.advance(disconnectGrace + time.Second)
	after := bench.pass(panel)

	for _, device := range []string{"hdmi-a-2", "hdmi-a-2-draw"} {
		if back[device] {
			t.Errorf("%s taints on the pass the panel came back", device)
		}
		if after[device] {
			t.Errorf("%s taints after the panel came back", device)
		}
	}
}

// The claim on the connector asked for the monitor that left, so a
// different monitor taints the devices, and the taint ends the pod that
// drew on them.
func TestAConnectorThatReconnectsWithAnotherMonitorTaints(t *testing.T) {
	bench := newLinkBench()
	bench.pass(connectedPanel("HDMI-A-2", labMonitor(), "3840x1600@60"))

	bench.advance(time.Second)
	bench.pass(disconnectedOutput("HDMI-A-2"))
	bench.advance(2 * time.Second)
	other := EDID{Manufacturer: "BOE", ProductCode: 0x1080, ModelName: "DISPLAY"}
	swapped := bench.pass(connectedPanel("HDMI-A-2", other, "3840x1600@60"))

	for _, device := range []string{"hdmi-a-2", "hdmi-a-2-draw"} {
		if !swapped[device] {
			t.Errorf("%s has no taint with another monitor on the connector", device)
		}
	}

	bench.advance(time.Second)
	standing := bench.pass(connectedPanel("HDMI-A-2", other, "3840x1600@60"))
	for _, device := range []string{"hdmi-a-2", "hdmi-a-2-draw"} {
		if !standing[device] {
			t.Errorf("%s lost the taint one second into the grace", device)
		}
	}

	bench.advance(disconnectGrace + time.Second)
	settled := bench.pass(connectedPanel("HDMI-A-2", other, "3840x1600@60"))
	for _, device := range []string{"hdmi-a-2", "hdmi-a-2-draw"} {
		if settled[device] {
			t.Errorf("%s still taints once the new monitor has settled", device)
		}
	}
}

// A connector that is disconnected on the operator's first pass has no
// known monitor, and a claim allocated to it would give its pod no
// screen, so it is tainted at once.
func TestAConnectorDisconnectedAtStartTaintsAtOnce(t *testing.T) {
	bench := newLinkBench()

	tainted := bench.pass(disconnectedOutput("DP-1"))

	for _, device := range []string{"dp-1", "dp-1-draw"} {
		if !tainted[device] {
			t.Errorf("%s has no taint on a connector that has never had a monitor", device)
		}
	}
}

// The card can set a different mode when the link comes back, and it is
// still the monitor the claim asked for, because monitorID leaves out
// the mode.
func TestAConnectorThatReconnectsAtAnotherModeNeverTaints(t *testing.T) {
	bench := newLinkBench()
	bench.pass(connectedPanel("HDMI-A-2", labMonitor(), "3840x1600@60"))

	bench.advance(time.Second)
	bench.pass(disconnectedOutput("HDMI-A-2"))
	bench.advance(2 * time.Second)
	tainted := bench.pass(connectedPanel("HDMI-A-2", labMonitor(), "1920x1080@60"))

	for _, device := range []string{"hdmi-a-2", "hdmi-a-2-draw"} {
		if tainted[device] {
			t.Errorf("%s taints on the same monitor back at another mode", device)
		}
	}
}

// A monitor with no readable EDID has no monitorID to compare, and a
// taint on a guess would end a pod for no reason.
func TestAConnectorThatReconnectsWithNoMonitorIDNeverTaints(t *testing.T) {
	bench := newLinkBench()
	bench.pass(connectedPanel("HDMI-A-2", labMonitor(), "3840x1600@60"))

	bench.advance(time.Second)
	bench.pass(disconnectedOutput("HDMI-A-2"))
	bench.advance(2 * time.Second)
	tainted := bench.pass(connectedPanel("HDMI-A-2", EDID{}, "3840x1600@60"))

	for _, device := range []string{"hdmi-a-2", "hdmi-a-2-draw"} {
		if tainted[device] {
			t.Errorf("%s taints on a connector whose monitor returned no EDID", device)
		}
	}
}

// publishedDisplay is the Display that the operator writes for one
// monitor on one connector.
func publishedDisplay(node, connector string, monitor EDID) Display {
	return Display{
		Metadata: DisplayMeta{Name: monitorID(monitor)},
		Status: DisplayStatus{
			Node:         node,
			Connector:    connector,
			Manufacturer: monitor.Manufacturer,
			Model:        monitor.ModelName,
			Serial:       monitor.Serial,
		},
	}
}

// A machine that boots with its panel on another input has no EDID in
// sysfs and an empty history. loadFromDisplays loads the monitor from
// the Display, so the connector has no taint.
func TestAConnectorLoadedFromADisplayHasNoTaintWhileDisconnected(t *testing.T) {
	bench := newLinkBench()
	bench.history.loadFromDisplays([]Display{publishedDisplay("liken-1", "HDMI-A-2", labMonitor())},
		"liken-1", []Output{disconnectedOutput("HDMI-A-2")})

	tainted := bench.pass(disconnectedOutput("HDMI-A-2"))

	for _, device := range []string{"hdmi-a-2", "hdmi-a-2-draw"} {
		if tainted[device] {
			t.Errorf("%s taints, and a Display records its monitor", device)
		}
	}
}

// Each case checks the monitorID that hdmi-a-2 publishes after
// loadFromDisplays.
func TestLoadFromDisplaysSkipsADisplayThatWouldPublishTheWrongMonitor(t *testing.T) {
	misnamed := publishedDisplay("liken-1", "HDMI-A-2", labMonitor())
	misnamed.Status.Model = "Some Other Panel"

	for _, tc := range []struct {
		name    string
		display Display
		outputs []Output
		want    string
		why     string
	}{
		{
			name:    "another node's screen",
			display: publishedDisplay("stick-1", "HDMI-A-2", labMonitor()),
			outputs: []Output{disconnectedOutput("HDMI-A-2")},
			want:    "",
			why:     "the record names another machine",
		},
		{
			name:    "a connector with a monitor on it now",
			display: publishedDisplay("liken-1", "HDMI-A-2", labMonitor()),
			outputs: []Output{connectedPanel("HDMI-A-2", portableMonitor(), "1920x1080@60")},
			want:    monitorID(portableMonitor()),
			why:     "the connector reads connected, and its reading takes precedence",
		},
		{
			name:    "a screen that is connected to another connector",
			display: publishedDisplay("liken-1", "HDMI-A-2", labMonitor()),
			outputs: []Output{disconnectedOutput("HDMI-A-2"), connectedPanel("HDMI-A-1", labMonitor(), "3840x1600@60")},
			want:    "",
			why:     "the monitor moved, and two devices would publish one monitorID",
		},
		{
			name:    "a record whose facts do not rebuild its name",
			display: misnamed,
			outputs: []Output{disconnectedOutput("HDMI-A-2")},
			want:    "",
			why:     "the name is what a claim matches",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bench := newLinkBench()
			bench.history.loadFromDisplays([]Display{tc.display}, "liken-1", tc.outputs)

			var got string
			for _, device := range sliceDevices(withLinks(tc.outputs, bench.history)) {
				if device.Name != "hdmi-a-2" {
					continue
				}
				if id := device.Attributes[pairingAttribute].String; id != nil {
					got = *id
				}
			}
			if got != tc.want {
				t.Errorf("hdmi-a-2 publishes %q, want %q: %s", got, tc.want, tc.why)
			}
		})
	}
}

func TestAConnectorLoadedFromADisplayPublishesTheDisplaysName(t *testing.T) {
	for _, monitor := range []EDID{labMonitor(), portableMonitor()} {
		t.Run(monitorID(monitor), func(t *testing.T) {
			bench := newLinkBench()
			bench.history.loadFromDisplays([]Display{publishedDisplay("liken-1", "HDMI-A-2", monitor)},
				"liken-1", []Output{disconnectedOutput("HDMI-A-2")})

			devices := sliceDevices(withLinks([]Output{disconnectedOutput("HDMI-A-2")}, bench.history))

			var got string
			for _, device := range devices {
				if device.Name == "hdmi-a-2" {
					if id := device.Attributes[pairingAttribute].String; id != nil {
						got = *id
					}
				}
			}
			if want := monitorID(monitor); got != want {
				t.Errorf("the disconnected connector publishes %q, want %q", got, want)
			}
		})
	}
}
