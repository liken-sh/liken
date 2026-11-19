package main

// The fields of a Pod that the operator writes for its upload, scan,
// rehearsal, and apply pods and reads back. The operator declares its
// own structs for the objects it writes, and imports k8s.io/api only
// for the device attribute types that the CEL compiler reads.

import (
	"encoding/json"

	"github.com/liken-sh/liken/kubernetes/endedpods"
)

type pod struct {
	APIVersion string     `json:"apiVersion,omitempty"`
	Kind       string     `json:"kind,omitempty"`
	Metadata   objectMeta `json:"metadata"`
	Spec       podSpec    `json:"spec"`
	Status     podStatus  `json:"status,omitzero"`
}

type objectMeta struct {
	Name              string            `json:"name"`
	Namespace         string            `json:"namespace,omitempty"`
	UID               string            `json:"uid,omitempty"`
	Labels            map[string]string `json:"labels,omitempty"`
	Annotations       map[string]string `json:"annotations,omitempty"`
	OwnerReferences   []ownerReference  `json:"ownerReferences,omitempty"`
	CreationTimestamp string            `json:"creationTimestamp,omitempty"`
	DeletionTimestamp string            `json:"deletionTimestamp,omitempty"`
}

type ownerReference struct {
	APIVersion string `json:"apiVersion"`
	Kind       string `json:"kind"`
	Name       string `json:"name"`
	UID        string `json:"uid"`
}

type podSpec struct {
	RestartPolicy                 string             `json:"restartPolicy,omitempty"`
	ActiveDeadlineSeconds         *int64             `json:"activeDeadlineSeconds,omitempty"`
	TerminationGracePeriodSeconds *int64             `json:"terminationGracePeriodSeconds,omitempty"`
	NodeName                      string             `json:"nodeName,omitempty"`
	NodeSelector                  map[string]string  `json:"nodeSelector,omitempty"`
	Tolerations                   []toleration       `json:"tolerations,omitempty"`
	AutomountServiceAccountToken  *bool              `json:"automountServiceAccountToken,omitempty"`
	EnableServiceLinks            *bool              `json:"enableServiceLinks,omitempty"`
	SecurityContext               *podSecurity       `json:"securityContext,omitempty"`
	ResourceClaims                []podResourceClaim `json:"resourceClaims,omitempty"`
	Volumes                       []volume           `json:"volumes,omitempty"`
	Containers                    []container        `json:"containers"`
}

type volume struct {
	Name        string             `json:"name"`
	HostPath    *hostPath          `json:"hostPath,omitempty"`
	EmptyDir    *emptyDir          `json:"emptyDir,omitempty"`
	DownwardAPI *downwardAPIVolume `json:"downwardAPI,omitempty"`
	ConfigMap   *configMapVolume   `json:"configMap,omitempty"`
}

type configMapVolume struct {
	Name string `json:"name"`
}

type downwardAPIVolume struct {
	Items []downwardAPIFile `json:"items"`
}

type downwardAPIFile struct {
	Path     string   `json:"path"`
	FieldRef fieldRef `json:"fieldRef"`
}

type fieldRef struct {
	FieldPath string `json:"fieldPath"`
}

type hostPath struct {
	Path string `json:"path"`
	Type string `json:"type,omitempty"`
}

type emptyDir struct {
	Medium    string `json:"medium,omitempty"`
	SizeLimit string `json:"sizeLimit,omitempty"`
}

type volumeMount struct {
	Name              string `json:"name"`
	MountPath         string `json:"mountPath"`
	ReadOnly          bool   `json:"readOnly,omitempty"`
	RecursiveReadOnly string `json:"recursiveReadOnly,omitempty"`
}

type toleration struct {
	Key      string `json:"key,omitempty"`
	Operator string `json:"operator"`
	Effect   string `json:"effect,omitempty"`
}

type podSecurity struct {
	RunAsNonRoot   *bool           `json:"runAsNonRoot,omitempty"`
	RunAsUser      *int64          `json:"runAsUser,omitempty"`
	RunAsGroup     *int64          `json:"runAsGroup,omitempty"`
	SeccompProfile *seccompProfile `json:"seccompProfile,omitempty"`
}

type seccompProfile struct {
	Type string `json:"type"`
}

type podResourceClaim struct {
	Name              string `json:"name"`
	ResourceClaimName string `json:"resourceClaimName"`
}

type container struct {
	Name                     string           `json:"name"`
	Image                    string           `json:"image,omitempty"`
	Command                  []string         `json:"command,omitempty"`
	Args                     []string         `json:"args,omitempty"`
	Env                      []envVar         `json:"env,omitempty"`
	SecurityContext          *securityContext `json:"securityContext,omitempty"`
	Resources                *resources       `json:"resources,omitempty"`
	VolumeMounts             []volumeMount    `json:"volumeMounts,omitempty"`
	ReadinessProbe           *probe           `json:"readinessProbe,omitempty"`
	TerminationMessagePolicy string           `json:"terminationMessagePolicy,omitempty"`
}

// probe is a command that the kubelet runs in the container. An apply
// pod's readiness probe succeeds when the pod has finished preparing.
type probe struct {
	Exec          *execAction `json:"exec,omitempty"`
	PeriodSeconds int32       `json:"periodSeconds,omitempty"`
}

type execAction struct {
	Command []string `json:"command"`
}

type envVar struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

type securityContext struct {
	Privileged               *bool         `json:"privileged,omitempty"`
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
	Claims   []containerClaim  `json:"claims,omitempty"`
}

type containerClaim struct {
	Name string `json:"name"`
}

type podStatus struct {
	Phase             string            `json:"phase,omitempty"`
	Reason            string            `json:"reason,omitempty"`
	Message           string            `json:"message,omitempty"`
	Conditions        []podCondition    `json:"conditions,omitempty"`
	ContainerStatuses []containerStatus `json:"containerStatuses,omitempty"`
}

type podCondition struct {
	Type   string `json:"type"`
	Status string `json:"status"`
	Reason string `json:"reason,omitempty"`
}

type containerStatus struct {
	Name  string `json:"name"`
	State struct {
		Terminated *terminated `json:"terminated,omitempty"`
	} `json:"state"`
}

type terminated struct {
	ExitCode   int32  `json:"exitCode"`
	Reason     string `json:"reason,omitempty"`
	Message    string `json:"message,omitempty"`
	FinishedAt string `json:"finishedAt,omitempty"`
}

const (
	podPending   = "Pending"
	podSucceeded = "Succeeded"
	podFailed    = "Failed"
)

// scheduled reports whether the pod's PodScheduled condition is True.
// The scheduler places a pod only after it allocates the pod's claims,
// so a scheduled upload pod's claim exists.
func (p pod) scheduled() bool { return p.conditionTrue("PodScheduled") }

func (p pod) ready() bool { return p.conditionTrue("Ready") }

func (p pod) conditionTrue(kind string) bool {
	for _, c := range p.Status.Conditions {
		if c.Type == kind {
			return c.Status == "True"
		}
	}
	return false
}

func (p pod) over() bool { return p.Status.Phase == podSucceeded || p.Status.Phase == podFailed }

// loader returns the terminated state of the upload container, or nil
// if the container has not terminated.
func (p pod) loader() *terminated { return p.ended(uploadContainer) }

// ended returns the terminated state of the named container, or nil if
// it has not terminated.
func (p pod) ended(name string) *terminated {
	for _, status := range p.Status.ContainerStatuses {
		if status.Name == name {
			return status.State.Terminated
		}
	}
	return nil
}

func podsPathIn(namespace string) string {
	return "/api/v1/namespaces/" + namespace + "/pods"
}

func podPath(namespace, name string) string {
	return podsPathIn(namespace) + "/" + name
}

// mustJSON ignores the error of json.Marshal, because the operator's
// structs contain only strings, numbers, booleans, maps, and slices, which
// always encode.
func mustJSON(object any) []byte {
	body, _ := json.Marshal(object)
	return body
}

// endedByKubelet reports whether the kubelet ended the pod for a reason
// that is not the pod's own program (endedpods.Pod.EndedByKubelet).
func (p pod) endedByKubelet() bool { return p.end().EndedByKubelet() }

// endedAtShutdown reports whether the kubelet ended the pod because its
// node shuts down. The reasons Terminated and Shutdown mean a graceful
// node shutdown, and NodeShutdown means that the kubelet rejected the
// pod at admission while the node shut down. The condition
// DisruptionTarget with the reason TerminationByKubelet also marks an
// eviction and a preemption for a critical pod, so the condition alone
// means a shutdown only when the pod's reason is neither Evicted nor
// Preempting.
func (p pod) endedAtShutdown() bool {
	if !p.endedByKubelet() {
		return false
	}
	switch p.Status.Reason {
	case "Terminated", "Shutdown", "NodeShutdown":
		return true
	case "Evicted", "Preempting":
		return false
	}
	for _, c := range p.Status.Conditions {
		if c.Type == "DisruptionTarget" && c.Status == "True" && c.Reason == "TerminationByKubelet" {
			return true
		}
	}
	return false
}

// kubeletEnd describes why the kubelet ended the pod, for a status
// message: the pod's reason and message (endedpods.Words), or the
// DisruptionTarget condition when the pod has neither.
func (p pod) kubeletEnd() string {
	if words := endedpods.Words(p.Status.Reason, p.Status.Message); words != "" {
		return words
	}
	return "DisruptionTarget"
}
