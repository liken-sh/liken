package main

// Tests for the demotion cleanup decision. A machine whose
// derived role is follower, but whose Node object still claims
// control-plane, was demoted. The leftover Node, with its etcd
// membership, must be removed automatically, because a dead etcd
// member still counts toward the quorum size and breaks the
// majority math the next time an actual leader reboots.

import (
	"testing"

	"github.com/liken-sh/liken/api"
	"github.com/liken-sh/liken/machine"
)

func TestDemotionCleanupFiresForADemotedFollower(t *testing.T) {
	labels := map[string]string{"node-role.kubernetes.io/control-plane": "true"}
	d := decideDemotion(api.RoleFollower, labels, machine.RebootAuto, turnGranted)
	if !d.cleanup {
		t.Error("a follower with a control-plane Node needs cleanup")
	}
	if d.condition.Status != "False" || d.condition.Reason != "DemotionRebooting" {
		t.Errorf("got %+v", d.condition)
	}
}

func TestDemotionCleanupWaitsUnderManualPolicy(t *testing.T) {
	labels := map[string]string{"node-role.kubernetes.io/etcd": "true"}
	d := decideDemotion(api.RoleFollower, labels, machine.RebootManual, turnGranted)
	if d.cleanup {
		t.Error("cleanup deletes the Node and reboots; Manual policy must gate it")
	}
	if d.condition.Status != "False" || d.condition.Reason != "DemotionPending" {
		t.Errorf("got %+v", d.condition)
	}
}

func TestACleanFollowerNeedsNothing(t *testing.T) {
	d := decideDemotion(api.RoleFollower, map[string]string{"kubernetes.io/hostname": "node-4"}, machine.RebootAuto, turnGranted)
	if d.cleanup || d.condition.Status != "True" {
		t.Errorf("got %+v", d)
	}
}

func TestALeaderIsAlwaysCurrent(t *testing.T) {
	// A leader's control-plane labels are exactly right. A leader
	// still starting up, with labels not yet set, is k3s's job, not
	// the operator's.
	labels := map[string]string{"node-role.kubernetes.io/control-plane": "true"}
	d := decideDemotion(api.RoleLeader, labels, machine.RebootAuto, turnGranted)
	if d.cleanup || d.condition.Status != "True" {
		t.Errorf("got %+v", d)
	}
}

func TestDemotionWaitsForItsRebootTurn(t *testing.T) {
	labels := map[string]string{"node-role.kubernetes.io/control-plane": "true"}
	d := decideDemotion(api.RoleFollower, labels, machine.RebootAuto, turnAwaiting)
	if d.cleanup {
		t.Error("no turn granted means no Node deletion and no reboot")
	}
	if d.condition.Reason != "AwaitingTurn" {
		t.Errorf("got %+v", d.condition)
	}
}
