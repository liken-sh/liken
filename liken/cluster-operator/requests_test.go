package main

// The rollout's decision with Disruptions and MachineDisruptionBudgets:
// which budget refuses a turn, the turn that waits for a download, the
// requests an entry names, and the entry that outlives its grant.

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

// labeled sets one label on the named machines.
func labeled(machines []machine.Machine, key, value string, names ...string) []machine.Machine {
	for i := range machines {
		if slices.Contains(names, machines[i].Metadata.Name) {
			machines[i].Metadata.Labels = map[string]string{key: value}
		}
	}
	return machines
}

func budget(name string, maxUnavailable int, matchLabels map[string]string) disruption.Budget {
	b := disruption.NewDefaultBudget(maxUnavailable)
	b.Metadata.Name = name
	b.Spec.Selector.MatchLabels = matchLabels
	return *b
}

// request is an approved request of a machine on rebootPolicy Auto,
// the policy rolloutInputs leaves every machine on.
func request(name, machineName string) disruption.Disruption {
	return *disruption.New(name, &machine.Machine{Metadata: api.ObjectMeta{Name: machineName}},
		disruption.Spec{Machine: machineName, Requester: disruption.MachineOperator, Kind: disruption.KindReboot, Hash: "6f2a91c04b7e00"})
}

// autoPolicy puts every machine on rebootPolicy Auto.
func autoPolicy(machines []machine.Machine) []machine.Machine {
	for i := range machines {
		machines[i].Spec.RebootPolicy = machine.RebootAuto
	}
	return machines
}

// A machine takes a turn only when every budget that selects it has
// room, and a machine that only the default budget selects is still
// limited by it.
func TestEveryBudgetThatSelectsAMachineMustHaveRoom(t *testing.T) {
	cases := []struct {
		name    string
		budgets []disruption.Budget
		grant   []string
		waits   string
	}{
		{
			name:    "the gpus budget is full, and the default budget has room",
			budgets: []disruption.Budget{budget("default", 3, nil), budget("gpus", 1, map[string]string{"pool": "gpu"})},
			grant:   []string{"node-5"},
			waits:   "the gpus budget allows 1 out of service, and node-3 is out",
		},
		{
			name:    "the default budget is full",
			budgets: []disruption.Budget{budget("default", 1, nil), budget("gpus", 2, map[string]string{"pool": "gpu"})},
			waits:   "the default budget allows 1 out of service, and node-3 is out",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			machines, renewals := rolloutInputs(
				rolloutEntry{"node-3", api.PhaseLost, -1, false, -1},
				rolloutEntry{"node-4", api.PhaseUpdatePending, fresh, true, -1},
				rolloutEntry{"node-5", api.PhaseUpdatePending, fresh, true, -1},
			)
			machines = labeled(machines, "pool", "gpu", "node-3", "node-4")

			q := asking(machines)
			q.budgets = c.budgets
			r := decideRollout(machines, renewals, time.Time{}, labCluster(1), "", q, sweepNow)

			if !slices.Equal(r.grant, c.grant) {
				t.Errorf("grants = %v, want %v", r.grant, c.grant)
			}
			if r.waits["node-4"] != c.waits {
				t.Errorf("node-4 waits for %q, want %q", r.waits["node-4"], c.waits)
			}
		})
	}
}

// With no budget named default, a stand-in of one machine at a time
// limits the fleet, and spec.disruption is not read.
func TestAMissingDefaultBudgetStandsInAtOne(t *testing.T) {
	machines, renewals := rolloutInputs(
		rolloutEntry{"node-3", api.PhaseUpdatePending, fresh, true, -1},
		rolloutEntry{"node-4", api.PhaseUpdatePending, fresh, true, -1},
	)
	q := asking(machines)
	q.budgets = []disruption.Budget{budget("gpus", 1, map[string]string{"pool": "gpu"})}

	r := decideRollout(machines, renewals, time.Time{}, labCluster(2), "", q, sweepNow)

	if !slices.Equal(r.grant, []string{"node-3"}) {
		t.Errorf("grants = %v, want node-3 alone under the stand-in's limit of one", r.grant)
	}
}

// A budget reports the machines it selects, the ones out of service
// after the sweep's grants, and how many more may go.
func TestABudgetCountsAfterTheSweepsGrants(t *testing.T) {
	machines, renewals := rolloutInputs(
		rolloutEntry{"node-3", api.PhaseLost, -1, false, -1},
		rolloutEntry{"node-4", api.PhaseUpdatePending, fresh, true, -1},
		rolloutEntry{"node-5", api.PhaseReady, fresh, false, -1},
	)
	served := []disruption.Budget{budget("default", 3, nil)}
	q := asking(machines)
	q.budgets = served

	r := decideRollout(machines, renewals, time.Time{}, labCluster(1), "", q, sweepNow)

	got := r.budgets["default"]
	if got.Selected != 3 || got.Unavailable != 2 || got.Allowed != 1 {
		t.Errorf("default counts %d selected, %d out, %d allowed; want 3, 2, 1", got.Selected, got.Unavailable, got.Allowed)
	}
	if c := got.Conditions[0]; c.Status != api.ConditionTrue || c.Message != "2 of 3 out of service: node-3, node-4" {
		t.Errorf("DisruptionAllowed = %s %q", c.Status, c.Message)
	}
}

// downloadingAs sets the reason of the machine's VersionConverged
// condition.
func downloadingAs(machines []machine.Machine, reason string, names ...string) []machine.Machine {
	for i := range machines {
		if slices.Contains(names, machines[i].Metadata.Name) {
			machines[i].Status.Conditions = api.SetCondition(machines[i].Status.Conditions, api.Condition{
				Type: "VersionConverged", Status: api.ConditionFalse, Reason: reason,
			}, sweepNow)
		}
	}
	return machines
}

// A machine whose release still downloads waits for it, so one drain
// and one reboot apply both. A fetch that failed and waits to retry
// holds no turn.
func TestATurnWaitsForAReleaseThatIsDownloading(t *testing.T) {
	cases := []struct {
		reason string
		grant  []string
	}{
		{"Downloading", nil},
		{"DownloadRetrying", []string{"node-4"}},
	}
	for _, c := range cases {
		t.Run(c.reason, func(t *testing.T) {
			machines, renewals := rolloutInputs(rolloutEntry{"node-4", api.PhaseUpdatePending, fresh, true, -1})
			machines = downloadingAs(machines, c.reason, "node-4")

			r := decideFor(machines, renewals, time.Time{}, labCluster(1), "", sweepNow)

			if !slices.Equal(r.grant, c.grant) {
				t.Errorf("grants = %v, want %v", r.grant, c.grant)
			}
		})
	}
}

// A downloading machine's wait shows on the Cluster's Progressing
// message.
func TestProgressingNamesAMachineThatWaitsForItsDownload(t *testing.T) {
	machines, renewals := rolloutInputs(rolloutEntry{"node-4", api.PhaseUpdatePending, fresh, true, -1})
	machines = downloadingAs(machines, "Downloading", "node-4")

	r := decideFor(machines, renewals, time.Time{}, labCluster(1), "", sweepNow)

	want := "reboot turns are waiting for a release to finish downloading; waiting for a release to finish downloading: node-4"
	if r.progressing.Message != want {
		t.Errorf("Progressing = %q, want %q", r.progressing.Message, want)
	}
}

// A new turn names the machine's open requests, and a request that
// arrives during the turn joins it.
func TestATurnNamesTheRequestsItGrants(t *testing.T) {
	machines, renewals := rolloutInputs(
		rolloutEntry{"node-3", api.PhaseUpdatePending, fresh, true, -1},
		rolloutEntry{"node-4", api.PhaseUpdating, fresh, true, time.Minute},
	)
	machines = autoPolicy(atVersion7(machines))
	q := fleetRequests{disruptions: []disruption.Disruption{
		request("node-3.machine-operator", "node-3"),
		request("node-4.machine-operator", "node-4"),
		request("node-4.reboot-0c1e9a2b77b0", "node-4"),
	}}
	c := withTurns(2, cluster.RebootTurn{Machine: "node-4", MachineResourceVersion: "7", Since: sweepNow.Add(-time.Minute),
		Disruptions: []string{"node-4.machine-operator"}})

	r := decideRollout(machines, renewals, time.Time{}, c, "", q, sweepNow)

	want := map[string][]string{
		"node-3": {"node-3.machine-operator"},
		"node-4": {"node-4.machine-operator", "node-4.reboot-0c1e9a2b77b0"},
	}
	for _, turn := range r.turns {
		if !slices.Equal(turn.Disruptions, want[turn.Machine]) {
			t.Errorf("%s's turn names %v, want %v", turn.Machine, turn.Disruptions, want[turn.Machine])
		}
	}
}

// A request that waits for a Manual machine's approval, or that is
// spent, asks for no turn.
func TestOnlyAnOpenApprovedRequestAsksForATurn(t *testing.T) {
	machines, renewals := rolloutInputs(rolloutEntry{"node-4", api.PhaseUpdatePending, fresh, true, -1})
	booted := sweepNow.Add(-time.Hour)
	machines[0].Status.Boot = machine.BootStatus{Time: &booted}
	waiting := request("node-4.machine-operator", "node-4")
	spent := request("node-4.reboot-0c1e9a2b77b0", "node-4")
	spent.Spec.Requester, spent.Spec.ApprovedHash = "kubernetes-admin", spent.Spec.Hash

	r := decideRollout(machines, renewals, time.Time{}, labCluster(1), "",
		fleetRequests{disruptions: []disruption.Disruption{waiting, spent}}, sweepNow)

	if len(r.grant) != 0 || len(r.turns) != 0 {
		t.Errorf("grants = %v and turns = %+v, want none: AwaitingTurn alone asks for nothing", r.grant, r.turns)
	}
}

// An entry whose Machine holds no grant at another version stays while
// one of its requests is open, or while the machine is unavailable, and
// leaves when the machine is back and every request is done.
func TestAnEntryStaysWhileItsRequestsAreOpen(t *testing.T) {
	cases := []struct {
		name     string
		phase    api.Phase
		requests []disruption.Disruption
		turns    []string
	}{
		{"an open request holds the entry", api.PhaseReady,
			[]disruption.Disruption{request("node-4.machine-operator", "node-4")}, []string{"node-4"}},
		{"an unavailable machine holds the entry", api.PhaseBooting, nil, []string{"node-4"}},
		{"a deleted request frees it", api.PhaseReady, nil, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			machines, renewals := rolloutInputs(rolloutEntry{"node-4", c.phase, fresh, false, -1})
			machines = autoPolicy(atVersion7(machines))
			ledger := withTurns(1, cluster.RebootTurn{Machine: "node-4", MachineResourceVersion: "6",
				Since: sweepNow.Add(-time.Minute), Disruptions: []string{"node-4.machine-operator"}})

			r := decideRollout(machines, renewals, time.Time{}, ledger, "", fleetRequests{disruptions: c.requests}, sweepNow)

			if got := turnNames(r.turns); !slices.Equal(got, c.turns) {
				t.Errorf("ledger = %v, want %v", got, c.turns)
			}
		})
	}
}

// An entry with no requests keeps milestone 82's rule: it leaves once
// the Machine holds no grant at another version.
func TestAnEntryWithNoRequestsLeavesAtAnotherVersion(t *testing.T) {
	machines, renewals := rolloutInputs(rolloutEntry{"node-4", api.PhaseBooting, fresh, false, -1})
	machines = atVersion7(machines)

	r := decideFor(machines, renewals, time.Time{}, withTurns(1, turnAt("node-4", "6")), "", sweepNow)

	if len(r.turns) != 0 {
		t.Errorf("ledger = %v, want empty", turnNames(r.turns))
	}
}

// A request that arrives as the last grant is reclaimed holds the
// entry, and when the machine asks for its turn again, the grant goes
// out again in the same entry, at the Machine's current version, with
// no other budget slot.
func TestAMachineWhoseEntryOutlivedItsGrantTakesItAgain(t *testing.T) {
	machines, renewals := rolloutInputs(
		rolloutEntry{"node-4", api.PhaseUpdatePending, fresh, true, -1},
		rolloutEntry{"node-5", api.PhaseUpdatePending, fresh, true, -1},
	)
	machines = autoPolicy(atVersion7(machines))
	since := sweepNow.Add(-time.Minute)
	ledger := withTurns(1, cluster.RebootTurn{Machine: "node-4", MachineResourceVersion: "6", Since: since,
		Disruptions: []string{"node-4.reboot-0c1e9a2b77b0"}})
	person := request("node-4.reboot-0c1e9a2b77b0", "node-4")
	person.Spec.Requester = "kubernetes-admin"

	r := decideRollout(machines, renewals, time.Time{}, ledger, "", fleetRequests{disruptions: []disruption.Disruption{person}}, sweepNow)

	if !slices.Equal(r.grant, []string{"node-4"}) {
		t.Errorf("grants = %v, want node-4 again and not node-5", r.grant)
	}
	if len(r.turns) != 1 || r.turns[0].MachineResourceVersion != "7" || !r.turns[0].Since.Equal(since) {
		t.Errorf("ledger = %+v, want node-4's entry at version 7, since %s", r.turns, since)
	}
}

// A stalled rollout grants nothing, not even again in an entry.
func TestAStalledRolloutGrantsNoEntryAgain(t *testing.T) {
	machines, renewals := rolloutInputs(
		rolloutEntry{"node-3", api.PhaseBooting, fresh, false, 20 * time.Minute},
		rolloutEntry{"node-4", api.PhaseUpdatePending, fresh, true, -1},
	)
	machines = autoPolicy(atVersion7(machines))
	ledger := withTurns(2, turnAt("node-3", "7"), cluster.RebootTurn{Machine: "node-4", MachineResourceVersion: "6",
		Since: sweepNow.Add(-time.Minute), Disruptions: []string{"node-4.machine-operator"}})

	r := decideRollout(machines, renewals, time.Time{}, ledger, "",
		fleetRequests{disruptions: []disruption.Disruption{request("node-4.machine-operator", "node-4")}}, sweepNow)

	if len(r.grant) != 0 || !strings.Contains(r.waits["node-4"], "stalled") {
		t.Errorf("grants = %v and node-4 waits for %q; want none, for the stall", r.grant, r.waits["node-4"])
	}
}

// A request's Approved condition follows its machine's policy and its
// approvedHash. The approve annotation approves nothing.
func TestARequestIsApprovedByPolicyOrHash(t *testing.T) {
	staged := machine.PendingDisruption{Condition: "SpecConverged", Kind: machine.DisruptionReboot, Hash: "9d3b2a7c11e4ff"}
	cases := []struct {
		name       string
		policy     machine.RebootPolicy
		approved   string
		annotation string
		reason     string
	}{
		{"Auto", machine.RebootAuto, "", "", "AutoPolicy"},
		{"approvedHash names hash", machine.RebootManual, "6f2a91c04b7e", "", "HashApproved"},
		{"approvedHash names other bytes", machine.RebootManual, "aaaaaaaaaaaa", "", "AwaitingApproval"},
		{"the annotation names a staged document", machine.RebootManual, "", "9d3b2a7c11e4", "AwaitingApproval"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m := &machine.Machine{Metadata: api.ObjectMeta{Name: "node-4",
				Annotations: map[string]string{"liken.sh/approve-disruption": c.annotation}}}
			m.Spec.RebootPolicy = c.policy
			m.Status.Pending = []machine.PendingDisruption{staged}
			d := request("node-4.machine-operator", "node-4")
			d.Spec.Hash, d.Spec.ApprovedHash = "6f2a91c04b7e00", c.approved

			if got := approve(&d, m); got.reason != c.reason {
				t.Errorf("approval = %s (%s), want %s", got.reason, got.message, c.reason)
			}
		})
	}
}

// A request reads Granted only once the stored ledger names it, and a
// waiting request's message says what it waits for.
func TestARequestReadsGrantedFromTheStoredLedger(t *testing.T) {
	m := &machine.Machine{Metadata: api.ObjectMeta{Name: "node-4"}}
	m.Spec.RebootPolicy = machine.RebootAuto
	d := request("node-4.machine-operator", "node-4")
	r := rollout{waits: map[string]string{"node-4": "the default budget allows 1 out of service, and node-3 is out"}}
	cases := []struct {
		name    string
		stored  []cluster.RebootTurn
		status  api.ConditionStatus
		message string
	}{
		{"no entry", nil, api.ConditionFalse, "the default budget allows 1 out of service, and node-3 is out"},
		{"an entry that names it", []cluster.RebootTurn{{Machine: "node-4", Disruptions: []string{"node-4.machine-operator"}}},
			api.ConditionTrue, "the machine's turn in the Cluster's status.rebootTurns grants this request"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, granted := requestConditions(&d, m, r, c.stored)

			if granted.Status != c.status || granted.Message != c.message {
				t.Errorf("Granted = %s %q, want %s %q", granted.Status, granted.Message, c.status, c.message)
			}
		})
	}
}
