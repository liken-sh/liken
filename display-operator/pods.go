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
	"encoding/json"
	"fmt"
	"io"
	"os"
	"slices"
	"time"
)

// The pod collection, and the field selector that keeps a read to one
// node's pods.
const (
	PodsPath     = "/api/v1/pods"
	podsOnNode   = "?fieldSelector=spec.nodeName="
	podsResource = "pods"
)

// Pod holds the part of a pod this operator reads. The labels are
// what a region's selector matches, and the name and namespace are
// what status.surfaces reports beside them. The operator never writes
// a pod.
type Pod struct {
	Metadata PodMeta   `json:"metadata"`
	Status   PodStatus `json:"status,omitempty"`
}

type PodList struct {
	Items []Pod `json:"items"`
}

type PodMeta struct {
	Name      string            `json:"name"`
	Namespace string            `json:"namespace"`
	Labels    map[string]string `json:"labels,omitempty"`
}

// PodStatus is what the kubelet reports about one pod's containers. A
// native sidecar is an init container whose restartPolicy is Always,
// so the compositor's count is in initContainerStatuses.
type PodStatus struct {
	ContainerStatuses     []ContainerStatus `json:"containerStatuses,omitempty"`
	InitContainerStatuses []ContainerStatus `json:"initContainerStatuses,omitempty"`
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
func getPod(c *Client, namespace, name string) (*Pod, error) {
	return get[Pod](c, "/api/v1/namespaces/"+namespace+"/pods/"+name)
}

// listPods reads the pods on one node. The field selector is what
// holds the read to this node: a pod that holds a claim on a screen
// this operator drives runs on the same node as the screen, so the
// pods of every other node answer nothing a pass reads.
func listPods(c *Client, node string) ([]Pod, error) {
	list, err := get[PodList](c, PodsPath+podsOnNode+node)
	if err != nil {
		return nil, err
	}
	return list.Items, nil
}

// The watch turns a pod that gained or lost a label into one wake,
// because the labels a region's selector matches are the pods' own. It
// keeps the bounds the Display watch keeps, and it carries the same
// field selector the listing does.
func watchPods(ctx context.Context, c *Client, node string, wake func(), readings *metrics) {
	first := true
	for ctx.Err() == nil {
		if !first {
			// The API server closed the last connection and this one
			// opens in its place, the one restart milestone 65 counts.
			readings.watchRestarted(kindPod)
		}
		first = false
		if err := streamPods(ctx, c, node, wake); err != nil && ctx.Err() == nil {
			fmt.Fprintf(os.Stderr, "watching the pods on %s: %v\n", node, err)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(displayWatchRetry):
		}
	}
}

// One watch connection. It starts at the present, because an event
// carries nothing the pass uses and a missed event costs one backstop
// tick.
func streamPods(ctx context.Context, c *Client, node string, wake func()) error {
	path := fmt.Sprintf("%s%s%s&watch=true&timeoutSeconds=%d",
		PodsPath, podsOnNode, node, int(displayWatchTimeout.Seconds()))
	body, err := c.Watch(ctx, path)
	if err != nil {
		return err
	}
	defer drain(body)

	events := json.NewDecoder(body)
	for {
		var event struct {
			Type string `json:"type"`
		}
		if err := events.Decode(&event); err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}
		wake()
	}
}
