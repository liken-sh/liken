package main

// This file holds display-api's memory of the capture sidecars: one
// pod per node, found by label. The API keeps one watch on those pods
// instead of asking the API server on every request, so a capture
// costs one call to the node and nothing else, and the API answers
// "no ready sidecar on that node" from memory. A sidecar is ready when
// the kubelet's own Ready condition on its pod is True, which covers
// every container of the pod, the compositor included.

import (
	"context"
	"strings"
	"sync"

	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/tools/cache"

	"github.com/liken-sh/liken/kubernetes/informer"
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
// The answer is the node's Ready pod, even one being deleted: it still
// serves captures through its grace period, and a DaemonSet rollout
// with no surge starts the new pod only after the old one begins to
// leave, so preferring the new pod would refuse captures until it is
// Ready. Among pods of equal readiness, one not being deleted answers
// first, and pods of equal rank answer by name, so every request gets
// the same one.
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

// How strongly a pod answers for its node: Ready counts before not
// being deleted.
func sidecarRank(pod Pod) int {
	rank := 0
	if pod.Status.ready() {
		rank += 2
	}
	if pod.Metadata.DeletionTimestamp == nil {
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

// forget drops a deleted pod that the index cannot read, such as one
// whose tombstone holds no copy. The key is namespace/name, and the
// index holds pods of one namespace by name.
func (i *sidecarIndex) forget(key string) {
	_, name, found := strings.Cut(key, "/")
	if !found {
		name = key
	}
	i.mu.Lock()
	defer i.mu.Unlock()
	delete(i.byName, name)
}

// The watch keeps the index current for as long as the API runs.
func (i *sidecarIndex) run(ctx context.Context, client dynamic.Interface, namespace string) {
	source := informer.Source{Resource: podResource, Namespace: namespace, LabelSelector: sidecarSelector}
	<-informer.Start(ctx, client, source, informer.Options{Handler: i.handler("the capture sidecars in " + namespace)}).Done()
}

// handler moves the index with each change the informer reports. The
// informer's first read adds every sidecar pod, and each change after
// it moves one pod. A watch that resumes after a gap receives each
// change made during it. When the watch cannot resume, after a 410
// Gone, the informer reads the pods again and reports each difference
// from what it held, so a pod that left during the gap is dropped and
// no pod that stayed leaves the index on the way.
func (i *sidecarIndex) handler(what string) cache.ResourceEventHandler {
	take := func(object any) {
		pod, err := informer.Convert[Pod](object)
		if err != nil {
			informer.Report(what, err)
			return
		}
		i.hold(pod)
	}
	return cache.ResourceEventHandlerFuncs{
		AddFunc:    take,
		UpdateFunc: func(_, object any) { take(object) },
		DeleteFunc: func(object any) {
			pod, err := informer.Convert[Pod](object)
			if err == nil {
				i.drop(pod)
				return
			}
			// A pod that does not convert still left, so the index
			// drops it by its key, which needs none of the fields that
			// failed.
			informer.Report(what, err)
			if key, err := cache.DeletionHandlingMetaNamespaceKeyFunc(object); err == nil {
				i.forget(key)
			}
		},
	}
}
