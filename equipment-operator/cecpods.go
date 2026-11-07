package main

// Whether an adapter's entry under a CECBus's status.adapters still
// describes the adapter. A node workload writes its entry only when the
// entry changes, so the age of the entry says nothing about the pod
// that wrote it. A pod that dies without its Stopped entry, because its
// container was killed or its node lost power, leaves an entry that
// reads as current. So the Deployment and every node workload read the
// pods of the node workload instead: an entry that is not Stopped is
// current while a pod of the node workload on the entry's machine is
// Ready, or while that pod starts (stale). The kubelet marks the pod not Ready when its container
// exits, the readiness probe fails a loop that stops answering
// (cecnode_probe.go), and the node controller marks the pods of a node
// that stops reporting as not Ready. Each of those is a write to the
// pod, and the watch here delivers it, so no clock is needed to find a
// stale entry.

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/liken-sh/liken/kubernetes/apiclient"
	"github.com/liken-sh/liken/kubernetes/informer"
)

// podResource is the pods collection. The watch takes the pods of the
// node workload in the operator's own namespace, which deploy/cec.yaml
// labels with cecPodSelector.
var podResource = schema.GroupVersionResource{Version: "v1", Resource: "pods"}

// cecPodSelector selects the pods of the cec DaemonSet, by the label
// of its pod template in deploy/cec.yaml.
const cecPodSelector = "app=equipment-operator-cec"

// cecPodsPath is the pods collection of one namespace.
func cecPodsPath(namespace string) string {
	return "/api/v1/namespaces/" + namespace + "/pods"
}

// cecPod is the part of a pod that tells whether its node workload
// runs: the node it runs on and its Ready condition.
type cecPod struct {
	Metadata podMeta `json:"metadata"`
	Spec     struct {
		NodeName string `json:"nodeName,omitempty"`
	} `json:"spec"`
	Status struct {
		Conditions []podCondition `json:"conditions,omitempty"`
	} `json:"status"`
}

// podMeta is a pod's metadata. A pod is namespaced, so its key in the
// store holds the namespace, which ObjectMeta does not.
type podMeta struct {
	Name            string `json:"name"`
	Namespace       string `json:"namespace,omitempty"`
	UID             string `json:"uid,omitempty"`
	ResourceVersion string `json:"resourceVersion,omitempty"`
}

func (m *podMeta) GetName() string             { return m.Name }
func (m *podMeta) GetNamespace() string        { return m.Namespace }
func (m *podMeta) GetResourceVersion() string  { return m.ResourceVersion }
func (p *cecPod) GetObjectMeta() informer.Meta { return &p.Metadata }

type podCondition struct {
	Type               string `json:"type"`
	Status             string `json:"status"`
	LastTransitionTime string `json:"lastTransitionTime,omitempty"`
}

// cecStartupGrace is how long an entry stays current while the pod
// that wrote it has not become Ready yet. A node workload writes its
// first entry before the readiness probe first passes, so a new pod's
// entry would read as stale on every start without it. The grace is
// the readiness probe's window in deploy/cec.yaml, periodSeconds times
// failureThreshold, the same time the kubelet takes to mark a Ready pod
// whose loop stops answering as not Ready. A pod that never becomes
// Ready, such as one whose loop hangs at its start, makes its entry
// stale when the grace ends.
const cecStartupGrace = 60 * time.Second

// readiness answers whether the kubelet reports the pod Ready, and when
// the Ready condition last changed. The kubelet stamps the change with
// its node's clock, the clock the node workload stamps its entry with.
func (p cecPod) readiness() (bool, time.Time) {
	for _, condition := range p.Status.Conditions {
		if condition.Type == "Ready" {
			changed, _ := time.Parse(time.RFC3339, condition.LastTransitionTime)
			return condition.Status == "True", changed
		}
	}
	return false, time.Time{}
}

// podState is what the pods of the node workload on one machine show:
// whether one of them is Ready, and the latest time a pod there that
// is not Ready changed its Ready condition.
type podState struct {
	ready bool
	since time.Time
}

// podLiveness is what the pods of the node workload show for each
// machine, read at now. A machine with no such pod is not in machines.
// liken names each node after its Machine, so a pod's spec.nodeName is
// the machine a CECBus names. A nil machines says the pods could not
// be read, and judges no entry.
type podLiveness struct {
	machines map[string]podState
	now      time.Time
}

// livenessOf answers what the pods show for each machine at now. A
// rollout can run two pods on one machine for a moment, and one Ready
// pod is enough.
func livenessOf(pods []cecPod, now time.Time) podLiveness {
	live := podLiveness{machines: map[string]podState{}, now: now}
	for _, pod := range pods {
		if pod.Spec.NodeName == "" {
			continue
		}
		state := live.machines[pod.Spec.NodeName]
		ready, changed := pod.readiness()
		state.ready = state.ready || ready
		if !ready && changed.After(state.since) {
			state.since = changed
		}
		live.machines[pod.Spec.NodeName] = state
	}
	return live
}

// stale answers why an entry that is not Stopped may no longer describe
// its adapter, and false while it does. An entry is current while a pod
// of the node workload on its machine is Ready. It is also current
// while the pods there are not Ready only because they have not become
// Ready since the entry was written, until cecStartupGrace after the
// write. recheck is then the end of the grace, when the entry goes
// stale with no event, and zero otherwise.
//
// Both stamps have one-second precision. A pod that stops being Ready
// in the same second as the write counts as one that has not become
// Ready yet, so its entry goes stale when the grace ends.
func (l podLiveness) stale(entry CECAdapterStatus) (message string, stale bool, recheck time.Time) {
	if l.machines == nil {
		return "", false, time.Time{}
	}
	state, runs := l.machines[entry.Machine]
	switch {
	case !runs:
		return fmt.Sprintf("no equipment-operator-cec pod runs on machine %s, so its entry, last written at %s, may not describe the adapter",
			entry.Machine, entry.ReportedAt), true, time.Time{}
	case state.ready:
		return "", false, time.Time{}
	}
	written, err := time.Parse(time.RFC3339, entry.ReportedAt)
	if err != nil || state.since.After(written) {
		return fmt.Sprintf("the equipment-operator-cec pod on machine %s stopped being Ready at %s, after its entry was last written at %s, so the entry may not describe the adapter",
			entry.Machine, timestamp(state.since), entry.ReportedAt), true, time.Time{}
	}
	ends := written.Add(cecStartupGrace)
	if !l.now.Before(ends) {
		return fmt.Sprintf("the equipment-operator-cec pod on machine %s has not been Ready in the %s since its entry was written at %s, so the entry may not describe the adapter",
			entry.Machine, cecStartupGrace, entry.ReportedAt), true, time.Time{}
	}
	return "", false, ends
}

// readCECPods answers what the pods of the node workload show at now,
// from the watch's store, and from the API server while the store has
// nothing to give (objectcache.go). A refusal names the Role that
// grants the read.
func readCECPods(c *Client, held *watchStore, namespace string, now time.Time) (podLiveness, error) {
	listPath := cecPodsPath(namespace) + "?labelSelector=" + url.QueryEscape(cecPodSelector)
	pods, err := informer.List[cecPod](c.Client, informer.Held{View: held.view()}, listPath, func(key string) string {
		space, name, _ := strings.Cut(key, "/")
		return cecPodsPath(space) + "/" + name
	})
	if errors.Is(err, apiclient.ErrForbidden) {
		return podLiveness{}, fmt.Errorf("%w; the Role equipment-operator-cec-pods must grant get, list, and watch on pods in %s", err, namespace)
	}
	if err != nil {
		return podLiveness{}, err
	}
	return livenessOf(pods, now), nil
}

// podPlace is the part of a pod that podLiveness reads, and the pod's
// UID, so a pod deleted and created again with the same name during a
// gap in the watch still wakes the loop.
type podPlace struct {
	uid, node string
	ready     bool
}

func podMark(pod cecPod) podPlace {
	ready, _ := pod.readiness()
	return podPlace{uid: pod.Metadata.UID, node: pod.Spec.NodeName, ready: ready}
}

// cecPods answers the watch of the node workload's pods in one
// namespace, which wakes the loop when a pod arrives, leaves, moves to
// another node, or changes its Ready condition. The kubelet writes a
// pod's status for other reasons too, such as a container's restart
// count, and those wake nothing.
func cecPods(namespace string) func(context.Context, *Client, chan<- struct{}, func(), *watchStore) {
	return func(ctx context.Context, client *Client, wake chan<- struct{}, restarted func(), held *watchStore) {
		pods := markHandler[cecPod, podPlace]{what: "the equipment-operator-cec pods", wake: wake, mark: podMark}
		source := informer.Source{Resource: podResource, Namespace: namespace, LabelSelector: cecPodSelector}
		watchCollection(ctx, client, source, nil, pods.handler(), func() { poke(wake) }, restarted, held)
	}
}
