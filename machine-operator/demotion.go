package main

// Demotion cleanup finishes what a role change starts.
//
// Promotion is self-completing: a follower rebooted into the leader
// role starts a control plane, and k3s labels the Node and registers
// the etcd member on its own. Demotion is not. A leader rebooted
// into the follower role runs `k3s agent`, but the Kubernetes Node
// object it re-attaches to still claims control-plane and etcd, and,
// worse, its etcd membership stays registered. A registered member
// that never votes still counts toward the quorum size, so it breaks
// the majority math the next time a real leader reboots.
//
// The demoted machine's own operator holds everything needed to
// finish the job: the facts say what this machine is (follower), and
// the Node object says what the cluster still thinks it is. When
// they disagree, the operator requests a reboot through the intent
// channel it already owns, then deletes its own Node object.
// Deleting the Node is what triggers k3s's etcd member-removal
// controller. The intent is written first, deliberately: deleting
// the Node kills this very pod (pods bound to a deleted Node are
// garbage-collected), and a machine whose Node is gone cannot
// re-register without a reboot, so the reboot must already be in
// flight before the delete lands. If the delete itself fails, the
// next boot simply detects the same mismatch and retries. Each
// retry costs a reboot, but the state converges.
//
// The reboot policy gates all of it, same as every other staged
// change: under Manual the operator only reports (DemotionPending),
// because deleting the Node without the reboot in hand would strand
// a working machine.

import (
	"fmt"

	"github.com/chrisguidry/liken/kubernetes"
	"github.com/chrisguidry/liken/machine"
)

// The role labels k3s stamps on a Node when it runs a control plane.
// Their presence on a follower's Node is what a demotion leaves
// behind.
var leaderNodeLabels = []string{
	"node-role.kubernetes.io/control-plane",
	"node-role.kubernetes.io/etcd",
}

// A demotion is the cleanup decision: whether to act, and the
// NodeCurrent condition to publish either way.
type demotion struct {
	cleanup   bool
	condition machine.Condition
}

// decideDemotion compares what this machine is (the derived role)
// against what its Node object claims. Only one mismatch is the
// operator's to fix: a follower whose Node still says control-plane.
// The other direction, a leader whose Node lacks the labels, is
// just a control plane still coming up, and k3s finishes that on
// its own.
//
// The demotion's reboot waits its turn like any other: a demotion is
// always the aftermath of a Cluster edit, which means other machines
// are converging on the same edit at the same time, and this is
// exactly the traffic the rollout conductor sequences.
func decideDemotion(role machine.Role, nodeLabels map[string]string, rebootPolicy machine.RebootPolicy, t turn) demotion {
	nodeCurrent := func(status machine.ConditionStatus, reason, message string) machine.Condition {
		return machine.Condition{Type: "NodeCurrent", Status: status, Reason: reason, Message: message}
	}

	if role != machine.RoleFollower {
		return demotion{condition: nodeCurrent("True", "NodeMatchesRole", "the Node object matches this machine's role")}
	}
	stale := false
	for _, label := range leaderNodeLabels {
		if _, ok := nodeLabels[label]; ok {
			stale = true
		}
	}
	if !stale {
		return demotion{condition: nodeCurrent("True", "NodeMatchesRole", "the Node object matches this machine's role")}
	}

	if rebootPolicy != machine.RebootAuto {
		return demotion{condition: nodeCurrent("False", "DemotionPending",
			"this machine was demoted to follower but its Node object still claims control-plane; set rebootPolicy: Auto to let the operator delete the Node and reboot, completing the demotion")}
	}
	if t == turnAwaiting {
		return demotion{condition: nodeCurrent("False", "AwaitingTurn",
			"this machine was demoted to follower; waiting for the cluster to grant a reboot turn to complete the demotion")}
	}
	return demotion{
		cleanup: true,
		condition: nodeCurrent("False", "DemotionRebooting",
			"completing the demotion: deleting the stale control-plane Node object and rebooting to re-register as a follower"),
	}
}

// carryOutDemotion performs the cleanup: reboot intent first (the
// delete kills this pod, so the reboot must already be in flight),
// then the Node deletion that triggers etcd member removal.
func carryOutDemotion(c *kubernetes.Client, name string, d demotion) machine.Condition {
	if !d.cleanup {
		return d.condition
	}
	intent := &machine.RebootIntent{Reason: "completing the demotion to follower"}
	if err := machine.WriteRebootIntent(machine.OperatorRunDir, intent); err != nil {
		return machine.Condition{Type: "NodeCurrent", Status: machine.ConditionFalse, Reason: "DemotionFailed",
			Message: fmt.Sprintf("writing the reboot intent: %v", err)}
	}
	if err := deleteNode(c, name); err != nil {
		// The reboot is already in flight; the next boot re-detects
		// the mismatch and retries the delete.
		fmt.Printf("deleting the stale Node %s: %v\n", name, err)
	} else {
		fmt.Printf("deleted the stale control-plane Node %s; rebooting to re-register as a follower\n", name)
	}
	return d.condition
}
