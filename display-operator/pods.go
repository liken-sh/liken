package main

// A region's selector matches pods by label, like a Service's selector,
// and it can match only the pods that a claim on the screen lists in
// status.reservedFor. The list and the watch read only this node's
// pods, because a pod that holds a claim on this node's screen runs on
// this node. getPod reads any other pod by name. The operator never
// writes a pod.

import (
	"context"
	"maps"
	"slices"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/tools/cache"

	"github.com/liken-sh/liken/kubernetes/apiclient"
	"github.com/liken-sh/liken/kubernetes/conditions"
	"github.com/liken-sh/liken/kubernetes/informer"
)

const (
	PodsPath        = "/api/v1/pods"
	podsOnNodeField = "spec.nodeName="
	podsOnNode      = "?fieldSelector=" + podsOnNodeField
	podsResource    = "pods"
)

// Pod is the part of a pod that this program reads. The node, IP
// address, and readiness locate a pod's display-api sidecar, and the
// restart counts count the compositor's restarts.
type Pod struct {
	Metadata PodMeta   `json:"metadata"`
	Spec     PodSpec   `json:"spec,omitempty"`
	Status   PodStatus `json:"status,omitempty"`
}

type PodList struct {
	Items []Pod `json:"items"`
}

type PodSpec struct {
	NodeName string `json:"nodeName,omitempty"`
}

type PodMeta struct {
	Name      string            `json:"name"`
	Namespace string            `json:"namespace"`
	Labels    map[string]string `json:"labels,omitempty"`
	// DeletionTimestamp is set once the API server starts deleting the
	// pod. During the grace period the pod can still run and report
	// Ready, and a replacement pod may already exist.
	DeletionTimestamp *string `json:"deletionTimestamp,omitempty"`
}

// A native sidecar is an init container with restartPolicy Always, so
// the compositor's restart count is in initContainerStatuses.
type PodStatus struct {
	PodIP                 string            `json:"podIP,omitempty"`
	Conditions            []PodCondition    `json:"conditions,omitempty"`
	ContainerStatuses     []ContainerStatus `json:"containerStatuses,omitempty"`
	InitContainerStatuses []ContainerStatus `json:"initContainerStatuses,omitempty"`
}

type PodCondition struct {
	Type   string            `json:"type"`
	Status conditions.Status `json:"status"`
}

func (s PodStatus) ready() bool {
	for _, condition := range s.Conditions {
		if condition.Type == "Ready" {
			return condition.Status == conditionTrue
		}
	}
	return false
}

type ContainerStatus struct {
	Name         string `json:"name"`
	RestartCount int    `json:"restartCount"`
}

func (s PodStatus) restarts(container string) int {
	for _, status := range slices.Concat(s.InitContainerStatuses, s.ContainerStatuses) {
		if status.Name == container {
			return status.RestartCount
		}
	}
	return 0
}

// key returns "namespace/name", the form that status uses for a pod.
func (m PodMeta) key() string {
	return m.Namespace + "/" + m.Name
}

// getPod reads a pod that the node's store does not have yet, or that
// runs on another node.
func getPod(c *apiclient.Client, namespace, name string) (*Pod, error) {
	return apiclient.Get[Pod](c, "/api/v1/namespaces/"+namespace+"/pods/"+name)
}

func listPods(c *apiclient.Client, node string) ([]Pod, error) {
	list, err := apiclient.Get[PodList](c, PodsPath+podsOnNode+node)
	if err != nil {
		return nil, err
	}
	return list.Items, nil
}

// openPods returns a store of this node's pods. The placement pass reads
// it for the holders of a claim, and the compositor restart count reads
// it for this operator's own pod.
//
// An update calls wake only when the labels or the UID changed, because
// the placement pass reads only the name, namespace, and labels. The
// kubelet updates a pod's status on every start, probe, and restart,
// and a pass for each update would read a claim from the API server and
// probe the compositor for no change. The restart count needs no wake
// from this watch, because a compositor restart creates its socket again,
// and the socket wakes the pass that reads the count.
func openPods(ctx context.Context, client dynamic.Interface, node string, wake func(), readings *metrics) *informer.Collection {
	labels := cache.ResourceEventHandlerFuncs{
		AddFunc: func(any) { wake() },
		UpdateFunc: func(before, after any) {
			if labelsOrUIDChanged(before, after) {
				wake()
			}
		},
		DeleteFunc: func(any) { wake() },
	}
	return wakeWatch(ctx, client, kindPod, informer.Source{Resource: podResource, FieldSelector: podsOnNodeField + node}, labels, wake, readings)
}

// labelsOrUIDChanged compares the UID because, after a gap in the
// watch, a pod that was deleted and created again with the same name
// reaches the handler as an update. An argument of another type counts
// as a change, because nothing shows what differs.
func labelsOrUIDChanged(before, after any) bool {
	was, ok := before.(*unstructured.Unstructured)
	if !ok {
		return true
	}
	is, ok := after.(*unstructured.Unstructured)
	if !ok {
		return true
	}
	return was.GetUID() != is.GetUID() || !maps.Equal(was.GetLabels(), is.GetLabels())
}
