package main

// The fields of a Pod that the operator writes for a baker pod and reads
// back. The operator declares its own structs so that it does not link
// k8s.io/api, whose core package is large.

import (
	"encoding/json"
	"time"

	"github.com/liken-sh/liken/kubernetes/endedpods"
)

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
	Phase      string         `json:"phase,omitempty"`
	Reason     string         `json:"reason,omitempty"`
	Message    string         `json:"message,omitempty"`
	Conditions []podCondition `json:"conditions,omitempty"`

	ContainerStatuses []containerStatus `json:"containerStatuses,omitempty"`
}

type podCondition struct {
	Type               string `json:"type"`
	Status             string `json:"status"`
	LastTransitionTime string `json:"lastTransitionTime,omitempty"`
}

type containerStatus struct {
	State containerState `json:"state"`
}

type containerState struct {
	Terminated *terminatedState `json:"terminated,omitempty"`
}

type terminatedState struct {
	FinishedAt string `json:"finishedAt,omitempty"`
}

// The pod phases that are final.
const (
	podSucceeded = "Succeeded"
	podFailed    = "Failed"
)

func (p pod) finished() bool {
	return p.Status.Phase == podSucceeded || p.Status.Phase == podFailed
}

// endedByKubelet reports whether the kubelet ended a failed pod for a
// cause outside the pod's program (endedpods.Pod.EndedByKubelet).
func (p pod) endedByKubelet() bool { return p.end().EndedByKubelet() }

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

// tries returns the count of earlier pods of the same bake that the
// kubelet ended, from the annotation triesKey. A pod with no count is
// the first.
func (p pod) tries() int {
	return endedpods.Tries(p.Metadata.Annotations, triesKey)
}

// end returns what the rule of kubernetes/endedpods reads of a pod. A
// baker pod that mounts a claim on a node-local class can run on one
// node only, and the kubelet of that node can refuse each next pod at
// admission, so the next pod waits by that rule.
func (p pod) end() endedpods.Pod {
	end := endedpods.Pod{
		Phase:   p.Status.Phase,
		Reason:  p.Status.Reason,
		Created: parseTime(p.Metadata.CreationTimestamp),
		Tries:   p.tries(),
	}
	for _, c := range p.Status.Conditions {
		end.Conditions = append(end.Conditions, endedpods.Condition{Type: c.Type, Status: c.Status, LastTransitionTime: parseTime(c.LastTransitionTime)})
	}
	for _, c := range p.Status.ContainerStatuses {
		if c.State.Terminated != nil {
			end.Finished = append(end.Finished, parseTime(c.State.Terminated.FinishedAt))
		}
	}
	return end
}

// parseTime returns the time of an RFC 3339 timestamp, and the zero time
// for a timestamp that is empty or does not parse.
func parseTime(value string) time.Time {
	parsed, _ := time.Parse(time.RFC3339, value)
	return parsed
}
