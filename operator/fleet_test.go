package main

// The fleet sweep's decision table: who counts as ready, who gets
// declared Lost, and who is left alone.

import (
	"testing"
	"time"

	"github.com/chrisguidry/liken/machine"
)

var sweepNow = time.Date(2026, 7, 6, 12, 0, 0, 0, time.UTC)

// fleetMachine builds one machine as the sweep would see it: a name,
// a phase, and a heartbeat some age ago (negative means no heartbeat
// was ever written).
func fleetMachine(name, phase string, heartbeatAge time.Duration) machine.Machine {
	m := machine.Machine{Metadata: machine.ObjectMeta{Name: name}}
	m.Status.Phase = phase
	if heartbeatAge >= 0 {
		at := sweepNow.Add(-heartbeatAge)
		m.Status.ObservedAt = &at
	}
	return m
}

func TestSweepCountsFreshReadyMachines(t *testing.T) {
	s := decideFleetSweep([]machine.Machine{
		fleetMachine("node-1", machine.PhaseReady, 10*time.Second),
		fleetMachine("node-2", machine.PhaseReady, 30*time.Second),
		fleetMachine("node-3", machine.PhaseUpdatePending, 10*time.Second),
	}, "node-1", sweepNow)
	if s.tally.Ready != 2 || s.tally.Total != 3 || s.tally.Summary != "2/3" {
		t.Errorf("got %+v", s.tally)
	}
	if len(s.lost) != 0 {
		t.Errorf("nobody here is silent: %v", s.lost)
	}
	if s.phase != machine.PhaseUpdating {
		t.Errorf("a fleet whose only exception is mid-update is Updating, got %s", s.phase)
	}
}

func TestSweepOfAWhollyReadyFleet(t *testing.T) {
	s := decideFleetSweep([]machine.Machine{
		fleetMachine("node-1", machine.PhaseReady, 10*time.Second),
		fleetMachine("node-2", machine.PhaseReady, 30*time.Second),
	}, "node-1", sweepNow)
	if s.phase != machine.PhaseReady {
		t.Errorf("everyone ready means the cluster is, got %s", s.phase)
	}
}

func TestSweepDeclaresSilentMachinesLost(t *testing.T) {
	s := decideFleetSweep([]machine.Machine{
		fleetMachine("node-1", machine.PhaseReady, 10*time.Second),
		fleetMachine("node-2", machine.PhaseReady, 5*time.Minute),
	}, "node-1", sweepNow)
	if s.tally.Summary != "1/2" {
		t.Errorf("a stale Ready must not count: %+v", s.tally)
	}
	if len(s.lost) != 1 || s.lost[0] != "node-2" {
		t.Errorf("got %v", s.lost)
	}
	if s.phase != machine.PhaseDegraded {
		t.Errorf("a lost machine degrades the cluster, got %s", s.phase)
	}
}

func TestSweepDegradedOutweighsUpdating(t *testing.T) {
	s := decideFleetSweep([]machine.Machine{
		fleetMachine("node-1", machine.PhaseReady, 10*time.Second),
		fleetMachine("node-2", machine.PhaseBlocked, 10*time.Second),
		fleetMachine("node-3", machine.PhaseUpdatePending, 10*time.Second),
	}, "node-1", sweepNow)
	if s.phase != machine.PhaseDegraded {
		t.Errorf("a blocked machine outweighs a rolling update, got %s", s.phase)
	}
}

func TestSweepLeavesAlreadyLostMachinesAlone(t *testing.T) {
	s := decideFleetSweep([]machine.Machine{
		fleetMachine("node-1", machine.PhaseReady, 10*time.Second),
		fleetMachine("node-2", machine.PhaseLost, 5*time.Minute),
	}, "node-1", sweepNow)
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
	// read (its status write could still be landing), but it is
	// running this very code: its liveness is not in question.
	s := decideFleetSweep([]machine.Machine{
		fleetMachine("node-1", machine.PhaseReady, 5*time.Minute),
	}, "node-1", sweepNow)
	if len(s.lost) != 0 {
		t.Errorf("the sweeper is self-evidently alive: %v", s.lost)
	}
	if s.tally.Summary != "1/1" {
		t.Errorf("got %+v", s.tally)
	}
}

func TestSweepTreatsAMissingHeartbeatAsSilence(t *testing.T) {
	// A Machine with no observedAt at all has never had an operator
	// heartbeat: declared, perhaps, but never heard from.
	s := decideFleetSweep([]machine.Machine{
		fleetMachine("node-1", machine.PhaseReady, 10*time.Second),
		fleetMachine("node-2", "", -1),
	}, "node-1", sweepNow)
	if len(s.lost) != 1 || s.lost[0] != "node-2" {
		t.Errorf("got %v", s.lost)
	}
	if s.tally.Summary != "1/2" {
		t.Errorf("got %+v", s.tally)
	}
}
