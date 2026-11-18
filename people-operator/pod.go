package main

// The fields of a Pod that the operator writes for a baker pod and reads
// back. The operator declares its own structs so that it does not link
// k8s.io/api, whose core package is large.

import "encoding/json"

type pod struct {
	APIVersion string    `json:"apiVersion,omitempty"`
	Kind       string    `json:"kind,omitempty"`
	Metadata   podMeta   `json:"metadata"`
	Spec       podSpec   `json:"spec"`
	Status     podStatus `json:"status,omitzero"`
}

type podMeta struct {
	Name              string            `json:"name"`
	Namespace         string            `json:"namespace,omitempty"`
	Labels            map[string]string `json:"labels,omitempty"`
	Annotations       map[string]string `json:"annotations,omitempty"`
	CreationTimestamp string            `json:"creationTimestamp,omitempty"`
	DeletionTimestamp string            `json:"deletionTimestamp,omitempty"`
}

type podSpec struct {
	RestartPolicy                string      `json:"restartPolicy,omitempty"`
	AutomountServiceAccountToken *bool       `json:"automountServiceAccountToken,omitempty"`
	EnableServiceLinks           *bool       `json:"enableServiceLinks,omitempty"`
	Containers                   []container `json:"containers"`
	Volumes                      []volume    `json:"volumes,omitempty"`
}

type container struct {
	Name            string           `json:"name"`
	Image           string           `json:"image,omitempty"`
	Args            []string         `json:"args,omitempty"`
	VolumeMounts    []volumeMount    `json:"volumeMounts,omitempty"`
	SecurityContext *securityContext `json:"securityContext,omitempty"`
	Resources       *resources       `json:"resources,omitempty"`
}

type volumeMount struct {
	Name      string `json:"name"`
	MountPath string `json:"mountPath"`
	ReadOnly  bool   `json:"readOnly,omitempty"`
}

type volume struct {
	Name                  string       `json:"name"`
	NFS                   *nfsVolume   `json:"nfs,omitempty"`
	PersistentVolumeClaim *claimVolume `json:"persistentVolumeClaim,omitempty"`
}

type nfsVolume struct {
	Server   string `json:"server"`
	Path     string `json:"path"`
	ReadOnly bool   `json:"readOnly,omitempty"`
}

type claimVolume struct {
	ClaimName string `json:"claimName"`
	ReadOnly  bool   `json:"readOnly,omitempty"`
}

type securityContext struct {
	AllowPrivilegeEscalation *bool         `json:"allowPrivilegeEscalation,omitempty"`
	ReadOnlyRootFilesystem   *bool         `json:"readOnlyRootFilesystem,omitempty"`
	Capabilities             *capabilities `json:"capabilities,omitempty"`
}

type capabilities struct {
	Add  []string `json:"add,omitempty"`
	Drop []string `json:"drop,omitempty"`
}

type resources struct {
	Requests map[string]string `json:"requests,omitempty"`
	Limits   map[string]string `json:"limits,omitempty"`
}

type podStatus struct {
	Phase string `json:"phase,omitempty"`
}

// The pod phases that are final.
const (
	podSucceeded = "Succeeded"
	podFailed    = "Failed"
)

func (p pod) finished() bool {
	return p.Status.Phase == podSucceeded || p.Status.Phase == podFailed
}

func podPath(namespace, name string) string {
	return "/api/v1/namespaces/" + namespace + "/pods/" + name
}

// mustJSON encodes an object that the operator built. The structs hold
// only strings, numbers, booleans, maps, and slices, so encoding cannot
// fail.
func mustJSON(object any) []byte {
	body, _ := json.Marshal(object)
	return body
}
