package main

// The Kubernetes objects a Library touches, in the same hand-written
// form as the Library API in api.go: the claim and the volume behind
// it, which the operator reads, and the scanner pod, which it writes.
// Each type carries only the fields this operator reads or writes;
// the API server fills in the rest.

import (
	"encoding/json"
	"maps"
	"slices"
)

// A PersistentVolumeClaim is read for two answers: whether it is
// bound, and which volume it is bound to. The operator never writes
// one, so the type carries nothing else.
type PersistentVolumeClaim struct {
	APIVersion string                      `json:"apiVersion,omitempty"`
	Kind       string                      `json:"kind,omitempty"`
	Metadata   ObjectMeta                  `json:"metadata"`
	Spec       PersistentVolumeClaimSpec   `json:"spec"`
	Status     PersistentVolumeClaimStatus `json:"status"`
}

// VolumeName is written by the binder, not by whoever created the
// claim, so it is empty until the claim binds.
//
// PersistentVolumeClaimSpec is the write half of the claim. The operator
// reads a media claim through VolumeName and Phase, and it writes a catalog
// claim through AccessModes, Resources, and StorageClassName. An empty
// StorageClassName is omitted, so the cluster's default StorageClass binds
// the claim.
type PersistentVolumeClaimSpec struct {
	AccessModes      []string                   `json:"accessModes,omitempty"`
	Resources        VolumeResourceRequirements `json:"resources,omitzero"`
	StorageClassName string                     `json:"storageClassName,omitempty"`
	VolumeName       string                     `json:"volumeName,omitempty"`
}

// VolumeResourceRequirements is the size a claim asks for. Only storage is
// stated, as a quantity carried as written rather than parsed.
type VolumeResourceRequirements struct {
	Requests map[string]string `json:"requests,omitempty"`
}

type PersistentVolumeClaimStatus struct {
	Phase string `json:"phase,omitempty"`
}

// A claim is usable only in the Bound phase. Pending means no volume
// answered it yet, and Lost means the volume behind it is gone.
const claimBound = "Bound"

// The core group a PersistentVolumeClaim belongs to, and the access
// mode the catalog claim takes: ReadWriteOnce, because one agent writes
// one SQLite database and Corrosion agents gossip rather than share a
// file.
const (
	claimAPIVersion         = "v1"
	accessModeReadWriteOnce = "ReadWriteOnce"
)

// A PersistentVolume is read for one answer: what serves the storage.
// The operator never writes one.
type PersistentVolume struct {
	APIVersion string               `json:"apiVersion,omitempty"`
	Kind       string               `json:"kind,omitempty"`
	Metadata   ObjectMeta           `json:"metadata"`
	Spec       PersistentVolumeSpec `json:"spec"`
}

// PersistentVolumeSpec is the half of a PersistentVolume that says
// where the storage is. Kubernetes gives each kind of storage its own
// key under the spec, and a volume carries exactly one of them, so
// this type reports the key's name instead of holding a field per
// driver. A cluster that serves its movies through a driver this
// operator carries no type for still reports what serves them.
type PersistentVolumeSpec struct {
	// Source is the name of the storage key, such as nfs or csi, and
	// it is the type the status reports.
	Source string

	// NFS is the one source this operator reads in full, because a
	// media reference over NFS is built from the server and the export
	// path.
	NFS *NFSVolumeSource
}

type NFSVolumeSource struct {
	Server string `json:"server,omitempty"`
	Path   string `json:"path,omitempty"`
}

// The spec keys that describe a volume rather than serve it. Every
// other key is a storage source. Naming the few settings, rather than
// the many drivers, is what lets an unknown driver report its own
// name.
var persistentVolumeSettings = map[string]bool{
	"accessModes":                   true,
	"capacity":                      true,
	"claimRef":                      true,
	"mountOptions":                  true,
	"nodeAffinity":                  true,
	"persistentVolumeReclaimPolicy": true,
	"storageClassName":              true,
	"volumeAttributesClassName":     true,
	"volumeMode":                    true,
}

// UnmarshalJSON reads the spec as its raw keys and names the first one
// that is not a setting. The keys are sorted first, so a spec that
// somehow carries two sources decodes the same way every time.
func (s *PersistentVolumeSpec) UnmarshalJSON(data []byte) error {
	var keys map[string]json.RawMessage
	if err := json.Unmarshal(data, &keys); err != nil {
		return err
	}
	for _, name := range slices.Sorted(maps.Keys(keys)) {
		if persistentVolumeSettings[name] {
			continue
		}
		s.Source = name
		if name != "nfs" {
			return nil
		}
		s.NFS = &NFSVolumeSource{}
		return json.Unmarshal(keys[name], s.NFS)
	}
	return nil
}

// The marks every scanner pod carries. The first pair is the standard
// Kubernetes name label, and it is what one cluster-wide watch selects
// on, so the operator sees its own pods and no others. The second
// names the Library the pod scans, so a person can list the pods of
// one library. The annotation carries the hash of the template the pod
// was built from, which is how a pass tells a live pod from the pod it
// would build now.
const (
	scannerLabelKey        = "app.kubernetes.io/name"
	scannerLabelValue      = "library-scanner"
	libraryLabelKey        = "library.liken.sh/library"
	templateHashAnnotation = "library.liken.sh/template-hash"
)

// The scanner pod. The operator writes its spec once and reads its
// status every pass, because a Library is Ready only when its scanner
// runs.
type Pod struct {
	APIVersion string     `json:"apiVersion,omitempty"`
	Kind       string     `json:"kind,omitempty"`
	Metadata   ObjectMeta `json:"metadata"`
	Spec       PodSpec    `json:"spec"`
	Status     PodStatus  `json:"status"`
}

// PodList is the collection ListScannerPods returns. Its
// resourceVersion is where the pod watch begins.
type PodList struct {
	Metadata ListMeta `json:"metadata"`
	Items    []Pod    `json:"items"`
}

// The pod spec's few fields: a restartPolicy, Always on the scanner
// pod because a scanner is a standing service, and Never on the
// cleanup pod because a restart there would hide a failure the
// operator reports as Blocked. The termination grace period is long
// enough for a busy catalog agent to finish its exit.
type PodSpec struct {
	RestartPolicy string `json:"restartPolicy,omitempty"`
	// NodeName is written by the scheduler, never by the builder. The
	// catalog EndpointSlice carries it, so a reader can tell which peers
	// are local to a node.
	NodeName                      string `json:"nodeName,omitempty"`
	TerminationGracePeriodSeconds *int64 `json:"terminationGracePeriodSeconds,omitempty"`
	// AutomountServiceAccountToken is a pointer because the field's
	// default is true, and only an explicit false keeps the namespace's
	// default ServiceAccount token out of the pod.
	AutomountServiceAccountToken *bool `json:"automountServiceAccountToken,omitempty"`
	// initContainers holds the native sidecar. A container here
	// with restartPolicy Always starts before the containers below and
	// keeps running beside them, so the kubelet brings the catalog agent
	// up and passes its startupProbe before it starts the scanner.
	InitContainers []Container `json:"initContainers,omitempty"`
	Containers     []Container `json:"containers"`
	Volumes        []Volume    `json:"volumes,omitempty"`
}

// Command replaces the image's entrypoint, which is how one image runs
// the operator and the scanner.
type Container struct {
	Name            string               `json:"name"`
	Image           string               `json:"image"`
	Command         []string             `json:"command,omitempty"`
	Args            []string             `json:"args,omitempty"`
	Env             []EnvVar             `json:"env,omitempty"`
	Ports           []ContainerPort      `json:"ports,omitempty"`
	Resources       ResourceRequirements `json:"resources,omitzero"`
	VolumeMounts    []VolumeMount        `json:"volumeMounts,omitempty"`
	SecurityContext *SecurityContext     `json:"securityContext,omitempty"`
	// RestartPolicy is set to Always on an initContainer to make
	// it a native sidecar: the kubelet keeps it running for the life of
	// the pod rather than waiting for it to exit before the next
	// container starts.
	RestartPolicy string `json:"restartPolicy,omitempty"`
	// The two probes on the catalog agent. The startupProbe gates
	// the scanner's start, and the livenessProbe covers the agent's
	// running life. There is no readinessProbe: the scanner pod's
	// readiness gates its place in the catalog gossip EndpointSlice, and a
	// momentary API hiccup must not drop the agent from the bootstrap
	// list.
	StartupProbe  *Probe `json:"startupProbe,omitempty"`
	LivenessProbe *Probe `json:"livenessProbe,omitempty"`
}

// A ContainerPort is one port a container listens on. Declaring it changes
// nothing at run time. It is how a person who reads the pod finds the port
// without reading this operator's source.
type ContainerPort struct {
	Name          string `json:"name"`
	ContainerPort int32  `json:"containerPort"`
	Protocol      string `json:"protocol,omitempty"`
}

// A Probe is the check the kubelet runs on a container. This
// operator probes the catalog agent by running a command inside the
// container, because the agent's API binds loopback alone, so nothing
// the kubelet dials over the pod network reaches it.
type Probe struct {
	Exec                *ExecAction `json:"exec,omitempty"`
	InitialDelaySeconds int         `json:"initialDelaySeconds,omitempty"`
	PeriodSeconds       int         `json:"periodSeconds,omitempty"`
	TimeoutSeconds      int         `json:"timeoutSeconds,omitempty"`
	FailureThreshold    int         `json:"failureThreshold,omitempty"`
}

// An ExecAction runs a command inside the container. An exit of
// zero is the check passing.
type ExecAction struct {
	Command []string `json:"command,omitempty"`
}

// ResourceRequirements is the room a container asks for and the
// ceiling the kubelet holds it to. Kubernetes measures both in
// quantities, which are strings with a suffix: 10m is a thousandth of
// a core and 64Mi is a mebibyte count. The values are carried as
// written rather than parsed, because this operator only states them.
type ResourceRequirements struct {
	Requests map[string]string `json:"requests,omitempty"`
	Limits   map[string]string `json:"limits,omitempty"`
}

// A scanner reads a volume and writes to a socket on its own pod, so
// it needs no capability at all and can never gain one.
type SecurityContext struct {
	Capabilities             *Capabilities `json:"capabilities,omitempty"`
	AllowPrivilegeEscalation *bool         `json:"allowPrivilegeEscalation,omitempty"`
}

type Capabilities struct {
	Drop []string `json:"drop,omitempty"`
}

// An env var carries a literal value, or a reference to a field of the
// pod itself. The catalog agent needs the address it gossips on, and
// the kubelet assigns that address when the pod starts, so that one
// value comes through valueFrom.
type EnvVar struct {
	Name      string        `json:"name"`
	Value     string        `json:"value,omitempty"`
	ValueFrom *EnvVarSource `json:"valueFrom,omitempty"`
}

// An EnvVarSource reads the value from somewhere other than the pod
// spec. A fieldRef is the downward API: the kubelet reads the field
// off the pod it is starting and sets the variable from it.
type EnvVarSource struct {
	FieldRef *ObjectFieldSelector `json:"fieldRef,omitempty"`
}

// The field to read, as a path into the pod, such as status.podIP.
// That one is the address the catalog agent gossips on, which nothing
// knows until the pod has an address.
type ObjectFieldSelector struct {
	FieldPath string `json:"fieldPath"`
}

type VolumeMount struct {
	Name      string `json:"name"`
	MountPath string `json:"mountPath"`
	ReadOnly  bool   `json:"readOnly,omitempty"`
}

// A scanner pod carries two volumes: the library's claim, mounted
// read-only, and the catalog agent's durable claim.
type Volume struct {
	Name                  string                             `json:"name"`
	PersistentVolumeClaim *PersistentVolumeClaimVolumeSource `json:"persistentVolumeClaim,omitempty"`
}

// ReadOnly here is the mount the kubelet makes, so a scanner cannot
// write to the media volume even if its own mount said otherwise.
type PersistentVolumeClaimVolumeSource struct {
	ClaimName string `json:"claimName"`
	ReadOnly  bool   `json:"readOnly,omitempty"`
}

// The pod status this operator reads: the phase, the per container
// readiness, the words the kubelet gives for a failure, and the
// address the catalog agents gossip on.
type PodStatus struct {
	Phase   string `json:"phase,omitempty"`
	Reason  string `json:"reason,omitempty"`
	Message string `json:"message,omitempty"`
	// PodIP is the address the kubelet assigned. It is the address the
	// catalog agents gossip on, and a pod without one is not a peer yet.
	PodIP string `json:"podIP,omitempty"`
	// The catalog agent is a native sidecar, so the kubelet reports it
	// here and not beside the scanner. A readiness gate that read only
	// the list below would never see the agent at all.
	InitContainerStatuses []ContainerStatus `json:"initContainerStatuses,omitempty"`
	ContainerStatuses     []ContainerStatus `json:"containerStatuses,omitempty"`
	// Read for one answer: why a cleanup pod does not schedule. The
	// scheduler writes that sentence on the PodScheduled condition,
	// and the pod's own status.message stays empty.
	Conditions []PodCondition `json:"conditions,omitempty"`
}

// PodCondition is the fields this operator reads of a pod condition.
// Status is a string rather than a bool, because a condition has
// three states: True, False, and Unknown.
type PodCondition struct {
	Type    string `json:"type"`
	Status  string `json:"status"`
	Reason  string `json:"reason,omitempty"`
	Message string `json:"message,omitempty"`
}

// The one pod condition this operator reads, and the verdict that
// says the scheduler has not placed the pod.
const (
	podScheduled     = "PodScheduled"
	conditionIsFalse = "False"
)

// Ready is the kubelet's own verdict on the container, which is what
// the Library's Ready condition folds. A Running pod whose catalog
// agent has not opened its API yet is not ready.
type ContainerStatus struct {
	Name  string `json:"name"`
	Ready bool   `json:"ready"`
}

// The pod phases Kubernetes reports, named here so the derivation
// reads as the mapping it is. A scanner pod restarts in place, so it
// reaches Failed only when the kubelet gives up on it.
const (
	podPending = "Pending"
	podRunning = "Running"
	podFailed  = "Failed"
)
