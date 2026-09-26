package main

// The claim end and the claim start on one panel, as a rollout runs
// them, and the rule that a panel that already holds a value takes no
// write.

import (
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

// graceTimers holds every standby the plugin scheduled until the test
// ends the grace period, so a test states when the period ends.
type graceTimers struct {
	mu      sync.Mutex
	pending []func()
	delays  []time.Duration
}

func holdGrace(plugin *draPlugin) *graceTimers {
	timers := &graceTimers{}
	plugin.afterGrace = func(delay time.Duration, run func()) {
		timers.mu.Lock()
		defer timers.mu.Unlock()
		timers.pending = append(timers.pending, run)
		timers.delays = append(timers.delays, delay)
	}
	return timers
}

// The grace period ends: every scheduled standby runs.
func (g *graceTimers) end() {
	g.mu.Lock()
	pending := g.pending
	g.pending = nil
	g.mu.Unlock()
	for _, run := range pending {
		run()
	}
}

func (g *graceTimers) scheduled() []time.Duration {
	g.mu.Lock()
	defer g.mu.Unlock()
	return slices.Clone(g.delays)
}

// The claim end waits the grace period before the standby, and the
// record marks the panel so a restarted operator finds it.
func TestUnprepareWaitsTheGracePeriodBeforeTheStandby(t *testing.T) {
	panel := newFakeMonitor()
	plugin, _ := labPluginWithPanels(t, claimControl(`{"power": "onWhileClaimed"}`), claimedPanel(panel))
	plugin.releaseGrace = powerReleaseGrace
	grace := holdGrace(plugin)
	if claim := prepare(t, plugin); claim.Error != "" {
		t.Fatal(claim.Error)
	}

	unprepare(t, plugin)

	if got := panel.took(vcpPowerMode); !slices.Equal(got, []uint16{powerModeOn}) {
		t.Errorf("the panel took %v before the grace period ended, want only the power-on", got)
	}
	if got := powerRecord(t, plugin)["HDMI-A-2"]; got != powerReleased {
		t.Errorf("record = %q, want the panel marked %s", got, powerReleased)
	}
	if got := grace.scheduled(); !slices.Equal(got, []time.Duration{powerReleaseGrace}) {
		t.Errorf("scheduled %v, want one standby after %s", got, powerReleaseGrace)
	}
}

// A rollout: the old claim ends, the new claim prepares on the same
// connector inside the grace period, and the panel stays on with no
// write at all.
func TestAPrepareInsideTheGracePeriodKeepsThePanelOn(t *testing.T) {
	cases := []struct {
		name   string
		config string
		record string
	}{
		{"the new claim states onWhileClaimed", claimControl(`{"power": "onWhileClaimed"}`), powerOnWhileClaimed},
		{"the new claim states on", claimControl(`{"power": "on"}`), ""},
		{"the new claim states no power", claimControl(`{"brightness": 50}`), ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			panel := newFakeMonitor()
			plugin, _ := labPluginWithPanels(t, claimControl(`{"power": "onWhileClaimed"}`), claimedPanel(panel))
			grace := holdGrace(plugin)
			if claim := prepare(t, plugin); claim.Error != "" {
				t.Fatal(claim.Error)
			}
			unprepare(t, plugin)
			written := len(panel.writes())

			plugin.client = allocatedClaim(t, screenRequest(), c.config)
			if claim := prepare(t, plugin); claim.Error != "" {
				t.Fatal(claim.Error)
			}
			grace.end()

			if got := panel.writes()[written:]; len(got) != 0 {
				t.Errorf("the panel took %v across the rollout, want nothing", got)
			}
			if got := panel.holds(vcpPowerMode); got != powerModeOn {
				t.Errorf("the panel holds the power mode %#02x, want on", got)
			}
			if got := powerRecord(t, plugin)["HDMI-A-2"]; got != c.record {
				t.Errorf("record = %q, want %q", got, c.record)
			}
		})
	}
}

// An operator container that restarts inside the grace period finds
// the mark in the record and puts the panel down when the period ends
// again.
func TestARestartedOperatorFinishesTheRelease(t *testing.T) {
	panel := newFakeMonitor()
	panel.values[vcpPowerMode] = powerModeOn
	plugin, _ := labPluginWithPanels(t, claimControl(`{"power": "onWhileClaimed"}`), claimedPanel(panel))
	if err := writePowerRecord(plugin.powerPath, map[string]string{"HDMI-A-2": powerReleased}); err != nil {
		t.Fatal(err)
	}
	grace := holdGrace(plugin)

	plugin.resumeReleases()
	grace.end()

	if got := panel.took(vcpPowerMode); !slices.Equal(got, []uint16{powerModeStandby}) {
		t.Errorf("the panel took %v, want one standby", got)
	}
	if got := powerRecord(t, plugin); len(got) != 0 {
		t.Errorf("record = %v, want nothing left", got)
	}
}

// A restarted operator with nothing released schedules nothing.
func TestARestartedOperatorWithNothingReleasedWaitsForNothing(t *testing.T) {
	plugin, _ := labPluginWithPanels(t, claimControl(`{"power": "onWhileClaimed"}`), claimedPanel(newFakeMonitor()))
	if err := writePowerRecord(plugin.powerPath, map[string]string{"HDMI-A-2": powerOnWhileClaimed}); err != nil {
		t.Fatal(err)
	}
	grace := holdGrace(plugin)

	plugin.resumeReleases()

	if got := grace.scheduled(); len(got) != 0 {
		t.Errorf("scheduled %v, want nothing", got)
	}
}

// A panel a person turned off inside the grace period is already
// down, so the standby writes nothing.
func TestTheStandbyLeavesAPanelThatIsAlreadyDown(t *testing.T) {
	panel := newFakeMonitor()
	plugin, _ := labPluginWithPanels(t, claimControl(`{"power": "onWhileClaimed"}`), claimedPanel(panel))
	grace := holdGrace(plugin)
	if claim := prepare(t, plugin); claim.Error != "" {
		t.Fatal(claim.Error)
	}
	unprepare(t, plugin)
	panel.turnedTo(vcpPowerMode, powerModeOff)

	grace.end()

	if got := panel.took(vcpPowerMode); !slices.Equal(got, []uint16{powerModeOn}) {
		t.Errorf("the panel took %v, want only the power-on of the prepare", got)
	}
}

// A prepare reads each control before it writes it. A panel that
// already holds what the claim states takes no write, which is the
// operator restart and the rollout onto a lit panel, and a panel that
// holds something else still takes one write.
func TestAPrepareWritesOnlyWhatThePanelDoesNotHold(t *testing.T) {
	cases := []struct {
		name       string
		config     string
		power      uint16
		brightness uint16
		want       []monitorSet
	}{
		{
			name:   "a lit panel and a power-on",
			config: claimControl(`{"power": "onWhileClaimed"}`),
			power:  powerModeOn, brightness: 50,
		},
		{
			name:   "a panel at the stated brightness",
			config: claimControl(`{"brightness": 50}`),
			power:  powerModeOn, brightness: 50,
		},
		{
			name:   "a lit panel at the stated brightness, both stated",
			config: claimControl(`{"power": "on", "brightness": 50}`),
			power:  powerModeOn, brightness: 50,
		},
		{
			name:   "a panel in standby",
			config: claimControl(`{"power": "on", "brightness": 50}`),
			power:  powerModeStandby, brightness: 50,
			want: []monitorSet{{Code: vcpPowerMode, Value: powerModeOn}},
		},
		{
			name:   "a panel at another brightness",
			config: claimControl(`{"power": "on", "brightness": 50}`),
			power:  powerModeOn, brightness: 80,
			want: []monitorSet{{Code: vcpBrightness, Value: 50}},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			panel := newFakeMonitor()
			panel.values[vcpPowerMode] = c.power
			panel.values[vcpBrightness] = c.brightness
			plugin, _ := labPluginWithPanels(t, c.config, claimedPanel(panel))

			if claim := prepare(t, plugin); claim.Error != "" {
				t.Fatal(claim.Error)
			}

			if got := panel.writes(); !slices.Equal(got, c.want) {
				t.Errorf("the panel took %v, want %v", got, c.want)
			}
		})
	}
}

// A panel that does not answer the read takes no write, and the
// prepare fails with the read's error for the kubelet to retry.
func TestAPrepareWritesNothingToAPanelItCannotRead(t *testing.T) {
	panel := newFakeMonitor()
	plugin, _ := labPluginWithPanels(t, claimControl(`{"power": "on"}`), claimedPanel(panel))
	for _, output := range discoverOutputs(plugin.sysRoot, plugin.card) {
		plugin.controls.of(output)
	}
	panel.silence()

	claim := prepare(t, plugin)

	if !strings.Contains(claim.Error, "reading the power mode") {
		t.Fatalf("error = %q, want the failed read named", claim.Error)
	}
	if got := panel.writes(); len(got) != 0 {
		t.Errorf("the panel took %v, want nothing", got)
	}
}
