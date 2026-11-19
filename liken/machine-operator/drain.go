package main

// The drain moves pods off this machine's node before a reboot, the
// way `kubectl drain` does. It cordons the node so nothing new is
// scheduled there, then evicts each pod through the Eviction API. The
// API server refuses an eviction with a 429 when it would break the
// workload's PodDisruptionBudget, which a plain delete would ignore.
// The drain starts when cluster-operator grants this machine its
// reboot turn (cluster-operator/rollout.go).
//
// Each reconcile pass cordons the node if needed, evicts what it can,
// and returns. A pass that blocked until the node was empty would stop
// the heartbeat, and cluster-operator marks a machine without a
// heartbeat Lost. The drain's state is in annotations on the Node, so
// a restarted operator continues the same drain.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/liken-sh/liken/kubernetes/apiclient"
	"github.com/liken-sh/liken/liken/api"
	"github.com/liken-sh/liken/liken/kubernetes"
)

const (
	// cordonedAnnotation marks a cordon that the operator applied. A
	// node that was already unschedulable when the drain started was
	// cordoned by a person, so only a node with this annotation is
	// uncordoned.
	cordonedAnnotation = "liken.sh/cordoned"

	// drainingSinceAnnotation records when the drain started. The
	// deadline counts from it, so an operator restart does not reset
	// the deadline.
	drainingSinceAnnotation = "liken.sh/draining-since"

	// mirrorPodAnnotation marks the API object of a static pod. The
	// kubelet runs that pod from a manifest on disk, so evicting it does
	// nothing.
	mirrorPodAnnotation = "kubernetes.io/config.mirror"

	// unschedulableTaint is the taint that Kubernetes adds to a
	// cordoned Node.
	unschedulableTaint = "node.kubernetes.io/unschedulable"

	// drainDeadline is the longest that pods can delay the reboot. A
	// PodDisruptionBudget can refuse an eviction forever, or a pod can
	// have no other node to go to. The machine reboots anyway at the
	// deadline, because a machine that can never apply its staged change
	// is worse than a pod that restarts.
	drainDeadline = 5 * time.Minute
)

// evictablePods returns the pods that the drain has to move. It leaves
// out:
//   - pods that have completed.
//   - mirror pods, which the kubelet recreates from disk.
//   - DaemonSet pods, because the DaemonSet controller ignores the
//     cordon and would recreate them. This operator is one of them.
//   - pods that tolerate the cordon's taint by its key. The scheduler
//     can put such a pod, or its replacement, back on the cordoned
//     node, so evicting it only restarts it. Pods tolerate the cordon
//     on purpose so that they keep running on a drained node: the pod
//     that works on the machine while a Disruption holds the reboot
//     turn (hold.go), and the operator that releases that hold. A
//     toleration with an empty key and the Exists operator matches
//     every taint, but it is not aimed at the cordon, so that pod is
//     still evicted.
//
// A completed pod needs nothing more from its DRA driver, because the
// kubelet unprepares a pod's claims before the pod reports a terminal
// phase.
func evictablePods(pods []kubernetes.Pod) []kubernetes.Pod {
	var evictable []kubernetes.Pod
	for _, p := range pods {
		if p.Completed() {
			continue
		}
		if _, ok := p.Metadata.Annotations[mirrorPodAnnotation]; ok {
			continue
		}
		if p.IsDaemon() || toleratesCordon(p) {
			continue
		}
		evictable = append(evictable, p)
	}
	return evictable
}

// toleratesCordon reports whether the pod has a toleration with the
// cordon's taint key that matches the NoSchedule effect.
func toleratesCordon(p kubernetes.Pod) bool {
	for _, t := range p.Spec.Tolerations {
		if t.Key != unschedulableTaint || (t.Effect != "" && t.Effect != "NoSchedule") {
			continue
		}
		if t.Operator == "Exists" || (t.Operator == "Equal" || t.Operator == "") && t.Value == "" {
			return true
		}
	}
	return false
}

// draPluginPaths are the kubelet directories that a DRA driver's pod
// mounts from the host. The kubelet finds a driver by its registration
// socket in plugins_registry, and calls prepare and unprepare on a
// socket in the driver's directory under plugins.
var draPluginPaths = []string{
	"/var/lib/kubelet/plugins_registry",
	"/var/lib/kubelet/plugins",
}

// servesDRAPlugin reports whether the pod mounts a DRA driver's kubelet
// directories. Nothing in the API links a driver in a ResourceSlice to
// the pod that serves it, so the hostPath mounts are the only signal.
// The check also matches a CSI driver, which registers in the same
// directory. That only moves the pod to the second phase of
// drainOrder.
//
// This operator is the DRA driver for liken.sh and matches the check.
// evictablePods leaves it out as a DaemonSet pod, so the kubelet can
// call it until the machine reboots.
func servesDRAPlugin(p kubernetes.Pod) bool {
	for _, v := range p.Spec.Volumes {
		if v.HostPath == nil {
			continue
		}
		for _, path := range draPluginPaths {
			if v.HostPath.Path == path || strings.HasPrefix(v.HostPath.Path, path+"/") {
				return true
			}
		}
	}
	return false
}

// holdsResourceClaim reports whether the pod uses DRA devices. The
// kubelet unprepares every entry of spec.resourceClaims, whether it
// names a ResourceClaim or a template.
func holdsResourceClaim(p kubernetes.Pod) bool {
	return len(p.Spec.ResourceClaims) > 0
}

// drainOrder returns the pods to evict on this pass. Pods that use DRA
// claims must leave before the DRA drivers. While a pod terminates, the
// kubelet calls NodeUnprepareResources on its driver. When the driver
// is already gone, the call fails and the kubelet retries it without
// limit, so the pod never finishes terminating.
//
// So drainOrder evicts every pod that is not a driver first. It adds
// the drivers only on a pass where no pod with a claim is in the
// listing. A terminating pod stays in the listing until the kubelet
// finishes with it, so a terminating pod with a claim still holds back
// the drivers. A terminating pod is not evicted again.
//
// A driver pod that has a claim of its own goes with the drivers. A
// device operator claims its hardware from liken.sh, whose driver is
// this operator, and the drain never evicts this operator. If that
// claim held back the drivers, the device operator would wait for
// itself.
func drainOrder(evictable []kubernetes.Pod) []kubernetes.Pod {
	var first, drivers []kubernetes.Pod
	holders := 0
	for _, p := range evictable {
		if servesDRAPlugin(p) {
			if !p.Terminating() {
				drivers = append(drivers, p)
			}
			continue
		}
		if holdsResourceClaim(p) {
			holders++
		}
		if !p.Terminating() {
			first = append(first, p)
		}
	}
	if holders > 0 {
		return first
	}
	return append(first, drivers...)
}

// A drainStep is what one pass of the drain does.
type drainStep struct {
	// patch cordons the node and sets draining-since. It is nil when
	// both are already set.
	patch []byte
	// evict holds the pods to evict on this pass. It is shorter than
	// remaining while the drivers wait for the pods with claims.
	evict     []kubernetes.Pod
	remaining int
	// clear is true when the node can reboot.
	clear bool
	// deadline is draining-since plus drainDeadline, at the whole
	// seconds of the annotation.
	deadline time.Time
}

// decideDrainStep returns what one pass of the drain does. When the
// node is schedulable or has no valid draining-since annotation, the
// step sets draining-since to now, and it cordons the node when it is
// schedulable. At or after the deadline the step clears the node with
// pods still on it, as Cluster API does when its drain timeout passes.
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
		since, _ = time.Parse(time.RFC3339, now.Format(time.RFC3339))
	}

	step.deadline = since.Add(drainDeadline)
	if !now.Before(step.deadline) {
		step.clear = true
		return step
	}
	evictable := evictablePods(pods)
	step.remaining = len(evictable)
	step.evict = drainOrder(evictable)
	step.clear = step.remaining == 0
	return step
}

// gateThroughDrain withholds the reboot of conv until the node is
// clear or the deadline passes, and reports the drain on conv's
// condition. The cordon posts an Event on the Machine, because the
// Node does not record who cordoned it or why.
//
// The next pass starts when a pod leaves the node, when a
// PodDisruptionBudget changes (watchBudgets in waits.go), or at the
// deadline.
func gateThroughDrain(r *reader, node *nodeObject, conv convergence, now time.Time, notes machineEvents, out *passOutcome) convergence {
	r.watchBudgets()
	pods, err := r.nodePods(node.Metadata.Name)
	if err != nil {
		fmt.Printf("listing the pods of Node %s for the drain: %v\n", node.Metadata.Name, err)
		return holdForDrain(conv, "The reboot waits because the pods of the Node cannot be listed. The operator retries")
	}
	step := decideDrainStep(node, pods, now)
	if step.patch != nil {
		if err := kubernetes.PatchJSON(r.client, nodesPath+"/"+node.Metadata.Name, step.patch); err != nil {
			fmt.Printf("cordoning Node %s: %v\n", node.Metadata.Name, err)
			return holdForDrain(conv, "The reboot waits because the Node is not cordoned. The operator retries")
		}
		fmt.Printf("cordoned Node %s ahead of its reboot\n", node.Metadata.Name)
		notes.normal(reasonCordoned, fmt.Sprintf("Cordoned the Node %s before the reboot. Pods to evict: %d", node.Metadata.Name, step.remaining))
	}
	evict(r, step.evict, now, out)
	if step.clear {
		return conv
	}
	out.wakeBy(step.deadline)
	return holdForDrain(conv, fmt.Sprintf("The Node is draining before the reboot. Pods left to evict: %d", step.remaining))
}

// evict evicts each pod and handles the responses as `kubectl drain`
// does (k8s.io/kubectl/pkg/drain). A 404 means the pod is already
// gone. A 429 means a PodDisruptionBudget refused the eviction, and it
// is not a failure: the pass wakes at the Retry-After time when the
// response has one, and otherwise waits for the budget to change. The
// observer records every other error as a failure of the pass, so the
// loop retries.
func evict(r *reader, pods []kubernetes.Pod, now time.Time, out *passOutcome) {
	if len(pods) == 0 {
		return
	}
	client := r.client.WithObserver(func(result apiclient.Outcome) {
		if result.Status == http.StatusNotFound || result.Status == http.StatusTooManyRequests {
			return
		}
		out.observe(result)
	})
	for _, p := range pods {
		err := kubernetes.EvictPod(client, p)
		if err == nil {
			continue
		}
		fmt.Printf("evicting Pod %s/%s: %v\n", p.Metadata.Namespace, p.Metadata.Name, err)
		if seconds := apiclient.RetryAfterSeconds(err); seconds > 0 {
			out.wakeBy(now.Add(time.Duration(seconds) * time.Second))
		}
	}
}

// holdForDrain withholds the reboot and sets conv's condition to False
// with the reason Draining.
func holdForDrain(conv convergence, message string) convergence {
	conv.requestReboot = false
	conv.condition = api.Condition{
		Type: conv.condition.Type, Status: api.ConditionFalse, Reason: "Draining", Message: message,
	}
	return conv
}

// decideUncordon reports whether the node has a cordon that this
// operator applied.
func decideUncordon(node *nodeObject) bool {
	return node.Spec.Unschedulable && node.Metadata.Annotations[cordonedAnnotation] == "true"
}

// uncordonMessage returns the Event message for the uncordon. A boot
// before draining-since means the machine did not reboot, because the
// reboot request ended during the drain: for example, a person deleted
// the Disruption or withdrew the staged document.
func uncordonMessage(node *nodeObject, boot *time.Time) string {
	since, err := time.Parse(time.RFC3339, node.Metadata.Annotations[drainingSinceAnnotation])
	if err == nil && boot != nil && boot.Before(since) {
		return "Uncordoned the Node " + node.Metadata.Name + " without a reboot, because no request waits for one"
	}
	return "Uncordoned the Node " + node.Metadata.Name + " after its reboot"
}

// uncordonPatch makes the node schedulable and deletes the drain's
// annotations. In a JSON merge patch, null deletes a key.
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
