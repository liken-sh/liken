package astrophotography

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	structuralschema "k8s.io/apiextensions-apiserver/pkg/apiserver/schema"
	"k8s.io/apiextensions-apiserver/pkg/apiserver/schema/defaulting"
)

func labSolver(more map[string]any) map[string]any {
	spec := map[string]any{"indexFiles": map[string]any{"claimName": "astrometry-indexes"}}
	for key, value := range more {
		spec[key] = value
	}
	return spec
}

// A person who leaves out spec.search gets the attempts of plan 02 and
// the 10° that the simulator's frame needed, then a blind solve.
func TestTheCRDFillsTheSolversSearch(t *testing.T) {
	structural, err := structuralschema.NewStructural(internalSchema(t, loadCRD(t, SolverKind)))
	if err != nil {
		t.Fatal(err)
	}
	object := resource(SolverKind, labSolver(nil))
	defaulting.Default(object, structural)
	raw, _ := json.Marshal(object)
	var solver Solver
	if err := json.Unmarshal(raw, &solver); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(solver.Spec.Search.Radii, []float64{5, 10}) || !solver.Spec.Search.Blind {
		t.Errorf("search = %+v, want radii [5 10] and blind", solver.Spec.Search)
	}
}

func TestTheAPIServerAcceptsAndRefusesASolversSearch(t *testing.T) {
	cases := []struct {
		name    string
		search  map[string]any
		refusal string
	}{
		{"one radius and no blind solve", map[string]any{"radii": []any{2.0}, "blind": false}, ""},
		{"no radius", map[string]any{"radii": []any{}}, "spec.search.radii"},
		{"a radius of 0", map[string]any{"radii": []any{0.0}}, "spec.search.radii[0]"},
		{"a radius past the pole", map[string]any{"radii": []any{181.0}}, "spec.search.radii[0]"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			verdict(t, validate(t, SolverKind, resource(SolverKind, labSolver(map[string]any{"search": c.search})), nil), c.refusal)
		})
	}
}

// The solver's Service is named <name>-solver, and a Service's name is
// at most 63 characters.
func TestASolversNameFitsItsService(t *testing.T) {
	cases := []struct {
		name    string
		length  int
		refusal string
	}{
		{"56 characters", 56, ""},
		{"57 characters", 57, "at most 56 characters"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			object := resource(SolverKind, labSolver(nil))
			object["metadata"].(map[string]any)["name"] = strings.Repeat("a", c.length)
			verdict(t, validate(t, SolverKind, object, nil), c.refusal)
		})
	}
}
