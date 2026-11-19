package main

// A long-running pod that the kubelet ended. Every pod that standPod
// stands has restartPolicy Always, so its own containers never end it.
// The kubelet ends it for an eviction, a rejection at admission, or a
// node shutdown, and then the pod stays Failed or Succeeded, and no
// kubelet starts it again. The pass replaces it.
//
// A claim can pin the replacement to the node that ended the pod: a
// display claim pins a screen pod, and a volume on a node-local class
// pins a store copy. The scheduler then puts the replacement on the same
// node, and no taint keeps it away after a rejection at admission, so
// the kubelet can reject it again at once. So the replacement waits: 10
// seconds after the ended pod's creation for its first try, doubling
// with each try, to a limit of 5 minutes. A pod that ran longer than its
// wait is replaced at once, so only a pod that ends young waits.
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
	"strconv"
	"strings"
	"time"

	"github.com/liken-sh/liken/kubernetes/apiclient"
)

// triesAnnotation records how many earlier pods of the same name the
// kubelet ended. The count is in the pod, not in memory, so a restarted
// operator waits as long as the one before it. The template hash reads
// the spec alone, so the count does not make a pod stale. It is never
// reset, because the wait counts from the pod's creation.
const triesAnnotation = "library.liken.sh/tries"

// The wait before the first try, and the limit the doubling stops at.
const (
	firstReplaceWait = 10 * time.Second
	maxReplaceWait   = 5 * time.Minute
)

// endedByKubelet reports a pod that will not run again. With
// restartPolicy Always, both terminal phases mean that the kubelet ended
// the pod.
func endedByKubelet(pod *Pod) bool {
	return pod.Status.Phase == podFailed || pod.Status.Phase == podSucceeded
}

// tries is the pod's count of earlier pods that the kubelet ended. A pod
// with no count reads as zero.
func tries(pod *Pod) int {
	count, _ := strconv.Atoi(pod.Metadata.Annotations[triesAnnotation])
	return max(count, 0)
}

func replaceWait(tries int) time.Duration {
	wait := firstReplaceWait
	for range tries {
		if wait >= maxReplaceWait {
			break
		}
		wait *= 2
	}
	return min(wait, maxReplaceWait)
}

// replaceAt is the earliest time at which the pass replaces an ended pod.
func replaceAt(pod *Pod) time.Time {
	return pod.Metadata.CreationTimestamp.Add(replaceWait(tries(pod)))
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
	if now.Before(replaceAt(live)) || o.leftToTheHeal(live, now) {
		return live, nil
	}
	namespace, name := live.Metadata.Namespace, live.Metadata.Name
	if err := DeletePod(ctx, o.client, namespace, name); err != nil {
		return nil, err
	}
	o.forgetPod(namespace, name)

	next := *desired
	next.Metadata.Annotations = maps.Clone(desired.Metadata.Annotations)
	next.Metadata.Annotations[triesAnnotation] = strconv.Itoa(tries(live) + 1)
	created, err := CreatePod(ctx, o.client, &next)
	if errors.Is(err, apiclient.ErrConflict) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	// A retry loop posts its first failure and not each retry. A pod that
	// ran longer than the longest wait before the kubelet ended it ended
	// for a cause of its own. The pod's age at the replace is no measure:
	// once the wait is at its limit, every pod is older than the limit
	// when its turn comes.
	if tries(live) == 0 || ranFor(live) > maxReplaceWait {
		postPodRecreated(o.recorder, live)
	}
	return created, nil
}

// endedAt is when the kubelet ended a pod: the time of the
// DisruptionTarget condition that an eviction or a node shutdown writes,
// or else the time the last of its containers stopped. A pod that the
// kubelet rejected at admission has neither, because it never ran.
func endedAt(pod *Pod) (time.Time, bool) {
	for _, condition := range pod.Status.Conditions {
		if condition.Type == "DisruptionTarget" && condition.Status == "True" &&
			!condition.LastTransitionTime.IsZero() {
			return condition.LastTransitionTime, true
		}
	}
	var last time.Time
	for _, status := range slices.Concat(pod.Status.InitContainerStatuses, pod.Status.ContainerStatuses) {
		if status.State.Terminated != nil && status.State.Terminated.FinishedAt.After(last) {
			last = status.State.Terminated.FinishedAt
		}
	}
	return last, !last.IsZero()
}

// ranFor is how long a pod ran before the kubelet ended it, and zero for
// a pod that never ran.
func ranFor(pod *Pod) time.Duration {
	end, held := endedAt(pod)
	if !held {
		return 0
	}
	return end.Sub(pod.Metadata.CreationTimestamp)
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
		end, held := endedAt(pod)
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

// podEndedBy is the kubelet's words for why it ended a pod: the reason,
// such as Evicted or Terminated, and the message beside it. The kubelet
// ends its message with a period, which the Event's sentence drops.
func podEndedBy(pod *Pod) string {
	message := strings.TrimSuffix(pod.Status.Message, ".")
	switch {
	case pod.Status.Reason != "" && message != "":
		return pod.Status.Reason + ": " + message
	case pod.Status.Reason != "":
		return pod.Status.Reason
	case message != "":
		return message
	}
	return "the pod status gives no reason"
}
