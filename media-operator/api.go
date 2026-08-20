package main

// The wire types are hand-written, the way liken and the sibling
// operators write theirs. The Kubernetes API is HTTPS that serves
// JSON, and importing client-go for a dozen structs brings informers,
// work queues, and a release cadence this program does not use. Each
// type carries only the fields this operator reads or writes; the
// API server fills in the rest.

import "encoding/json"

// The group this operator serves, and the two Kubernetes groups it
// writes into: claims live under resource.k8s.io and pods under the
// core group, because the objects a Play becomes are ordinary
// Kubernetes objects any tool can read.
const (
	mediaAPIVersion = "media.liken.sh/v1alpha1"
	claimAPIVersion = "resource.k8s.io/v1"
	podAPIVersion   = "v1"
)

// ObjectMeta carries five fields and they are enough: name and
// namespace for the URL, resourceVersion for the conditional write,
// and uid with ownerReferences for garbage collection.
type ObjectMeta struct {
	Name            string           `json:"name,omitempty"`
	Namespace       string           `json:"namespace,omitempty"`
	UID             string           `json:"uid,omitempty"`
	ResourceVersion string           `json:"resourceVersion,omitempty"`
	OwnerReferences []OwnerReference `json:"ownerReferences,omitempty"`
}

// An ownerReference ties an object's life to its owner's: the
// garbage collector deletes the owned object when the owner goes,
// which is this operator's whole teardown. Controller is true
// because exactly one thing manages each pod and claim; there is no
// blockOwnerDeletion, because nothing here needs the owner to wait.
type OwnerReference struct {
	APIVersion string `json:"apiVersion"`
	Kind       string `json:"kind"`
	Name       string `json:"name"`
	UID        string `json:"uid"`
	Controller bool   `json:"controller"`
}

// A list's own resourceVersion is the revision of the whole
// collection, which is what a watch resumes from.
type ListMeta struct {
	ResourceVersion string `json:"resourceVersion,omitempty"`
}

// A Player is equipment, not a running thing. It holds no claims of
// its own, and this operator only reads it.
type Player struct {
	APIVersion string     `json:"apiVersion,omitempty"`
	Kind       string     `json:"kind,omitempty"`
	Metadata   ObjectMeta `json:"metadata"`
	Spec       PlayerSpec `json:"spec"`
}

// The three device roles. Display and render are single because one
// pod drives one screen through one GPU; sinks is a list because a
// unit plays through however many outputs it has. The CRD requires a
// display or at least one sink.
type PlayerSpec struct {
	Zone    string         `json:"zone,omitempty"`
	Display *PlayerDevice  `json:"display,omitempty"`
	Sinks   []PlayerDevice `json:"sinks,omitempty"`
	Render  *PlayerDevice  `json:"render,omitempty"`
}

// One device selection. The three fields become a DeviceClass name,
// a CEL selector, and an opaque config block on the claim.
type PlayerDevice struct {
	Class      string            `json:"class"`
	Selector   string            `json:"selector,omitempty"`
	Parameters *DeviceParameters `json:"parameters,omitempty"`
}

// Values is raw JSON because the driver defines the parameters and
// this operator carries them onto the claim unread.
type DeviceParameters struct {
	Driver string          `json:"driver"`
	Values json.RawMessage `json:"values,omitempty"`
}

// A Play is one run of media on a Player, with a lifecycle analogous
// to a Job: it runs once to completion and stays for its status
// until deleted.
type Play struct {
	APIVersion string     `json:"apiVersion,omitempty"`
	Kind       string     `json:"kind,omitempty"`
	Metadata   ObjectMeta `json:"metadata"`
	Spec       PlaySpec   `json:"spec"`
	Status     PlayStatus `json:"status"`
}

// Players is a list of one today, because the carriage layer will
// let one Play reach several and a grown list is no migration. The
// URIs play in order.
type PlaySpec struct {
	Players []string `json:"players"`
	URIs    []string `json:"uris"`
}

// The status the operator alone writes: the phase, the paused flag,
// the item counting from 1, the playhead, the pod's name, and the
// message that says why when a word is not enough. Every field is
// omitempty so a column with nothing to say shows blank in kubectl
// rather than a zero.
type PlayStatus struct {
	Phase    string `json:"phase,omitempty"`
	Paused   bool   `json:"paused,omitempty"`
	Item     int    `json:"item,omitempty"`
	Position string `json:"position,omitempty"`
	Duration string `json:"duration,omitempty"`
	Pod      string `json:"pod,omitempty"`
	Message  string `json:"message,omitempty"`
}

// The four phases, in the words Jobs and Pods use so nobody learns a
// new vocabulary. Finished and Failed are terminal: a phase moves
// forward only.
const (
	phasePending  = "Pending"
	phaseRunning  = "Running"
	phaseFinished = "Finished"
	phaseFailed   = "Failed"
)

// A Play in a terminal phase gets no further reconcile. Its pod and
// claim stay for reading until the Play is deleted, and the garbage
// collector tears them down then.
func terminalPhase(phase string) bool {
	return phase == phaseFinished || phase == phaseFailed
}

type PlayList struct {
	Metadata ListMeta `json:"metadata"`
	Items    []Play   `json:"items"`
}

// A ResourceClaim is the request for hardware. The operator writes
// only the spec; the scheduler writes an allocation into the status,
// and nothing here reads it, because the pod's own phase already
// says whether the devices were found.
type ResourceClaim struct {
	APIVersion string            `json:"apiVersion,omitempty"`
	Kind       string            `json:"kind,omitempty"`
	Metadata   ObjectMeta        `json:"metadata"`
	Spec       ResourceClaimSpec `json:"spec"`
}

type ResourceClaimSpec struct {
	Devices DeviceClaim `json:"devices"`
}

// Requests name the devices by role and config carries driver
// parameters for them. Both are claim-level, so one claim covers the
// whole player.
type DeviceClaim struct {
	Requests []DeviceRequest            `json:"requests,omitempty"`
	Config   []DeviceClaimConfiguration `json:"config,omitempty"`
}

// The request name is the role the pod refers to, and exactly is
// DRA's one-of that holds a plain request.
type DeviceRequest struct {
	Name    string              `json:"name"`
	Exactly *ExactDeviceRequest `json:"exactly,omitempty"`
}

// ExactCount with count 1 asks for one device, no more offered and
// no fewer accepted. The selector list is omitted when the class
// alone chooses the device.
type ExactDeviceRequest struct {
	DeviceClassName string             `json:"deviceClassName"`
	AllocationMode  string             `json:"allocationMode,omitempty"`
	Count           int                `json:"count,omitempty"`
	Selectors       []DeviceSelector   `json:"selectors,omitempty"`
	Tolerations     []DeviceToleration `json:"tolerations,omitempty"`
}

// A selector is a CEL expression over device.attributes, the same
// expression a hand-written claim would carry.
type DeviceSelector struct {
	CEL *CELDeviceSelector `json:"cel,omitempty"`
}

type CELDeviceSelector struct {
	Expression string `json:"expression"`
}

// A device taint evicts the pod that holds the device. A toleration
// with tolerationSeconds is how long the play survives an unplugged
// cable.
type DeviceToleration struct {
	Key               string `json:"key,omitempty"`
	Operator          string `json:"operator,omitempty"`
	Effect            string `json:"effect,omitempty"`
	TolerationSeconds *int64 `json:"tolerationSeconds,omitempty"`
}

// An opaque config block reaches the driver unread by the scheduler,
// and requests names which of the claim's requests it applies to.
type DeviceClaimConfiguration struct {
	Requests []string                   `json:"requests,omitempty"`
	Opaque   *OpaqueDeviceConfiguration `json:"opaque,omitempty"`
}

type OpaqueDeviceConfiguration struct {
	Driver     string          `json:"driver"`
	Parameters json.RawMessage `json:"parameters"`
}

// The playback pod. The operator writes its spec once and reads its
// status every pass, because the pod's phase is where the Play's
// phase comes from.
type Pod struct {
	APIVersion string     `json:"apiVersion,omitempty"`
	Kind       string     `json:"kind,omitempty"`
	Metadata   ObjectMeta `json:"metadata"`
	Spec       PodSpec    `json:"spec"`
	Status     PodStatus  `json:"status"`
}

// The pod spec's few fields: restartPolicy Never because the pod's
// end is the play's end, and the short grace period because mpv
// exits promptly on SIGTERM.
type PodSpec struct {
	RestartPolicy                 string             `json:"restartPolicy,omitempty"`
	TerminationGracePeriodSeconds *int64             `json:"terminationGracePeriodSeconds,omitempty"`
	ResourceClaims                []PodResourceClaim `json:"resourceClaims,omitempty"`
	Containers                    []Container        `json:"containers"`
	Volumes                       []Volume           `json:"volumes,omitempty"`
}

// A pod names a claim once, and its containers refer to that name
// request by request, which is what keeps a device out of a
// container that must not hold it.
type PodResourceClaim struct {
	Name              string `json:"name"`
	ResourceClaimName string `json:"resourceClaimName,omitempty"`
}

type Container struct {
	Name         string               `json:"name"`
	Image        string               `json:"image"`
	Args         []string             `json:"args,omitempty"`
	Env          []EnvVar             `json:"env,omitempty"`
	Resources    ResourceRequirements `json:"resources"`
	VolumeMounts []VolumeMount        `json:"volumeMounts,omitempty"`
}

// resources.claims is how a container holds one of the pod's
// claims, and request narrows it to one role inside that claim.
type ResourceRequirements struct {
	Claims []ContainerClaim `json:"claims,omitempty"`
}

type ContainerClaim struct {
	Name    string `json:"name"`
	Request string `json:"request,omitempty"`
}

type EnvVar struct {
	Name  string `json:"name"`
	Value string `json:"value,omitempty"`
}

type VolumeMount struct {
	Name      string `json:"name"`
	MountPath string `json:"mountPath"`
	ReadOnly  bool   `json:"readOnly,omitempty"`
}

// An inline NFS volume needs no PersistentVolume and no CSI driver.
// The kubelet mounts it with the kernel's NFS client, through the
// mount helper liken's image carries.
type Volume struct {
	Name string           `json:"name"`
	NFS  *NFSVolumeSource `json:"nfs,omitempty"`
}

type NFSVolumeSource struct {
	Server   string `json:"server"`
	Path     string `json:"path"`
	ReadOnly bool   `json:"readOnly,omitempty"`
}

// The pod status fields the phase derivation reads. The container's
// terminated state is the specific half of a failure message,
// because it carries the exit code.
type PodStatus struct {
	Phase             string            `json:"phase,omitempty"`
	Reason            string            `json:"reason,omitempty"`
	Message           string            `json:"message,omitempty"`
	ContainerStatuses []ContainerStatus `json:"containerStatuses,omitempty"`
}

type ContainerStatus struct {
	Name  string         `json:"name"`
	State ContainerState `json:"state"`
}

type ContainerState struct {
	Terminated *ContainerStateTerminated `json:"terminated,omitempty"`
}

type ContainerStateTerminated struct {
	ExitCode int    `json:"exitCode"`
	Reason   string `json:"reason,omitempty"`
	Message  string `json:"message,omitempty"`
}

// The pod phases Kubernetes reports, named here so the derivation
// reads as the mapping it is.
const (
	podPending   = "Pending"
	podRunning   = "Running"
	podSucceeded = "Succeeded"
	podFailed    = "Failed"
)
