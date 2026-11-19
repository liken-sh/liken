package main

// A long-running pod that the kubelet ended. Every pod that standPod
// stands has restartPolicy Always, so its own containers never end it.
// The kubelet ends it for an eviction, a rejection at admission, or a
// node shutdown, and then the pod stays Failed or Succeeded, and no
// kubelet starts it again. The pass replaces it.
//
// A claim can pin the replacement to the node that ended the pod: a
// display claim pins a screen pod, and a volume on a node-local class
// pins a store copy. So the replacement waits, by the rule of
// kubernetes/endedpods: 10 seconds after the ended pod's creation for
// its first try, doubling with each try, to a limit of 5 minutes.
//
// The backstop tick of the loop (operate.go) is this component's only
// clock, and it serves every deadline of the pass. So the wait ends on
// the first tick after it, at most one backstop interval late.

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"os"
	"slices"
	"time"

	"github.com/liken-sh/liken/kubernetes/apiclient"
	"github.com/liken-sh/liken/kubernetes/endedpods"
)

// triesAnnotation records how many earlier pods of the same name the
// kubelet ended. The template hash reads the spec alone, so the count
// does not make a pod stale. It is never reset, because the wait counts
// from the pod's creation.
const triesAnnotation = "library.liken.sh/tries"

func tries(pod *Pod) int {
	return endedpods.Tries(pod.Metadata.Annotations, triesAnnotation)
}

// endOf returns what the rule of kubernetes/endedpods reads of a pod.
// The catalog agent is a native sidecar, so the time the last container
// stopped can be in initContainerStatuses.
func endOf(pod *Pod) endedpods.Pod {
	end := endedpods.Pod{
		Phase:   pod.Status.Phase,
		Reason:  pod.Status.Reason,
		Created: pod.Metadata.CreationTimestamp,
		Tries:   tries(pod),
	}
	for _, c := range pod.Status.Conditions {
		end.Conditions = append(end.Conditions, endedpods.Condition{Type: c.Type, Status: c.Status, LastTransitionTime: c.LastTransitionTime})
	}
	for _, status := range slices.Concat(pod.Status.InitContainerStatuses, pod.Status.ContainerStatuses) {
		if status.State.Terminated != nil {
			end.Finished = append(end.Finished, status.State.Terminated.FinishedAt)
		}
	}
	return end
}

// replaceEnded replaces a pod that the kubelet ended, when its turn has
// come, and answers the pod that stands after the pass: the ended pod
// while it waits, the new pod, or nil when another writer created it
// first. The ended pod stays during the wait, so a person reads its
// status, and the Catalog's condition reports it.
//
// The create follows the delete in the same pass, because the new pod
// carries the ended pod's count of tries. The API server removes a pod in
// a terminal phase at once, because its containers have stopped, so the
// name is free for the create. When a finalizer keeps the ended pod, the
// create meets a conflict, and the pass after the pod is gone creates it
// with no count, as it does when the operator restarts between the
// delete and the create. The wait then starts again at 10 seconds.
func (o *operator) replaceEnded(ctx context.Context, live, desired *Pod, now time.Time) (*Pod, error) {
	end := endOf(live)
	if now.Before(end.ReplaceAt()) || o.leftToTheHeal(live, now) {
		return live, nil
	}
	namespace, name := live.Metadata.Namespace, live.Metadata.Name
	if err := DeletePod(ctx, o.client, namespace, name); err != nil {
		return nil, err
	}
	o.forgetPod(namespace, name)

	next := *desired
	next.Metadata.Annotations = maps.Clone(desired.Metadata.Annotations)
	endedpods.SetTries(next.Metadata.Annotations, triesAnnotation, end.Tries+1)
	created, err := CreatePod(ctx, o.client, &next)
	if errors.Is(err, apiclient.ErrConflict) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if end.FirstOfLoop() {
		postPodRecreated(o.recorder, live)
	}
	return created, nil
}

// leftToTheHeal reports an ended store copy whose node is not Ready. A
// node-local class keeps the copy's data on that node, and a replacement
// that the scheduler put on another node would start empty. So the heal
// of a stranded copy (storeheal.go) takes it, after the grace that lets
// a node come back from a reboot. A copy that a node has not taken yet
// has no node to wait for, and a pod of a node that is gone from the list
// is the pod garbage collector's to delete. A Jellyfin pod and a screen
// pod keep no store, so they are replaced on any node.
//
// The heal measures its grace from the time of the node's Ready
// condition, and takes no copy on a node whose condition has no time.
// For such a node the pass measures the same grace from the copy's end,
// or from its creation when it never ran, and replaces the copy after
// it, so that no copy waits for ever.
func (o *operator) leftToTheHeal(pod *Pod, now time.Time) bool {
	if pod.Metadata.Labels[storeLabelKey] == "" || pod.Spec.NodeName == "" {
		return false
	}
	nodes, err := o.watched.readNodes()
	if err != nil {
		fmt.Fprintf(os.Stderr, "reading the nodes for the ended copy %s/%s: %v\n",
			pod.Metadata.Namespace, pod.Metadata.Name, err)
		return true
	}
	for index := range nodes.Items {
		if nodes.Items[index].Metadata.Name != pod.Spec.NodeName {
			continue
		}
		ready, timed := nodeReadiness(&nodes.Items[index])
		if ready {
			return false
		}
		if timed {
			return true
		}
		end, held := endOf(pod).EndedAt()
		if !held {
			end = pod.Metadata.CreationTimestamp
		}
		return now.Sub(end) <= strandedNodeGrace
	}
	return true
}

// nodeReadiness reports whether a node is Ready, and whether its Ready
// condition carries the time the heal measures its grace from.
func nodeReadiness(node *Node) (ready, timed bool) {
	for _, condition := range node.Status.Conditions {
		if condition.Type == nodeConditionReady {
			return condition.Status == nodeReadyVerdict, !condition.LastTransitionTime.IsZero()
		}
	}
	return false, false
}

// podEndedBy is the kubelet's words for why it ended a pod
// (endedpods.Words).
func podEndedBy(pod *Pod) string {
	if words := endedpods.Words(pod.Status.Reason, pod.Status.Message); words != "" {
		return words
	}
	return "the pod status gives no reason"
}
