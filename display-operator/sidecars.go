package main

// This file holds display-api's memory of the capture sidecars: one
// pod per node, found by label. The API keeps one list-and-watch on
// those pods instead of asking the API server on every request, so
// a capture costs one call to the node and nothing else, and the API
// answers "no ready sidecar on that node" from memory. A sidecar is
// ready when the kubelet's own Ready condition on its pod is True,
// which covers every container of the pod, the compositor included.

import (
	"context"
	"fmt"
	"sync"
)

// The pods the API watches: the display-operator DaemonSet's pods in
// liken-system, one per node, and the node each one runs on is the
// key a Display's status.node is looked up by.
const (
	sidecarNamespace = "liken-system"
	sidecarSelector  = "app=display-operator"
)

// What the API holds about one node's sidecar: where to reach it and
// whether the kubelet calls its pod ready.
type sidecarPod struct {
	Namespace string
	Name      string
	Node      string
	IP        string
	Ready     bool
}

// The index holds every sidecar pod by name, and answers per node. A
// rollout runs two pods on one node for a while, and their events
// arrive interleaved, so a map keyed by node would let the old pod's
// late events take the node from the new one.
type sidecarIndex struct {
	mu     sync.RWMutex
	byName map[string]Pod
}

func newSidecarIndex() *sidecarIndex {
	return &sidecarIndex{byName: map[string]Pod{}}
}

// A request reads this map and never the API server, so a capture
// costs one call to the node and nothing else.
//
// The answer is the node's pod that is not being deleted, a Ready one
// before one that is not. A pod that is being deleted answers only
// when the node has no other, because it still runs through its grace
// period. Pods of equal rank answer by name, so every request gets the
// same one.
func (i *sidecarIndex) on(node string) (sidecarPod, bool) {
	i.mu.RLock()
	defer i.mu.RUnlock()
	var best *Pod
	for name := range i.byName {
		pod := i.byName[name]
		if pod.Spec.NodeName != node {
			continue
		}
		if best == nil || sidecarRank(pod) > sidecarRank(*best) ||
			(sidecarRank(pod) == sidecarRank(*best) && pod.Metadata.Name < best.Metadata.Name) {
			best = &pod
		}
	}
	if best == nil {
		return sidecarPod{}, false
	}
	return sidecarPod{
		Namespace: best.Metadata.Namespace,
		Name:      best.Metadata.Name,
		Node:      best.Spec.NodeName,
		IP:        best.Status.PodIP,
		Ready:     best.Status.ready(),
	}, true
}

// How strongly a pod answers for its node: not being deleted counts
// before Ready.
func sidecarRank(pod Pod) int {
	rank := 0
	if pod.Metadata.DeletionTimestamp == nil {
		rank += 2
	}
	if pod.Status.ready() {
		rank++
	}
	return rank
}

func (i *sidecarIndex) hold(pod Pod) {
	if pod.Spec.NodeName == "" {
		return
	}
	i.mu.Lock()
	defer i.mu.Unlock()
	i.byName[pod.Metadata.Name] = pod
}

func (i *sidecarIndex) drop(pod Pod) {
	i.mu.Lock()
	defer i.mu.Unlock()
	delete(i.byName, pod.Metadata.Name)
}

// The listing is the whole truth. The new map is built first and
// swapped in under one lock, so a request never reads an index that is
// half filled.
func (i *sidecarIndex) replace(pods []Pod) {
	byName := make(map[string]Pod, len(pods))
	for _, pod := range pods {
		if pod.Spec.NodeName != "" {
			byName[pod.Metadata.Name] = pod
		}
	}
	i.mu.Lock()
	defer i.mu.Unlock()
	i.byName = byName
}

// The loop: one listing, then a watch from the listing's version, for
// as long as the API runs. The listing replaces the whole index, and
// each event after it moves one pod.
func (i *sidecarIndex) run(ctx context.Context, c *Client, namespace string) {
	path := fmt.Sprintf("/api/v1/namespaces/%s/pods?labelSelector=%s", namespace, sidecarSelector)
	watchList(ctx, c, path, "the capture sidecars in "+namespace, i.replace,
		func(kind string, pod Pod) {
			if kind == "DELETED" {
				i.drop(pod)
				return
			}
			i.hold(pod)
		})
}
