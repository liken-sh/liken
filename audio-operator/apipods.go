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
	"fmt"
	"io"
	"net/url"
	"os"
	"sync"
	"time"
)

// operatorSelector is the label the DaemonSet's pods carry.
const operatorSelector = "app=audio-operator"

// captureContainer is the container whose readiness decides whether a
// pod can answer a tap.
const captureContainer = "capture"

// podWatchTimeout and podWatchRetry follow the endpoint watches in
// sinks.go, for the same reasons.
const (
	podWatchTimeout = 290 * time.Second
	podWatchRetry   = 5 * time.Second
)

// capturePod is what the API needs about one pod: where to dial it and
// whether its capture container is running.
type capturePod struct {
	Name  string
	Node  string
	IP    string
	Ready bool
}

// pod is the part of a Kubernetes Pod this API reads.
type pod struct {
	Metadata EndpointMeta `json:"metadata"`
	Spec     struct {
		NodeName string `json:"nodeName"`
	} `json:"spec"`
	Status struct {
		PodIP             string `json:"podIP"`
		ContainerStatuses []struct {
			Name  string `json:"name"`
			Ready bool   `json:"ready"`
		} `json:"containerStatuses"`
	} `json:"status"`
}

type podList struct {
	Metadata struct {
		ResourceVersion string `json:"resourceVersion"`
	} `json:"metadata"`
	Items []pod `json:"items"`
}

// capture reads one pod into the answer a request needs. A pod with no
// address, or whose capture container is not running, is held with
// Ready false rather than dropped, so the 503 can say which it was.
func (p pod) capture() capturePod {
	found := capturePod{
		Name: p.Metadata.Name,
		Node: p.Spec.NodeName,
		IP:   p.Status.PodIP,
	}
	for _, container := range p.Status.ContainerStatuses {
		if container.Name == captureContainer && container.Ready && found.IP != "" {
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
// and every reopen deliver.
func (index *podIndex) replace(pods []pod) {
	next := map[string]capturePod{}
	for _, held := range pods {
		if held.Spec.NodeName == "" {
			continue
		}
		next[held.Spec.NodeName] = held.capture()
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
		delete(index.byNode, held.Spec.NodeName)
		return
	}
	index.byNode[held.Spec.NodeName] = held.capture()
}

// watchPods holds one list and one watch open for the life of the
// process. A watch the API server closes, and a network fault, both
// bring the loop back to the list, so the memory is rebuilt whole
// rather than followed from a resource version that may have aged out.
func watchPods(ctx context.Context, client *Client, namespace string,
	index *podIndex, complain func(error)) {
	path := "/api/v1/namespaces/" + namespace + "/pods?labelSelector=" +
		url.QueryEscape(operatorSelector)
	for ctx.Err() == nil {
		if err := followPods(ctx, client, path, index); err != nil && ctx.Err() == nil {
			complain(err)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(podWatchRetry):
		}
	}
}

// followPods lists once and then follows the changes.
func followPods(ctx context.Context, client *Client, path string, index *podIndex) error {
	list, err := get[podList](client, path)
	if err != nil {
		return fmt.Errorf("listing the operator's pods: %w", err)
	}
	index.replace(list.Items)

	body, err := client.Watch(ctx, fmt.Sprintf("%s&watch=true&resourceVersion=%s&timeoutSeconds=%d",
		path, list.Metadata.ResourceVersion, int(podWatchTimeout.Seconds())))
	if err != nil {
		return fmt.Errorf("watching the operator's pods: %w", err)
	}
	defer drain(body)

	events := json.NewDecoder(body)
	for {
		var event struct {
			Type   string `json:"type"`
			Object pod    `json:"object"`
		}
		if err := events.Decode(&event); err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}
		index.apply(event.Type, event.Object)
	}
}

// podNamespace is where the API looks for the operator's pods, which
// is its own namespace, from the downward API.
func podNamespace() string {
	if namespace := os.Getenv("POD_NAMESPACE"); namespace != "" {
		return namespace
	}
	return "liken-system"
}
