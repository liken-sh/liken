package main

// When a leader reboots as a follower, it runs `k3s agent`, but its Node
// keeps the control-plane and etcd role labels and its etcd member stays
// registered. A member that never votes still counts toward the quorum
// size, so the cluster needs more votes than it has the next time a
// leader reboots. A follower that reboots as a leader needs no cleanup,
// because k3s labels the Node and registers the member itself.
//
// So the operator on a follower whose Node has the leader labels writes
// a reboot intent and then deletes its own Node. k3s's etcd
// member-removal controller then removes the member.
//
// The intent comes first because deleting the Node also deletes this
// pod, and a machine whose Node is gone cannot register again without a
// reboot. When the delete fails, the next boot finds the same labels and
// tries again, at the cost of one reboot per attempt.
//
// Under the Manual reboot policy the operator only reports
// DemotionPending, because deleting the Node with no reboot to follow
// would leave a working machine with no Node.

import (
	"fmt"

	"github.com/liken-sh/liken/kubernetes/apiclient"
	"github.com/liken-sh/liken/liken/api"
	"github.com/liken-sh/liken/liken/machine"
)

// leaderNodeLabels are the role labels that k3s puts on the Node of a
// control-plane machine.
var leaderNodeLabels = []string{
	"node-role.kubernetes.io/control-plane",
	"node-role.kubernetes.io/etcd",
}

// demotion is the result of decideDemotion: whether to run the cleanup,
// and the NodeCurrent condition to publish in either case.
type demotion struct {
	cleanup   bool
	condition api.Condition
}

// decideDemotion acts only on a follower whose Node has a control-plane
// or etcd label. A leader whose Node lacks the labels is still starting,
// and k3s adds them.
//
// The demotion's reboot waits for a turn like any other reboot, because
// a demotion follows a Cluster edit that other machines are also
// applying, and cluster-operator orders their reboots.
func decideDemotion(role api.Role, nodeLabels map[string]string, rebootPolicy machine.RebootPolicy, t turn) demotion {
	nodeCurrent := func(status api.ConditionStatus, reason, message string) api.Condition {
		return api.Condition{Type: "NodeCurrent", Status: status, Reason: reason, Message: message}
	}

	if role != api.RoleFollower {
		return demotion{condition: nodeCurrent("True", "NodeMatchesRole", "The Node matches the role of this machine")}
	}
	stale := false
	for _, label := range leaderNodeLabels {
		if _, ok := nodeLabels[label]; ok {
			stale = true
		}
	}
	if !stale {
		return demotion{condition: nodeCurrent("True", "NodeMatchesRole", "The Node matches the role of this machine")}
	}

	if rebootPolicy != machine.RebootAuto {
		return demotion{condition: nodeCurrent("False", "DemotionPending",
			"This machine was demoted to follower, but its Node still has the control-plane role. Set rebootPolicy to Auto so that the operator deletes the Node and reboots to complete the demotion")}
	}
	if t == turnAwaiting {
		return demotion{condition: nodeCurrent("False", "AwaitingTurn",
			"This machine was demoted to follower and waits for a reboot turn from the cluster to complete the demotion")}
	}
	return demotion{
		cleanup: true,
		condition: nodeCurrent("False", "DemotionRebooting",
			"The operator deletes the stale control-plane Node and reboots, so that the machine registers again as a follower"),
	}
}

// carryOutDemotion runs the cleanup when d.cleanup is set. It deletes
// the Node that the pass read with its UID as a precondition, so a Node
// that k3s registered again under the same name is kept.
func carryOutDemotion(c *apiclient.Client, runDir string, node *nodeObject, d demotion, out *passOutcome) api.Condition {
	if !d.cleanup {
		return d.condition
	}
	intent := &machine.RebootIntent{Reason: "completing the demotion to follower"}
	if err := machine.WriteRebootIntent(runDir, intent); err != nil {
		out.fail("writing the demotion's reboot intent", err)
		return api.Condition{Type: "NodeCurrent", Status: api.ConditionFalse, Reason: "DemotionFailed",
			Message: fmt.Sprintf("The reboot intent is not written: %v", err)}
	}
	out.wrote("writing the demotion's reboot intent")
	name := node.Metadata.Name
	if err := deleteNode(c, name, node.Metadata.UID); err != nil {
		// The reboot intent is already written, and the next boot
		// retries the delete.
		fmt.Printf("deleting the stale control-plane Node %s: %v; the next boot retries the delete\n", name, err)
	} else {
		fmt.Printf("deleted the stale control-plane Node %s; rebooting so it registers again as a follower\n", name)
	}
	return d.condition
}
