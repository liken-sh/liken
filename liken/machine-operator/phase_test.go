package main

import (
	"testing"

	"github.com/liken-sh/liken/liken/api"
	"github.com/liken-sh/liken/liken/machine"
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
			"unreadable facts are Unknown",
			[]api.Condition{condition("FactsPublished", "False", "FactsUnreadable")},
			api.PhaseUnknown,
		},
		{
			"no boot record yet is Booting",
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
			"a spec the machine cannot stage is blocked",
			[]api.Condition{condition("SpecConverged", "False", "StagingRejected")},
			api.PhaseBlocked,
		},
		{
			"a machine with no persistent storage to stage on is blocked",
			[]api.Condition{condition("ClusterConverged", "False", "MachineStateEphemeral")},
			api.PhaseBlocked,
		},
		{
			"a machine with no system slots is blocked",
			[]api.Condition{condition("VersionConverged", "False", "NoSystemSlots")},
			api.PhaseBlocked,
		},
		{
			"a corrupt release blocks until the catalog changes",
			[]api.Condition{condition("VersionConverged", "False", "DigestMismatch")},
			api.PhaseBlocked,
		},
		{
			"a machine that did not boot from a slot is blocked",
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
			"a release downloading in the background is downloading",
			[]api.Condition{condition("VersionConverged", "False", "Downloading")},
			api.PhaseDownloading,
		},
		{
			"a change waiting on a reboot shows over a download",
			[]api.Condition{
				condition("VersionConverged", "False", "Downloading"),
				condition("SpecConverged", "False", "RebootPending"),
			},
			api.PhaseUpdatePending,
		},
		{
			"a fault shows over a download",
			[]api.Condition{
				condition("VersionConverged", "False", "Downloading"),
				condition("SysctlsApplied", "False", "ApplyFailed"),
			},
			api.PhaseDegraded,
		},
		{
			"a demotion mid-reboot is an update in flight",
			[]api.Condition{condition("NodeCurrent", "False", "DemotionRebooting")},
			api.PhaseUpdating,
		},
		{
			"a change staged for a manual reboot is pending",
			[]api.Condition{condition("ClusterConverged", "False", "RebootPending")},
			api.PhaseUpdatePending,
		},
		{
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
			// A default that cannot apply comes with the release, so
			// every machine reports it at once, and a Degraded fleet
			// would hide the machine with a real fault.
			"a default that liken could not apply is not degradation",
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
			"the most severe phase wins",
			[]api.Condition{
				condition("SysctlsApplied", "False", "ApplyFailed"),
				condition("SpecConverged", "False", "RebootPending"),
				condition("ClusterConverged", "False", "RejectedLastBoot"),
			},
			api.PhaseBlocked,
		},
		{
			"the Ready roll-up indicates no phase",
			[]api.Condition{condition("Ready", "False", "Degraded")},
			api.PhaseReady,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := decidePhase(tt.conditions); got != tt.want {
				t.Errorf("decidePhase returns %s, want %s", got, tt.want)
			}
		})
	}
}

func TestAwaitingTurnIsUpdatePending(t *testing.T) {
	phase := decidePhase([]api.Condition{
		{Type: "ClusterConverged", Status: "False", Reason: "AwaitingTurn"},
	})
	if phase != api.PhaseUpdatePending {
		t.Errorf("the phase is %s, want UpdatePending because the machine waits for its turn", phase)
	}
}

func TestReadyRollupAllTrue(t *testing.T) {
	c := readyCondition([]api.Condition{
		condition("HostEntriesApplied", "True", "Applied"),
	})
	if c.Status != api.ConditionTrue || c.Reason != "Reconciled" {
		t.Errorf("the Ready condition is %+v, want status True with reason Reconciled", c)
	}
}

func TestReadyRollupReasonFollowsThePhase(t *testing.T) {
	c := readyCondition([]api.Condition{
		condition("HostEntriesApplied", "False", "AwaitingPodRefresh"),
	})
	if c.Status != api.ConditionFalse || c.Reason != "UpdatePending" {
		t.Errorf("the Ready condition is %+v, want status False with reason UpdatePending", c)
	}
}

func TestReadyRollupIsDegradedWhenThePhaseIsDegraded(t *testing.T) {
	c := readyCondition([]api.Condition{
		condition("SysctlsApplied", "False", "ApplyFailed"),
	})
	if c.Status != api.ConditionFalse || c.Reason != "Degraded" {
		t.Errorf("the Ready condition is %+v, want status False with reason Degraded", c)
	}
}

func TestReadyRollupSkipsThePreviousReadyAndRebootApproved(t *testing.T) {
	c := readyCondition([]api.Condition{
		condition("Ready", "False", "Degraded"),
		condition(machine.RebootApprovedCondition, "True", "DisruptionBudgetAllows"),
		condition("HostEntriesApplied", "True", "Applied"),
	})
	if c.Status != api.ConditionTrue || c.Reason != "Reconciled" {
		t.Errorf("the Ready condition is %+v, want status True with reason Reconciled because Ready and RebootApproved are skipped", c)
	}
}

func TestAwaitingPodRefreshIsUpdatePending(t *testing.T) {
	phase := decidePhase([]api.Condition{
		{Type: "HostEntriesApplied", Status: "False", Reason: "AwaitingPodRefresh"},
	})
	if phase != api.PhaseUpdatePending {
		t.Errorf("the phase is %s, want UpdatePending because the pod template is stale", phase)
	}
}

// healthyBesides returns the conditions of a machine where everything
// is True except the wireless condition, so that condition alone
// decides the phase.
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

// oneRadio returns the interfaces of a machine with one wired port and
// one radio in the given state.
func oneRadio(state machine.WirelessState) []machine.InterfaceStatus {
	return []machine.InterfaceStatus{
		{Name: "eth0", Address: "10.10.0.5/24"},
		{Name: "wlan0", Wireless: &machine.WirelessStatus{SSID: "homenet", State: state}},
	}
}

func TestARadioStillJoiningLeavesTheMachineReady(t *testing.T) {
	wireless := wirelessCondition(oneRadio(machine.WirelessAssociating))
	if wireless.Status != api.ConditionFalse || wireless.Reason != "Joining" {
		t.Fatalf("the wireless condition is %+v, want status False with reason Joining", wireless)
	}
	if phase := decidePhase(healthyBesides(wireless)); phase != api.PhaseReady {
		t.Errorf("the phase is %s with a radio still joining, want Ready", phase)
	}
}

func TestASettledRadioFailureIsDegraded(t *testing.T) {
	for _, state := range []machine.WirelessState{
		machine.WirelessNoCarrier, machine.WirelessNotRaised, machine.WirelessWrongKey,
	} {
		t.Run(string(state), func(t *testing.T) {
			wireless := wirelessCondition(oneRadio(state))
			if wireless.Reason != "NotJoined" {
				t.Errorf("the wireless reason is %q, want NotJoined", wireless.Reason)
			}
			if phase := decidePhase(healthyBesides(wireless)); phase != api.PhaseDegraded {
				t.Errorf("the phase is %s after a settled radio failure, want Degraded", phase)
			}
		})
	}
}

func TestDrainingIsUpdating(t *testing.T) {
	phase := decidePhase([]api.Condition{
		{Type: "SpecConverged", Status: "False", Reason: "Draining"},
	})
	if phase != api.PhaseUpdating {
		t.Errorf("the phase is %s, want Updating because draining comes before a reboot", phase)
	}
}

func TestALiveLoadInProgressIsUpdating(t *testing.T) {
	phase := decidePhase([]api.Condition{
		{Type: "SpecConverged", Status: "False", Reason: "LoadRequested"},
	})
	if phase != api.PhaseUpdating {
		t.Errorf("the phase is %s, want Updating because a live load is in progress", phase)
	}
}
