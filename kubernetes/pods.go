package kubernetes

// Pod eviction, shared because both operators ask pods to leave: the
// machine operator drains its own node ahead of a granted reboot,
// and the cluster operator refreshes the OS's own pods after an
// upgrade.

import (
	"encoding/json"
	"net/http"
)

type PodMetadata struct {
	Name            string            `json:"name"`
	Namespace       string            `json:"namespace"`
	Annotations     map[string]string `json:"annotations"`
	OwnerReferences []OwnerReference  `json:"ownerReferences"`
}

type PodSpec struct {
	NodeName string `json:"nodeName"`
}

// ContainerStatus is the sliver of a container's status liken needs:
// which image it runs and whether it is currently serving. Ready is
// the kubelet's own verdict, and it covers every way a container can
// fail to serve, from a crash loop to an image whose binary won't
// even exec.
type ContainerStatus struct {
	Name  string `json:"name"`
	Image string `json:"image"`
	Ready bool   `json:"ready"`
}

type PodStatus struct {
	Phase             string            `json:"phase"`
	ContainerStatuses []ContainerStatus `json:"containerStatuses"`
}

// Pod is the sliver of a Kubernetes Pod liken needs: identity, where
// it runs, who owns it, and whether it is still running.
type Pod struct {
	Metadata PodMetadata `json:"metadata"`
	Spec     PodSpec     `json:"spec"`
	Status   PodStatus   `json:"status"`
}

// Completed reports whether the pod has run to its end. A completed
// pod's containers are legitimately not ready and never will be, so
// drains don't evict them and health judgments don't count them.
func (p *Pod) Completed() bool {
	return p.Status.Phase == "Succeeded" || p.Status.Phase == "Failed"
}

// IsDaemon reports whether a DaemonSet owns the pod. Drains skip
// daemon pods (the DaemonSet controller ignores cordons and would
// just recreate them), and the pod steward refreshes exactly them.
func (p *Pod) IsDaemon() bool {
	for _, owner := range p.Metadata.OwnerReferences {
		if owner.Kind == "DaemonSet" {
			return true
		}
	}
	return false
}

// ListPodsOnNode reads every pod running on one node, across all
// namespaces: /api/v1/pods is the whole cluster's pod collection, and
// the fieldSelector asks the server to filter it by spec.nodeName, so
// only that node's pods ever cross the wire. This is the view a drain
// starts from: everything that might still have to move off a machine
// before it may reboot.
func ListPodsOnNode(c *Client, nodeName string) ([]Pod, error) {
	return List[Pod](c, "/api/v1/pods?fieldSelector=spec.nodeName%3D"+nodeName)
}

// EvictPod asks a pod to leave through the eviction subresource. The
// Eviction API is what separates asking from plain deletion: the
// request is refused while removing the pod would violate its
// PodDisruptionBudget, and the caller simply asks again later.
func EvictPod(c *Client, p Pod) error {
	body, err := json.Marshal(map[string]any{
		"apiVersion": "policy/v1",
		"kind":       "Eviction",
		"metadata":   map[string]string{"name": p.Metadata.Name, "namespace": p.Metadata.Namespace},
	})
	if err != nil {
		return err
	}
	path := "/api/v1/namespaces/" + p.Metadata.Namespace + "/pods/" + p.Metadata.Name + "/eviction"
	return c.RequestJSON(http.MethodPost, path, body, nil)
}
