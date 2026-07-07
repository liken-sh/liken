package main

// The rollout conductor's decision table: who gets a reboot turn, in
// what order, and when the whole procession pauses.

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/chrisguidry/liken/machine"
)

// A rolloutEntry is one machine as the conductor sees it: its phase,
// its heartbeat age (negative means no lease), whether it is asking
// for a reboot turn, and how long ago it was granted one (negative
// means no grant outstanding).
type rolloutEntry struct {
	name       string
	phase      machine.Phase
	age        time.Duration
	awaiting   bool
	grantedAgo time.Duration
}

func rolloutInputs(entries ...rolloutEntry) ([]machine.Machine, map[string]time.Time) {
	var machines []machine.Machine
	renewals := map[string]time.Time{}
	for _, e := range entries {
		m := machine.Machine{Metadata: machine.ObjectMeta{Name: e.name}}
		m.Status.Phase = e.phase
		if e.awaiting {
			m.Status.Conditions = machine.SetCondition(m.Status.Conditions, machine.Condition{
				Type: "SpecConverged", Status: "False", Reason: "AwaitingTurn",
			}, sweepNow)
		}
		if e.grantedAgo >= 0 {
			m.Status.Conditions = machine.SetCondition(m.Status.Conditions, machine.Condition{
				Type: "RebootApproved", Status: "True", Reason: "DisruptionBudgetAllows",
			}, sweepNow.Add(-e.grantedAgo))
		}
		machines = append(machines, m)
		if e.age >= 0 {
			renewals[e.name] = sweepNow.Add(-e.age)
		}
	}
	return machines, renewals
}

// labCluster declares node-1 and node-2 the leaders, so every other
// name in these tests is a worker.
func labCluster(maxUnavailable int) *machine.Cluster {
	c := &machine.Cluster{}
	c.Spec.Leaders = []string{"node-1", "node-2"}
	c.Spec.Disruption.MaxUnavailable = maxUnavailable
	return c
}

const fresh = 10 * time.Second

func TestRolloutGrantsAWaitingMachine(t *testing.T) {
	machines, renewals := rolloutInputs(
		rolloutEntry{"node-1", machine.PhaseReady, fresh, false, -1},
		rolloutEntry{"node-3", machine.PhaseUpdatePending, fresh, true, -1},
	)
	r := decideRollout(machines, renewals, labCluster(0), "node-1", sweepNow)
	if !slices.Equal(r.grant, []string{"node-3"}) {
		t.Errorf("got grants %v", r.grant)
	}
	if r.progressing.Status != "True" || r.progressing.Reason != "RollingOut" {
		t.Errorf("got %s/%s", r.progressing.Status, r.progressing.Reason)
	}
}

func TestRolloutAtRestIsComplete(t *testing.T) {
	machines, renewals := rolloutInputs(
		rolloutEntry{"node-1", machine.PhaseReady, fresh, false, -1},
		rolloutEntry{"node-3", machine.PhaseReady, fresh, false, -1},
	)
	r := decideRollout(machines, renewals, labCluster(0), "node-1", sweepNow)
	if len(r.grant) != 0 || len(r.revoke) != 0 {
		t.Errorf("nothing to do here: grant %v revoke %v", r.grant, r.revoke)
	}
	if r.progressing.Status != "True" || r.progressing.Reason != "RolloutComplete" {
		t.Errorf("got %s/%s", r.progressing.Status, r.progressing.Reason)
	}
}

func TestRolloutHonorsTheDefaultBudgetOfOne(t *testing.T) {
	machines, renewals := rolloutInputs(
		rolloutEntry{"node-1", machine.PhaseReady, fresh, false, -1},
		rolloutEntry{"node-3", machine.PhaseUpdatePending, fresh, true, -1},
		rolloutEntry{"node-4", machine.PhaseUpdatePending, fresh, true, -1},
	)
	r := decideRollout(machines, renewals, labCluster(0), "node-1", sweepNow)
	if !slices.Equal(r.grant, []string{"node-3"}) {
		t.Errorf("one turn at a time by default: %v", r.grant)
	}
	if !strings.Contains(r.progressing.Message, "node-4") {
		t.Errorf("the condition should name who is still waiting: %s", r.progressing.Message)
	}
}

func TestRolloutABiggerBudgetGrantsMoreTurns(t *testing.T) {
	machines, renewals := rolloutInputs(
		rolloutEntry{"node-1", machine.PhaseReady, fresh, false, -1},
		rolloutEntry{"node-3", machine.PhaseUpdatePending, fresh, true, -1},
		rolloutEntry{"node-4", machine.PhaseUpdatePending, fresh, true, -1},
		rolloutEntry{"node-5", machine.PhaseUpdatePending, fresh, true, -1},
	)
	r := decideRollout(machines, renewals, labCluster(2), "node-1", sweepNow)
	if !slices.Equal(r.grant, []string{"node-3", "node-4"}) {
		t.Errorf("got %v", r.grant)
	}
}

func TestRolloutGrantsWorkersBeforeLeaders(t *testing.T) {
	machines, renewals := rolloutInputs(
		rolloutEntry{"node-1", machine.PhaseUpdatePending, fresh, true, -1},
		rolloutEntry{"node-4", machine.PhaseUpdatePending, fresh, true, -1},
	)
	r := decideRollout(machines, renewals, labCluster(0), "node-2", sweepNow)
	if !slices.Equal(r.grant, []string{"node-4"}) {
		t.Errorf("the worker goes first: %v", r.grant)
	}
}

func TestRolloutGrantsInNameOrder(t *testing.T) {
	machines, renewals := rolloutInputs(
		rolloutEntry{"node-5", machine.PhaseUpdatePending, fresh, true, -1},
		rolloutEntry{"node-4", machine.PhaseUpdatePending, fresh, true, -1},
	)
	r := decideRollout(machines, renewals, labCluster(0), "node-1", sweepNow)
	if !slices.Equal(r.grant, []string{"node-4"}) {
		t.Errorf("deterministic order, lowest name first: %v", r.grant)
	}
}

func TestRolloutOneLeaderAtATimeRegardlessOfBudget(t *testing.T) {
	machines, renewals := rolloutInputs(
		rolloutEntry{"node-1", machine.PhaseUpdatePending, fresh, true, -1},
		rolloutEntry{"node-2", machine.PhaseUpdatePending, fresh, true, -1},
	)
	r := decideRollout(machines, renewals, labCluster(3), "node-1", sweepNow)
	if !slices.Equal(r.grant, []string{"node-1"}) {
		t.Errorf("quorum is arithmetic: %v", r.grant)
	}
}

func TestRolloutNeverGrantsALeaderWhileAnotherLeaderIsDown(t *testing.T) {
	machines, renewals := rolloutInputs(
		rolloutEntry{"node-1", machine.PhaseUpdatePending, fresh, true, -1},
		rolloutEntry{"node-2", machine.PhaseLost, 5 * time.Minute, false, -1},
	)
	r := decideRollout(machines, renewals, labCluster(3), "node-1", sweepNow)
	if len(r.grant) != 0 {
		t.Errorf("a downed leader freezes leader turns: %v", r.grant)
	}
}

func TestRolloutUnplannedTroubleConsumesTheBudget(t *testing.T) {
	machines, renewals := rolloutInputs(
		rolloutEntry{"node-1", machine.PhaseReady, fresh, false, -1},
		rolloutEntry{"node-4", machine.PhaseLost, 5 * time.Minute, false, -1},
		rolloutEntry{"node-5", machine.PhaseUpdatePending, fresh, true, -1},
	)
	r := decideRollout(machines, renewals, labCluster(0), "node-1", sweepNow)
	if len(r.grant) != 0 {
		t.Errorf("a hurting fleet pauses its own rollout: %v", r.grant)
	}
}

func TestRolloutAnOutstandingGrantConsumesTheBudget(t *testing.T) {
	machines, renewals := rolloutInputs(
		rolloutEntry{"node-1", machine.PhaseReady, fresh, false, -1},
		rolloutEntry{"node-4", machine.PhaseUpdatePending, fresh, true, time.Minute},
		rolloutEntry{"node-5", machine.PhaseUpdatePending, fresh, true, -1},
	)
	r := decideRollout(machines, renewals, labCluster(0), "node-1", sweepNow)
	if len(r.grant) != 0 {
		t.Errorf("node-4 already holds the turn: %v", r.grant)
	}
	if len(r.revoke) != 0 {
		t.Errorf("node-4 is still waiting to use it: %v", r.revoke)
	}
}

func TestRolloutKeepsTheGrantThroughTheReboot(t *testing.T) {
	// The granted machine has gone silent — that silence is the reboot
	// the conductor asked for, not a loss, so the grant stands and the
	// budget stays consumed.
	machines, renewals := rolloutInputs(
		rolloutEntry{"node-1", machine.PhaseReady, fresh, false, -1},
		rolloutEntry{"node-4", machine.PhaseUpdating, 3 * time.Minute, false, 3 * time.Minute},
		rolloutEntry{"node-5", machine.PhaseUpdatePending, fresh, true, -1},
	)
	r := decideRollout(machines, renewals, labCluster(0), "node-1", sweepNow)
	if len(r.grant) != 0 || len(r.revoke) != 0 {
		t.Errorf("mid-reboot means wait: grant %v revoke %v", r.grant, r.revoke)
	}
	if r.progressing.Reason != "RollingOut" {
		t.Errorf("got %s", r.progressing.Reason)
	}
}

func TestRolloutRevokesAfterTheMachineConverges(t *testing.T) {
	machines, renewals := rolloutInputs(
		rolloutEntry{"node-1", machine.PhaseReady, fresh, false, -1},
		rolloutEntry{"node-4", machine.PhaseReady, fresh, false, 5 * time.Minute},
		rolloutEntry{"node-5", machine.PhaseUpdatePending, fresh, true, -1},
	)
	r := decideRollout(machines, renewals, labCluster(0), "node-1", sweepNow)
	if !slices.Equal(r.revoke, []string{"node-4"}) {
		t.Errorf("the turn is spent: %v", r.revoke)
	}
	if !slices.Equal(r.grant, []string{"node-5"}) {
		t.Errorf("the next machine takes its turn: %v", r.grant)
	}
}

func TestRolloutStallsOnAMachineThatNeverReturns(t *testing.T) {
	machines, renewals := rolloutInputs(
		rolloutEntry{"node-1", machine.PhaseReady, fresh, false, -1},
		rolloutEntry{"node-4", machine.PhaseUpdating, 12 * time.Minute, false, 12 * time.Minute},
		rolloutEntry{"node-5", machine.PhaseUpdatePending, fresh, true, -1},
	)
	r := decideRollout(machines, renewals, labCluster(3), "node-1", sweepNow)
	if len(r.grant) != 0 {
		t.Errorf("a stalled rollout grants nothing: %v", r.grant)
	}
	if r.progressing.Status != "False" || r.progressing.Reason != "RolloutStalled" {
		t.Errorf("got %s/%s", r.progressing.Status, r.progressing.Reason)
	}
	if !strings.Contains(r.progressing.Message, "node-4") {
		t.Errorf("the condition should name the machine holding things up: %s", r.progressing.Message)
	}
}

func TestRolloutAManualMachineDoesNotHoldTheQueue(t *testing.T) {
	// A Manual machine reports RebootPending, not AwaitingTurn: it is
	// waiting on its human, not on the cluster, so it gets no grant and
	// blocks nobody.
	machines, renewals := rolloutInputs(
		rolloutEntry{"node-1", machine.PhaseReady, fresh, false, -1},
		rolloutEntry{"node-4", machine.PhaseUpdatePending, fresh, false, -1},
		rolloutEntry{"node-5", machine.PhaseUpdatePending, fresh, true, -1},
	)
	r := decideRollout(machines, renewals, labCluster(0), "node-1", sweepNow)
	if !slices.Equal(r.grant, []string{"node-5"}) {
		t.Errorf("got %v", r.grant)
	}
}

func TestRolloutRevokesAGrantTheMachineNoLongerWants(t *testing.T) {
	// Granted, then the edit was reverted (or the policy flipped to
	// Manual) before the machine acted: it is available and no longer
	// asking, so the grant comes back.
	machines, renewals := rolloutInputs(
		rolloutEntry{"node-1", machine.PhaseReady, fresh, false, -1},
		rolloutEntry{"node-4", machine.PhaseReady, fresh, false, time.Minute},
	)
	r := decideRollout(machines, renewals, labCluster(0), "node-1", sweepNow)
	if !slices.Equal(r.revoke, []string{"node-4"}) {
		t.Errorf("got %v", r.revoke)
	}
}
