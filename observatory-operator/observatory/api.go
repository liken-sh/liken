// Package observatory holds the resources of the API group
// observatory.liken.sh, version v1alpha1, as Go types.
// observatory-operator reads and writes them, and
// astrophotography-operator imports them to read a telescope's state
// and to create a Reservation.
//
// The types are written by hand, the way liken and the other operators
// write theirs. The operators speak to the API server with net/http
// through the shared kubernetes module, so a generated client and
// k8s.io/apimachinery would add a large dependency tree for a few
// structs. ObjectMeta and Condition copy only the fields that an
// operator reads or writes, and they keep the wire format of
// metav1.ObjectMeta and metav1.Condition.
//
// The CRDs in deploy/ are the schema that the API server enforces. A
// test compares each type here with its CRD, field by field, so the
// two cannot drift apart.
package observatory

import "time"

const (
	// Group is the API group of every kind in this package.
	Group = "observatory.liken.sh"
	// Version is the one version that the CRDs serve.
	Version = "v1alpha1"
	// APIVersion is the value of apiVersion in every object.
	APIVersion = Group + "/" + Version
)

// ObjectMeta is the part of an object's metadata that the operators
// use. Every kind is namespaced, so the namespace is part of each
// object's key. Labels and annotations are here so that an object the
// operator reads and writes back keeps them.
type ObjectMeta struct {
	Name              string            `json:"name"`
	Namespace         string            `json:"namespace,omitempty"`
	UID               string            `json:"uid,omitempty"`
	ResourceVersion   string            `json:"resourceVersion,omitempty"`
	Generation        int64             `json:"generation,omitempty"`
	CreationTimestamp *time.Time        `json:"creationTimestamp,omitempty"`
	DeletionTimestamp *time.Time        `json:"deletionTimestamp,omitempty"`
	Finalizers        []string          `json:"finalizers,omitempty"`
	Labels            map[string]string `json:"labels,omitempty"`
	Annotations       map[string]string `json:"annotations,omitempty"`
}

// Meta is the part of the metadata that a watch's store reads. It is
// the same interface literal as the shared kubernetes module's
// memo.Meta, so the types satisfy that interface with no import of the
// module.
type Meta = interface {
	GetNamespace() string
	GetName() string
	GetResourceVersion() string
}

func (m *ObjectMeta) GetName() string            { return m.Name }
func (m *ObjectMeta) GetNamespace() string       { return m.Namespace }
func (m *ObjectMeta) GetResourceVersion() string { return m.ResourceVersion }

// ListMeta is the metadata of a list. Its resourceVersion is the
// version that a watch starts from.
type ListMeta struct {
	ResourceVersion string `json:"resourceVersion,omitempty"`
}

// Object is one resource of a kind: the metadata, the spec that a
// person writes, and the status that the operator writes. Each kind in
// this package is an alias of Object with its own spec and status, so
// every kind has the same envelope and the same methods.
type Object[Spec, Status any] struct {
	APIVersion string     `json:"apiVersion,omitempty"`
	Kind       string     `json:"kind,omitempty"`
	Metadata   ObjectMeta `json:"metadata"`
	Spec       Spec       `json:"spec"`
	Status     Status     `json:"status,omitzero"`
}

// GetObjectMeta answers the metadata, for the shared module's watch
// and its memo of the operator's own writes.
func (o *Object[Spec, Status]) GetObjectMeta() Meta { return &o.Metadata }

// List is the answer to a list request for one kind.
type List[T any] struct {
	Metadata ListMeta `json:"metadata"`
	Items    []T      `json:"items"`
}

// ConditionStatus is a condition's verdict. Unknown is a third state:
// the operator cannot tell yet.
type ConditionStatus string

const (
	ConditionTrue    ConditionStatus = "True"
	ConditionFalse   ConditionStatus = "False"
	ConditionUnknown ConditionStatus = "Unknown"
)

// Condition has the shape of metav1.Condition, so `kubectl describe`
// and `kubectl wait --for=condition=Ready` read it the way they read a
// Pod's conditions. ObservedGeneration records which
// metadata.generation the condition judged.
type Condition struct {
	Type               string          `json:"type"`
	Status             ConditionStatus `json:"status"`
	ObservedGeneration int64           `json:"observedGeneration,omitempty"`
	Reason             string          `json:"reason"`
	Message            string          `json:"message"`
	LastTransitionTime time.Time       `json:"lastTransitionTime"`
}

// The condition types that more than one kind reports.
const (
	// Ready is True when the resource does its job: a device is
	// connected, a telescope or a reservation is ready for its holder.
	ConditionReady = "Ready"
	// ParentFound is True when every resource that the spec names, up
	// to the Observatory, exists. A resource whose parent is missing
	// stays in place, and this condition states which parent is
	// missing.
	ConditionParentFound = "ParentFound"
)
