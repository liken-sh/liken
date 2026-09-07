package main

// The heal for a durable copy stranded on a node that is gone. A
// node-local class binds a claim to the machine the copy first landed
// on, so a copy on a machine that never comes back waits there for good.
// The heal deletes the copy, and its claim with it on a class that pins
// the claim, and the reconcile stands the copy again wherever the
// scheduler can place it. The peers hold the rows, so the fresh copy
// syncs from them.
//
// On a per-node class the claim is the store's own and every copy mounts
// it, so the heal deletes the pod alone. The copy stands again on another
// node with a fresh directory there.

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"time"
)

// How long a node stays NotReady before its copies are healed. Ten
// minutes clears a liken machine's upgrade reboot without a false heal.
const strandedNodeGrace = 10 * time.Minute

// The node condition the heal reads, and the verdict that means the
// kubelet is answering.
const (
	nodeConditionReady = "Ready"
	nodeReadyVerdict   = "True"
)

// A node, as this operator reads it: the name a pod's nodeName carries,
// and the conditions the Ready verdict is one of.
type Node struct {
	Metadata ObjectMeta `json:"metadata"`
	Status   NodeStatus `json:"status"`
}

type NodeStatus struct {
	Conditions []NodeCondition `json:"conditions,omitempty"`
}

// The fields this operator reads of a node condition. The status is a
// string, because a condition has three states. The transition time is
// the clock the grace is measured on, so no pass keeps a timer.
type NodeCondition struct {
	Type               string    `json:"type"`
	Status             string    `json:"status"`
	LastTransitionTime time.Time `json:"lastTransitionTime,omitzero"`
}

type NodeList struct {
	Metadata ListMeta `json:"metadata"`
	Items    []Node   `json:"items"`
}

// Nodes are cluster-scoped, so the path carries no namespace.
const nodesPath = "/api/v1/nodes"

// ListNodes reads every node of the cluster, for the Ready verdict of the
// machine each durable copy runs on.
func ListNodes(ctx context.Context, c *Client) (*NodeList, error) {
	list := &NodeList{}
	if err := c.RequestJSON(ctx, http.MethodGet, nodesPath, nil, list); err != nil {
		return nil, err
	}
	return list, nil
}

// Take down every durable copy on a node that has been NotReady past the
// grace. The store label is the guard: a Job's pod and a screen pod carry
// a catalog agent too, and neither holds the store of record. A failure
// on one copy is reported, and the rest still heal.
func (o *operator) healStrandedStoreReplicas(ctx context.Context, pods []Pod, nodes []Node, now time.Time) {
	stranded := strandedNodes(nodes, now)
	for index := range pods {
		pod := &pods[index]
		if pod.Metadata.Labels[storeLabelKey] == "" || !stranded[pod.Spec.NodeName] {
			continue
		}
		if err := o.healStoreReplica(ctx, pod); err != nil {
			fmt.Fprintf(os.Stderr, "healing the stranded copy %s/%s: %v\n",
				pod.Metadata.Namespace, pod.Metadata.Name, err)
		}
	}
}

// The nodes whose Ready condition has said anything but True for longer
// than the grace. A node with no Ready condition, or one with no
// transition time, is not one of them: a verdict with no time is no
// verdict.
func strandedNodes(nodes []Node, now time.Time) map[string]bool {
	stranded := map[string]bool{}
	for index := range nodes {
		node := &nodes[index]
		if notReadyPastGrace(node, now) {
			stranded[node.Metadata.Name] = true
		}
	}
	return stranded
}

func notReadyPastGrace(node *Node, now time.Time) bool {
	for _, condition := range node.Status.Conditions {
		if condition.Type != nodeConditionReady || condition.Status == nodeReadyVerdict {
			continue
		}
		return !condition.LastTransitionTime.IsZero() &&
			now.Sub(condition.LastTransitionTime) > strandedNodeGrace
	}
	return false
}

// Take down one stranded copy: the pod first, its claim after, so the
// volume is released before it is deleted. The pod delete is forced,
// because the kubelet that would confirm a graceful one is the thing that
// is gone. A pod left Terminating would hold its claim through the claim
// protection finalizer, and the copy would never move.
//
// The claim is the one the pod itself mounts, and two guards hold before
// its delete. First, the claim carries the same store label as the pod
// it served. A claim a person made and named in the Catalog carries none,
// and the heal leaves it alone. Second, the claim's class is not
// per-node. A claim on a per-node class pins no pod, and the copies that
// still mount it need it, so the heal leaves it in place.
func (o *operator) healStoreReplica(ctx context.Context, pod *Pod) error {
	namespace, name := pod.Metadata.Namespace, pod.Metadata.Name

	if err := ForceDeletePod(ctx, o.client, namespace, name); err != nil {
		return err
	}
	mounted := storeClaimOf(pod)
	if mounted == "" {
		return nil
	}
	claim, err := GetPersistentVolumeClaim(ctx, o.client, namespace, mounted)
	if errors.Is(err, ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if claim.Metadata.Labels[storeLabelKey] != pod.Metadata.Labels[storeLabelKey] {
		return nil
	}
	perNode, err := o.classIsPerNode(ctx, claim.Spec.StorageClassName)
	if err != nil || perNode {
		return err
	}
	return DeletePersistentVolumeClaim(ctx, o.client, namespace, mounted)
}

// storeClaimOf names the claim one copy mounts, read off the pod, because
// the pod is what states which claim it runs on. A copy holds one claim
// and no other volume, so the first claim volume is the one.
func storeClaimOf(pod *Pod) string {
	for _, volume := range pod.Spec.Volumes {
		if volume.PersistentVolumeClaim != nil {
			return volume.PersistentVolumeClaim.ClaimName
		}
	}
	return ""
}
