package kubernetes

// This file implements pod eviction. Both operators use it because
// both ask pods to leave: the machine operator drains its own node
// ahead of a granted reboot, and the cluster operator refreshes the
// OS's own pods after an upgrade.

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

// HostPathVolume is a directory or file the pod takes from the
// machine it runs on. The path is the whole of it: what a hostPath
// mount reaches on the host is what a drain reads to tell a DRA
// driver's pod from an ordinary workload.
type HostPathVolume struct {
	Path string `json:"path"`
}

// PodVolume carries the one volume kind liken reads. Every other
// kind decodes with a nil HostPath, which is the answer the readers
// want anyway.
type PodVolume struct {
	Name     string          `json:"name"`
	HostPath *HostPathVolume `json:"hostPath"`
}

// PodResourceClaim is one entry in spec.resourceClaims: a DRA claim
// the pod's containers may reference by Name. The claim itself is
// either named directly or made from a template, so exactly one of
// the last two fields carries a value.
type PodResourceClaim struct {
	Name                      string `json:"name"`
	ResourceClaimName         string `json:"resourceClaimName"`
	ResourceClaimTemplateName string `json:"resourceClaimTemplateName"`
}

type PodSpec struct {
	NodeName       string             `json:"nodeName"`
	Volumes        []PodVolume        `json:"volumes"`
	ResourceClaims []PodResourceClaim `json:"resourceClaims"`
}

// ContainerStatus holds the part of a container's status that liken
// needs: which image it runs, and whether it is currently serving.
// Ready is the kubelet's own verdict. It covers every way a
// container can fail to serve, from a crash loop to an image whose
// binary fails to exec.
type ContainerStatus struct {
	Name  string `json:"name"`
	Image string `json:"image"`
	Ready bool   `json:"ready"`
}

type PodStatus struct {
	Phase             string            `json:"phase"`
	ContainerStatuses []ContainerStatus `json:"containerStatuses"`
}

// Pod holds the part of a Kubernetes Pod that liken needs: identity,
// where it runs, who owns it, what it takes from the host and from
// DRA, and whether it is still running.
type Pod struct {
	Metadata PodMetadata `json:"metadata"`
	Spec     PodSpec     `json:"spec"`
	Status   PodStatus   `json:"status"`
}

// Completed reports whether the pod has finished running. A completed
// pod's containers are correctly not ready, and never will be ready
// again. Because of this, drains do not evict them, and health checks
// do not count them.
func (p *Pod) Completed() bool {
	return p.Status.Phase == "Succeeded" || p.Status.Phase == "Failed"
}

// IsDaemon reports whether a DaemonSet owns the pod. Drains skip
// daemon pods, because the DaemonSet controller ignores cordons and
// would only recreate them. The pod steward refreshes only these
// pods.
func (p *Pod) IsDaemon() bool {
	for _, owner := range p.Metadata.OwnerReferences {
		if owner.Kind == "DaemonSet" {
			return true
		}
	}
	return false
}

// ListPodsOnNode reads every pod running on one node, across all
// namespaces. /api/v1/pods is the whole cluster's pod collection. The
// fieldSelector asks the server to filter this collection by
// spec.nodeName, so only that node's pods ever transfer over the
// network. This is the starting view for a drain: everything that
// might still need to move off a machine before the machine may
// reboot.
func ListPodsOnNode(c *Client, nodeName string) ([]Pod, error) {
	return List[Pod](c, "/api/v1/pods?fieldSelector=spec.nodeName%3D"+nodeName)
}

// EvictPod asks a pod to leave through the eviction subresource. The
// Eviction API is what separates a polite request from plain
// deletion. The server refuses the request while removing the pod
// would violate its PodDisruptionBudget, and the caller then asks
// again later.
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
