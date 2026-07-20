package main

// Demotion cleanup finishes what a role change starts.
//
// Promotion completes on its own. A follower rebooted into the
// leader role starts a control plane, and k3s labels the Node and
// registers the etcd member without help. Demotion does not
// complete on its own. A leader rebooted into the follower role
// runs `k3s agent`, but the Kubernetes Node object it reattaches to
// still claims control-plane and etcd. Worse, its etcd membership
// stays registered. A registered member that never votes still
// counts toward the quorum size, so it breaks the majority math the
// next time an actual leader reboots.
//
// The demoted machine's own operator holds everything needed to
// finish this job. The facts state what this machine is (a
// follower), and the Node object states what the cluster still
// thinks it is. When these disagree, the operator requests a reboot
// through the intent channel it already owns, then deletes its own
// Node object. Deleting the Node triggers k3s's etcd
// member-removal controller. The operator writes the intent first,
// deliberately. Deleting the Node kills this same pod, because pods
// bound to a deleted Node are garbage-collected, and a machine whose
// Node is gone cannot re-register without a reboot. So the reboot
// must already be in progress before the delete happens. If the
// delete itself fails, the next boot detects the same mismatch and
// tries again. Each retry costs a reboot, but the state converges.
//
// The reboot policy gates all of this, the same as every other
// staged change. Under Manual, the operator only reports the state
// (DemotionPending), because deleting the Node without a reboot
// already under way would strand a working machine.

import (
	"fmt"

	"github.com/liken-sh/liken/api"
	"github.com/liken-sh/liken/kubernetes"
	"github.com/liken-sh/liken/machine"
)

// The role labels that k3s applies to a Node when it runs a control
// plane. Their presence on a follower's Node is what a demotion
// leaves behind.
var leaderNodeLabels = []string{
	"node-role.kubernetes.io/control-plane",
	"node-role.kubernetes.io/etcd",
}

// A demotion is the cleanup decision: whether to act, and the
// NodeCurrent condition to publish either way.
type demotion struct {
	cleanup   bool
	condition api.Condition
}

// decideDemotion compares what this machine is, its derived role,
// against what its Node object claims. Only one mismatch is the
// operator's job to fix: a follower whose Node still says
// control-plane. The other direction, a leader whose Node lacks the
// labels, only means a control plane still starting up, and k3s
// finishes that on its own.
//
// The demotion's reboot waits its turn like any other reboot. A
// demotion always follows a Cluster edit, so other machines are
// converging on the same edit at the same time. This is exactly the
// traffic the rollout conductor is built to sequence.
func decideDemotion(role api.Role, nodeLabels map[string]string, rebootPolicy machine.RebootPolicy, t turn) demotion {
	nodeCurrent := func(status api.ConditionStatus, reason, message string) api.Condition {
		return api.Condition{Type: "NodeCurrent", Status: status, Reason: reason, Message: message}
	}

	if role != api.RoleFollower {
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

// carryOutDemotion performs the cleanup. It writes the reboot intent
// first, because deleting the Node kills this pod, so the reboot
// must already be in progress. Then it deletes the Node, which
// triggers etcd member removal.
func carryOutDemotion(c *kubernetes.Client, name string, d demotion) api.Condition {
	if !d.cleanup {
		return d.condition
	}
	intent := &machine.RebootIntent{Reason: "completing the demotion to follower"}
	if err := machine.WriteRebootIntent(machine.OperatorRunDir, intent); err != nil {
		return api.Condition{Type: "NodeCurrent", Status: api.ConditionFalse, Reason: "DemotionFailed",
			Message: fmt.Sprintf("writing the reboot intent: %v", err)}
	}
	if err := deleteNode(c, name); err != nil {
		// The reboot is already in progress. The next boot detects
		// the mismatch again and retries the delete.
		fmt.Printf("deleting the stale Node %s: %v\n", name, err)
	} else {
		fmt.Printf("deleted the stale control-plane Node %s; rebooting to re-register as a follower\n", name)
	}
	return d.condition
}
