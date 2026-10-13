// Package astrophotography holds the resources of the API group
// astrophotography.liken.sh, version v1alpha1, as Go types: the Target
// that a person declares to image an object, and the Solver that holds
// the index files of astrometry.net.
//
// The envelope of each object, its metadata, and its conditions are
// observatory-operator's (the observatory package), so the resources of
// the two groups read and decode the same way. The types are written by
// hand for the reason that package gives, and the CRDs in deploy/ are
// written by hand too. The tests here hold each type to its CRD, field
// by field.
package astrophotography

import (
	"github.com/liken-sh/liken/observatory-operator/observatory"
)

const (
	// Group is the API group of every kind in this package.
	Group = "astrophotography.liken.sh"
	// Version is the one version that the CRDs serve.
	Version = "v1alpha1"
	// APIVersion is the value of apiVersion in every object.
	APIVersion = Group + "/" + Version
)

// ObjectMeta is the part of an object's metadata that the operator
// uses.
type ObjectMeta = observatory.ObjectMeta

// Object is one resource of a kind: the metadata, the spec that a
// person writes, and the status that the operator writes.
type Object[Spec, Status any] = observatory.Object[Spec, Status]

// Condition has the shape of metav1.Condition. It is the condition type
// that every liken component shares.
type Condition = observatory.Condition

// Kind names one resource of the group: its kind and the plural that
// its URL uses.
type Kind struct {
	Name   string
	Plural string
}

var (
	TargetKind = Kind{"Target", "targets"}
	SolverKind = Kind{"Solver", "solvers"}
)

// Kinds lists every kind of the group.
var Kinds = []Kind{TargetKind, SolverKind}
