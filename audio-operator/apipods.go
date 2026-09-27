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
	"encoding/json"
	"os"
	"sync"
	"time"
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

// podIndex is the memory the informer fills: one pod per node.
type podIndex struct {
	mu     sync.RWMutex
	byNode map[string]capturePod
}

func newPodIndex() *podIndex {
	return &podIndex{byNode: map[string]capturePod{}}
}

// on answers which pod runs on one node.
func (index *podIndex) on(node string) (capturePod, bool) {
	index.mu.RLock()
	defer index.mu.RUnlock()
	found, held := index.byNode[node]
	return found, held
}

// replace takes a whole list, which is what the informer's first read
// and every list after an expired version deliver.
func (index *podIndex) replace(pods []pod) {
	next := map[string]capturePod{}
	for _, held := range pods {
		if held.Spec.NodeName == "" {
			continue
		}
		incoming := held.capture()
		if current, found := next[incoming.Node]; !found || supersedes(incoming, current) {
			next[incoming.Node] = incoming
		}
	}
	index.mu.Lock()
	index.byNode = next
	index.mu.Unlock()
}

// apply takes one watch event.
func (index *podIndex) apply(kind string, held pod) {
	if held.Spec.NodeName == "" {
		return
	}
	index.mu.Lock()
	defer index.mu.Unlock()
	if kind == "DELETED" {
		// A DaemonSet can start a node's new pod before the old pod's
		// final DELETED event arrives. That event names the old pod,
		// and removing the node on it would drop the new pod until
		// the new pod next changes, which may be never.
		if current, found := index.byNode[held.Spec.NodeName]; found && current.Name == held.Metadata.Name {
			delete(index.byNode, held.Spec.NodeName)
		}
		return
	}
	incoming := held.capture()
	if current, found := index.byNode[incoming.Node]; !found || supersedes(incoming, current) {
		index.byNode[incoming.Node] = incoming
	}
}

// supersedes answers whether incoming takes the place of the pod the
// index holds for the same node. An update to the held pod always
// does. While a DaemonSet replaces a pod, the old pod and its
// replacement are both on the node, and the old pod's late updates can
// arrive after the replacement's last one. So another pod takes the
// place only when it is not leaving and the held pod is leaving or
// older.
func supersedes(incoming, current capturePod) bool {
	switch {
	case incoming.Name == current.Name:
		return true
	case incoming.Leaving:
		return false
	case current.Leaving:
		return true
	default:
		return incoming.Created.After(current.Created)
	}
}

// watchPods lists the operator's pods once and follows the changes
// for the life of the process. The watch is the one in apiwatch.go: a
// watch the API server ends opens again from the last event's version,
// and only a version the server no longer keeps lists the pods again.
func watchPods(ctx context.Context, client *Client, namespace string,
	index *podIndex, complain func(error)) {
	go podWatch(client, namespace, index, complain).run(ctx)
}

// podWatch builds the watch with the production waits.
func podWatch(client *Client, namespace string, index *podIndex, complain func(error)) *objectWatch {
	return &objectWatch{
		client:     client,
		kind:       "Pod",
		collection: "/api/v1/namespaces/" + namespace + "/pods",
		selector:   operatorSelector,
		labels:     true,
		keep:       podKeeper{index},
		complain:   complain,
		retry:      objectWatchRetry,
		retryLimit: objectWatchRetryLimit,
		shortLife:  objectWatchShortLife,
	}
}

// podKeeper reads the watch's objects as pods into the index.
type podKeeper struct{ index *podIndex }

func (k podKeeper) replace(items json.RawMessage) error {
	var pods []pod
	// A list with no pods can leave the items out.
	if len(items) > 0 {
		if err := json.Unmarshal(items, &pods); err != nil {
			return err
		}
	}
	k.index.replace(pods)
	return nil
}

func (k podKeeper) apply(kind string, object json.RawMessage) error {
	var held pod
	if err := json.Unmarshal(object, &held); err != nil {
		return err
	}
	k.index.apply(kind, held)
	return nil
}

// podNamespace is where the API looks for the operator's pods, which
// is its own namespace, from the downward API.
func podNamespace() string {
	if namespace := os.Getenv("POD_NAMESPACE"); namespace != "" {
		return namespace
	}
	return "liken-system"
}
