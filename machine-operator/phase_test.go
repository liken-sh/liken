package main

// The phase table, pinned: which condition puts a machine in which
// phase, and which phase wins when several conditions disagree.

import (
	"testing"

	"github.com/chrisguidry/liken/machine"
)

func condition(ctype string, status machine.ConditionStatus, reason string) machine.Condition {
	return machine.Condition{Type: ctype, Status: status, Reason: reason}
}

func TestDecidePhase(t *testing.T) {
	allTrue := []machine.Condition{
		condition("FactsPublished", "True", "FactsRead"),
		condition("SysctlsApplied", "True", "Applied"),
		condition("SpecConverged", "True", "Converged"),
		condition("ClusterConverged", "True", "Converged"),
		condition("NodeHealthy", "True", "KubeletReady"),
		condition("Ready", "True", "Reconciled"),
	}
	tests := []struct {
		name       string
		conditions []machine.Condition
		want       machine.Phase
	}{
		{"everything true is ready", allTrue, machine.PhaseReady},
		{"no conditions at all is ready", nil, machine.PhaseReady},
		{
			"unreadable facts leave the operator blind",
			[]machine.Condition{condition("FactsPublished", "False", "FactsUnreadable")},
			machine.PhaseUnknown,
		},
		{
			"no boot record yet means init is still working",
			[]machine.Condition{
				condition("SpecConverged", "Unknown", "FactsIncomplete"),
				condition("ClusterConverged", "Unknown", "FactsIncomplete"),
			},
			machine.PhaseBooting,
		},
		{
			"a rejected spec is blocked, not pending",
			[]machine.Condition{condition("SpecConverged", "False", "RejectedLastBoot")},
			machine.PhaseBlocked,
		},
		{
			"a spec the machine can't satisfy is blocked",
			[]machine.Condition{condition("SpecConverged", "False", "StagingRejected")},
			machine.PhaseBlocked,
		},
		{
			"nowhere durable to stage is blocked",
			[]machine.Condition{condition("ClusterConverged", "False", "MachineStateEphemeral")},
			machine.PhaseBlocked,
		},
		{
			"a machine with no system slots can never take a release",
			[]machine.Condition{condition("VersionConverged", "False", "NoSystemSlots")},
			machine.PhaseBlocked,
		},
		{
			"a corrupt release blocks until the catalog changes",
			[]machine.Condition{condition("VersionConverged", "False", "DigestMismatch")},
			machine.PhaseBlocked,
		},
		{
			"a machine not booted from a slot can't take releases",
			[]machine.Condition{condition("VersionConverged", "False", "NotInstalled")},
			machine.PhaseBlocked,
		},
		{
			"a staged release waits like any staged change",
			[]machine.Condition{condition("VersionConverged", "False", "RebootPending")},
			machine.PhaseUpdatePending,
		},
		{
			"a requested reboot is an update in flight",
			[]machine.Condition{condition("SpecConverged", "False", "RebootRequested")},
			machine.PhaseUpdating,
		},
		{
			"a requested k3s restart is an update in flight",
			[]machine.Condition{condition("ClusterConverged", "False", "RestartRequested")},
			machine.PhaseUpdating,
		},
		{
			"a staged restart waits like any staged change",
			[]machine.Condition{condition("CredentialsConverged", "False", "RestartPending")},
			machine.PhaseUpdatePending,
		},
		{
			"a malformed credentials Secret is blocked, not pending",
			[]machine.Condition{condition("CredentialsConverged", "False", "CredentialsInvalid")},
			machine.PhaseBlocked,
		},
		{
			"a release downloading is an update in flight",
			[]machine.Condition{condition("VersionConverged", "False", "Downloading")},
			machine.PhaseUpdating,
		},
		{
			"a demotion mid-reboot is an update in flight",
			[]machine.Condition{condition("NodeCurrent", "False", "DemotionRebooting")},
			machine.PhaseUpdating,
		},
		{
			"staged and waiting on a manual reboot",
			[]machine.Condition{condition("ClusterConverged", "False", "RebootPending")},
			machine.PhaseUpdatePending,
		},
		{
			"a pending demotion waits the same way",
			[]machine.Condition{condition("NodeCurrent", "False", "DemotionPending")},
			machine.PhaseUpdatePending,
		},
		{
			"a failing sysctl is plain degradation",
			[]machine.Condition{condition("SysctlsApplied", "False", "ApplyFailed")},
			machine.PhaseDegraded,
		},
		{
			"an unreachable cluster is plain degradation",
			[]machine.Condition{condition("ClusterConverged", "Unknown", "ClusterUnavailable")},
			machine.PhaseDegraded,
		},
		{
			"a dead kubelet is plain degradation",
			[]machine.Condition{condition("NodeHealthy", "False", "NodeNotReady")},
			machine.PhaseDegraded,
		},
		{
			"the gravest condition wins",
			[]machine.Condition{
				condition("SysctlsApplied", "False", "ApplyFailed"),
				condition("SpecConverged", "False", "RebootPending"),
				condition("ClusterConverged", "False", "RejectedLastBoot"),
			},
			machine.PhaseBlocked,
		},
		{
			"the ready roll-up never argues on its own",
			[]machine.Condition{condition("Ready", "False", "Degraded")},
			machine.PhaseReady,
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
	phase := decidePhase([]machine.Condition{
		{Type: "ClusterConverged", Status: "False", Reason: "AwaitingTurn"},
	})
	if phase != machine.PhaseUpdatePending {
		t.Errorf("waiting on the cluster's grant is a pending update: %s", phase)
	}
}

func TestDrainingIsUpdating(t *testing.T) {
	phase := decidePhase([]machine.Condition{
		{Type: "SpecConverged", Status: "False", Reason: "Draining"},
	})
	if phase != machine.PhaseUpdating {
		t.Errorf("draining is the reboot's opening move: %s", phase)
	}
}
