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
type PersistentVolumeClaimSpec struct {
	VolumeName string `json:"volumeName,omitempty"`
}

type PersistentVolumeClaimStatus struct {
	Phase string `json:"phase,omitempty"`
}

// A claim is usable only in the Bound phase. Pending means no volume
// answered it yet, and Lost means the volume behind it is gone.
const claimBound = "Bound"

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
// driver. A cluster that serves its films through a driver this
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

// The pod spec's few fields: restartPolicy Always because a scanner is
// a standing service and not a run to completion, and a termination
// grace period long enough for a busy catalog agent to finish its
// exit.
type PodSpec struct {
	RestartPolicy                 string `json:"restartPolicy,omitempty"`
	TerminationGracePeriodSeconds *int64 `json:"terminationGracePeriodSeconds,omitempty"`
	// AutomountServiceAccountToken is a pointer because the field's
	// default is true, and only an explicit false keeps the namespace's
	// default ServiceAccount token out of the pod.
	AutomountServiceAccountToken *bool       `json:"automountServiceAccountToken,omitempty"`
	Containers                   []Container `json:"containers"`
	Volumes                      []Volume    `json:"volumes,omitempty"`
}

// Command replaces the image's entrypoint, which is how one image runs
// the operator and the scanner.
type Container struct {
	Name            string               `json:"name"`
	Image           string               `json:"image"`
	Command         []string             `json:"command,omitempty"`
	Args            []string             `json:"args,omitempty"`
	Env             []EnvVar             `json:"env,omitempty"`
	Resources       ResourceRequirements `json:"resources,omitzero"`
	VolumeMounts    []VolumeMount        `json:"volumeMounts,omitempty"`
	SecurityContext *SecurityContext     `json:"securityContext,omitempty"`
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
// read-only, and a directory for the catalog agent's database.
type Volume struct {
	Name                  string                             `json:"name"`
	PersistentVolumeClaim *PersistentVolumeClaimVolumeSource `json:"persistentVolumeClaim,omitempty"`
	EmptyDir              *EmptyDirVolumeSource              `json:"emptyDir,omitempty"`
}

// ReadOnly here is the mount the kubelet makes, so a scanner cannot
// write to the media volume even if its own mount said otherwise.
type PersistentVolumeClaimVolumeSource struct {
	ClaimName string `json:"claimName"`
	ReadOnly  bool   `json:"readOnly,omitempty"`
}

// An emptyDir is a directory the kubelet creates with the pod and
// deletes with it. The catalog is derived, so a pod that restarts and
// syncs again loses nothing.
//
// SizeLimit caps the volume, so a catalog that grows beyond what the
// node can hold fails the pod instead of the node.
type EmptyDirVolumeSource struct {
	SizeLimit string `json:"sizeLimit,omitempty"`
}

// The pod status the Ready condition reads: the phase, the per
// container readiness, and the words the kubelet gives for a failure.
type PodStatus struct {
	Phase             string            `json:"phase,omitempty"`
	Reason            string            `json:"reason,omitempty"`
	Message           string            `json:"message,omitempty"`
	ContainerStatuses []ContainerStatus `json:"containerStatuses,omitempty"`
}

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
