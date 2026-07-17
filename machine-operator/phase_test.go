package main

// The phase table, pinned: which condition puts a machine in which
// phase, and which phase wins when several conditions disagree.

import (
	"testing"

	"github.com/liken-sh/liken/api"
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
			"unreadable facts leave the operator blind",
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

func TestDrainingIsUpdating(t *testing.T) {
	phase := decidePhase([]api.Condition{
		{Type: "SpecConverged", Status: "False", Reason: "Draining"},
	})
	if phase != api.PhaseUpdating {
		t.Errorf("draining is the reboot's opening move: %s", phase)
	}
}
