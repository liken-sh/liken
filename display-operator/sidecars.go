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
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sync"
	"time"
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
	IP        string
	Ready     bool
}

type sidecarIndex struct {
	mu     sync.RWMutex
	byNode map[string]sidecarPod
}

func newSidecarIndex() *sidecarIndex {
	return &sidecarIndex{byNode: map[string]sidecarPod{}}
}

// A request reads this map and never the API server, so a capture
// costs one call to the node and nothing else.
func (i *sidecarIndex) on(node string) (sidecarPod, bool) {
	i.mu.RLock()
	defer i.mu.RUnlock()
	pod, held := i.byNode[node]
	return pod, held
}

func (i *sidecarIndex) hold(pod Pod) {
	i.mu.Lock()
	defer i.mu.Unlock()
	if pod.Spec.NodeName == "" {
		return
	}
	i.byNode[pod.Spec.NodeName] = sidecarPod{
		Namespace: pod.Metadata.Namespace,
		Name:      pod.Metadata.Name,
		IP:        pod.Status.PodIP,
		Ready:     pod.Status.ready(),
	}
}

func (i *sidecarIndex) drop(pod Pod) {
	i.mu.Lock()
	defer i.mu.Unlock()
	held, there := i.byNode[pod.Spec.NodeName]
	if there && held.Name == pod.Metadata.Name {
		delete(i.byNode, pod.Spec.NodeName)
	}
}

func (i *sidecarIndex) replace(pods []Pod) {
	i.mu.Lock()
	i.byNode = map[string]sidecarPod{}
	i.mu.Unlock()
	for _, pod := range pods {
		i.hold(pod)
	}
}

// The loop: one listing, then one watch, and a listing again
// whenever the watch ends, so a missed event costs one reconnection
// and never a stale answer.
func (i *sidecarIndex) run(ctx context.Context, c *Client, namespace string) {
	for ctx.Err() == nil {
		if err := i.session(ctx, c, namespace); err != nil && ctx.Err() == nil {
			fmt.Fprintf(os.Stderr, "watching the capture sidecars in %s: %v\n", namespace, err)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(displayWatchRetry):
		}
	}
}

// The listing is the whole truth and the watch is the news. The
// watch starts at the version the listing answered with, so no event
// between the two is missed.
func (i *sidecarIndex) session(ctx context.Context, c *Client, namespace string) error {
	path := fmt.Sprintf("/api/v1/namespaces/%s/pods?labelSelector=%s", namespace, sidecarSelector)
	list, err := get[PodList](c, path)
	if err != nil {
		return err
	}
	i.replace(list.Items)

	stream := fmt.Sprintf("%s&watch=true&resourceVersion=%s&timeoutSeconds=%d",
		path, list.Metadata.ResourceVersion, int(displayWatchTimeout.Seconds()))
	body, err := c.Watch(ctx, stream)
	if err != nil {
		return err
	}
	defer drain(body)

	events := json.NewDecoder(body)
	for {
		var event struct {
			Type   string `json:"type"`
			Object Pod    `json:"object"`
		}
		if err := events.Decode(&event); err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}
		switch event.Type {
		case "ADDED", "MODIFIED":
			i.hold(event.Object)
		case "DELETED":
			i.drop(event.Object)
		}
	}
}
