package astrophotography

// examples/first-session.yaml is a complete first session that readers
// copy. These tests check it against the CRDs of both operators and the
// Go types, and check that the file defines every claim and Solver that
// it refers to.

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"sigs.k8s.io/yaml"

	"github.com/liken-sh/liken/observatory-operator/observatory"
)

const example = "first-session.yaml"

// documents returns the objects in one file of examples/, in order.
func documents(t *testing.T, name string) []map[string]any {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "examples", name))
	if err != nil {
		t.Fatalf("reading %s: %v", name, err)
	}
	var objects []map[string]any
	for _, document := range strings.Split(string(raw), "\n---\n")[1:] {
		object := map[string]any{}
		if err := yaml.UnmarshalStrict([]byte(document), &object); err != nil {
			t.Fatalf("decoding a document of %s: %v", name, err)
		}
		objects = append(objects, object)
	}
	return objects
}

func objectName(object map[string]any) string {
	return object["metadata"].(map[string]any)["name"].(string)
}

// ofKind returns the example's objects of one kind, encoded as JSON so
// that a test can decode them into a Go type.
func ofKind(t *testing.T, kind string) [][]byte {
	t.Helper()
	var out [][]byte
	for _, object := range documents(t, example) {
		if object["kind"] == kind {
			raw, err := json.Marshal(object)
			if err != nil {
				t.Fatalf("encoding the %s %s as JSON: %v", kind, objectName(object), err)
			}
			out = append(out, raw)
		}
	}
	return out
}

// decodeStrict fails on a field that the Go type lacks, which the
// operator's own decode drops without an error.
func decodeStrict(t *testing.T, raw []byte, into any) {
	t.Helper()
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(into); err != nil {
		t.Fatal(err)
	}
}

// crdOf returns the path of the CRD file for a kind in the example, or
// "" for a kind whose CRD the test does not load.
func crdOf(t *testing.T, kind string) string {
	t.Helper()
	switch kind {
	case TargetKind.Name:
		return crdPath(TargetKind)
	case SolverKind.Name:
		return crdPath(SolverKind)
	case observatory.ReservationKind.Name:
		return filepath.Join("..", "..", "observatory-operator", "deploy", "reservations-crd.yaml")
	}
	return ""
}

func TestTheAPIServerAcceptsTheExample(t *testing.T) {
	for _, object := range documents(t, example) {
		kind := object["kind"].(string)
		path := crdOf(t, kind)
		if path == "" {
			continue
		}
		t.Run(kind+"/"+objectName(object), func(t *testing.T) {
			if errs := validateWith(t, loadCRDFile(t, path), object, nil); len(errs) > 0 {
				t.Errorf("the CRD of %s refuses the example: %v", kind, errs.ToAggregate())
			}
		})
	}
}

func TestTheGoTypesReadTheExample(t *testing.T) {
	for _, raw := range ofKind(t, TargetKind.Name) {
		decodeStrict(t, raw, &Target{})
	}
	for _, raw := range ofKind(t, SolverKind.Name) {
		decodeStrict(t, raw, &Solver{})
	}
	for _, raw := range ofKind(t, observatory.ReservationKind.Name) {
		decodeStrict(t, raw, &observatory.Reservation{})
	}
}

func TestTheExampleRefersOnlyToItsOwnObjects(t *testing.T) {
	names := map[string][]string{}
	for _, object := range documents(t, example) {
		kind := object["kind"].(string)
		names[kind] = append(names[kind], objectName(object))
	}
	var target Target
	decodeStrict(t, ofKind(t, TargetKind.Name)[0], &target)
	var solver Solver
	decodeStrict(t, ofKind(t, SolverKind.Name)[0], &solver)
	var reservation observatory.Reservation
	decodeStrict(t, ofKind(t, observatory.ReservationKind.Name)[0], &reservation)
	cases := []struct {
		field, kind, name string
	}{
		{"the Target's archive", "PersistentVolumeClaim", target.Spec.Archive.ClaimName},
		{"the Target's solver", SolverKind.Name, target.Spec.Centering.Solver},
		{"the Solver's index files", "PersistentVolumeClaim", solver.Spec.IndexFiles.ClaimName},
		{"the Reservation's outbox", "PersistentVolumeClaim", reservation.Spec.Outbox.ClaimName},
	}
	for _, c := range cases {
		t.Run(c.field, func(t *testing.T) {
			if !slices.Contains(names[c.kind], c.name) {
				t.Errorf("%s names the %s %q, which the example does not declare", c.field, c.kind, c.name)
			}
		})
	}
	if reservation.Spec.Holder != "astrophotography-operator" {
		t.Errorf("the Reservation's holder is %q, want astrophotography-operator", reservation.Spec.Holder)
	}
}
