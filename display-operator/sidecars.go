package main

// display-api watches the capture sidecar pods and caches them, so a
// capture request finds its node's sidecar without listing pods from
// the API server. A sidecar is ready when its pod's Ready condition is
// True, which covers every container of the pod, including the
// compositor.

import (
	"context"
	"strings"
	"sync"

	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/tools/cache"

	"github.com/liken-sh/liken/kubernetes/informer"
)

// The pods the API watches: the display-operator DaemonSet's pods in
// liken-system. Each runs on one node, and a Display's status.node
// selects the pod to use.
const (
	sidecarNamespace = "liken-system"
	sidecarSelector  = "app=display-operator"
)

// sidecarPod is one node's sidecar: where to reach it and whether its
// pod is ready.
type sidecarPod struct {
	Namespace string
	Name      string
	Node      string
	IP        string
	Ready     bool
}

// sidecarIndex stores every sidecar pod by name and looks them up by
// node. During a rollout two pods run on one node, and their events
// can arrive interleaved. A map keyed by node would let a late event
// from the old pod overwrite the new pod's entry.
type sidecarIndex struct {
	mu     sync.RWMutex
	byName map[string]Pod
}

func newSidecarIndex() *sidecarIndex {
	return &sidecarIndex{byName: map[string]Pod{}}
}

// on returns the sidecar to use for a node. It prefers a Ready pod,
// even one that is being deleted. That pod still serves captures
// during its grace period, and a DaemonSet rollout without surge
// starts the new pod only after the old one begins to terminate, so
// preferring the new pod would refuse captures until it is Ready.
// Among equally ready pods it prefers one that is not being deleted,
// then the lowest name, so every request gets the same pod.
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

// sidecarRank orders the pods of one node. Ready outranks not being
// deleted.
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

func (i *sidecarIndex) add(pod Pod) {
	if pod.Spec.NodeName == "" {
		return
	}
	i.mu.Lock()
	defer i.mu.Unlock()
	i.byName[pod.Metadata.Name] = pod
}

func (i *sidecarIndex) remove(pod Pod) {
	i.mu.Lock()
	defer i.mu.Unlock()
	delete(i.byName, pod.Metadata.Name)
}

// removeKey removes a deleted pod by its namespace/name key, for a
// deleted object that does not convert to a Pod, such as a tombstone
// with no copy of the object. The index holds pods of one namespace, so
// it uses only the name.
func (i *sidecarIndex) removeKey(key string) {
	_, name, found := strings.Cut(key, "/")
	if !found {
		name = key
	}
	i.mu.Lock()
	defer i.mu.Unlock()
	delete(i.byName, name)
}

// run watches the sidecar pods and keeps the index current until ctx
// ends.
func (i *sidecarIndex) run(ctx context.Context, client dynamic.Interface, namespace string) {
	source := informer.Source{Resource: podResource, Namespace: namespace, LabelSelector: sidecarSelector}
	<-informer.Start(ctx, client, source, informer.Options{Handler: i.handler("the capture sidecars in " + namespace)}).Done()
}

// handler applies each change the informer reports to the index. After
// a 410 Gone the informer lists the pods again and reports a deletion
// for each pod that left during the gap and an update for each pod
// that stayed, so the index needs no reset.
func (i *sidecarIndex) handler(what string) cache.ResourceEventHandler {
	take := func(object any) {
		pod, err := informer.Convert[Pod](object)
		if err != nil {
			informer.Report(what, err)
			return
		}
		i.add(pod)
	}
	return cache.ResourceEventHandlerFuncs{
		AddFunc:    take,
		UpdateFunc: func(_, object any) { take(object) },
		DeleteFunc: func(object any) {
			pod, err := informer.Convert[Pod](object)
			if err == nil {
				i.remove(pod)
				return
			}
			// The key does not depend on the fields that failed to
			// convert.
			informer.Report(what, err)
			if key, err := cache.DeletionHandlingMetaNamespaceKeyFunc(object); err == nil {
				i.removeKey(key)
			}
		},
	}
}
