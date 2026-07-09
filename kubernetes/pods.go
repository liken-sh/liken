package kubernetes

// Pod eviction, shared because both operators ask pods to leave: the
// machine operator drains its own node ahead of a granted reboot,
// and the cluster operator refreshes the OS's own pods after an
// upgrade.

import (
	"encoding/json"
	"net/http"
)

type OwnerReference struct {
	Kind string `json:"kind"`
}

type PodMetadata struct {
	Name            string            `json:"name"`
	Namespace       string            `json:"namespace"`
	Annotations     map[string]string `json:"annotations"`
	OwnerReferences []OwnerReference  `json:"ownerReferences"`
}

type PodSpec struct {
	NodeName string `json:"nodeName"`
}

type PodStatus struct {
	Phase string `json:"phase"`
}

// Pod is the sliver of a Kubernetes Pod liken needs: identity, where
// it runs, who owns it, and whether it is still running.
type Pod struct {
	Metadata PodMetadata `json:"metadata"`
	Spec     PodSpec     `json:"spec"`
	Status   PodStatus   `json:"status"`
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
