package main

// Tests of the fleet gauges, scraped from a real registry.

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/liken-sh/liken/liken/api"
	"github.com/liken-sh/liken/liken/machine"
)

// series returns the sample lines of one metric in a scrape, without
// the HELP and TYPE lines.
func series(body, name string) []string {
	var out []string
	for _, line := range strings.Split(body, "\n") {
		if strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, name+" ") || strings.HasPrefix(line, name+"{") {
			out = append(out, line)
		}
	}
	return out
}

func requireSeries(t *testing.T, body, line string) {
	t.Helper()
	name, _, _ := strings.Cut(line, " ")
	name, _, _ = strings.Cut(name, "{")
	if found := series(body, name); !slices.Contains(found, line) {
		t.Errorf("the scrape has series %q, want %q", found, line)
	}
}

// With no address, or an address the listener refuses, the registry
// still holds the build info and the fleet gauges. Neither case opens
// a socket, so the test scrapes the listener's handler.
func TestTheRegistryHasTheFleetMetricsWithoutAListener(t *testing.T) {
	cases := []struct {
		name    string
		address string
	}{
		{"no address", ""},
		{"an address the listener refuses", "127.0.0.1:-1"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			o, layer := serveMetrics(c.address)
			if o == nil || layer == nil {
				t.Fatal("serveMetrics returned a nil operator or nil fleet metrics")
			}
			body := scrapeHandler(t, o.Handler())
			requireSeries(t, body, `liken_machines{phase="Ready"} 0`)
			if len(series(body, "liken_build_info")) != 1 {
				t.Errorf("the scrape has build info series %q, want exactly one", series(body, "liken_build_info"))
			}
		})
	}
}

func TestMachinesCountByTheirEffectivePhase(t *testing.T) {
	machines, renewals := fleetInputs(
		fleetEntry{"node-1", api.PhaseReady, 10 * time.Second},
		fleetEntry{"node-2", api.PhaseReady, 30 * time.Second},
		fleetEntry{"node-3", api.PhaseUpdatePending, 10 * time.Second},
		// An old heartbeat makes the machine Lost, whatever phase its
		// last status reported.
		fleetEntry{"node-4", api.PhaseReady, 5 * time.Minute},
	)
	layer, scrape := fleetMetrics(t)
	layer.observePass(decideFleet(machines, renewals, passTime), rollout{})

	body := scrape()
	requireSeries(t, body, `liken_machines{phase="Ready"} 2`)
	requireSeries(t, body, `liken_machines{phase="UpdatePending"} 1`)
	requireSeries(t, body, `liken_machines{phase="Lost"} 1`)
	requireSeries(t, body, `liken_machines{phase="Degraded"} 0`)
}

func TestAMachineWithNoStatusYetCountsAsUnknown(t *testing.T) {
	machines, renewals := fleetInputs(fleetEntry{"node-1", "", 10 * time.Second})
	layer, scrape := fleetMetrics(t)
	layer.observePass(decideFleet(machines, renewals, passTime), rollout{})

	requireSeries(t, scrape(), `liken_machines{phase="Unknown"} 1`)
}

func TestAPhaseWithNoMachinesReportsZero(t *testing.T) {
	layer, scrape := fleetMetrics(t)
	layer.observePass(decideFleet(nil, nil, passTime), rollout{})

	body := scrape()
	for _, phase := range fleetPhases {
		t.Run(string(phase), func(t *testing.T) {
			requireSeries(t, body, `liken_machines{phase="`+string(phase)+`"} 0`)
		})
	}
}

// awaitingApproval is a machine that staged a change and waits for a
// person to approve it, because its reboot policy is Manual.
func awaitingApproval(name string, kind string) machine.Machine {
	m := machine.Machine{Metadata: api.ObjectMeta{Name: name}}
	m.Status.Phase = api.PhaseUpdatePending
	m.Status.Conditions = []api.Condition{
		{Type: "SpecConverged", Status: api.ConditionFalse, Reason: kind + "Pending"},
	}
	return m
}

func TestApprovalsPendingCountsTheMachinesWaitingOnAPerson(t *testing.T) {
	machines := []machine.Machine{
		awaitingApproval("node-1", "Reboot"),
		awaitingApproval("node-2", "Restart"),
	}
	ready := machine.Machine{Metadata: api.ObjectMeta{Name: "node-3"}}
	ready.Status.Phase = api.PhaseReady
	machines = append(machines, ready)

	renewals := map[string]time.Time{
		"node-1": passTime, "node-2": passTime, "node-3": passTime,
	}
	layer, scrape := fleetMetrics(t)
	layer.observePass(decideFleet(machines, renewals, passTime), rollout{})

	requireSeries(t, scrape(), "liken_disruption_approvals_pending 2")
}

func TestMachinesBehindTargetCountsTheOnesNotOnTheTarget(t *testing.T) {
	machines, renewals := rolloutInputs(
		rolloutEntry{"node-1", api.PhaseReady, fresh, false, -1},
		rolloutEntry{"node-3", api.PhaseReady, fresh, false, -1},
	)
	machines[0].Status.Version.Liken = "2026.09.10-001"
	machines[1].Status.Version.Liken = "2026.09.09-001"

	layer, scrape := fleetMetrics(t)
	layer.observePass(fleetDecision{},
		decideFor(machines, renewals, heartbeatTrust{}, targetingCluster(1, "2026.09.10-001"), "", passTime))

	requireSeries(t, scrape(), "liken_machines_behind_target 1")
}

func TestAFleetWithNoTargetHasNobodyBehind(t *testing.T) {
	machines, renewals := rolloutInputs(
		rolloutEntry{"node-1", api.PhaseReady, fresh, false, -1},
		rolloutEntry{"node-3", api.PhaseReady, fresh, false, -1},
	)
	machines[0].Status.Version.Liken = "2026.09.10-001"

	layer, scrape := fleetMetrics(t)
	layer.observePass(fleetDecision{}, decideFor(machines, renewals, heartbeatTrust{}, labCluster(1), "", passTime))

	requireSeries(t, scrape(), "liken_machines_behind_target 0")
}

func TestRepeatedScrapesLeaveTheFleetSeriesUnchanged(t *testing.T) {
	machines, renewals := fleetInputs(
		fleetEntry{"node-1", api.PhaseReady, 10 * time.Second},
		fleetEntry{"node-2", api.PhaseDegraded, 10 * time.Second},
	)
	layer, scrape := fleetMetrics(t)
	layer.observePass(decideFleet(machines, renewals, passTime), rollout{behind: 1})

	first, second := scrape(), scrape()
	for _, name := range []string{
		"liken_machines",
		"liken_machines_behind_target",
		"liken_disruption_approvals_pending",
		"liken_build_info",
	} {
		t.Run(name, func(t *testing.T) {
			if !slices.Equal(series(first, name), series(second, name)) {
				t.Errorf("%s changed between scrapes: %q then %q", name, series(first, name), series(second, name))
			}
		})
	}
}
