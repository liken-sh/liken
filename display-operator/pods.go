package main

// The pods this operator reads.
//
// A region's selector matches pods, the way a Service's does, and the
// pods it can match are the holders of a claim on the screen. A
// claim's status.reservedFor names them, and this file reads their
// labels. Every read is scoped to this node with a field selector,
// because a pod that draws on this node's screens runs on this node,
// and a node-wide listing keeps the operator's reads to the pods it
// can ever place.
//
// The operator reads a pod's name, namespace, and labels, and nothing
// else. It never reads a pod's spec or its containers: where a pod is
// drawn is the Layout's business, and what it draws is its own. The
// one exception is the operator's own pod, whose
// status.initContainerStatuses it reads for the restart count of the
// compositor's container.

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

// The pod collection, and the field selector that keeps a read to one
// node's pods.
const (
	PodsPath        = "/api/v1/pods"
	podsOnNodeField = "spec.nodeName="
	podsOnNode      = "?fieldSelector=" + podsOnNodeField
	podsResource    = "pods"
)

// Pod holds the part of a pod this program reads: the labels a
// region's selector matches, the name and namespace status.surfaces
// reports beside them, and the node, address, and readiness
// display-api reaches a sidecar by. Nothing here ever writes a pod.
type Pod struct {
	Metadata PodMeta   `json:"metadata"`
	Spec     PodSpec   `json:"spec,omitempty"`
	Status   PodStatus `json:"status,omitempty"`
}

type PodList struct {
	Items []Pod `json:"items"`
}

// The node is the only field of a pod's spec this program reads. It
// answers which node's sidecar a pod is.
type PodSpec struct {
	NodeName string `json:"nodeName,omitempty"`
}

type PodMeta struct {
	Name      string            `json:"name"`
	Namespace string            `json:"namespace"`
	Labels    map[string]string `json:"labels,omitempty"`
	// DeletionTimestamp is set once the API server has begun to
	// delete the pod. The pod may still run and still report Ready
	// through its grace period, and a newer pod may already stand in
	// its place.
	DeletionTimestamp *string `json:"deletionTimestamp,omitempty"`
}

// PodStatus is what the kubelet reports about one pod's containers. A
// native sidecar is an init container whose restartPolicy is Always,
// so the compositor's count is in initContainerStatuses.
type PodStatus struct {
	PodIP                 string            `json:"podIP,omitempty"`
	Conditions            []PodCondition    `json:"conditions,omitempty"`
	ContainerStatuses     []ContainerStatus `json:"containerStatuses,omitempty"`
	InitContainerStatuses []ContainerStatus `json:"initContainerStatuses,omitempty"`
}

// The kubelet's own Ready condition is what display-api reads,
// because it is the one answer that covers every container of the
// pod.
type PodCondition struct {
	Type   string            `json:"type"`
	Status conditions.Status `json:"status"`
}

// A pod is ready only while the kubelet says so; a pod with no
// conditions yet is not.
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

// restarts reports the restart count of one container, and zero for a
// name the kubelet reports nothing for.
func (s PodStatus) restarts(container string) int {
	for _, status := range slices.Concat(s.InitContainerStatuses, s.ContainerStatuses) {
		if status.Name == container {
			return status.RestartCount
		}
	}
	return 0
}

// The pod as status names it, namespace and name, which is how a
// person finds it with kubectl.
func (m PodMeta) key() string {
	return m.Namespace + "/" + m.Name
}

// getPod reads one pod by name. It is the second read of a holder the
// node listing does not name, which is a pod that arrived after the
// listing was taken and a pod that runs somewhere else.
func getPod(c *apiclient.Client, namespace, name string) (*Pod, error) {
	return apiclient.Get[Pod](c, "/api/v1/namespaces/"+namespace+"/pods/"+name)
}

// listPods reads the pods on one node. The field selector is what
// holds the read to this node: a pod that holds a claim on a screen
// this operator drives runs on the same node as the screen, so the
// pods of every other node answer nothing a pass reads.
func listPods(c *apiclient.Client, node string) ([]Pod, error) {
	list, err := apiclient.Get[PodList](c, PodsPath+podsOnNode+node)
	if err != nil {
		return nil, err
	}
	return list.Items, nil
}

// openPods starts the pod watch. It turns a pod that arrived, left, or
// changed its labels into one wake, because the labels a region's
// selector matches are the pods' own. It carries the same field
// selector the listing does. Its store holds every pod on this node,
// which the placement pass reads for the holders of a claim, and the
// compositor's restart count reads for this operator's own pod
// (objectcache.go).
//
// The placement pass reads a pod's name, namespace, and labels, so an
// update wakes it only when the labels or the UID differ. The kubelet
// writes a pod's status at each start, probe, and restart, and a pass
// for each of those writes would read a claim from the API server and
// probe the compositor again to find nothing to change. The restart
// count is read on the passes that publish the slice, which the
// compositor's socket wakes, so it needs no wake from this watch.
func openPods(ctx context.Context, client dynamic.Interface, node string, wake func(), readings *metrics) *informer.Collection {
	labels := cache.ResourceEventHandlerFuncs{
		AddFunc: func(any) { wake() },
		UpdateFunc: func(before, after any) {
			if labelsMoved(before, after) {
				wake()
			}
		},
		DeleteFunc: func(any) { wake() },
	}
	return wakeWatch(ctx, client, kindPod, informer.Source{Resource: podResource, FieldSelector: podsOnNodeField + node}, labels, wake, readings)
}

// labelsMoved reports whether an update changed a pod's labels or its
// UID. After a gap in the watch, a pod deleted and created again with
// the same name reaches the handler as an update, so the UID is part of
// the compare. Something that is not an object counts as a move,
// because nothing says what it changed.
func labelsMoved(before, after any) bool {
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
