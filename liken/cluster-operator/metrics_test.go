package main

// Layer 3 for the fleet, proven the way a scraper reads it: the
// sweep's own verdict handed to the observer, then a real scrape of
// a real registry. fleetMetrics in helpers_test.go builds both ends.

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/liken-sh/liken/api"
	"github.com/liken-sh/liken/machine"
)

// series returns every sample line in a scrape whose metric name is
// name, so a test compares numbers and never help text.
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
		t.Errorf("the scrape has no %q; it has %q", line, found)
	}
}

func TestTheListenerStartsWithTheFleetsOwnMetrics(t *testing.T) {
	o, layer := serveMetrics("127.0.0.1:0")
	if o == nil || layer == nil {
		t.Fatal("the operator started with no metrics")
	}
	// The registry answers with both layers: the build info every
	// liken process publishes, and the fleet's own gauges.
	body := scrapeHandler(t, o.Handler())
	requireSeries(t, body, `liken_machines{phase="Ready"} 0`)
	if len(series(body, "liken_build_info")) != 1 {
		t.Errorf("the scrape has no build info: %q", series(body, "liken_build_info"))
	}
}

func TestAnEmptyAddressStillBuildsTheRegistry(t *testing.T) {
	// An owner who runs no Prometheus gives the port back. The
	// operator still counts, so turning the listener on again needs
	// no restart of anything else.
	o, layer := serveMetrics("")
	if o == nil || layer == nil {
		t.Fatal("the operator started with no metrics")
	}
}

func TestMachinesCountByTheirEffectivePhase(t *testing.T) {
	machines, renewals := fleetInputs(
		fleetEntry{"node-1", api.PhaseReady, 10 * time.Second},
		fleetEntry{"node-2", api.PhaseReady, 30 * time.Second},
		fleetEntry{"node-3", api.PhaseUpdatePending, 10 * time.Second},
		// A machine whose heartbeat has aged out is Lost, whatever
		// its own last status claims.
		fleetEntry{"node-4", api.PhaseReady, 5 * time.Minute},
	)
	layer, scrape := fleetMetrics(t)
	layer.observeSweep(decideFleetSweep(machines, renewals, sweepNow), rollout{})

	body := scrape()
	requireSeries(t, body, `liken_machines{phase="Ready"} 2`)
	requireSeries(t, body, `liken_machines{phase="UpdatePending"} 1`)
	requireSeries(t, body, `liken_machines{phase="Lost"} 1`)
	requireSeries(t, body, `liken_machines{phase="Degraded"} 0`)
}

func TestAMachineWithNoStatusYetCountsAsUnknown(t *testing.T) {
	// A Machine that a machine operator has created and not yet
	// reported on holds no phase. The fleet has an object for it and
	// no observation of it, which is what Unknown says.
	machines, renewals := fleetInputs(fleetEntry{"node-1", "", 10 * time.Second})
	layer, scrape := fleetMetrics(t)
	layer.observeSweep(decideFleetSweep(machines, renewals, sweepNow), rollout{})

	requireSeries(t, scrape(), `liken_machines{phase="Unknown"} 1`)
}

func TestAPhaseWithNoMachinesReportsZero(t *testing.T) {
	layer, scrape := fleetMetrics(t)
	layer.observeSweep(decideFleetSweep(nil, nil, sweepNow), rollout{})

	body := scrape()
	for _, phase := range fleetPhases {
		t.Run(string(phase), func(t *testing.T) {
			requireSeries(t, body, `liken_machines{phase="`+string(phase)+`"} 0`)
		})
	}
}

// awaitingApproval is one machine that staged a change and stopped,
// because its reboot policy is Manual and no person has approved the
// staged hash yet.
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
		"node-1": sweepNow, "node-2": sweepNow, "node-3": sweepNow,
	}
	layer, scrape := fleetMetrics(t)
	layer.observeSweep(decideFleetSweep(machines, renewals, sweepNow), rollout{})

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
	layer.observeSweep(fleetSweep{},
		decideRollout(machines, renewals, targetingCluster(1, "2026.09.10-001"), "", sweepNow))

	requireSeries(t, scrape(), "liken_machines_behind_target 1")
}

func TestAFleetWithNoTargetHasNobodyBehind(t *testing.T) {
	machines, renewals := rolloutInputs(
		rolloutEntry{"node-1", api.PhaseReady, fresh, false, -1},
		rolloutEntry{"node-3", api.PhaseReady, fresh, false, -1},
	)
	machines[0].Status.Version.Liken = "2026.09.10-001"

	layer, scrape := fleetMetrics(t)
	layer.observeSweep(fleetSweep{}, decideRollout(machines, renewals, labCluster(1), "", sweepNow))

	requireSeries(t, scrape(), "liken_machines_behind_target 0")
}

func TestRepeatedScrapesLeaveTheFleetSeriesUnchanged(t *testing.T) {
	machines, renewals := fleetInputs(
		fleetEntry{"node-1", api.PhaseReady, 10 * time.Second},
		fleetEntry{"node-2", api.PhaseDegraded, 10 * time.Second},
	)
	layer, scrape := fleetMetrics(t)
	layer.observeSweep(decideFleetSweep(machines, renewals, sweepNow), rollout{behind: 1})

	first, second := scrape(), scrape()
	for _, name := range []string{
		"liken_machines",
		"liken_machines_behind_target",
		"liken_disruption_approvals_pending",
		"liken_build_info",
	} {
		t.Run(name, func(t *testing.T) {
			if !slices.Equal(series(first, name), series(second, name)) {
				t.Errorf("%s moved between scrapes: %q then %q", name, series(first, name), series(second, name))
			}
		})
	}
}
