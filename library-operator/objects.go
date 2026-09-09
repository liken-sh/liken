package main

// The Kubernetes objects a Library touches, in the same
// hand-written form as the Library API in api.go: the claim and the
// volume behind it, which the operator reads, and the pods, which it
// writes. Each type carries only the fields this operator reads or
// writes; the API server fills in the rest.

import (
	"encoding/json"
	"maps"
	"slices"
	"time"
)

// A PersistentVolumeClaim is read for two answers: whether it is
// bound, and which volume it is bound to. The operator also writes the
// claims its own pods and Jobs mount, so the type carries the spec
// fields those claims need and nothing else.
type PersistentVolumeClaim struct {
	APIVersion string                      `json:"apiVersion,omitempty"`
	Kind       string                      `json:"kind,omitempty"`
	Metadata   ObjectMeta                  `json:"metadata"`
	Spec       PersistentVolumeClaimSpec   `json:"spec"`
	Status     PersistentVolumeClaimStatus `json:"status"`
}

// VolumeName is the volume the claim is bound to. The binder writes it on
// an ordinary claim. The operator writes it on a per-node claim, which
// names the volume the operator wrote first, so the claim waits on no
// provisioner.
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

// The core group a PersistentVolumeClaim belongs to, and the two access
// modes a claim takes. ReadWriteOnce is the mode on every class but a
// per-node one, because one agent writes one SQLite database and
// Corrosion agents gossip rather than share a file. ReadWriteMany is the
// mode on a per-node class, because every node that mounts the volume
// holds a copy of its own, and ReadWriteOnce would refuse the second
// node.
const (
	claimAPIVersion         = "v1"
	accessModeReadWriteOnce = "ReadWriteOnce"
	accessModeReadWriteMany = "ReadWriteMany"
)

// A PersistentVolume is read for what serves the storage behind a
// Library's claim, and written for a claim on a per-node class, which
// binds to a volume nothing else provisions.
type PersistentVolume struct {
	APIVersion string                 `json:"apiVersion,omitempty"`
	Kind       string                 `json:"kind,omitempty"`
	Metadata   ObjectMeta             `json:"metadata"`
	Spec       PersistentVolumeSpec   `json:"spec"`
	Status     PersistentVolumeStatus `json:"status,omitzero"`
}

// PersistentVolumeList is the volumes one list answers, which is how the
// sweep reads the volumes this operator wrote.
type PersistentVolumeList struct {
	Metadata ListMeta           `json:"metadata"`
	Items    []PersistentVolume `json:"items"`
}

// PersistentVolumeStatus carries the phase a volume is in. Released is
// the phase of a volume whose claim is gone, which is the volume the
// sweep deletes.
type PersistentVolumeStatus struct {
	Phase string `json:"phase,omitempty"`
}

// Bound is the phase of a volume a claim holds, and Released is the
// phase of a volume whose claim is gone. A Retain volume stays Released
// until something deletes it.
const (
	volumeBound    = "Bound"
	volumeReleased = "Released"
)

// PersistentVolumeSpec is the half of a PersistentVolume that says
// where the storage is. Kubernetes gives each kind of storage its own
// key under the spec, and a volume carries exactly one of them, so
// this type reports the key's name instead of holding a field per
// driver. A cluster that serves its movies through a driver this
// operator carries no type for still reports what serves them.
type PersistentVolumeSpec struct {
	// Source is the name of the storage key, such as nfs or csi, and
	// it is the type the status reports.
	Source string `json:"-"`

	// NFS is the one source this operator reads in full, because a
	// media reference over NFS is built from the server and the export
	// path.
	NFS *NFSVolumeSource `json:"nfs,omitempty"`

	// The fields below are the volume the operator writes for a claim
	// on a per-node class: the CSI source that names the driver and
	// the handle, and the settings that bind the volume to that one
	// claim.
	CSI                           *CSIPersistentVolumeSource `json:"csi,omitempty"`
	StorageClassName              string                     `json:"storageClassName,omitempty"`
	AccessModes                   []string                   `json:"accessModes,omitempty"`
	Capacity                      map[string]string          `json:"capacity,omitempty"`
	PersistentVolumeReclaimPolicy string                     `json:"persistentVolumeReclaimPolicy,omitempty"`
	ClaimRef                      *ClaimReference            `json:"claimRef,omitempty"`
}

// CSIPersistentVolumeSource is the CSI source of a volume: the driver
// that publishes it, and the handle the driver names it by.
type CSIPersistentVolumeSource struct {
	Driver       string `json:"driver"`
	VolumeHandle string `json:"volumeHandle"`
}

// ClaimReference names the claim a volume is reserved for, so the binder
// gives the volume to that claim and to no other. The operator writes the
// namespace and the name. The binder writes the uid when it binds, so a
// reference that carries a uid names a claim that was bound.
type ClaimReference struct {
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
	UID       string `json:"uid,omitempty"`
}

// Retain leaves the volume in place when its claim goes, so the operator
// removes it and the binder never does.
const reclaimRetain = "Retain"

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
//
// The tagged fields are decoded first through a type that carries no
// method of its own, because a type that decodes itself would call this
// function again.
func (s *PersistentVolumeSpec) UnmarshalJSON(data []byte) error {
	type fields PersistentVolumeSpec
	var read fields
	if err := json.Unmarshal(data, &read); err != nil {
		return err
	}
	*s = PersistentVolumeSpec(read)

	var keys map[string]json.RawMessage
	if err := json.Unmarshal(data, &keys); err != nil {
		return err
	}
	for _, name := range slices.Sorted(maps.Keys(keys)) {
		if persistentVolumeSettings[name] {
			continue
		}
		s.Source = name
		return nil
	}
	return nil
}

// A StorageClass is read for one answer: the provisioner behind the
// class a claim names, which is what tells a per-node class from every
// other class.
type StorageClass struct {
	Metadata    ObjectMeta `json:"metadata"`
	Provisioner string     `json:"provisioner,omitempty"`
}

// The marks the objects this operator writes carry. The name
// label is the standard Kubernetes one, and its value tells a Job of
// this operator's from a catalog pod and from a screen pod, so one
// cluster-wide list answers one kind. The library label names the
// Library an object belongs to, and the worker label names which worker
// a Job runs. The member label is on every pod that holds a catalog
// agent, whatever kind of pod it is, and it is what the catalog
// EndpointSlice is written over. The progress member label is the same
// mark for the progress cluster, on a key of its own, because a pod may
// hold an agent of either cluster or of both. The annotation carries
// the hash of the template an object was built from, which is how a
// pass tells a live object from the one it would build now.
const (
	scannerLabelKey          = "app.kubernetes.io/name"
	workerLabelValue         = "library-worker"
	catalogLabelValue        = "library-catalog"
	progressLabelValue       = "library-progress"
	libraryLabelKey          = "library.liken.sh/library"
	workerLabelKey           = "library.liken.sh/worker"
	memberLabelKey           = "library.liken.sh/catalog"
	memberLabelValue         = "member"
	progressMemberLabelKey   = "library.liken.sh/progress"
	progressMemberLabelValue = "member"
	templateHashAnnotation   = "library.liken.sh/template-hash"
)

// The store label names which of the namespace's two durable stores a
// pod or a claim is a copy of. The member labels above are on every pod
// that holds an agent, a Job's pod and a screen pod included, so they
// cannot tell the durable copies from the rest.
const (
	storeLabelKey           = "library.liken.sh/store"
	catalogStoreLabelValue  = "catalog"
	progressStoreLabelValue = "progress"
)

// The label pair that names one Library's objects, on the
// catalog claim the Library owns.
func libraryLabels(library string) map[string]string {
	return map[string]string{libraryLabelKey: library}
}

// The labels one Job and its pods carry: the name label one list
// selects on, the Library the Job works for, and the worker it runs.
func workerLabels(library, worker string) map[string]string {
	return map[string]string{
		scannerLabelKey: workerLabelValue,
		libraryLabelKey: library,
		workerLabelKey:  worker,
	}
}

// The member label on top of the labels a pod already carries,
// so that every pod holding a catalog agent reaches the namespace's
// EndpointSlice through one selector.
func withMemberLabel(labels map[string]string) map[string]string {
	marked := maps.Clone(labels)
	marked[memberLabelKey] = memberLabelValue
	return marked
}

// The same mark for the progress cluster, so the progress
// EndpointSlice is written over the pods that hold a progress agent and
// over no others.
func withProgressMemberLabel(labels map[string]string) map[string]string {
	marked := maps.Clone(labels)
	marked[progressMemberLabelKey] = progressMemberLabelValue
	return marked
}

// A pod. The operator writes a spec once and reads a status
// every pass, because a Library is Ready only when its namespace's
// catalog pod runs.
type Pod struct {
	APIVersion string     `json:"apiVersion,omitempty"`
	Kind       string     `json:"kind,omitempty"`
	Metadata   ObjectMeta `json:"metadata"`
	Spec       PodSpec    `json:"spec"`
	Status     PodStatus  `json:"status"`
}

// PodList is the collection ListCatalogMemberPods returns. Its
// resourceVersion is where the pod watch begins.
type PodList struct {
	Metadata ListMeta `json:"metadata"`
	Items    []Pod    `json:"items"`
}

// The pod spec's few fields: a restartPolicy, Always on a
// standing pod and Never on a Job's pod, which runs to completion. The
// termination grace period is long enough for a busy catalog agent to
// finish its exit.
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
	// InitContainers holds the native sidecar. A container here
	// with restartPolicy Always starts before the containers below and
	// keeps running beside them, so the kubelet brings the catalog agent
	// up and passes its startupProbe before it starts the container it
	// serves.
	InitContainers []Container `json:"initContainers,omitempty"`
	Containers     []Container `json:"containers"`
	Volumes        []Volume    `json:"volumes,omitempty"`
	// The claims the pod holds, under the names its containers ask for
	// them by. A screen pod names the display claim media-operator stood for
	// its Player, and no other pod this operator builds holds one.
	ResourceClaims []PodResourceClaim `json:"resourceClaims,omitempty"`
	// The scheduling rules the pod carries. The durable copies of a store
	// are the only pods this operator gives any.
	Affinity *Affinity `json:"affinity,omitempty"`
	// The taints the pod accepts. A screen pod is the only pod this
	// operator gives any, because its display claim already pins it to
	// one machine. Every other pod this operator builds stays off a
	// tainted machine, which is why the cluster owner set the taint.
	Tolerations []Toleration `json:"tolerations,omitempty"`
}

// One taint the pod accepts. The fields are core/v1's, so the JSON this
// operator sends is the JSON the API server reads. Operator Exists
// matches a taint under Key whatever its value, and Value is then
// empty. Effect names one effect, and an empty Effect would accept
// every effect of that key, which this operator never asks for.
type Toleration struct {
	Key      string `json:"key"`
	Operator string `json:"operator,omitempty"`
	Value    string `json:"value,omitempty"`
	Effect   string `json:"effect,omitempty"`
}

// The one kind of affinity this operator states: the rule that keeps two
// copies of one store off one node.
type Affinity struct {
	PodAntiAffinity *PodAntiAffinity `json:"podAntiAffinity,omitempty"`
}

// A required term is a placement the scheduler refuses to break, so a
// copy with nowhere to go stays Pending and the status says so.
type PodAntiAffinity struct {
	RequiredDuringSchedulingIgnoredDuringExecution []PodAffinityTerm `json:"requiredDuringSchedulingIgnoredDuringExecution,omitempty"`
}

// One term names the pods it reads and the topology it spreads them
// over. The selector reads the pod's own namespace.
type PodAffinityTerm struct {
	LabelSelector *LabelSelector `json:"labelSelector,omitempty"`
	TopologyKey   string         `json:"topologyKey"`
}

// The equality half of a Kubernetes label selector, the only half this
// operator states.
type LabelSelector struct {
	MatchLabels map[string]string `json:"matchLabels,omitempty"`
}

// One claim the pod holds. Name is the pod-local name a container's
// resources.claims entry refers to, and ResourceClaimName is the
// ResourceClaim in the pod's namespace it stands for.
type PodResourceClaim struct {
	Name              string `json:"name"`
	ResourceClaimName string `json:"resourceClaimName,omitempty"`
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
	// the start of the container beside it, and the livenessProbe covers
	// the agent's running life. Neither Corrosion agent carries a
	// readinessProbe: a pod's readiness gates its place in the catalog
	// gossip EndpointSlice, and a momentary API hiccup must not drop the
	// agent from the bootstrap list.
	StartupProbe  *Probe `json:"startupProbe,omitempty"`
	LivenessProbe *Probe `json:"livenessProbe,omitempty"`
	// The readinessProbe gates a container's place in the endpoints of a
	// Service that states a selector. The jellyfin role stands behind
	// such a Service, so its readiness decides whether Jellyfin's webhook
	// post reaches it.
	ReadinessProbe *Probe `json:"readinessProbe,omitempty"`
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
//
// A role that listens on the pod network is probed by an httpGet instead,
// which costs no process inside the container.
type Probe struct {
	Exec *ExecAction `json:"exec,omitempty"`
	// A container that binds a port on the pod network is probed by an
	// httpGet from the kubelet, because the kubelet reaches that port and
	// no command has to run inside the container.
	HTTPGet             *HTTPGetAction `json:"httpGet,omitempty"`
	InitialDelaySeconds int            `json:"initialDelaySeconds,omitempty"`
	PeriodSeconds       int            `json:"periodSeconds,omitempty"`
	TimeoutSeconds      int            `json:"timeoutSeconds,omitempty"`
	FailureThreshold    int            `json:"failureThreshold,omitempty"`
}

// An HTTPGetAction is one GET the kubelet makes against the container's own
// address. A status under 400 is the check passing.
type HTTPGetAction struct {
	Path string `json:"path,omitempty"`
	Port int32  `json:"port"`
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
	// The requests of the pod's claims this container takes. A claim
	// can hold several devices, and a container receives only the requests it
	// names here.
	Claims []ResourceClaim `json:"claims,omitempty"`
}

// One request of one claim the container takes. Name is the pod-local
// claim name from the pod spec, and Request is the name of a request inside
// that claim.
type ResourceClaim struct {
	Name    string `json:"name"`
	Request string `json:"request,omitempty"`
}

// Every container this operator builds reads a volume or writes
// to a socket on its own pod, so none needs a capability at all and
// none may gain one.
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

// An EnvVarSource reads the value from somewhere other than the pod spec. A
// fieldRef is the downward API: the kubelet reads the field off the pod it is
// starting and sets the variable from it. A secretKeyRef is one key of one
// Secret in the pod's namespace, which is how a provider key reaches an
// enricher container without passing through the operator.
type EnvVarSource struct {
	FieldRef     *ObjectFieldSelector `json:"fieldRef,omitempty"`
	SecretKeyRef *SecretKeySelector   `json:"secretKeyRef,omitempty"`
}

// One key of one Secret in the pod's own namespace.
type SecretKeySelector struct {
	Name string `json:"name"`
	Key  string `json:"key"`
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
	// SubPath mounts one directory of the volume, so two agents keep
	// their files on one claim without seeing each other's.
	SubPath string `json:"subPath,omitempty"`
}

// A scan pod carries two volumes: the library's claim, mounted
// read-only, and the catalog agent's durable claim. A cleanup pod
// carries the second alone, and the catalog pod carries the
// namespace's own.
//
// A screen pod carries one claim per Library of its namespace, all
// read-only, and its catalog agent's own claim beside them. That agent holds
// a copy of the namespace's catalog, and the claim is what makes a restart a
// delta sync. A screen in a namespace with no single Catalog carries an
// unbounded emptyDir there. Its art cache uses a separate bounded emptyDir.
type Volume struct {
	Name                  string                             `json:"name"`
	PersistentVolumeClaim *PersistentVolumeClaimVolumeSource `json:"persistentVolumeClaim,omitempty"`
	EmptyDir              *EmptyDirVolumeSource              `json:"emptyDir,omitempty"`
	ConfigMap             *ConfigMapVolumeSource             `json:"configMap,omitempty"`
}

// A ConfigMap projected as files, one per key. Optional lets the pod
// start before the map exists, with an empty directory in its place.
type ConfigMapVolumeSource struct {
	Name     string `json:"name"`
	Optional *bool  `json:"optional,omitempty"`
}

// A ConfigMap. The operator writes one per screen namespace, the
// people file the browser reads, and reads it back only to compare.
type ConfigMap struct {
	APIVersion string            `json:"apiVersion,omitempty"`
	Kind       string            `json:"kind,omitempty"`
	Metadata   ObjectMeta        `json:"metadata"`
	Data       map[string]string `json:"data,omitempty"`
}

// An emptyDir the kubelet creates with the pod and removes with it. A
// `SizeLimit` bounds the volume where the pod states one. The node's ephemeral
// storage limits apply as well.
type EmptyDirVolumeSource struct {
	SizeLimit string `json:"sizeLimit,omitempty"`
}

// ReadOnly here is the mount the kubelet makes, so a scanner cannot
// write to the media volume even if its own mount said otherwise.
type PersistentVolumeClaimVolumeSource struct {
	ClaimName string `json:"claimName"`
	ReadOnly  bool   `json:"readOnly,omitempty"`
}

// The pod status this operator reads: the phase, the per
// container readiness, the words the kubelet gives for a failure, and
// the address the catalog agents gossip on.
type PodStatus struct {
	Phase   string `json:"phase,omitempty"`
	Reason  string `json:"reason,omitempty"`
	Message string `json:"message,omitempty"`
	// PodIP is the address the kubelet assigned. It is the address the
	// catalog agents gossip on, and a pod without one is not a peer yet.
	PodIP string `json:"podIP,omitempty"`
	// The catalog agent is a native sidecar, so the kubelet reports it
	// here and not beside the container it serves. A readiness gate that
	// read only the list below would never see the agent at all.
	InitContainerStatuses []ContainerStatus `json:"initContainerStatuses,omitempty"`
	ContainerStatuses     []ContainerStatus `json:"containerStatuses,omitempty"`
	Conditions            []PodCondition    `json:"conditions,omitempty"`
}

// PodCondition is the fields this operator reads of a pod
// condition. Status is a string rather than a bool, because a condition
// has three states: True, False, and Unknown.
type PodCondition struct {
	Type    string `json:"type"`
	Status  string `json:"status"`
	Reason  string `json:"reason,omitempty"`
	Message string `json:"message,omitempty"`
	// When the API server last wrote this verdict. The
	// unschedulable grace is measured from it, so no pass keeps a timer.
	LastTransitionTime time.Time `json:"lastTransitionTime,omitzero"`
}

// The one pod condition this operator reads, and the verdict that
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

// The pod phases Kubernetes reports, named here so the
// derivation reads as the mapping it is. A standing pod restarts in
// place, so it reaches Failed only when the kubelet gives up on it; a
// Job's pod runs to completion, so it reaches Succeeded or Failed on
// every run.
const (
	podPending   = "Pending"
	podRunning   = "Running"
	podSucceeded = "Succeeded"
	podFailed    = "Failed"
)
