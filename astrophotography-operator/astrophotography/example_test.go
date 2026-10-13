package astrophotography

// examples/first-session.yaml is the drill of plan 08, which a reader
// also copies. These tests hold it to the CRDs of both operators, to
// the Go types, and to itself: every claim and Solver it names exists
// in it.

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

// documents answers the objects in one file of examples/, in order.
func documents(t *testing.T, name string) []map[string]any {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "examples", name))
	if err != nil {
		t.Fatal(err)
	}
	var objects []map[string]any
	for _, document := range strings.Split(string(raw), "\n---\n")[1:] {
		object := map[string]any{}
		if err := yaml.UnmarshalStrict([]byte(document), &object); err != nil {
			t.Fatal(err)
		}
		objects = append(objects, object)
	}
	return objects
}

func objectName(object map[string]any) string {
	return object["metadata"].(map[string]any)["name"].(string)
}

// ofKind answers the example's objects of one kind, re-encoded as JSON
// so a test decodes them into a Go type.
func ofKind(t *testing.T, kind string) [][]byte {
	t.Helper()
	var out [][]byte
	for _, object := range documents(t, example) {
		if object["kind"] == kind {
			raw, err := json.Marshal(object)
			if err != nil {
				t.Fatal(err)
			}
			out = append(out, raw)
		}
	}
	return out
}

// decodeStrict decodes JSON into a Go type and refuses a field that the
// type lacks, which the operator's decode would drop with no error.
func decodeStrict(t *testing.T, raw []byte, into any) {
	t.Helper()
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(into); err != nil {
		t.Fatal(err)
	}
}

// crdOf answers the CRD of each kind of the example that a CRD defines:
// this operator's kinds, and the Reservation of observatory-operator.
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
				t.Error(errs.ToAggregate())
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

// Each name that the example gives in a spec names an object of the
// example, and the Reservation names this operator as its holder.
func TestTheExampleNamesOnlyItsOwnObjects(t *testing.T) {
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
				t.Errorf("%s names the %s %q, which the example does not hold", c.field, c.kind, c.name)
			}
		})
	}
	if reservation.Spec.Holder != "astrophotography-operator" {
		t.Errorf("the Reservation's holder is %q", reservation.Spec.Holder)
	}
}
