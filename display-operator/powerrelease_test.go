package main

// Tests of what unprepare and prepare write to one panel's power and
// brightness, including a rollout that replaces one claim with another.

import (
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

// graceTimers replaces the plugin's timer. It records each scheduled
// standby and runs them only when the test calls end, so the test
// decides when the grace period ends.
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

// end runs every scheduled standby, as the end of the grace period
// does.
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

// Unprepare does not put the panel in standby at once. It waits the
// grace period, and it marks the panel in the power record so that a
// restarted operator can finish the release.
func TestUnprepareWaitsTheGracePeriodBeforeTheStandby(t *testing.T) {
	panel := newFakeMonitor()
	plugin, _ := labPluginWithPanels(t, claimControl(`{"power": "onWhileClaimed"}`), claimedPanel(panel))
	plugin.releaseGrace = powerReleaseGrace
	grace := holdGrace(plugin)
	if claim := prepare(t, plugin); claim.Error != "" {
		t.Fatalf("prepare returned the error %q, want none", claim.Error)
	}

	unprepare(t, plugin)

	if got := panel.writesTo(vcpPowerMode); !slices.Equal(got, []uint16{powerModeOn}) {
		t.Errorf("the panel received the writes %v before the grace period ended, want only the power-on", got)
	}
	if got := powerRecord(t, plugin)["HDMI-A-2"]; got != powerReleased {
		t.Errorf("record = %q, want the panel marked %s", got, powerReleased)
	}
	if got := grace.scheduled(); !slices.Equal(got, []time.Duration{powerReleaseGrace}) {
		t.Errorf("scheduled %v, want one standby after %s", got, powerReleaseGrace)
	}
}

// In a rollout, the old claim is unprepared and the new claim is
// prepared on the same connector inside the grace period. The panel
// receives no write and stays on.
func TestAPrepareInsideTheGracePeriodKeepsThePanelOn(t *testing.T) {
	cases := []struct {
		name   string
		config string
		record string
	}{
		{"the new claim sets onWhileClaimed", claimControl(`{"power": "onWhileClaimed"}`), powerOnWhileClaimed},
		{"the new claim sets on", claimControl(`{"power": "on"}`), ""},
		{"the new claim sets no power", claimControl(`{"brightness": 50}`), ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			panel := newFakeMonitor()
			plugin, _ := labPluginWithPanels(t, claimControl(`{"power": "onWhileClaimed"}`), claimedPanel(panel))
			grace := holdGrace(plugin)
			if claim := prepare(t, plugin); claim.Error != "" {
				t.Fatalf("prepare returned the error %q, want none", claim.Error)
			}
			unprepare(t, plugin)
			written := len(panel.writes())

			plugin.client = allocatedClaim(t, screenRequest(), c.config)
			if claim := prepare(t, plugin); claim.Error != "" {
				t.Fatalf("prepare returned the error %q, want none", claim.Error)
			}
			grace.end()

			if got := panel.writes()[written:]; len(got) != 0 {
				t.Errorf("the panel received the writes %v across the rollout, want nothing", got)
			}
			if got := panel.value(vcpPowerMode); got != powerModeOn {
				t.Errorf("the panel reports the power mode %#02x, want on", got)
			}
			if got := powerRecord(t, plugin)["HDMI-A-2"]; got != c.record {
				t.Errorf("record = %q, want %q", got, c.record)
			}
		})
	}
}

// An operator container that restarts inside the grace period reads
// the released mark from the record and starts the grace period again.
// When it ends, the panel goes to standby.
func TestARestartedOperatorFinishesTheRelease(t *testing.T) {
	panel := newFakeMonitor()
	panel.values[vcpPowerMode] = powerModeOn
	plugin, _ := labPluginWithPanels(t, claimControl(`{"power": "onWhileClaimed"}`), claimedPanel(panel))
	if err := writePowerRecord(plugin.powerPath, map[string]string{"HDMI-A-2": powerReleased}); err != nil {
		t.Fatalf("writing the power record: %v", err)
	}
	grace := holdGrace(plugin)

	plugin.resumeReleases()
	grace.end()

	if got := panel.writesTo(vcpPowerMode); !slices.Equal(got, []uint16{powerModeStandby}) {
		t.Errorf("the panel received the writes %v, want one standby", got)
	}
	if got := powerRecord(t, plugin); len(got) != 0 {
		t.Errorf("record = %v, want nothing left", got)
	}
}

// A restarted operator schedules no standby when the record has no
// released mark.
func TestARestartedOperatorWithNothingReleasedWaitsForNothing(t *testing.T) {
	plugin, _ := labPluginWithPanels(t, claimControl(`{"power": "onWhileClaimed"}`), claimedPanel(newFakeMonitor()))
	if err := writePowerRecord(plugin.powerPath, map[string]string{"HDMI-A-2": powerOnWhileClaimed}); err != nil {
		t.Fatalf("writing the power record: %v", err)
	}
	grace := holdGrace(plugin)

	plugin.resumeReleases()

	if got := grace.scheduled(); len(got) != 0 {
		t.Errorf("scheduled %v, want nothing", got)
	}
}

// A person turned the panel off inside the grace period, so the
// standby does not write to it.
func TestTheStandbySkipsAPanelThatIsAlreadyOff(t *testing.T) {
	panel := newFakeMonitor()
	plugin, _ := labPluginWithPanels(t, claimControl(`{"power": "onWhileClaimed"}`), claimedPanel(panel))
	grace := holdGrace(plugin)
	if claim := prepare(t, plugin); claim.Error != "" {
		t.Fatalf("prepare returned the error %q, want none", claim.Error)
	}
	unprepare(t, plugin)
	panel.setWithButtons(vcpPowerMode, powerModeOff)

	grace.end()

	if got := panel.writesTo(vcpPowerMode); !slices.Equal(got, []uint16{powerModeOn}) {
		t.Errorf("the panel received the writes %v, want only the power-on of the prepare", got)
	}
}

// Prepare reads each control before it writes it, and writes only the
// controls whose value differs from the claim's. So an operator restart
// or a rollout onto a panel that is on writes nothing.
func TestAPrepareWritesOnlyTheControlsThatDiffer(t *testing.T) {
	cases := []struct {
		name       string
		config     string
		power      uint16
		brightness uint16
		want       []monitorSet
	}{
		{
			name:   "a panel that is on and a power-on",
			config: claimControl(`{"power": "onWhileClaimed"}`),
			power:  powerModeOn, brightness: 50,
		},
		{
			name:   "a panel at the declared brightness",
			config: claimControl(`{"brightness": 50}`),
			power:  powerModeOn, brightness: 50,
		},
		{
			name:   "a panel that is on at the declared brightness, both declared",
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
				t.Fatalf("prepare returned the error %q, want none", claim.Error)
			}

			if got := panel.writes(); !slices.Equal(got, c.want) {
				t.Errorf("the panel received the writes %v, want %v", got, c.want)
			}
		})
	}
}

// When the panel does not respond to the read, prepare writes nothing and
// fails with the read error, so the kubelet retries it.
func TestAPrepareWritesNothingToAPanelItCannotRead(t *testing.T) {
	panel := newFakeMonitor()
	plugin, _ := labPluginWithPanels(t, claimControl(`{"power": "on"}`), claimedPanel(panel))
	for _, output := range discoverOutputs(plugin.sysRoot, plugin.card) {
		plugin.controls.of(output)
	}
	panel.silence()

	claim := prepare(t, plugin)

	if !strings.Contains(claim.Error, "reading the power mode") {
		t.Fatalf("prepare error is %q, want it to name the failed read", claim.Error)
	}
	if got := panel.writes(); len(got) != 0 {
		t.Errorf("the panel received the writes %v, want nothing", got)
	}
}
