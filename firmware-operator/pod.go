package main

// The part of a Pod that the operator writes for an upload and reads
// back. The operator declares its own structs for the objects it
// writes, and links k8s.io/api only for the attribute types that the
// CEL compiler reads (selector.go).

import "encoding/json"

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
	RestartPolicy                string             `json:"restartPolicy,omitempty"`
	ActiveDeadlineSeconds        *int64             `json:"activeDeadlineSeconds,omitempty"`
	NodeSelector                 map[string]string  `json:"nodeSelector,omitempty"`
	AutomountServiceAccountToken *bool              `json:"automountServiceAccountToken,omitempty"`
	EnableServiceLinks           *bool              `json:"enableServiceLinks,omitempty"`
	SecurityContext              *podSecurity       `json:"securityContext,omitempty"`
	ResourceClaims               []podResourceClaim `json:"resourceClaims,omitempty"`
	Containers                   []container        `json:"containers"`
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
	TerminationMessagePolicy string           `json:"terminationMessagePolicy,omitempty"`
}

type envVar struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

type securityContext struct {
	AllowPrivilegeEscalation *bool         `json:"allowPrivilegeEscalation,omitempty"`
	ReadOnlyRootFilesystem   *bool         `json:"readOnlyRootFilesystem,omitempty"`
	Capabilities             *capabilities `json:"capabilities,omitempty"`
}

type capabilities struct {
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

// The phases after which a pod runs no more.
const (
	podPending   = "Pending"
	podSucceeded = "Succeeded"
	podFailed    = "Failed"
)

// scheduled reports whether the scheduler placed the pod on its node.
// The scheduler places a pod only after it allocated the pod's claim,
// so a scheduled pod's claim exists.
func (p pod) scheduled() bool {
	for _, c := range p.Status.Conditions {
		if c.Type == "PodScheduled" {
			return c.Status == "True"
		}
	}
	return false
}

// loader answers the terminated state of the upload's container, or
// nil while it has not ended.
func (p pod) loader() *terminated {
	for _, held := range p.Status.ContainerStatuses {
		if held.Name == uploadContainer {
			return held.State.Terminated
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

// mustJSON encodes an object the operator built. Its structs hold only
// strings, numbers, booleans, maps, and slices, which always encode.
func mustJSON(object any) []byte {
	body, _ := json.Marshal(object)
	return body
}
