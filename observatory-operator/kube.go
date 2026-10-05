package main

// The Kubernetes objects that the operator creates and reads: the pods
// and Services of the topology, the ResourceClaim of a device on real
// hardware, and the Events of a Reservation. Each type holds only the
// fields the operator writes or reads, in the wire format of the
// Kubernetes API, the way the other operators of the repository write
// theirs. A generated client would bring k8s.io/api for a few structs.

import "time"

// meta is the metadata of an object the operator creates. It holds the
// owner references, which the observatory package's ObjectMeta leaves
// out because a person's resources carry none.
type meta struct {
	Name              string            `json:"name"`
	Namespace         string            `json:"namespace,omitempty"`
	UID               string            `json:"uid,omitempty"`
	ResourceVersion   string            `json:"resourceVersion,omitempty"`
	Labels            map[string]string `json:"labels,omitempty"`
	Annotations       map[string]string `json:"annotations,omitempty"`
	OwnerReferences   []ownerReference  `json:"ownerReferences,omitempty"`
	DeletionTimestamp *time.Time        `json:"deletionTimestamp,omitempty"`
}

// ownerReference names the resource that caused an object. Kubernetes'
// garbage collector deletes the object when its owner is deleted.
type ownerReference struct {
	APIVersion string `json:"apiVersion"`
	Kind       string `json:"kind"`
	Name       string `json:"name"`
	UID        string `json:"uid"`
	Controller bool   `json:"controller,omitempty"`
}

type pod struct {
	APIVersion string    `json:"apiVersion,omitempty"`
	Kind       string    `json:"kind,omitempty"`
	Metadata   meta      `json:"metadata"`
	Spec       podSpec   `json:"spec"`
	Status     podStatus `json:"status,omitzero"`
}

type podSpec struct {
	NodeName                      string      `json:"nodeName,omitempty"`
	RestartPolicy                 string      `json:"restartPolicy,omitempty"`
	TerminationGracePeriodSeconds *int64      `json:"terminationGracePeriodSeconds,omitempty"`
	EnableServiceLinks            *bool       `json:"enableServiceLinks,omitempty"`
	AutomountServiceAccountToken  *bool       `json:"automountServiceAccountToken,omitempty"`
	InitContainers                []container `json:"initContainers,omitempty"`
	Containers                    []container `json:"containers"`
	Volumes                       []volume    `json:"volumes,omitempty"`
	ResourceClaims                []podClaim  `json:"resourceClaims,omitempty"`
}

type container struct {
	Name            string           `json:"name"`
	Image           string           `json:"image"`
	Command         []string         `json:"command,omitempty"`
	Args            []string         `json:"args,omitempty"`
	Ports           []containerPort  `json:"ports,omitempty"`
	SecurityContext *securityContext `json:"securityContext,omitempty"`
	VolumeMounts    []volumeMount    `json:"volumeMounts,omitempty"`
	ReadinessProbe  *probe           `json:"readinessProbe,omitempty"`
	Resources       *resources       `json:"resources,omitempty"`
}

type containerPort struct {
	Name          string `json:"name"`
	ContainerPort int32  `json:"containerPort"`
}

type securityContext struct {
	RunAsNonRoot             bool          `json:"runAsNonRoot"`
	RunAsUser                int64         `json:"runAsUser"`
	RunAsGroup               int64         `json:"runAsGroup"`
	AllowPrivilegeEscalation bool          `json:"allowPrivilegeEscalation"`
	ReadOnlyRootFilesystem   bool          `json:"readOnlyRootFilesystem"`
	Capabilities             *capabilities `json:"capabilities,omitempty"`
}

type capabilities struct {
	Drop []string `json:"drop"`
}

type volumeMount struct {
	Name      string `json:"name"`
	MountPath string `json:"mountPath"`
}

type volume struct {
	Name     string    `json:"name"`
	EmptyDir *emptyDir `json:"emptyDir,omitempty"`
}

type emptyDir struct{}

type probe struct {
	TCPSocket     *tcpSocket `json:"tcpSocket,omitempty"`
	PeriodSeconds int32      `json:"periodSeconds,omitempty"`
}

type tcpSocket struct {
	Port string `json:"port"`
}

type resources struct {
	Claims []resourceName `json:"claims,omitempty"`
}

type resourceName struct {
	Name string `json:"name"`
}

// podClaim names a ResourceClaim that the pod's containers use.
type podClaim struct {
	Name              string `json:"name"`
	ResourceClaimName string `json:"resourceClaimName"`
}

type podStatus struct {
	Phase      string         `json:"phase,omitempty"`
	PodIP      string         `json:"podIP,omitempty"`
	Conditions []podCondition `json:"conditions,omitempty"`
}

type podCondition struct {
	Type   string `json:"type"`
	Status string `json:"status"`
}

// ready reports whether the kubelet reports the pod Ready: every
// container runs and passes its readiness probe.
func (p *pod) ready() bool {
	if p.Metadata.DeletionTimestamp != nil {
		return false
	}
	for _, c := range p.Status.Conditions {
		if c.Type == "Ready" {
			return c.Status == "True"
		}
	}
	return false
}

type service struct {
	APIVersion string      `json:"apiVersion,omitempty"`
	Kind       string      `json:"kind,omitempty"`
	Metadata   meta        `json:"metadata"`
	Spec       serviceSpec `json:"spec"`
}

type serviceSpec struct {
	Selector map[string]string `json:"selector"`
	Ports    []servicePort     `json:"ports"`
}

type servicePort struct {
	Name string `json:"name"`
	Port int32  `json:"port"`
	// TargetPort names the container's port. The operator always names
	// it, so the text form is enough.
	TargetPort string `json:"targetPort"`
}

// resourceClaim is a ResourceClaim of resource.k8s.io/v1. Its spec is
// the device's spec.claim, as the person wrote it, and the API server
// validates it.
type resourceClaim struct {
	APIVersion string `json:"apiVersion"`
	Kind       string `json:"kind"`
	Metadata   meta   `json:"metadata"`
	Spec       any    `json:"spec"`
}
