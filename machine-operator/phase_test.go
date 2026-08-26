package main

// The phase table, pinned: which condition puts a machine in which
// phase, and which phase wins when several conditions disagree.

import (
	"testing"

	"github.com/liken-sh/liken/api"
	"github.com/liken-sh/liken/machine"
)

func condition(ctype string, status api.ConditionStatus, reason string) api.Condition {
	return api.Condition{Type: ctype, Status: status, Reason: reason}
}

func TestDecidePhase(t *testing.T) {
	allTrue := []api.Condition{
		condition("FactsPublished", "True", "FactsRead"),
		condition("SysctlsApplied", "True", "Applied"),
		condition("SpecConverged", "True", "Converged"),
		condition("ClusterConverged", "True", "Converged"),
		condition("NodeHealthy", "True", "KubeletReady"),
		condition("Ready", "True", "Reconciled"),
	}
	tests := []struct {
		name       string
		conditions []api.Condition
		want       api.Phase
	}{
		{"everything true is ready", allTrue, api.PhaseReady},
		{"no conditions at all is ready", nil, api.PhaseReady},
		{
			"unreadable facts give the operator nothing to judge",
			[]api.Condition{condition("FactsPublished", "False", "FactsUnreadable")},
			api.PhaseUnknown,
		},
		{
			"no boot record yet means init is still working",
			[]api.Condition{
				condition("SpecConverged", "Unknown", "FactsIncomplete"),
				condition("ClusterConverged", "Unknown", "FactsIncomplete"),
			},
			api.PhaseBooting,
		},
		{
			"a rejected spec is blocked, not pending",
			[]api.Condition{condition("SpecConverged", "False", "RejectedLastBoot")},
			api.PhaseBlocked,
		},
		{
			"a spec the machine can't satisfy is blocked",
			[]api.Condition{condition("SpecConverged", "False", "StagingRejected")},
			api.PhaseBlocked,
		},
		{
			"nowhere durable to stage is blocked",
			[]api.Condition{condition("ClusterConverged", "False", "MachineStateEphemeral")},
			api.PhaseBlocked,
		},
		{
			"a machine with no system slots can never take a release",
			[]api.Condition{condition("VersionConverged", "False", "NoSystemSlots")},
			api.PhaseBlocked,
		},
		{
			"a corrupt release blocks until the catalog changes",
			[]api.Condition{condition("VersionConverged", "False", "DigestMismatch")},
			api.PhaseBlocked,
		},
		{
			"a machine not booted from a slot can't take releases",
			[]api.Condition{condition("VersionConverged", "False", "NotInstalled")},
			api.PhaseBlocked,
		},
		{
			"a staged release waits like any staged change",
			[]api.Condition{condition("VersionConverged", "False", "RebootPending")},
			api.PhaseUpdatePending,
		},
		{
			"a requested reboot is an update in flight",
			[]api.Condition{condition("SpecConverged", "False", "RebootRequested")},
			api.PhaseUpdating,
		},
		{
			"a requested k3s restart is an update in flight",
			[]api.Condition{condition("ClusterConverged", "False", "RestartRequested")},
			api.PhaseUpdating,
		},
		{
			"a staged restart waits like any staged change",
			[]api.Condition{condition("CredentialsConverged", "False", "RestartPending")},
			api.PhaseUpdatePending,
		},
		{
			"a malformed credentials Secret is blocked, not pending",
			[]api.Condition{condition("CredentialsConverged", "False", "CredentialsInvalid")},
			api.PhaseBlocked,
		},
		{
			"a release downloading is an update in flight",
			[]api.Condition{condition("VersionConverged", "False", "Downloading")},
			api.PhaseUpdating,
		},
		{
			"a demotion mid-reboot is an update in flight",
			[]api.Condition{condition("NodeCurrent", "False", "DemotionRebooting")},
			api.PhaseUpdating,
		},
		{
			"staged and waiting on a manual reboot",
			[]api.Condition{condition("ClusterConverged", "False", "RebootPending")},
			api.PhaseUpdatePending,
		},
		{
			// The change needs no disruption of its own, but the
			// machine still does not run the deployment's document
			// yet, so the listing shows it as pending rather than
			// Ready.
			"a document staged for the next boot is pending",
			[]api.Condition{condition("ClusterConverged", "False", "StagedForNextBoot")},
			api.PhaseUpdatePending,
		},
		{
			"a pending demotion waits the same way",
			[]api.Condition{condition("NodeCurrent", "False", "DemotionPending")},
			api.PhaseUpdatePending,
		},
		{
			"a failing sysctl is plain degradation",
			[]api.Condition{condition("SysctlsApplied", "False", "ApplyFailed")},
			api.PhaseDegraded,
		},
		{
			// A default that will not apply ships with the release, so
			// every machine reports it at once. Degrading the fleet on
			// it would bury whichever machine has a real fault.
			"a default liken could not apply is not degradation",
			[]api.Condition{condition("SysctlsApplied", "True", "DefaultsIncomplete")},
			api.PhaseReady,
		},
		{
			"an unreachable cluster is plain degradation",
			[]api.Condition{condition("ClusterConverged", "Unknown", "ClusterUnavailable")},
			api.PhaseDegraded,
		},
		{
			"a dead kubelet is plain degradation",
			[]api.Condition{condition("NodeHealthy", "False", "NodeNotReady")},
			api.PhaseDegraded,
		},
		{
			"the gravest condition wins",
			[]api.Condition{
				condition("SysctlsApplied", "False", "ApplyFailed"),
				condition("SpecConverged", "False", "RebootPending"),
				condition("ClusterConverged", "False", "RejectedLastBoot"),
			},
			api.PhaseBlocked,
		},
		{
			"the ready roll-up never argues on its own",
			[]api.Condition{condition("Ready", "False", "Degraded")},
			api.PhaseReady,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := decidePhase(tt.conditions); got != tt.want {
				t.Errorf("got %s, want %s", got, tt.want)
			}
		})
	}
}

func TestAwaitingTurnIsUpdatePending(t *testing.T) {
	phase := decidePhase([]api.Condition{
		{Type: "ClusterConverged", Status: "False", Reason: "AwaitingTurn"},
	})
	if phase != api.PhaseUpdatePending {
		t.Errorf("waiting on the cluster's grant is a pending update: %s", phase)
	}
}

func TestReadyRollupAllTrue(t *testing.T) {
	c := readyCondition([]api.Condition{
		condition("HostEntriesApplied", "True", "Applied"),
	})
	if c.Status != api.ConditionTrue || c.Reason != "Reconciled" {
		t.Errorf("got %+v", c)
	}
}

func TestReadyRollupReasonFollowsThePhase(t *testing.T) {
	c := readyCondition([]api.Condition{
		condition("HostEntriesApplied", "False", "AwaitingPodRefresh"),
	})
	if c.Status != api.ConditionFalse || c.Reason != "UpdatePending" {
		t.Errorf("a waiting machine is not degraded: %+v", c)
	}
}

func TestReadyRollupDegradedWhenThePhaseSaysSo(t *testing.T) {
	c := readyCondition([]api.Condition{
		condition("SysctlsApplied", "False", "ApplyFailed"),
	})
	if c.Status != api.ConditionFalse || c.Reason != "Degraded" {
		t.Errorf("got %+v", c)
	}
}

func TestReadyRollupSkipsThePriorReadyAndTheGrant(t *testing.T) {
	c := readyCondition([]api.Condition{
		condition("Ready", "False", "Degraded"),
		condition(machine.RebootApprovedCondition, "True", "DisruptionBudgetAllows"),
		condition("HostEntriesApplied", "True", "Applied"),
	})
	if c.Status != api.ConditionTrue || c.Reason != "Reconciled" {
		t.Errorf("a stale roll-up and the grant say nothing about health: %+v", c)
	}
}

func TestAwaitingPodRefreshIsUpdatePending(t *testing.T) {
	phase := decidePhase([]api.Condition{
		{Type: "HostEntriesApplied", Status: "False", Reason: "AwaitingPodRefresh"},
	})
	if phase != api.PhaseUpdatePending {
		t.Errorf("a stale pod template waiting on the steward's refresh is a pending update: %s", phase)
	}
}

// healthyBesides is a machine whose every other condition is True, so
// the radio's own verdict decides the phase by itself.
func healthyBesides(wireless api.Condition) []api.Condition {
	return []api.Condition{
		condition("FactsPublished", "True", "FactsRead"),
		condition("SysctlsApplied", "True", "Applied"),
		condition("SpecConverged", "True", "Converged"),
		condition("ClusterConverged", "True", "Converged"),
		condition("NodeHealthy", "True", "KubeletReady"),
		wireless,
	}
}

// oneRadio is a machine with one wired port and one radio in the
// given state, as the facts tree reports it.
func oneRadio(state machine.WirelessState) []machine.InterfaceStatus {
	return []machine.InterfaceStatus{
		{Name: "eth0", Address: "10.10.0.5/24"},
		{Name: "wlan0", Wireless: &machine.WirelessStatus{SSID: "stonypoint", State: state}},
	}
}

func TestARadioStillJoiningLeavesTheMachineReady(t *testing.T) {
	// The boot handed the radio to the background on purpose, so the
	// join window is not a fault. A listing that showed every such
	// machine as Degraded for the length of a join would teach a
	// person to ignore the column.
	wireless := wirelessCondition(oneRadio(machine.WirelessAssociating))
	if wireless.Status != api.ConditionFalse || wireless.Reason != "Joining" {
		t.Fatalf("the condition still reports what the boot actuated: %+v", wireless)
	}
	if phase := decidePhase(healthyBesides(wireless)); phase != api.PhaseReady {
		t.Errorf("a radio still joining put the machine in %s", phase)
	}
}

func TestASettledRadioFailureIsDegraded(t *testing.T) {
	// Only the join window is soft. Every state a radio settles on is
	// a fault a person must see in the listing.
	for _, state := range []machine.WirelessState{
		machine.WirelessNoCarrier, machine.WirelessNotRaised, machine.WirelessWrongKey,
	} {
		t.Run(string(state), func(t *testing.T) {
			wireless := wirelessCondition(oneRadio(state))
			if wireless.Reason != "NotJoined" {
				t.Errorf("got reason %q", wireless.Reason)
			}
			if phase := decidePhase(healthyBesides(wireless)); phase != api.PhaseDegraded {
				t.Errorf("put the machine in %s", phase)
			}
		})
	}
}

func TestDrainingIsUpdating(t *testing.T) {
	phase := decidePhase([]api.Condition{
		{Type: "SpecConverged", Status: "False", Reason: "Draining"},
	})
	if phase != api.PhaseUpdating {
		t.Errorf("draining is the reboot's opening move: %s", phase)
	}
}

func TestALiveLoadInFlightIsUpdating(t *testing.T) {
	phase := decidePhase([]api.Condition{
		{Type: "SpecConverged", Status: "False", Reason: "LoadRequested"},
	})
	if phase != api.PhaseUpdating {
		t.Errorf("a requested live load is a change in progress: %s", phase)
	}
}
