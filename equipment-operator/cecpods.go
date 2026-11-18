package main

// A node workload writes its entry under a CECBus's status.adapters only
// when the entry changes, so the entry's age does not show whether the
// pod that wrote it still runs. A pod that is killed before it writes a
// Stopped entry, for example when its node loses power, leaves an entry
// that looks current. So the Deployment and the node workloads judge an
// entry by the node workload's pods on the entry's machine. An entry
// that is not Stopped is current while one of those pods is Ready, or
// for cecStartupGrace while the pod starts.
//
// A pod's Ready condition goes False when its container exits, when the
// readiness probe fails (cecnode_probe.go), and when the node controller
// marks the pods of a node that stopped reporting. Each of these is a
// write to the pod that the watch receives, so finding a stale entry
// needs no polling.

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

var podResource = schema.GroupVersionResource{Version: "v1", Resource: "pods"}

// cecPodSelector matches the label on the pod template of the cec
// DaemonSet in deploy/cec.yaml.
const cecPodSelector = "app=equipment-operator-cec"

func cecPodsPath(namespace string) string {
	return "/api/v1/namespaces/" + namespace + "/pods"
}

// cecPod holds the fields of a pod that podLiveness reads.
type cecPod struct {
	Metadata podMeta `json:"metadata"`
	Spec     struct {
		NodeName string `json:"nodeName,omitempty"`
	} `json:"spec"`
	Status struct {
		Conditions []podCondition `json:"conditions,omitempty"`
	} `json:"status"`
}

// podMeta is a pod's metadata. A pod is namespaced, and ObjectMeta has
// no namespace field.
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

// cecStartupGrace is how long an entry stays current while the pod that
// wrote it has not become Ready. A node workload writes its first entry
// before its readiness probe first passes, so without the grace every
// new pod's entry would be stale. The value is the readiness probe's
// periodSeconds times its failureThreshold in deploy/cec.yaml. A pod
// whose loop hangs at start never becomes Ready, and its entry is stale
// when the grace ends.
const cecStartupGrace = 60 * time.Second

// readiness returns whether the pod's Ready condition is True, and its
// lastTransitionTime. The kubelet writes that time from its node's
// clock, and the node workload writes the entry's reportedAt from the
// same clock, so stale can compare the two.
func (p cecPod) readiness() (bool, time.Time) {
	for _, condition := range p.Status.Conditions {
		if condition.Type == "Ready" {
			changed, _ := time.Parse(time.RFC3339, condition.LastTransitionTime)
			return condition.Status == "True", changed
		}
	}
	return false, time.Time{}
}

// podState describes the node workload's pods on one machine: whether
// any is Ready, and the latest lastTransitionTime of a pod that is not.
type podState struct {
	ready bool
	since time.Time
}

// podLiveness maps a machine name to its podState, read at now. A
// machine with no pod of the node workload has no key. A node has the
// name of its Machine, so a pod's spec.nodeName matches the machine of
// a CECBus entry. A nil machines means that the pods could not be read,
// and then stale reports no entry as stale.
type podLiveness struct {
	machines map[string]podState
	now      time.Time
}

// livenessOf builds the podLiveness of pods. During a rollout an old
// and a new pod can run on one machine, and the machine is ready when
// either of them is Ready.
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

// stale reports whether an entry that is not Stopped may no longer
// describe its adapter, with a message that says why. An entry is
// current while a pod on its machine is Ready, and stale when no pod
// runs there. When the pods there are not Ready, the entry stays current
// for cecStartupGrace after its reportedAt, unless a pod stopped being
// Ready after that time. While it is current that way, recheck is the
// end of the grace,
// because no event marks the moment the entry becomes stale. Otherwise
// recheck is zero.
//
// Both times have one-second precision. A pod that stops being Ready in
// the same second as the write counts as one that has not become Ready
// yet, so its entry becomes stale when the grace ends.
func (l podLiveness) stale(entry CECAdapterStatus) (message string, stale bool, recheck time.Time) {
	if l.machines == nil {
		return "", false, time.Time{}
	}
	state, runs := l.machines[entry.Machine]
	switch {
	case !runs:
		return fmt.Sprintf("No equipment-operator-cec pod runs on machine %s, so its entry, last written at %s, may not describe the adapter",
			entry.Machine, entry.ReportedAt), true, time.Time{}
	case state.ready:
		return "", false, time.Time{}
	}
	written, err := time.Parse(time.RFC3339, entry.ReportedAt)
	if err != nil || state.since.After(written) {
		return fmt.Sprintf("The equipment-operator-cec pod on machine %s stopped being Ready at %s, after its entry was last written at %s, so the entry may not describe the adapter",
			entry.Machine, timestamp(state.since), entry.ReportedAt), true, time.Time{}
	}
	ends := written.Add(cecStartupGrace)
	if !l.now.Before(ends) {
		return fmt.Sprintf("The equipment-operator-cec pod on machine %s has not been Ready in the %s since its entry was written at %s, so the entry may not describe the adapter",
			entry.Machine, cecStartupGrace, entry.ReportedAt), true, time.Time{}
	}
	return "", false, ends
}

// readCECPods returns the podLiveness at now, from the watch's store,
// or from the API server while the store is not ready (objectcache.go).
// A Forbidden error names the Role that grants the read.
func readCECPods(c *Client, store *watchStore, namespace string, now time.Time) (podLiveness, error) {
	listPath := cecPodsPath(namespace) + "?labelSelector=" + url.QueryEscape(cecPodSelector)
	pods, err := informer.List[cecPod](c.Client, informer.Held{View: store.view()}, listPath, func(key string) string {
		space, name, _ := strings.Cut(key, "/")
		return cecPodsPath(space) + "/" + name
	})
	if errors.Is(err, apiclient.ErrForbidden) {
		return podLiveness{}, fmt.Errorf("%w: the Role equipment-operator-cec-pods must grant get, list, and watch on pods in %s", err, namespace)
	}
	if err != nil {
		return podLiveness{}, err
	}
	return livenessOf(pods, now), nil
}

// podSummary is the part of a pod that podLiveness reads, plus its UID.
// After a gap in the watch, a pod that was deleted and created again
// with the same name arrives as an update, and the UID makes it a
// change.
type podSummary struct {
	uid, node string
	ready     bool
}

func summarizePod(pod cecPod) podSummary {
	ready, _ := pod.readiness()
	return podSummary{uid: pod.Metadata.UID, node: pod.Spec.NodeName, ready: ready}
}

// cecPods returns the watch of the node workload's pods in one
// namespace. It wakes the loop when a pod is created, deleted, or
// scheduled on another node, or when its Ready condition changes. Other
// status writes, such as a new container restart count, do not.
func cecPods(namespace string) func(context.Context, *Client, chan<- struct{}, func(), *watchStore) {
	return func(ctx context.Context, client *Client, wake chan<- struct{}, restarted func(), store *watchStore) {
		pods := markHandler[cecPod, podSummary]{what: "the equipment-operator-cec pods", wake: wake, mark: summarizePod}
		source := informer.Source{Resource: podResource, Namespace: namespace, LabelSelector: cecPodSelector}
		watchCollection(ctx, client, source, nil, pods.handler(), func() { sendWake(wake) }, restarted, store)
	}
}
