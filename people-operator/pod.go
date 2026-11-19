package main

// The fields of a Pod that the operator writes for a baker pod and reads
// back. The operator declares its own structs so that it does not link
// k8s.io/api, whose core package is large.

import (
	"encoding/json"
	"slices"
	"strconv"
	"time"
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
// cause outside the pod's program: an eviction, a rejection at
// admission, a graceful node shutdown, or a preemption. The kubelet
// then sets the pod-level status.reason, or the condition
// DisruptionTarget, or both. A container that exits with a status that
// is not zero leaves the pod-level reason empty. DeadlineExceeded is the
// end of the pod's own activeDeadlineSeconds, so it counts as the pod's
// own failure.
func (p pod) endedByKubelet() bool {
	if p.Status.Phase != podFailed {
		return false
	}
	disrupted := slices.ContainsFunc(p.Status.Conditions, func(c podCondition) bool {
		return c.Type == "DisruptionTarget" && c.Status == "True"
	})
	return disrupted || (p.Status.Reason != "" && p.Status.Reason != "DeadlineExceeded")
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

// The wait before the next pod of a bake whose pod the kubelet ended.
// A baker pod that mounts a claim on a node-local class can run on one
// node only, and the kubelet of that node can refuse each next pod at
// admission, so the wait doubles for each pod that the kubelet ended,
// up to maxRetryWait.
const (
	firstRetryWait = 10 * time.Second
	maxRetryWait   = 5 * time.Minute
)

// tries returns the count of earlier pods of the same bake that the
// kubelet ended, from the annotation triesKey. A pod with no count is
// the first. The count is in the pod, and not in memory, so a restarted
// operator waits as long as the one before it.
func (p pod) tries() int {
	count, _ := strconv.Atoi(p.Metadata.Annotations[triesKey])
	return max(count, 0)
}

func retryWait(tries int) time.Duration {
	wait := firstRetryWait
	for range tries {
		if wait >= maxRetryWait {
			break
		}
		wait *= 2
	}
	return min(wait, maxRetryWait)
}

// retryAt returns when the next pod can start: the wait for the pod's
// count of tries, counted from the pod's creation. The kubelet refuses a
// pod soon after its creation, so a pod that ran for longer than the
// wait is replaced at once, and the count is never reset.
func (p pod) retryAt() time.Time {
	created, _ := time.Parse(time.RFC3339, p.Metadata.CreationTimestamp)
	return created.Add(retryWait(p.tries()))
}

// ran returns how long a pod ran before the kubelet ended it: from its
// creation to the lastTransitionTime of its DisruptionTarget condition,
// or else to the latest finishedAt of a terminated container. A pod with
// neither was refused at admission and never ran. The pod's age when the
// pass replaces it is no measure, because a pass runs a little after
// each wait and the API server cuts each timestamp to whole seconds, so
// a pod refused at once would read as older than maxRetryWait.
func (p pod) ran() time.Duration {
	created, _ := time.Parse(time.RFC3339, p.Metadata.CreationTimestamp)
	var ended time.Time
	for _, c := range p.Status.Conditions {
		if c.Type == "DisruptionTarget" && c.Status == "True" {
			ended, _ = time.Parse(time.RFC3339, c.LastTransitionTime)
		}
	}
	if ended.IsZero() {
		for _, c := range p.Status.ContainerStatuses {
			if c.State.Terminated == nil {
				continue
			}
			if finished, err := time.Parse(time.RFC3339, c.State.Terminated.FinishedAt); err == nil && finished.After(ended) {
				ended = finished
			}
		}
	}
	if ended.IsZero() {
		return 0
	}
	return max(ended.Sub(created), 0)
}
