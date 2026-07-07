package main

// Drain: emptying a node of workloads before its granted reboot.
//
// A reboot that just happens takes every pod on the machine down with
// it, mid-request. Kubernetes' answer is `kubectl drain`: mark the
// node unschedulable so nothing new lands (cordon), then ask each pod
// to leave through the Eviction API — *ask*, because an eviction is
// refused (429) while it would violate the workload's own
// PodDisruptionBudget. That refusal is the entire value over plain
// deletion: the workload's availability promise holds while the
// machine empties. This file is that procedure, run by the machine's
// own operator on itself when the rollout conductor grants its turn
// (rollout.go).
//
// The drain is incremental on purpose: one reconcile pass cordons and
// asks, the next pass sees what's left and asks again. Blocking a
// pass until the node empties would stop the operator's heartbeat,
// and a machine that stops heartbeating gets declared Lost — the
// drain must never look like a death. State lives on the Node itself
// (annotations), so a restarted operator resumes where it left off.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/chrisguidry/liken/machine"
)

const (
	// cordonedAnnotation marks a cordon as liken's own. Uncordoning is
	// gated on it: a node that was already unschedulable when the
	// drain arrived was cordoned by a human, and their cordon is not
	// ours to take back.
	cordonedAnnotation = "liken.sh/cordoned"

	// drainingSinceAnnotation anchors the drain's deadline. It lives
	// on the Node rather than in memory so the clock keeps running
	// across operator restarts.
	drainingSinceAnnotation = "liken.sh/draining-since"

	// mirrorPodAnnotation is how the kubelet marks a static pod's
	// API-server reflection. Mirror pods can't be evicted — the
	// kubelet recreates them from disk — so the drain skips them.
	mirrorPodAnnotation = "kubernetes.io/config.mirror"

	// drainDeadline bounds how long workloads may hold the reboot. A
	// pod that won't move by then — a PodDisruptionBudget that can
	// never be satisfied, a workload with nowhere to go — rides
	// through the reboot instead, because a machine that can never
	// apply its staged change is worse than a pod restarting.
	drainDeadline = 5 * time.Minute
)

const podsPath = "/api/v1/pods"

type ownerReference struct {
	Kind string `json:"kind"`
}

// podObject is the sliver of a Pod the drain needs: identity, who owns
// it, and whether it is still running.
type podObject struct {
	Metadata struct {
		Name            string            `json:"name"`
		Namespace       string            `json:"namespace"`
		Annotations     map[string]string `json:"annotations"`
		OwnerReferences []ownerReference  `json:"ownerReferences"`
	} `json:"metadata"`
	Status struct {
		Phase string `json:"phase"`
	} `json:"status"`
}

func listPodsOnNode(c *apiClient, node string) ([]podObject, error) {
	var list struct {
		Items []podObject `json:"items"`
	}
	path := podsPath + "?fieldSelector=spec.nodeName%3D" + node
	if err := c.requestJSON(http.MethodGet, path, nil, &list); err != nil {
		return nil, err
	}
	return list.Items, nil
}

// evictPod asks a pod to leave through the eviction subresource. The
// Eviction API is what makes a drain polite: the request is refused
// while removing the pod would violate its PodDisruptionBudget, and
// the caller simply asks again later.
func evictPod(c *apiClient, p podObject) error {
	body, err := json.Marshal(map[string]any{
		"apiVersion": "policy/v1",
		"kind":       "Eviction",
		"metadata":   map[string]string{"name": p.Metadata.Name, "namespace": p.Metadata.Namespace},
	})
	if err != nil {
		return err
	}
	path := "/api/v1/namespaces/" + p.Metadata.Namespace + "/pods/" + p.Metadata.Name + "/eviction"
	return c.requestJSON(http.MethodPost, path, body, nil)
}

// evictablePods is the set a drain actually has to move: not
// DaemonSet pods (the daemon controller ignores the cordon and would
// just recreate them — this operator is itself one), not mirror pods
// (the kubelet recreates those from disk), and not pods that already
// ran to completion.
func evictablePods(pods []podObject) []podObject {
	var evictable []podObject
	for _, p := range pods {
		if p.Status.Phase == "Succeeded" || p.Status.Phase == "Failed" {
			continue
		}
		if _, ok := p.Metadata.Annotations[mirrorPodAnnotation]; ok {
			continue
		}
		daemon := false
		for _, owner := range p.Metadata.OwnerReferences {
			if owner.Kind == "DaemonSet" {
				daemon = true
			}
		}
		if daemon {
			continue
		}
		evictable = append(evictable, p)
	}
	return evictable
}

// A drainStep is one pass's worth of drain: the Node patch to apply
// (nil when the cordon and deadline anchor are already in place), the
// pods to ask to leave, and whether the node is clear — nothing left
// holding the reboot.
type drainStep struct {
	patch []byte
	evict []podObject
	clear bool
}

// decideDrainStep is the drain's decision for one pass. The deadline
// runs from the draining-since annotation; a node without one gets it
// stamped now, along with the cordon if the node isn't already
// unschedulable.
func decideDrainStep(node *nodeObject, pods []podObject, now time.Time) drainStep {
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

	evictable := evictablePods(pods)
	if now.Sub(since) > drainDeadline {
		step.clear = true // the reboot proceeds; whatever remains rides through it
		return step
	}
	step.evict = evictable
	step.clear = len(evictable) == 0
	return step
}

// gateThroughDrain intercepts a convergence that wants a reboot and
// releases it only once this machine's node is clear: cordon, evict,
// and until nothing evictable remains (or the deadline passes), hold
// the reboot and report the drain's progress on the same condition.
func gateThroughDrain(c *apiClient, node *nodeObject, conv convergence, now time.Time) convergence {
	pods, err := listPodsOnNode(c, node.Metadata.Name)
	if err != nil {
		fmt.Printf("listing pods for the drain: %v\n", err)
		return holdForDrain(conv, "listing this node's pods failed; retrying")
	}
	step := decideDrainStep(node, pods, now)
	if step.patch != nil {
		if err := c.patchJSON(nodesPath+"/"+node.Metadata.Name, step.patch); err != nil {
			fmt.Printf("cordoning %s: %v\n", node.Metadata.Name, err)
			return holdForDrain(conv, "cordoning this node failed; retrying")
		}
		fmt.Printf("cordoned %s ahead of its reboot\n", node.Metadata.Name)
	}
	for _, p := range step.evict {
		if err := evictPod(c, p); err != nil {
			// A refusal here is usually a PodDisruptionBudget doing
			// its job; the next pass asks again.
			fmt.Printf("evicting %s/%s: %v\n", p.Metadata.Namespace, p.Metadata.Name, err)
		}
	}
	if step.clear {
		return conv
	}
	return holdForDrain(conv, fmt.Sprintf("draining this node ahead of the reboot; %d pods still to move", len(step.evict)))
}

// holdForDrain keeps the convergence's story on its own condition
// while the drain works: same type, reboot withheld, reason Draining.
func holdForDrain(conv convergence, message string) convergence {
	conv.requestReboot = false
	conv.condition = machine.Condition{
		Type: conv.condition.Type, Status: "False", Reason: "Draining", Message: message,
	}
	return conv
}

// decideUncordon reports whether a node is wearing a cordon this
// operator put there. True only for our own: the annotation is the
// claim, and a cordon without it belongs to a human.
func decideUncordon(node *nodeObject) bool {
	return node.Spec.Unschedulable && node.Metadata.Annotations[cordonedAnnotation] == "true"
}

// uncordonPatch releases the node back to the scheduler and removes
// the drain's bookkeeping; merge-patch null is how a key is deleted.
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
