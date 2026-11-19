package main

// This file tests decideFleet: which machines count as ready,
// which are marked Lost, and which are left alone.

import (
	"strings"
	"testing"
	"time"

	"github.com/liken-sh/liken/liken/api"
	"github.com/liken-sh/liken/liken/machine"
)

var passTime = time.Date(2026, 7, 6, 12, 0, 0, 0, time.UTC)

// A fleetEntry describes one machine: its name, its phase, and how
// long ago its heartbeat lease was renewed. A negative age means the
// machine has no lease.
type fleetEntry struct {
	name  string
	phase api.Phase
	age   time.Duration
}

// fleetInputs builds the Machine list and the map of lease renewal
// times for the entries.
func fleetInputs(entries ...fleetEntry) ([]machine.Machine, map[string]time.Time) {
	var machines []machine.Machine
	renewals := map[string]time.Time{}
	for _, e := range entries {
		m := machine.Machine{Metadata: api.ObjectMeta{Name: e.name}}
		m.Status.Phase = e.phase
		machines = append(machines, m)
		if e.age >= 0 {
			renewals[e.name] = passTime.Add(-e.age)
		}
	}
	return machines, renewals
}

func TestDecideFleetCountsFreshReadyMachines(t *testing.T) {
	machines, renewals := fleetInputs(
		fleetEntry{"node-1", api.PhaseReady, 10 * time.Second},
		fleetEntry{"node-2", api.PhaseReady, 30 * time.Second},
		fleetEntry{"node-3", api.PhaseUpdatePending, 10 * time.Second},
	)
	s := decideFleet(machines, renewals, passTime)
	if s.machineCounts.Ready != 2 || s.machineCounts.Total != 3 || s.machineCounts.Summary != "2/3" {
		t.Errorf("machine counts are %+v, want 2 Ready of 3 with summary 2/3", s.machineCounts)
	}
	if len(s.lost) != 0 {
		t.Errorf("lost is %v, want none because every heartbeat is fresh", s.lost)
	}
	if s.phase != api.PhaseUpdating {
		t.Errorf("phase is %s, want Updating because the only machine not Ready is mid-update", s.phase)
	}
	if s.condition.Status != "False" || s.condition.Reason != "MachinesUpdating" {
		t.Errorf("condition is %s/%s, want False/MachinesUpdating", s.condition.Status, s.condition.Reason)
	}
	if !strings.Contains(s.condition.Message, "node-3") {
		t.Errorf("the condition message is %q, want it to name node-3, which is mid-update", s.condition.Message)
	}
}

func TestDecideFleetOfAWhollyReadyFleet(t *testing.T) {
	machines, renewals := fleetInputs(
		fleetEntry{"node-1", api.PhaseReady, 10 * time.Second},
		fleetEntry{"node-2", api.PhaseReady, 30 * time.Second},
	)
	s := decideFleet(machines, renewals, passTime)
	if s.phase != api.PhaseReady {
		t.Errorf("phase is %s, want Ready because every machine is Ready", s.phase)
	}
	if s.condition.Status != "True" || s.condition.Reason != "AllMachinesReady" {
		t.Errorf("condition is %s/%s, want True/AllMachinesReady", s.condition.Status, s.condition.Reason)
	}
}

func TestDecideFleetMarksSilentMachinesLost(t *testing.T) {
	machines, renewals := fleetInputs(
		fleetEntry{"node-1", api.PhaseReady, 10 * time.Second},
		fleetEntry{"node-2", api.PhaseReady, 5 * time.Minute},
	)
	s := decideFleet(machines, renewals, passTime)
	if s.machineCounts.Summary != "1/2" {
		t.Errorf("machine counts are %+v, want summary 1/2 because node-2's Ready is stale", s.machineCounts)
	}
	if len(s.lost) != 1 || s.lost[0] != "node-2" {
		t.Errorf("lost is %v, want [node-2]", s.lost)
	}
	if s.phase != api.PhaseDegraded {
		t.Errorf("phase is %s, want Degraded because node-2 is lost", s.phase)
	}
	if s.condition.Reason != "MachinesDegraded" || !strings.Contains(s.condition.Message, "node-2") {
		t.Errorf("condition is %s/%q, want MachinesDegraded naming node-2", s.condition.Reason, s.condition.Message)
	}
}

func TestDecideFleetReportsDegradedOverUpdating(t *testing.T) {
	machines, renewals := fleetInputs(
		fleetEntry{"node-1", api.PhaseReady, 10 * time.Second},
		fleetEntry{"node-2", api.PhaseBlocked, 10 * time.Second},
		fleetEntry{"node-3", api.PhaseUpdatePending, 10 * time.Second},
	)
	s := decideFleet(machines, renewals, passTime)
	if s.phase != api.PhaseDegraded {
		t.Errorf("phase is %s, want Degraded because a Blocked machine outweighs a rolling update", s.phase)
	}
}

func TestDecideFleetLeavesAlreadyLostMachinesAlone(t *testing.T) {
	machines, renewals := fleetInputs(
		fleetEntry{"node-1", api.PhaseReady, 10 * time.Second},
		fleetEntry{"node-2", api.PhaseLost, 5 * time.Minute},
	)
	s := decideFleet(machines, renewals, passTime)
	if len(s.lost) != 0 {
		t.Errorf("lost is %v, want none because node-2 is already Lost", s.lost)
	}
	if s.machineCounts.Summary != "1/2" {
		t.Errorf("machine counts are %+v, want summary 1/2", s.machineCounts)
	}
	if s.phase != api.PhaseDegraded {
		t.Errorf("phase is %s, want Degraded because node-2 is still Lost", s.phase)
	}
}

func TestDecideFleetTreatsAMissingHeartbeatAsSilence(t *testing.T) {
	// A declared Machine with no heartbeat lease counts as silent.
	machines, renewals := fleetInputs(
		fleetEntry{"node-1", api.PhaseReady, 10 * time.Second},
		fleetEntry{"node-2", "", -1},
	)
	s := decideFleet(machines, renewals, passTime)
	if len(s.lost) != 1 || s.lost[0] != "node-2" {
		t.Errorf("lost is %v, want [node-2] because it has no heartbeat Lease", s.lost)
	}
	if s.machineCounts.Summary != "1/2" {
		t.Errorf("machine counts are %+v, want summary 1/2", s.machineCounts)
	}
}

func TestDecideFleetTreatsSilenceAfterARebootGrantAsRebooting(t *testing.T) {
	// A machine with a recent reboot grant is expected to stop
	// renewing its lease while it reboots. The pass counts it as
	// mid-transition, and marks it Lost only when the grant is older
	// than rolloutStallAfter.
	machines, renewals := fleetInputs(
		fleetEntry{"node-1", api.PhaseReady, 10 * time.Second},
		fleetEntry{"node-2", api.PhaseUpdating, 3 * time.Minute},
	)
	machines[1].Status.Conditions = api.SetCondition(nil, api.Condition{
		Type: "RebootApproved", Status: "True", Reason: "DisruptionBudgetAllows",
	}, passTime.Add(-3*time.Minute))
	s := decideFleet(machines, renewals, passTime)
	if len(s.lost) != 0 {
		t.Errorf("lost is %v, want none because node-2 has a recent reboot grant", s.lost)
	}
	if s.phase != api.PhaseUpdating {
		t.Errorf("phase is %s, want Updating because a granted reboot is a transition", s.phase)
	}
}

func TestDecideFleetMarksAGrantedMachineLostAfterRolloutStallAfter(t *testing.T) {
	machines, renewals := fleetInputs(
		fleetEntry{"node-1", api.PhaseReady, 10 * time.Second},
		fleetEntry{"node-2", api.PhaseUpdating, 12 * time.Minute},
	)
	machines[1].Status.Conditions = api.SetCondition(nil, api.Condition{
		Type: "RebootApproved", Status: "True", Reason: "DisruptionBudgetAllows",
	}, passTime.Add(-12*time.Minute))
	s := decideFleet(machines, renewals, passTime)
	if len(s.lost) != 1 || s.lost[0] != "node-2" {
		t.Errorf("lost is %v, want [node-2] because its reboot grant is older than rolloutStallAfter", s.lost)
	}
}
