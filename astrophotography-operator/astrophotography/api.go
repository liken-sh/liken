// Package astrophotography defines the Go types of the API group
// astrophotography.liken.sh, version v1alpha1. A person declares a
// Target to image an object. A Solver serves the astrometry.net index
// files.
//
// The object, metadata, and condition types come from
// observatory-operator's observatory package, so both API groups decode
// the same way. The types and the CRDs in deploy/ are written by hand,
// because a generated client and k8s.io/apimachinery would add a large
// dependency tree for a few structs. The tests compare each type with
// its CRD field by field.
package astrophotography

import (
	"github.com/liken-sh/liken/observatory-operator/observatory"
)

const (
	Group      = "astrophotography.liken.sh"
	Version    = "v1alpha1"
	APIVersion = Group + "/" + Version
)

// ObjectMeta is the subset of an object's metadata that the operator
// reads.
type ObjectMeta = observatory.ObjectMeta

// Object is one resource of a kind. A person writes its spec, and the
// operator writes its status.
type Object[Spec, Status any] = observatory.Object[Spec, Status]

// Condition has the fields of metav1.Condition. It is the condition
// type of kubernetes/conditions, which every liken component uses.
type Condition = observatory.Condition

// Kind is a resource kind of the group, with the plural that its URL
// path uses.
type Kind struct {
	Name   string
	Plural string
}

var (
	TargetKind = Kind{"Target", "targets"}
	SolverKind = Kind{"Solver", "solvers"}
)

var Kinds = []Kind{TargetKind, SolverKind}
