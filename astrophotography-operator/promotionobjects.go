package main

// The parts of a Job, a pod, and a PersistentVolumeClaim that the
// promotion loop reads and writes, and the parts of a pod that the
// solvers' and workers' Deployments write (kube.go). Each is decoded from a watch's store
// with runtime.DefaultUnstructuredConverter, so a field that is not
// here costs nothing.

import (
	"github.com/liken-sh/liken/observatory-operator/observatory"
)

// objectMeta is the metadata the loop reads and writes, with the owner
// that ties a promotion Job to its Target.
type objectMeta struct {
	observatory.ObjectMeta `json:",inline"`
	OwnerReferences        []ownerReference `json:"ownerReferences,omitempty"`
}

type ownerReference struct {
	APIVersion string `json:"apiVersion"`
	Kind       string `json:"kind"`
	Name       string `json:"name"`
	UID        string `json:"uid"`
	Controller bool   `json:"controller,omitempty"`
}

// ownedBy reports whether an object's owner is the object of a UID.
func (m objectMeta) ownedBy(uid string) bool {
	for _, owner := range m.OwnerReferences {
		if owner.UID == uid {
			return true
		}
	}
	return false
}

type jobObject struct {
	APIVersion string     `json:"apiVersion"`
	Kind       string     `json:"kind"`
	Metadata   objectMeta `json:"metadata"`
	Spec       jobSpec    `json:"spec"`
	Status     jobStatus  `json:"status,omitzero"`
}

type jobSpec struct {
	BackoffLimit            int32       `json:"backoffLimit"`
	ActiveDeadlineSeconds   int64       `json:"activeDeadlineSeconds"`
	TTLSecondsAfterFinished int32       `json:"ttlSecondsAfterFinished"`
	Template                podTemplate `json:"template"`
}

type jobStatus struct {
	Conditions []objectCondition `json:"conditions,omitempty"`
}

// objectCondition is a condition of a built-in kind.
type objectCondition struct {
	Type    string `json:"type"`
	Status  string `json:"status"`
	Reason  string `json:"reason,omitempty"`
	Message string `json:"message,omitempty"`
}

// condition answers the condition of a type that is True, and false
// when none is.
func condition(list []objectCondition, conditionType, status string) (objectCondition, bool) {
	for _, c := range list {
		if c.Type == conditionType && c.Status == status {
			return c, true
		}
	}
	return objectCondition{}, false
}

type podTemplate struct {
	Metadata objectMeta `json:"metadata"`
	Spec     podSpec    `json:"spec"`
}

type podSpec struct {
	RestartPolicy                 string       `json:"restartPolicy,omitempty"`
	TerminationGracePeriodSeconds *int64       `json:"terminationGracePeriodSeconds,omitempty"`
	EnableServiceLinks            *bool        `json:"enableServiceLinks,omitempty"`
	AutomountServiceAccountToken  *bool        `json:"automountServiceAccountToken,omitempty"`
	SecurityContext               *podSecurity `json:"securityContext,omitempty"`
	Containers                    []container  `json:"containers"`
	Volumes                       []volume     `json:"volumes,omitempty"`
}

type podSecurity struct {
	RunAsUser           int64           `json:"runAsUser"`
	RunAsGroup          int64           `json:"runAsGroup"`
	RunAsNonRoot        bool            `json:"runAsNonRoot"`
	FSGroup             int64           `json:"fsGroup,omitempty"`
	FSGroupChangePolicy string          `json:"fsGroupChangePolicy,omitempty"`
	SeccompProfile      *seccompProfile `json:"seccompProfile,omitempty"`
}

type seccompProfile struct {
	Type string `json:"type"`
}

type container struct {
	Name            string             `json:"name"`
	Image           string             `json:"image"`
	Args            []string           `json:"args,omitempty"`
	Ports           []containerPort    `json:"ports,omitempty"`
	VolumeMounts    []volumeMount      `json:"volumeMounts,omitempty"`
	SecurityContext *containerSecurity `json:"securityContext,omitempty"`
	ReadinessProbe  *probe             `json:"readinessProbe,omitempty"`
	Resources       *resources         `json:"resources,omitempty"`
}

type containerSecurity struct {
	AllowPrivilegeEscalation bool          `json:"allowPrivilegeEscalation"`
	ReadOnlyRootFilesystem   bool          `json:"readOnlyRootFilesystem"`
	Capabilities             *capabilities `json:"capabilities,omitempty"`
}

type capabilities struct {
	Drop []string `json:"drop"`
}

type resources struct {
	Requests map[string]string `json:"requests,omitempty"`
	Limits   map[string]string `json:"limits,omitempty"`
}

type volumeMount struct {
	Name      string `json:"name"`
	MountPath string `json:"mountPath"`
	ReadOnly  bool   `json:"readOnly,omitempty"`
}

type volume struct {
	Name                  string     `json:"name"`
	PersistentVolumeClaim *claimName `json:"persistentVolumeClaim,omitempty"`
	EmptyDir              *emptyDir  `json:"emptyDir,omitempty"`
}

type claimName struct {
	ClaimName string `json:"claimName"`
	ReadOnly  bool   `json:"readOnly,omitempty"`
}

type podObject struct {
	Metadata objectMeta `json:"metadata"`
	Spec     podSpec    `json:"spec"`
	Status   podStatus  `json:"status,omitzero"`
}

type podStatus struct {
	Conditions        []objectCondition `json:"conditions,omitempty"`
	PodIP             string            `json:"podIP,omitempty"`
	ContainerStatuses []containerStatus `json:"containerStatuses,omitempty"`
}

type containerStatus struct {
	Name  string         `json:"name"`
	State containerState `json:"state"`
}

type containerState struct {
	Terminated *terminated `json:"terminated,omitempty"`
}

type terminated struct {
	ExitCode int32  `json:"exitCode"`
	Message  string `json:"message,omitempty"`
}

type claimObject struct {
	Metadata objectMeta `json:"metadata"`
}
