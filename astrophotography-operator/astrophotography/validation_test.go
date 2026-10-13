package astrophotography

// Each case is a resource that a person could write, and the verdict
// that the API server gives it. The tests run the structural schema
// validator and then the CEL rules, the two passes the API server runs
// on a create or an update.

import (
	"maps"
	"strings"
	"testing"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	structuralschema "k8s.io/apiextensions-apiserver/pkg/apiserver/schema"
	celschema "k8s.io/apiextensions-apiserver/pkg/apiserver/schema/cel"
	"k8s.io/apiextensions-apiserver/pkg/apiserver/validation"
	"k8s.io/apimachinery/pkg/util/validation/field"
	celconfig "k8s.io/apiserver/pkg/apis/cel"
)

// validateWith answers the errors that the API server would give an
// object under a CRD. With an old object, it is an update, and the
// rules that compare with oldSelf run too.
func validateWith(t *testing.T, crd *apiextensionsv1.CustomResourceDefinition, object, old map[string]any) field.ErrorList {
	t.Helper()
	internal := internalSchema(t, crd)
	schemaValidator, _, err := validation.NewSchemaValidator(internal)
	if err != nil {
		t.Fatal(err)
	}
	errs := validation.ValidateCustomResource(field.NewPath(""), object, schemaValidator)
	structural, err := structuralschema.NewStructural(internal)
	if err != nil {
		t.Fatal(err)
	}
	celErrs, _ := celschema.NewValidator(structural, true, celconfig.PerCallLimit).Validate(
		t.Context(), field.NewPath(""), structural, object, old, celconfig.RuntimeCELCostBudget)
	return append(errs, celErrs...)
}

func validate(t *testing.T, kind Kind, object, old map[string]any) field.ErrorList {
	t.Helper()
	return validateWith(t, loadCRD(t, kind), object, old)
}

// resource builds an object of a kind with the spec given.
func resource(kind Kind, spec map[string]any) map[string]any {
	return map[string]any{
		"apiVersion": APIVersion,
		"kind":       kind.Name,
		"metadata":   map[string]any{"name": "m42", "namespace": "observatory"},
		"spec":       spec,
	}
}

// m42 builds the spec of a target that images M42 into the claim
// archive, with more fields.
func m42(more map[string]any) map[string]any {
	spec := map[string]any{
		"object":      "M42",
		"coordinates": map[string]any{"ra": "05:35:17.3", "dec": "-05:23:28"},
		"sequences":   []any{map[string]any{"filter": "Luminance", "exposure": "300s", "count": int64(60)}},
		"archive":     map[string]any{"claimName": "archive"},
	}
	maps.Copy(spec, more)
	return spec
}

// without answers the spec with one field removed.
func without(spec map[string]any, field string) map[string]any {
	delete(spec, field)
	return spec
}

func at(ra, dec string) map[string]any {
	return map[string]any{"coordinates": map[string]any{"ra": ra, "dec": dec}}
}

func sequence(fields map[string]any) map[string]any {
	return map[string]any{"sequences": []any{fields}}
}

func archive(fields map[string]any) map[string]any {
	a := map[string]any{"claimName": "archive"}
	maps.Copy(a, fields)
	return map[string]any{"archive": a}
}

func keyword(name string) map[string]any {
	return map[string]any{"fitsKeywords": []any{map[string]any{"name": name, "value": "x"}}}
}

func TestTheAPIServerAcceptsAndRefusesATarget(t *testing.T) {
	cases := []struct {
		name string
		spec map[string]any
		// refusal is part of the error, or empty when the resource is
		// accepted.
		refusal string
	}{
		{"the example of plan 01", m42(map[string]any{
			"priority": int64(0), "suspend": false, "opticalTrain": "east-imaging",
			"sequences": []any{
				map[string]any{"filter": "Luminance", "exposure": "300s", "count": int64(60)},
				map[string]any{"filter": "Red", "exposure": "300s", "count": int64(20), "binning": int64(2), "gain": int64(100), "offset": int64(10)},
			},
			"dither":       map[string]any{"every": int64(2), "pixels": 5.0},
			"centering":    map[string]any{"solver": "lab", "tolerance": 30.0},
			"archive":      map[string]any{"claimName": "archive", "path": "astro", "format": "{object}/{night}/{filter}/{object}_{filter}_{exposure}_{datetime}", "promotion": "Continuous"},
			"fitsKeywords": []any{map[string]any{"name": "PROGRAM", "value": "Orion widefield"}},
		}), ""},
		{"a target with no sequence", m42(map[string]any{"sequences": []any{}}), "spec.sequences: Invalid value"},
		{"a target with no archive", without(m42(nil), "archive"), "spec.archive: Required value"},
		{"an object with a slash", m42(map[string]any{"object": "NGC 2024/Flame"}), "the header would not hold the object's name"},
		{"the north celestial pole", m42(at("02:31:49.1", "+90:00:00")), ""},
		{"a declination with no sign", m42(at("02:31:49.1", "89:15:51")), ""},
		{"a right ascension in decimal hours", m42(at("5.588", "-05:23:28")), "ra is hours, minutes, and seconds"},
		{"a right ascension of 24 hours", m42(at("24:00:00", "-05:23:28")), "ra is hours, minutes, and seconds"},
		{"a declination beyond the pole", m42(at("05:35:17.3", "-90:00:01")), "dec is degrees, minutes, and seconds"},
		{"a declination in decimal degrees", m42(at("05:35:17.3", "-5.391")), "dec is degrees, minutes, and seconds"},
		{"an exposure of half a second", m42(sequence(map[string]any{"exposure": "500ms", "count": int64(10)})), ""},
		{"an exposure with no unit", m42(sequence(map[string]any{"exposure": "300", "count": int64(10)})),
			"exposure must be a positive duration"},
		{"an exposure of nothing", m42(sequence(map[string]any{"exposure": "0s", "count": int64(10)})),
			"exposure must be a positive duration"},
		{"a count of none", m42(sequence(map[string]any{"exposure": "300s", "count": int64(0)})), "spec.sequences[0].count: Invalid value"},
		{"a binning of 32", m42(sequence(map[string]any{"exposure": "300s", "count": int64(1), "binning": int64(32)})),
			"spec.sequences[0].binning: Invalid value"},
		{"a dither of no pixels", m42(map[string]any{"dither": map[string]any{"every": int64(2), "pixels": 0.0}}),
			"spec.dither.pixels: Invalid value"},
		{"centering with no solver", m42(map[string]any{"centering": map[string]any{"tolerance": 30.0}}),
			"spec.centering.solver: Required value"},
		{"a centering exposure that is no duration", m42(map[string]any{"centering": map[string]any{"solver": "lab", "exposure": "soon"}}),
			"exposure must be a positive duration"},
		{"an archive path outside the claim", m42(archive(map[string]any{"path": "../frames"})), "path is a directory inside the claim"},
		{"an absolute archive path", m42(archive(map[string]any{"path": "/astro"})), "path is a directory inside the claim"},
		{"an archive path with dots in a name", m42(archive(map[string]any{"path": "astro..2026"})), ""},
		{"a format of every token", m42(archive(map[string]any{"format": "{target}/{object}/{filter}/{exposure}/{binning}/{gain}/{offset}/{camera}/{telescope}/{train}/{reservation}/{night}/{datetime}/{n}_{frameid}"})), ""},
		{"a format with an unknown token", m42(archive(map[string]any{"format": "{object}/{date}"})), "format holds only known tokens"},
		{"a format with a space", m42(archive(map[string]any{"format": "{object} {filter}"})), "format holds only known tokens"},
		{"an absolute format", m42(archive(map[string]any{"format": "/{object}"})), "format is relative to path"},
		{"a format in the catalog", m42(archive(map[string]any{"format": "catalog/{object}"})), "format is relative to path"},
		{"a format that leaves the path", m42(archive(map[string]any{"format": "../{object}"})), "format cannot hold '..'"},
		{"a format that the driver rewrites", m42(archive(map[string]any{"format": "{object}_XXX"})), "the camera's driver replaces XXX and ISO8601"},
		{"a format with the driver's time", m42(archive(map[string]any{"format": "{object}_ISO8601"})), "the camera's driver replaces XXX and ISO8601"},
		{"a promotion that does not exist", m42(archive(map[string]any{"promotion": "Nightly"})), "Unsupported value"},
		{"a keyword that a person declares", m42(keyword("OBSERVER")), ""},
		{"OBJECT, which the operator writes", m42(keyword("OBJECT")), "OBJECT is a keyword that astrophotography-operator writes"},
		{"FRAMEID, which the operator writes", m42(keyword("FRAMEID")), "FRAMEID is a keyword that astrophotography-operator writes"},
		{"a keyword that the driver measures", m42(keyword("EXPTIME")), "EXPTIME is a keyword that the camera's driver writes"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			errs := validate(t, TargetKind, resource(TargetKind, c.spec), nil)
			verdict(t, errs, c.refusal)
		})
	}
}

// verdict checks that a resource was accepted, or refused with an
// error that holds the refusal.
func verdict(t *testing.T, errs field.ErrorList, refusal string) {
	t.Helper()
	switch {
	case refusal == "" && len(errs) > 0:
		t.Errorf("refused: %v", errs.ToAggregate())
	case refusal != "" && len(errs) == 0:
		t.Errorf("accepted, want a refusal with %q", refusal)
	case refusal != "" && !strings.Contains(errs.ToAggregate().Error(), refusal):
		t.Errorf("refused with %v, want %q", errs.ToAggregate(), refusal)
	}
}

// The counts come from the archive's catalog, so a target keeps its
// claim for life.
func TestTheArchiveClaimCannotChange(t *testing.T) {
	cases := []struct {
		name    string
		claim   string
		refusal string
	}{
		{"the same claim", "archive", ""},
		{"another claim", "nas", "spec.archive.claimName cannot change; create another Target"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			old := resource(TargetKind, m42(nil))
			next := resource(TargetKind, m42(archive(map[string]any{"claimName": c.claim})))
			verdict(t, validate(t, TargetKind, next, old), c.refusal)
		})
	}
}

func TestTheAPIServerAcceptsAndRefusesASolver(t *testing.T) {
	cases := []struct {
		name    string
		spec    map[string]any
		refusal string
	}{
		{"the lab's solver", map[string]any{"indexFiles": map[string]any{"claimName": "astrometry-indexes", "path": ""}}, ""},
		{"index files in a directory", map[string]any{"indexFiles": map[string]any{"claimName": "astrometry-indexes", "path": "2mass"}}, ""},
		{"no claim", map[string]any{"indexFiles": map[string]any{}}, "spec.indexFiles.claimName: Required value"},
		{"a path outside the claim", map[string]any{"indexFiles": map[string]any{"claimName": "a", "path": "a/../.."}},
			"path is a directory inside the claim"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			verdict(t, validate(t, SolverKind, resource(SolverKind, c.spec), nil), c.refusal)
		})
	}
}
