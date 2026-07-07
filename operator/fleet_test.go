package main

// The fleet sweep's decision table: who counts as ready, who gets
// declared Lost, and who is left alone.

import (
	"strings"
	"testing"
	"time"

	"github.com/chrisguidry/liken/machine"
)

var sweepNow = time.Date(2026, 7, 6, 12, 0, 0, 0, time.UTC)

// A fleetEntry is one machine as the sweep would see it: a name, a
// phase, and a heartbeat lease renewed some age ago (negative means
// the machine has no lease at all).
type fleetEntry struct {
	name  string
	phase machine.Phase
	age   time.Duration
}

// fleetInputs builds the sweep's two inputs together: the Machine
// list and the heartbeat renewals read from the machines' leases.
func fleetInputs(entries ...fleetEntry) ([]machine.Machine, map[string]time.Time) {
	var machines []machine.Machine
	renewals := map[string]time.Time{}
	for _, e := range entries {
		m := machine.Machine{Metadata: machine.ObjectMeta{Name: e.name}}
		m.Status.Phase = e.phase
		machines = append(machines, m)
		if e.age >= 0 {
			renewals[e.name] = sweepNow.Add(-e.age)
		}
	}
	return machines, renewals
}

func TestSweepCountsFreshReadyMachines(t *testing.T) {
	machines, renewals := fleetInputs(
		fleetEntry{"node-1", machine.PhaseReady, 10 * time.Second},
		fleetEntry{"node-2", machine.PhaseReady, 30 * time.Second},
		fleetEntry{"node-3", machine.PhaseUpdatePending, 10 * time.Second},
	)
	s := decideFleetSweep(machines, renewals, "node-1", sweepNow)
	if s.tally.Ready != 2 || s.tally.Total != 3 || s.tally.Summary != "2/3" {
		t.Errorf("got %+v", s.tally)
	}
	if len(s.lost) != 0 {
		t.Errorf("nobody here is silent: %v", s.lost)
	}
	if s.phase != machine.PhaseUpdating {
		t.Errorf("a fleet whose only exception is mid-update is Updating, got %s", s.phase)
	}
	if s.condition.Status != "False" || s.condition.Reason != "MachinesUpdating" {
		t.Errorf("got %s/%s", s.condition.Status, s.condition.Reason)
	}
	if !strings.Contains(s.condition.Message, "node-3") {
		t.Errorf("the condition should name who is mid-transition: %s", s.condition.Message)
	}
}

func TestSweepOfAWhollyReadyFleet(t *testing.T) {
	machines, renewals := fleetInputs(
		fleetEntry{"node-1", machine.PhaseReady, 10 * time.Second},
		fleetEntry{"node-2", machine.PhaseReady, 30 * time.Second},
	)
	s := decideFleetSweep(machines, renewals, "node-1", sweepNow)
	if s.phase != machine.PhaseReady {
		t.Errorf("everyone ready means the cluster is, got %s", s.phase)
	}
	if s.condition.Status != "True" || s.condition.Reason != "AllMachinesReady" {
		t.Errorf("got %s/%s", s.condition.Status, s.condition.Reason)
	}
}

func TestSweepDeclaresSilentMachinesLost(t *testing.T) {
	machines, renewals := fleetInputs(
		fleetEntry{"node-1", machine.PhaseReady, 10 * time.Second},
		fleetEntry{"node-2", machine.PhaseReady, 5 * time.Minute},
	)
	s := decideFleetSweep(machines, renewals, "node-1", sweepNow)
	if s.tally.Summary != "1/2" {
		t.Errorf("a stale Ready must not count: %+v", s.tally)
	}
	if len(s.lost) != 1 || s.lost[0] != "node-2" {
		t.Errorf("got %v", s.lost)
	}
	if s.phase != machine.PhaseDegraded {
		t.Errorf("a lost machine degrades the cluster, got %s", s.phase)
	}
	if s.condition.Reason != "MachinesDegraded" || !strings.Contains(s.condition.Message, "node-2") {
		t.Errorf("the condition should name the unwell machine: %s/%s", s.condition.Reason, s.condition.Message)
	}
}

func TestSweepDegradedOutweighsUpdating(t *testing.T) {
	machines, renewals := fleetInputs(
		fleetEntry{"node-1", machine.PhaseReady, 10 * time.Second},
		fleetEntry{"node-2", machine.PhaseBlocked, 10 * time.Second},
		fleetEntry{"node-3", machine.PhaseUpdatePending, 10 * time.Second},
	)
	s := decideFleetSweep(machines, renewals, "node-1", sweepNow)
	if s.phase != machine.PhaseDegraded {
		t.Errorf("a blocked machine outweighs a rolling update, got %s", s.phase)
	}
}

func TestSweepLeavesAlreadyLostMachinesAlone(t *testing.T) {
	machines, renewals := fleetInputs(
		fleetEntry{"node-1", machine.PhaseReady, 10 * time.Second},
		fleetEntry{"node-2", machine.PhaseLost, 5 * time.Minute},
	)
	s := decideFleetSweep(machines, renewals, "node-1", sweepNow)
	if len(s.lost) != 0 {
		t.Errorf("re-marking a Lost machine is churn: %v", s.lost)
	}
	if s.tally.Summary != "1/2" {
		t.Errorf("got %+v", s.tally)
	}
	if s.phase != machine.PhaseDegraded {
		t.Errorf("a lost machine keeps the cluster degraded, got %s", s.phase)
	}
}

func TestSweepNeverDeclaresItselfLost(t *testing.T) {
	// The sweeper's own heartbeat may look stale in the list it just
	// read (its renewal could still be landing), but it is running
	// this very code: its liveness is not in question.
	machines, renewals := fleetInputs(
		fleetEntry{"node-1", machine.PhaseReady, 5 * time.Minute},
	)
	s := decideFleetSweep(machines, renewals, "node-1", sweepNow)
	if len(s.lost) != 0 {
		t.Errorf("the sweeper is self-evidently alive: %v", s.lost)
	}
	if s.tally.Summary != "1/1" {
		t.Errorf("got %+v", s.tally)
	}
}

func TestSweepTreatsAMissingHeartbeatAsSilence(t *testing.T) {
	// A Machine with no lease at all has never had an operator
	// heartbeat: declared, perhaps, but never heard from.
	machines, renewals := fleetInputs(
		fleetEntry{"node-1", machine.PhaseReady, 10 * time.Second},
		fleetEntry{"node-2", "", -1},
	)
	s := decideFleetSweep(machines, renewals, "node-1", sweepNow)
	if len(s.lost) != 1 || s.lost[0] != "node-2" {
		t.Errorf("got %v", s.lost)
	}
	if s.tally.Summary != "1/2" {
		t.Errorf("got %+v", s.tally)
	}
}

func TestSweepReadsGrantedSilenceAsTheRebootInProgress(t *testing.T) {
	// A machine holding a fresh reboot grant that goes silent is doing
	// exactly what it was told; the sweep counts it mid-transition and
	// does not declare it Lost until the grant is old enough to be a
	// stall.
	machines, renewals := fleetInputs(
		fleetEntry{"node-1", machine.PhaseReady, 10 * time.Second},
		fleetEntry{"node-2", machine.PhaseUpdating, 3 * time.Minute},
	)
	machines[1].Status.Conditions = machine.SetCondition(nil, machine.Condition{
		Type: "RebootApproved", Status: "True", Reason: "DisruptionBudgetAllows",
	}, sweepNow.Add(-3*time.Minute))
	s := decideFleetSweep(machines, renewals, "node-1", sweepNow)
	if len(s.lost) != 0 {
		t.Errorf("this silence was requested: %v", s.lost)
	}
	if s.phase != machine.PhaseUpdating {
		t.Errorf("a granted reboot is a transition, not an illness: %s", s.phase)
	}
}

func TestSweepDeclaresAGrantedMachineLostAfterTheStallWindow(t *testing.T) {
	machines, renewals := fleetInputs(
		fleetEntry{"node-1", machine.PhaseReady, 10 * time.Second},
		fleetEntry{"node-2", machine.PhaseUpdating, 12 * time.Minute},
	)
	machines[1].Status.Conditions = machine.SetCondition(nil, machine.Condition{
		Type: "RebootApproved", Status: "True", Reason: "DisruptionBudgetAllows",
	}, sweepNow.Add(-12*time.Minute))
	s := decideFleetSweep(machines, renewals, "node-1", sweepNow)
	if len(s.lost) != 1 || s.lost[0] != "node-2" {
		t.Errorf("past the stall window the honest word is Lost: %v", s.lost)
	}
}
