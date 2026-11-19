package main

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/liken-sh/liken/liken/api"
	"github.com/liken-sh/liken/liken/cluster"
	"github.com/liken-sh/liken/liken/disruption"
	"github.com/liken-sh/liken/liken/machine"
)

// A rolloutEntry describes one machine: its phase, the age of its
// heartbeat (negative means no Lease), whether it requests a reboot
// turn, and how long ago it was granted one (negative means no grant).
type rolloutEntry struct {
	name       string
	phase      api.Phase
	age        time.Duration
	awaiting   bool
	grantedAgo time.Duration
}

func rolloutInputs(entries ...rolloutEntry) ([]machine.Machine, map[string]time.Time) {
	var machines []machine.Machine
	renewals := map[string]time.Time{}
	for _, e := range entries {
		m := machine.Machine{Metadata: api.ObjectMeta{Name: e.name}}
		m.Status.Phase = e.phase
		if e.awaiting {
			m.Status.Conditions = api.SetCondition(m.Status.Conditions, api.Condition{
				Type: "SpecConverged", Status: "False", Reason: "AwaitingTurn",
			}, passTime)
		}
		if e.grantedAgo >= 0 {
			m.Status.Conditions = api.SetCondition(m.Status.Conditions, api.Condition{
				Type: "RebootApproved", Status: "True", Reason: "DisruptionBudgetAllows",
			}, passTime.Add(-e.grantedAgo))
		}
		machines = append(machines, m)
		if e.age >= 0 {
			renewals[e.name] = passTime.Add(-e.age)
		}
	}
	return machines, renewals
}

// labCluster declares node-1 and node-2 the leaders. Every other
// machine is a worker.
func labCluster(maxUnavailable int) *cluster.Cluster {
	c := &cluster.Cluster{}
	c.Spec.Leaders = []string{"node-1", "node-2"}
	c.Spec.Disruption.MaxUnavailable = maxUnavailable
	return c
}

// targetingCluster is labCluster with a target release.
func targetingCluster(maxUnavailable int, target string) *cluster.Cluster {
	c := labCluster(maxUnavailable)
	c.Spec.Version = target
	return c
}

const fresh = 10 * time.Second

func TestRolloutGrantsAWaitingMachine(t *testing.T) {
	machines, renewals := rolloutInputs(
		rolloutEntry{"node-1", api.PhaseReady, fresh, false, -1},
		rolloutEntry{"node-3", api.PhaseUpdatePending, fresh, true, -1},
	)
	r := decideFor(machines, renewals, time.Time{}, labCluster(0), "", passTime)
	if !slices.Equal(r.grant, []string{"node-3"}) {
		t.Errorf("grants are %v, want [node-3]", r.grant)
	}
	if r.progressing.Status != "True" || r.progressing.Reason != "RollingOut" {
		t.Errorf("Progressing is %s/%s, want %s/%s", r.progressing.Status, r.progressing.Reason, "True", "RollingOut")
	}
}

func TestRolloutAtRestIsComplete(t *testing.T) {
	machines, renewals := rolloutInputs(
		rolloutEntry{"node-1", api.PhaseReady, fresh, false, -1},
		rolloutEntry{"node-3", api.PhaseReady, fresh, false, -1},
	)
	r := decideFor(machines, renewals, time.Time{}, labCluster(0), "", passTime)
	if len(r.grant) != 0 || len(r.revoke) != 0 {
		t.Errorf("grants are %v and revokes are %v, want none of either", r.grant, r.revoke)
	}
	if r.progressing.Status != "True" || r.progressing.Reason != "RolloutComplete" {
		t.Errorf("Progressing is %s/%s, want %s/%s", r.progressing.Status, r.progressing.Reason, "True", "RolloutComplete")
	}
}

func TestRolloutHonorsTheDefaultBudgetOfOne(t *testing.T) {
	machines, renewals := rolloutInputs(
		rolloutEntry{"node-1", api.PhaseReady, fresh, false, -1},
		rolloutEntry{"node-3", api.PhaseUpdatePending, fresh, true, -1},
		rolloutEntry{"node-4", api.PhaseUpdatePending, fresh, true, -1},
	)
	r := decideFor(machines, renewals, time.Time{}, labCluster(0), "", passTime)
	if !slices.Equal(r.grant, []string{"node-3"}) {
		t.Errorf("grants are %v, want [node-3] because the default budget is 1", r.grant)
	}
	if !strings.Contains(r.progressing.Message, "node-4") {
		t.Errorf("the Progressing message is %q, want it to name node-4", r.progressing.Message)
	}
}

func TestRolloutABiggerBudgetGrantsMoreTurns(t *testing.T) {
	machines, renewals := rolloutInputs(
		rolloutEntry{"node-1", api.PhaseReady, fresh, false, -1},
		rolloutEntry{"node-3", api.PhaseUpdatePending, fresh, true, -1},
		rolloutEntry{"node-4", api.PhaseUpdatePending, fresh, true, -1},
		rolloutEntry{"node-5", api.PhaseUpdatePending, fresh, true, -1},
	)
	r := decideFor(machines, renewals, time.Time{}, labCluster(2), "", passTime)
	if !slices.Equal(r.grant, []string{"node-3", "node-4"}) {
		t.Errorf("grants are %v, want [node-3 node-4]", r.grant)
	}
}

func TestRolloutGrantsWorkersBeforeLeaders(t *testing.T) {
	machines, renewals := rolloutInputs(
		rolloutEntry{"node-1", api.PhaseUpdatePending, fresh, true, -1},
		rolloutEntry{"node-4", api.PhaseUpdatePending, fresh, true, -1},
	)
	r := decideFor(machines, renewals, time.Time{}, labCluster(0), "", passTime)
	if !slices.Equal(r.grant, []string{"node-4"}) {
		t.Errorf("grants are %v, want [node-4], the worker", r.grant)
	}
}

func TestRolloutGrantsInNameOrder(t *testing.T) {
	machines, renewals := rolloutInputs(
		rolloutEntry{"node-5", api.PhaseUpdatePending, fresh, true, -1},
		rolloutEntry{"node-4", api.PhaseUpdatePending, fresh, true, -1},
	)
	r := decideFor(machines, renewals, time.Time{}, labCluster(0), "", passTime)
	if !slices.Equal(r.grant, []string{"node-4"}) {
		t.Errorf("grants are %v, want [node-4], the lowest name", r.grant)
	}
}

// A worker goes ahead of a leader only when their requests were
// created in the same second.
func TestRolloutGrantsTheOldestRequestFirst(t *testing.T) {
	cases := []struct {
		name        string
		leaderAsked time.Duration
		workerAsked time.Duration
		want        string
	}{
		{"the leader asked first", 5 * time.Minute, 10 * time.Second, "node-1"},
		{"the worker asked first", 10 * time.Second, 5 * time.Minute, "node-4"},
		{"both asked in the same second", time.Minute, time.Minute, "node-4"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			machines, renewals := rolloutInputs(
				rolloutEntry{"node-1", api.PhaseUpdatePending, fresh, true, -1},
				rolloutEntry{"node-4", api.PhaseUpdatePending, fresh, true, -1},
			)
			q := requestsOfAwaitingMachines(machines)
			q.disruptions[0].Metadata.CreationTimestamp = passTime.Add(-c.leaderAsked)
			q.disruptions[1].Metadata.CreationTimestamp = passTime.Add(-c.workerAsked)
			q.budgets = []disruption.Budget{*disruption.NewDefaultBudget(1)}

			r := decideRollout(machines, renewals, time.Time{}, labCluster(0), "", q, passTime)

			if !slices.Equal(r.grant, []string{c.want}) {
				t.Errorf("granted %v, want %s", r.grant, c.want)
			}
		})
	}
}

func TestRolloutOneLeaderAtATimeRegardlessOfBudget(t *testing.T) {
	machines, renewals := rolloutInputs(
		rolloutEntry{"node-1", api.PhaseUpdatePending, fresh, true, -1},
		rolloutEntry{"node-2", api.PhaseUpdatePending, fresh, true, -1},
	)
	r := decideFor(machines, renewals, time.Time{}, labCluster(3), "", passTime)
	if !slices.Equal(r.grant, []string{"node-1"}) {
		t.Errorf("grants are %v, want only [node-1] because two leaders must not reboot together", r.grant)
	}
}

func TestRolloutNeverGrantsALeaderWhileAnotherLeaderIsDown(t *testing.T) {
	machines, renewals := rolloutInputs(
		rolloutEntry{"node-1", api.PhaseUpdatePending, fresh, true, -1},
		rolloutEntry{"node-2", api.PhaseLost, 5 * time.Minute, false, -1},
	)
	r := decideFor(machines, renewals, time.Time{}, labCluster(3), "", passTime)
	if len(r.grant) != 0 {
		t.Errorf("grants are %v, want none while node-2 is down", r.grant)
	}
}

func TestRolloutUnplannedTroubleConsumesTheBudget(t *testing.T) {
	machines, renewals := rolloutInputs(
		rolloutEntry{"node-1", api.PhaseReady, fresh, false, -1},
		rolloutEntry{"node-4", api.PhaseLost, 5 * time.Minute, false, -1},
		rolloutEntry{"node-5", api.PhaseUpdatePending, fresh, true, -1},
	)
	r := decideFor(machines, renewals, time.Time{}, labCluster(0), "", passTime)
	if len(r.grant) != 0 {
		t.Errorf("grants are %v, want none while node-4 is Lost", r.grant)
	}
}

func TestRolloutCountsAnExistingGrantAgainstTheBudget(t *testing.T) {
	machines, renewals := rolloutInputs(
		rolloutEntry{"node-1", api.PhaseReady, fresh, false, -1},
		rolloutEntry{"node-4", api.PhaseUpdatePending, fresh, true, time.Minute},
		rolloutEntry{"node-5", api.PhaseUpdatePending, fresh, true, -1},
	)
	r := decideFor(machines, renewals, time.Time{}, labCluster(0), "", passTime)
	if len(r.grant) != 0 {
		t.Errorf("grants are %v, want none while node-4 holds a turn", r.grant)
	}
	if len(r.revoke) != 0 {
		t.Errorf("revokes are %v, want none while node-4 still waits to reboot", r.revoke)
	}
}

func TestRolloutKeepsTheGrantWhileTheMachineReboots(t *testing.T) {
	// node-4's heartbeat stopped because it is rebooting on its grant,
	// for less than rolloutStallAfter.
	machines, renewals := rolloutInputs(
		rolloutEntry{"node-1", api.PhaseReady, fresh, false, -1},
		rolloutEntry{"node-4", api.PhaseUpdating, 3 * time.Minute, false, 3 * time.Minute},
		rolloutEntry{"node-5", api.PhaseUpdatePending, fresh, true, -1},
	)
	r := decideFor(machines, renewals, time.Time{}, labCluster(0), "", passTime)
	if len(r.grant) != 0 || len(r.revoke) != 0 {
		t.Errorf("grants are %v and revokes are %v, want none while node-4 reboots", r.grant, r.revoke)
	}
	if r.progressing.Reason != "RollingOut" {
		t.Errorf("Progressing reason is %s, want RollingOut", r.progressing.Reason)
	}
}

// The reclaim does not free node-4's place in the budget in the same
// pass. Its entry counts until a later pass reads the Machine without
// the grant.
func TestRolloutRevokesAfterTheMachineConverges(t *testing.T) {
	machines, renewals := rolloutInputs(
		rolloutEntry{"node-1", api.PhaseReady, fresh, false, -1},
		rolloutEntry{"node-4", api.PhaseReady, fresh, false, 5 * time.Minute},
		rolloutEntry{"node-5", api.PhaseUpdatePending, fresh, true, -1},
	)
	r := decideFor(machines, renewals, time.Time{}, labCluster(0), "", passTime)
	if !slices.Equal(r.revoke, []string{"node-4"}) {
		t.Errorf("revokes are %v, want [node-4]", r.revoke)
	}
	if len(r.grant) != 0 {
		t.Errorf("grants are %v, want none while node-4's entry still counts against the budget", r.grant)
	}
}

func TestRolloutStallsOnAMachineThatNeverReturns(t *testing.T) {
	machines, renewals := rolloutInputs(
		rolloutEntry{"node-1", api.PhaseReady, fresh, false, -1},
		rolloutEntry{"node-4", api.PhaseUpdating, 12 * time.Minute, false, 12 * time.Minute},
		rolloutEntry{"node-5", api.PhaseUpdatePending, fresh, true, -1},
	)
	r := decideFor(machines, renewals, time.Time{}, labCluster(3), "", passTime)
	if len(r.grant) != 0 {
		t.Errorf("grants are %v, want none for a stalled rollout", r.grant)
	}
	if r.progressing.Status != "False" || r.progressing.Reason != "RolloutStalled" {
		t.Errorf("Progressing is %s/%s, want %s/%s", r.progressing.Status, r.progressing.Reason, "False", "RolloutStalled")
	}
	if !strings.Contains(r.progressing.Message, "node-4") {
		t.Errorf("the Progressing message is %q, want it to name node-4", r.progressing.Message)
	}
}

func TestRolloutAManualMachineBlocksNoOtherMachine(t *testing.T) {
	// node-4 has a staged change but no request, as with
	// rebootPolicy Manual, so it does not count against the budget.
	machines, renewals := rolloutInputs(
		rolloutEntry{"node-1", api.PhaseReady, fresh, false, -1},
		rolloutEntry{"node-4", api.PhaseUpdatePending, fresh, false, -1},
		rolloutEntry{"node-5", api.PhaseUpdatePending, fresh, true, -1},
	)
	r := decideFor(machines, renewals, time.Time{}, labCluster(0), "", passTime)
	if !slices.Equal(r.grant, []string{"node-5"}) {
		t.Errorf("grants are %v, want [node-5]", r.grant)
	}
}

func TestRolloutRevokesAGrantTheMachineNoLongerWants(t *testing.T) {
	// node-4's change was reverted, or its policy set to Manual, after
	// its grant and before it rebooted.
	machines, renewals := rolloutInputs(
		rolloutEntry{"node-1", api.PhaseReady, fresh, false, -1},
		rolloutEntry{"node-4", api.PhaseReady, fresh, false, time.Minute},
	)
	r := decideFor(machines, renewals, time.Time{}, labCluster(0), "", passTime)
	if !slices.Equal(r.revoke, []string{"node-4"}) {
		t.Errorf("revokes are %v, want [node-4]", r.revoke)
	}
}

func TestRolloutGrantsAWaitingLeaderFirstWhileTheTemplateLags(t *testing.T) {
	machines, renewals := rolloutInputs(
		rolloutEntry{"node-1", api.PhaseUpdatePending, fresh, true, -1}, // leader
		rolloutEntry{"node-4", api.PhaseUpdatePending, fresh, true, -1}, // worker
	)
	r := decideFor(machines, renewals, time.Time{}, targetingCluster(0, "2026.08.12-002"), "2026.08.01-001", passTime)
	if !slices.Equal(r.grant, []string{"node-1"}) {
		t.Errorf("grants are %v, want [node-1] while the template lags", r.grant)
	}
	if !strings.Contains(r.progressing.Message, "node-4") {
		t.Errorf("the Progressing message is %q, want it to name node-4", r.progressing.Message)
	}
	for _, version := range []string{"2026.08.01-001", "2026.08.12-002"} {
		if !strings.Contains(r.progressing.Message, version) {
			t.Errorf("the Progressing message is %q, want it to name %s", r.progressing.Message, version)
		}
	}
}

func TestRolloutGrantsWorkersWhenNoLeaderWaitsAndTheTemplateLags(t *testing.T) {
	// No leader requests a turn, so workers do not wait for one.
	machines, renewals := rolloutInputs(
		rolloutEntry{"node-4", api.PhaseUpdatePending, fresh, true, -1},
		rolloutEntry{"node-5", api.PhaseUpdatePending, fresh, true, -1},
	)
	r := decideFor(machines, renewals, time.Time{}, targetingCluster(0, "2026.08.12-002"), "2026.08.01-001", passTime)
	if !slices.Equal(r.grant, []string{"node-4"}) {
		t.Errorf("grants are %v, want [node-4]", r.grant)
	}
}

func TestRolloutHoldsWorkersThroughALeaderTurnWhileTheTemplateLags(t *testing.T) {
	// node-1 is rebooting on its grant. The worker waits for the whole
	// of the leader's turn, not only in the pass that granted it, even
	// with room in the budget.
	machines, renewals := rolloutInputs(
		rolloutEntry{"node-1", api.PhaseUpdating, 3 * time.Minute, false, 3 * time.Minute},
		rolloutEntry{"node-4", api.PhaseUpdatePending, fresh, true, -1},
	)
	r := decideFor(machines, renewals, time.Time{}, targetingCluster(2, "2026.08.12-002"), "2026.08.01-001", passTime)
	if len(r.grant) != 0 {
		t.Errorf("grants are %v, want none while the leader's turn runs", r.grant)
	}
	if !strings.Contains(r.progressing.Message, "workers wait") {
		t.Errorf("the Progressing message is %q, want it to say that workers wait", r.progressing.Message)
	}
}

func TestRolloutGrantsWorkersWhenALeaderIsDownAndTheTemplateLags(t *testing.T) {
	// node-2 is down with no grant, so node-1 cannot get a turn, and
	// no leader turn will update the template. Workers that waited
	// would wait for a machine that may never return.
	machines, renewals := rolloutInputs(
		rolloutEntry{"node-1", api.PhaseUpdatePending, fresh, true, -1},
		rolloutEntry{"node-2", api.PhaseLost, 5 * time.Minute, false, -1},
		rolloutEntry{"node-4", api.PhaseUpdatePending, fresh, true, -1},
	)
	r := decideFor(machines, renewals, time.Time{}, targetingCluster(2, "2026.08.12-002"), "2026.08.01-001", passTime)
	if !slices.Equal(r.grant, []string{"node-4"}) {
		t.Errorf("grants are %v, want [node-4] because no leader turn can advance the template", r.grant)
	}
}

func TestRolloutGrantsWorkersFirstWithNoAppliedVersion(t *testing.T) {
	machines, renewals := rolloutInputs(
		rolloutEntry{"node-1", api.PhaseUpdatePending, fresh, true, -1}, // leader
		rolloutEntry{"node-4", api.PhaseUpdatePending, fresh, true, -1}, // worker
	)
	r := decideFor(machines, renewals, time.Time{}, targetingCluster(0, "2026.08.12-002"), "", passTime)
	if !slices.Equal(r.grant, []string{"node-4"}) {
		t.Errorf("grants are %v, want [node-4] when no applied version exists", r.grant)
	}
}

func TestRolloutGrantsWorkersFirstWhenTheTemplateMatches(t *testing.T) {
	machines, renewals := rolloutInputs(
		rolloutEntry{"node-1", api.PhaseUpdatePending, fresh, true, -1}, // leader
		rolloutEntry{"node-4", api.PhaseUpdatePending, fresh, true, -1}, // worker
	)
	r := decideFor(machines, renewals, time.Time{}, targetingCluster(0, "2026.08.12-002"), "2026.08.12-002", passTime)
	if !slices.Equal(r.grant, []string{"node-4"}) {
		t.Errorf("grants are %v, want [node-4] when the template matches the target", r.grant)
	}
}

func TestRolloutGivesARequestedRebootATurnLikeAnyOther(t *testing.T) {
	// A requested reboot counts against the same budget as a staged
	// change, because two machines rebooting on request at once cause
	// the same outage as two rebooting for an upgrade.
	machines, renewals := rolloutInputs(
		rolloutEntry{"node-1", api.PhaseReady, fresh, false, -1},
		rolloutEntry{"node-3", api.PhaseUpdatePending, fresh, false, -1},
		rolloutEntry{"node-4", api.PhaseUpdatePending, fresh, false, -1},
	)
	for i := range machines {
		if machines[i].Metadata.Name == "node-1" {
			continue
		}
		machines[i].Status.Conditions = api.SetCondition(machines[i].Status.Conditions, api.Condition{
			Type: "RebootRequestHonored", Status: "False", Reason: "AwaitingTurn",
		}, passTime)
	}
	r := decideFor(machines, renewals, time.Time{}, labCluster(0), "", passTime)
	if !slices.Equal(r.grant, []string{"node-3"}) {
		t.Errorf("grants are %v, want only [node-3]", r.grant)
	}
	if !strings.Contains(r.progressing.Message, "node-4") {
		t.Errorf("the Progressing message is %q, want it to name node-4", r.progressing.Message)
	}
}
