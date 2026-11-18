package main

// The API forwards a tap to the operator's DaemonSet pod on the tap's
// node. An informer keeps an index of those pods in memory, so a request
// makes no call to the API server to find the pod, and a request for a
// node with no Ready capture container gets its 503 at once.
//
// The Role grants list and watch on every pod in the namespace, because
// RBAC cannot restrict a list to a label selector. The informer sends the
// selector, so only this DaemonSet's pods arrive.

import (
	"context"
	"os"
	"sync"
	"time"

	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/tools/cache"

	"github.com/liken-sh/liken/kubernetes/informer"
)

const operatorSelector = "app=audio-operator"

// A pod can serve a tap only when this container is ready.
const captureContainer = "capture"

// capturePod is the part of a pod that the API needs to forward a tap.
type capturePod struct {
	Name  string
	Node  string
	IP    string
	Ready bool

	// Created and Leaving choose between two pods on one node while the
	// DaemonSet replaces a pod. Leaving is true for a pod with a
	// deletionTimestamp or in the phase Succeeded or Failed.
	Created time.Time
	Leaving bool
}

// pod is the part of a Pod that the API reads.
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

// capture sets Ready only for a pod that has an address, is not
// Leaving, and has a ready capture container. A Leaving pod is about to
// stop and would cut a tap short.
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
		if container.Name == captureContainer && container.Ready && found.IP != "" && !found.Leaving {
			found.Ready = true
		}
	}
	return found
}

// podIndex holds every pod that the watch delivered, by name. A node can
// run two pods while the DaemonSet replaces one, so the index keeps both
// and on chooses between them.
type podIndex struct {
	mu     sync.RWMutex
	byName map[string]capturePod
}

func newPodIndex() *podIndex {
	return &podIndex{byName: map[string]capturePod{}}
}

// on returns the pod to use for a tap on node, and false when the index
// has no pod for the node.
func (index *podIndex) on(node string) (capturePod, bool) {
	index.mu.RLock()
	defer index.mu.RUnlock()
	var found capturePod
	matched := false
	for _, candidate := range index.byName {
		if candidate.Node != node {
			continue
		}
		if !matched || supersedes(candidate, found) {
			found, matched = candidate, true
		}
	}
	return found, matched
}

// put drops a pod that is not scheduled yet, because it has no node and
// can serve no tap.
func (index *podIndex) put(changed pod) {
	index.mu.Lock()
	defer index.mu.Unlock()
	if changed.Spec.NodeName == "" {
		delete(index.byName, changed.Metadata.Name)
		return
	}
	index.byName[changed.Metadata.Name] = changed.capture()
}

// forget removes a pod by name, so a late delete of a node's old pod
// leaves the new pod on that node in the index.
func (index *podIndex) forget(name string) {
	index.mu.Lock()
	defer index.mu.Unlock()
	delete(index.byName, name)
}

// supersedes reports whether incoming should replace current as the pod
// for a node. A pod that is not Leaving wins over one that is. Between
// two pods in the same state, the newer one wins.
func supersedes(incoming, current capturePod) bool {
	switch {
	case incoming.Leaving != current.Leaving:
		return current.Leaving
	case !incoming.Created.Equal(current.Created):
		return incoming.Created.After(current.Created)
	default:
		// For two pods created in the same second, the name orders them,
		// so the result does not depend on the map's order.
		return incoming.Name > current.Name
	}
}

func watchPods(ctx context.Context, client dynamic.Interface, namespace string, index *podIndex) {
	informer.Start(ctx, client, informer.Source{Resource: podResource, Namespace: namespace, LabelSelector: operatorSelector},
		informer.Options{Handler: podHandler{index}.handler()})
}

type podHandler struct{ index *podIndex }

func (h podHandler) handler() cache.ResourceEventHandler {
	return cache.ResourceEventHandlerFuncs{
		AddFunc:    h.put,
		UpdateFunc: func(_, after any) { h.put(after) },
		DeleteFunc: h.removed,
	}
}

// put logs a pod that does not convert, and the index keeps what it had
// for that name.
func (h podHandler) put(object any) {
	changed, err := informer.Convert[pod](object)
	if err != nil {
		informer.Report("the operator's pods", err)
		return
	}
	h.index.put(changed)
}

// removed deletes a pod from the index. When the watch missed the
// delete, the informer passes a cache.DeletedFinalStateUnknown whose Obj
// can be nil. Its Key, namespace/name, still gives the pod's name, and
// the index needs only the name.
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

// podNamespace returns the API's own namespace, which is also the
// namespace of the operator's pods. deploy/api.yaml sets POD_NAMESPACE
// through the downward API.
func podNamespace() string {
	if namespace := os.Getenv("POD_NAMESPACE"); namespace != "" {
		return namespace
	}
	return "liken-system"
}
