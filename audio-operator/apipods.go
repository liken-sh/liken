package main

// Which pod of the operator's DaemonSet runs on one node.
//
// This is an informer and not a read per request. The answer is the
// same for every tap on a node, a list and a watch cost one
// connection for the life of the process, and "no Ready capture
// container" then comes out of memory as an instant 503 rather than
// an API server round trip inside a request.
//
// The Role grants list and watch over the namespace's pods rather
// than the labelled ones, because RBAC cannot restrict a list to a
// label selector. The request carries the selector, so only this
// DaemonSet's pods ever arrive.

import (
	"context"
	"os"
	"sync"
	"time"

	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/tools/cache"

	"github.com/liken-sh/liken/kubernetes/informer"
)

// operatorSelector is the label the DaemonSet's pods carry.
const operatorSelector = "app=audio-operator"

// captureContainer is the container whose readiness decides whether a
// pod can answer a tap.
const captureContainer = "capture"

// capturePod is what the API needs about one pod: where to dial it and
// whether its capture container is running.
type capturePod struct {
	Name  string
	Node  string
	IP    string
	Ready bool

	// Created and Leaving decide which pod the index holds while a
	// DaemonSet replaces one: a pod that is being deleted or has
	// ended is Leaving.
	Created time.Time
	Leaving bool
}

// pod is the part of a Kubernetes Pod this API reads.
type pod struct {
	Metadata struct {
		Name              string     `json:"name"`
		CreationTimestamp time.Time  `json:"creationTimestamp"`
		DeletionTimestamp *time.Time `json:"deletionTimestamp,omitempty"`
	} `json:"metadata"`
	Spec struct {
		NodeName string `json:"nodeName"`
	} `json:"spec"`
	Status struct {
		Phase             string `json:"phase"`
		PodIP             string `json:"podIP"`
		ContainerStatuses []struct {
			Name  string `json:"name"`
			Ready bool   `json:"ready"`
		} `json:"containerStatuses"`
	} `json:"status"`
}

// capture reads one pod into the answer a request needs. A pod with no
// address, or whose capture container is not running, is held with
// Ready false rather than dropped, so the 503 can say which it was.
func (p pod) capture() capturePod {
	found := capturePod{
		Name:    p.Metadata.Name,
		Node:    p.Spec.NodeName,
		IP:      p.Status.PodIP,
		Created: p.Metadata.CreationTimestamp,
		Leaving: p.Metadata.DeletionTimestamp != nil ||
			p.Status.Phase == "Succeeded" || p.Status.Phase == "Failed",
	}
	for _, container := range p.Status.ContainerStatuses {
		// A pod that is leaving is about to stop, and a tap sent to it
		// would be cut short, so it answers no tap.
		if container.Name == captureContainer && container.Ready && found.IP != "" && !found.Leaving {
			found.Ready = true
		}
	}
	return found
}

// podIndex is the memory the pod watch fills: every pod the watch
// holds, by name. A node can hold two pods while the DaemonSet
// replaces one, so the index keeps both and chooses when it answers.
type podIndex struct {
	mu     sync.RWMutex
	byName map[string]capturePod
}

func newPodIndex() *podIndex {
	return &podIndex{byName: map[string]capturePod{}}
}

// on answers which pod runs on one node.
func (index *podIndex) on(node string) (capturePod, bool) {
	index.mu.RLock()
	defer index.mu.RUnlock()
	var found capturePod
	held := false
	for _, candidate := range index.byName {
		if candidate.Node != node {
			continue
		}
		if !held || supersedes(candidate, found) {
			found, held = candidate, true
		}
	}
	return found, held
}

// put takes a pod that the watch added or changed. A pod with no node
// yet has no tap to answer, so the index does not hold it.
func (index *podIndex) put(held pod) {
	index.mu.Lock()
	defer index.mu.Unlock()
	if held.Spec.NodeName == "" {
		delete(index.byName, held.Metadata.Name)
		return
	}
	index.byName[held.Metadata.Name] = held.capture()
}

// forget removes the pod with one name. A late delete of a node's old
// pod names that pod, so the new pod on the same node stays.
func (index *podIndex) forget(name string) {
	index.mu.Lock()
	defer index.mu.Unlock()
	delete(index.byName, name)
}

// supersedes answers whether one pod on a node takes the place of
// another. While a DaemonSet replaces a pod, the old pod and its
// replacement are both on the node. A pod that is not leaving takes
// the place of one that is, and of two pods in the same state, the
// newer one answers.
func supersedes(incoming, current capturePod) bool {
	switch {
	case incoming.Leaving != current.Leaving:
		return current.Leaving
	case !incoming.Created.Equal(current.Created):
		return incoming.Created.After(current.Created)
	default:
		// Two pods created in the same second: the name orders them,
		// so the answer does not change with the map's order.
		return incoming.Name > current.Name
	}
}

// watchPods follows the operator's pods into the index until the
// context ends.
func watchPods(ctx context.Context, client dynamic.Interface, namespace string, index *podIndex) {
	informer.Start(ctx, client, informer.Source{Resource: podResource, Namespace: namespace, LabelSelector: operatorSelector},
		informer.Options{Handler: podHandler{index}.handler()})
}

// podHandler reads the watch's objects as pods into the index.
type podHandler struct{ index *podIndex }

func (h podHandler) handler() cache.ResourceEventHandler {
	return cache.ResourceEventHandlerFuncs{
		AddFunc:    h.put,
		UpdateFunc: func(_, after any) { h.put(after) },
		DeleteFunc: h.removed,
	}
}

// put holds a pod the watch added or changed. A pod that does not
// convert is logged, and the index keeps what it held for that name.
func (h podHandler) put(object any) {
	held, err := informer.Convert[pod](object)
	if err != nil {
		informer.Report("the operator's pods", err)
		return
	}
	h.index.put(held)
}

// removed forgets a pod the watch removed. A tombstone that holds no
// copy still carries the pod's key, namespace/name, and the name is
// all the index needs.
func (h podHandler) removed(object any) {
	if tombstone, ok := object.(cache.DeletedFinalStateUnknown); ok && tombstone.Obj == nil {
		_, name, err := cache.SplitMetaNamespaceKey(tombstone.Key)
		if err != nil {
			informer.Report("the operator's pods", err)
			return
		}
		h.index.forget(name)
		return
	}
	item, err := unwrap(object)
	if err != nil {
		informer.Report("the operator's pods", err)
		return
	}
	h.index.forget(item.GetName())
}

// podNamespace is where the API looks for the operator's pods, which
// is its own namespace, from the downward API.
func podNamespace() string {
	if namespace := os.Getenv("POD_NAMESPACE"); namespace != "" {
		return namespace
	}
	return "liken-system"
}
