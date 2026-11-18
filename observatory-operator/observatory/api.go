// Package observatory defines the Go types of the API group
// observatory.liken.sh/v1alpha1. observatory-operator reads and writes
// them, and astrophotography-operator imports them to read a
// telescope's state and to create a Reservation.
//
// The types are written by hand. The operators call the API server with
// net/http through the kubernetes module, and a generated client with
// k8s.io/apimachinery would add a large dependency tree for a few
// structs. So ObjectMeta has only the fields that an operator reads or
// writes, in the wire format of metav1.ObjectMeta. schema/ generates the typed fields of INDI's
// standard properties into the generated_*.go files, together with the
// CRDs that declare them.
//
// The CRDs in deploy/ are the schema that the API server enforces, and
// drift_test.go fails when a type here and its CRD differ in any field.
package observatory

import (
	"time"

	"github.com/liken-sh/liken/kubernetes/conditions"
)

const (
	// Group is the API group of every kind in this package.
	Group = "observatory.liken.sh"
	// Finalizer keeps a deleted resource in the API until the operator
	// has deactivated it: a Reservation until its deactivation steps are
	// done, a device until its pod is gone, and a Telescope or an
	// Observatory while a Reservation holds it or one of its telescopes.
	Finalizer = Group + "/deactivate"
	// Version is the only version that the CRDs serve.
	Version = "v1alpha1"
	// APIVersion is the value of apiVersion in every object.
	APIVersion = Group + "/" + Version
)

// ObjectMeta has the labels and annotations so that an object the
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

// Meta has the same method set as memo.Meta in the kubernetes module,
// so these types satisfy it without importing memo.
type Meta = interface {
	GetNamespace() string
	GetName() string
	GetResourceVersion() string
}

func (m *ObjectMeta) GetName() string            { return m.Name }
func (m *ObjectMeta) GetNamespace() string       { return m.Namespace }
func (m *ObjectMeta) GetResourceVersion() string { return m.ResourceVersion }

// ListMeta is the metadata of a list. A watch starts from its
// resourceVersion.
type ListMeta struct {
	ResourceVersion string `json:"resourceVersion,omitempty"`
}

// Object is one resource. Each kind in this package is an alias of
// Object with its own Spec and Status types, so every kind has the same
// metadata fields and methods.
type Object[Spec, Status any] struct {
	APIVersion string     `json:"apiVersion,omitempty"`
	Kind       string     `json:"kind,omitempty"`
	Metadata   ObjectMeta `json:"metadata"`
	Spec       Spec       `json:"spec"`
	Status     Status     `json:"status,omitzero"`
}

// GetObjectMeta returns the metadata for the kubernetes module's watch
// and memo.
func (o *Object[Spec, Status]) GetObjectMeta() Meta { return &o.Metadata }

type List[T any] struct {
	Metadata ListMeta `json:"metadata"`
	Items    []T      `json:"items"`
}

type ConditionStatus = conditions.Status

const (
	ConditionTrue    = conditions.True
	ConditionFalse   = conditions.False
	ConditionUnknown = conditions.Unknown
)

type Condition = conditions.Condition

// The condition types that more than one kind reports.
const (
	// Ready is True when the resource can do its job: a device is
	// connected, or a Telescope or a Reservation is ready for its
	// holder.
	ConditionReady = "Ready"
	// ParentFound is True when every resource that the spec names, up
	// to the Observatory, exists. When one is missing, the condition is
	// False and its message gives the missing resource.
	ConditionParentFound = "ParentFound"
)
