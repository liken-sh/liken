package main

// Tests for the demotion cleanup decision: a machine whose derived
// role is follower but whose Node object still claims control-plane
// was demoted, and the leftover Node (with its etcd membership) must
// go — automatically, because a phantom etcd voice breaks quorum
// math the next time a real leader reboots.

import (
	"testing"

	"github.com/chrisguidry/liken/machine"
)

func TestDemotionCleanupFiresForADemotedFollower(t *testing.T) {
	labels := map[string]string{"node-role.kubernetes.io/control-plane": "true"}
	d := decideDemotion(machine.RoleFollower, labels, machine.RebootAuto, turnGranted)
	if !d.cleanup {
		t.Error("a follower with a control-plane Node needs cleanup")
	}
	if d.condition.Status != "False" || d.condition.Reason != "DemotionRebooting" {
		t.Errorf("got %+v", d.condition)
	}
}

func TestDemotionCleanupWaitsUnderManualPolicy(t *testing.T) {
	labels := map[string]string{"node-role.kubernetes.io/etcd": "true"}
	d := decideDemotion(machine.RoleFollower, labels, machine.RebootManual, turnGranted)
	if d.cleanup {
		t.Error("cleanup deletes the Node and reboots; Manual policy must gate it")
	}
	if d.condition.Status != "False" || d.condition.Reason != "DemotionPending" {
		t.Errorf("got %+v", d.condition)
	}
}

func TestACleanFollowerNeedsNothing(t *testing.T) {
	d := decideDemotion(machine.RoleFollower, map[string]string{"kubernetes.io/hostname": "node-4"}, machine.RebootAuto, turnGranted)
	if d.cleanup || d.condition.Status != "True" {
		t.Errorf("got %+v", d)
	}
}

func TestALeaderIsAlwaysCurrent(t *testing.T) {
	// A leader's control-plane labels are exactly right, and a leader
	// still coming up (labels not yet set) is k3s's business, not the
	// operator's.
	labels := map[string]string{"node-role.kubernetes.io/control-plane": "true"}
	d := decideDemotion(machine.RoleLeader, labels, machine.RebootAuto, turnGranted)
	if d.cleanup || d.condition.Status != "True" {
		t.Errorf("got %+v", d)
	}
}

func TestDemotionWaitsForItsRebootTurn(t *testing.T) {
	labels := map[string]string{"node-role.kubernetes.io/control-plane": "true"}
	d := decideDemotion(machine.RoleFollower, labels, machine.RebootAuto, turnAwaiting)
	if d.cleanup {
		t.Error("no turn granted means no Node deletion and no reboot")
	}
	if d.condition.Reason != "AwaitingTurn" {
		t.Errorf("got %+v", d.condition)
	}
}
