package main

// Draining empties a node of workloads before its granted reboot.
//
// An unannounced reboot stops every pod on the machine at once, in
// the middle of whatever it was doing. Kubernetes answers this
// problem with `kubectl drain`: mark the node unschedulable so
// nothing new lands on it (a cordon), then ask each pod to leave
// through the Eviction API. Asking, rather than deleting directly,
// is the point. The API server refuses an eviction (429) when it
// would violate the workload's own PodDisruptionBudget. That
// refusal is the entire benefit over plain deletion: the workload's
// availability promise holds while the machine empties. This file
// implements that procedure. The machine's own operator runs it on
// itself when the rollout conductor grants its turn (rollout.go).
//
// The drain runs in steps, by design. One reconcile pass cordons
// the node and asks pods to leave; the next pass checks what
// remains and asks again. Blocking a pass until the node empties
// would stop the operator's heartbeat, and a machine that stops
// sending its heartbeat gets declared Lost. So the drain must never
// make a healthy machine look dead. The drain's state lives on the
// Node itself, in annotations, so a restarted operator resumes where
// it left off.

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/liken-sh/liken/api"
	"github.com/liken-sh/liken/kubernetes"
)

const (
	// cordonedAnnotation marks a cordon as one that liken applied.
	// Uncordoning checks this annotation: a node that was already
	// unschedulable when the drain started was cordoned by a person,
	// and the operator must not remove their cordon.
	cordonedAnnotation = "liken.sh/cordoned"

	// drainingSinceAnnotation records when the drain's deadline
	// started. It lives on the Node rather than in memory, so the
	// deadline keeps running across operator restarts.
	drainingSinceAnnotation = "liken.sh/draining-since"

	// mirrorPodAnnotation is how the kubelet marks a static pod's
	// reflection in the API server. The kubelet recreates mirror pods
	// from disk, so the operator cannot evict them, and the drain
	// skips them.
	mirrorPodAnnotation = "kubernetes.io/config.mirror"

	// drainDeadline limits how long workloads may delay the reboot. A
	// pod that has not moved by this deadline (because a
	// PodDisruptionBudget can never be satisfied, or a workload has
	// nowhere to go) stays running through the reboot instead. A
	// machine that can never apply its staged change is worse than a
	// pod that has to restart.
	drainDeadline = 5 * time.Minute
)

// evictablePods returns the pods a drain actually has to move. It
// excludes DaemonSet pods, because the DaemonSet controller ignores
// the cordon and would just recreate them (this operator is itself
// a DaemonSet pod). It also excludes mirror pods, because the
// kubelet recreates those from disk, and pods that have already run
// to completion.
func evictablePods(pods []kubernetes.Pod) []kubernetes.Pod {
	var evictable []kubernetes.Pod
	for _, p := range pods {
		if p.Completed() {
			continue
		}
		if _, ok := p.Metadata.Annotations[mirrorPodAnnotation]; ok {
			continue
		}
		if p.IsDaemon() {
			continue
		}
		evictable = append(evictable, p)
	}
	return evictable
}

// A drainStep is one pass's worth of drain: the Node patch to apply
// (nil when the cordon and deadline record are already in place),
// the pods to ask to leave, and whether the node is clear, meaning
// nothing is left delaying the reboot.
type drainStep struct {
	patch []byte
	evict []kubernetes.Pod
	clear bool
}

// decideDrainStep is the drain's decision for one pass. The
// deadline runs from the draining-since annotation. A node without
// this annotation gets it recorded now, along with the cordon, if
// the node is not already unschedulable.
func decideDrainStep(node *nodeObject, pods []kubernetes.Pod, now time.Time) drainStep {
	var step drainStep

	since, err := time.Parse(time.RFC3339, node.Metadata.Annotations[drainingSinceAnnotation])
	if !node.Spec.Unschedulable || err != nil {
		annotations := map[string]string{drainingSinceAnnotation: now.Format(time.RFC3339)}
		patch := map[string]any{"metadata": map[string]any{"annotations": annotations}}
		if !node.Spec.Unschedulable {
			annotations[cordonedAnnotation] = "true"
			patch["spec"] = map[string]any{"unschedulable": true}
		}
		step.patch, _ = json.Marshal(patch)
		since = now
	}

	if now.Sub(since) > drainDeadline {
		step.clear = true // the reboot proceeds; whatever remains stays running through it
		return step
	}
	evictable := evictablePods(pods)
	step.evict = evictable
	step.clear = len(evictable) == 0
	return step
}

// gateThroughDrain intercepts a convergence that wants a reboot and
// releases it only once this machine's node is clear. It cordons
// the node and evicts pods. Until nothing evictable remains, or the
// deadline passes, it holds the reboot and reports the drain's
// progress on the same condition.
func gateThroughDrain(c *kubernetes.Client, node *nodeObject, conv convergence, now time.Time) convergence {
	pods, err := kubernetes.ListPodsOnNode(c, node.Metadata.Name)
	if err != nil {
		fmt.Printf("listing pods for the drain: %v\n", err)
		return holdForDrain(conv, "listing this node's pods failed; retrying")
	}
	step := decideDrainStep(node, pods, now)
	if step.patch != nil {
		if err := c.PatchJSON(nodesPath+"/"+node.Metadata.Name, step.patch); err != nil {
			fmt.Printf("cordoning %s: %v\n", node.Metadata.Name, err)
			return holdForDrain(conv, "cordoning this node failed; retrying")
		}
		fmt.Printf("cordoned %s ahead of its reboot\n", node.Metadata.Name)
	}
	for _, p := range step.evict {
		if err := kubernetes.EvictPod(c, p); err != nil {
			// A refusal here is usually a PodDisruptionBudget working
			// as intended. The next pass asks again.
			fmt.Printf("evicting %s/%s: %v\n", p.Metadata.Namespace, p.Metadata.Name, err)
		}
	}
	if step.clear {
		return conv
	}
	return holdForDrain(conv, fmt.Sprintf("draining this node ahead of the reboot; %d pods still to move", len(step.evict)))
}

// holdForDrain keeps reporting the convergence on its own condition
// while the drain works. It keeps the same condition type, withholds
// the reboot, and sets the reason to Draining.
func holdForDrain(conv convergence, message string) convergence {
	conv.requestReboot = false
	conv.condition = api.Condition{
		Type: conv.condition.Type, Status: api.ConditionFalse, Reason: "Draining", Message: message,
	}
	return conv
}

// decideUncordon reports whether a node carries a cordon that this
// operator applied. It returns true only for the operator's own
// cordon: the annotation records that the operator set it, and a
// cordon without the annotation belongs to a person.
func decideUncordon(node *nodeObject) bool {
	return node.Spec.Unschedulable && node.Metadata.Annotations[cordonedAnnotation] == "true"
}

// uncordonPatch releases the node back to the scheduler and removes
// the drain's recorded state. In a merge patch, a null value deletes
// the key.
func uncordonPatch() []byte {
	patch, _ := json.Marshal(map[string]any{
		"spec": map[string]any{"unschedulable": false},
		"metadata": map[string]any{"annotations": map[string]any{
			cordonedAnnotation:      nil,
			drainingSinceAnnotation: nil,
		}},
	})
	return patch
}
