package astrophotography

import (
	"encoding/json"
	"testing"
	"time"

	structuralschema "k8s.io/apiextensions-apiserver/pkg/apiserver/schema"
	"k8s.io/apiextensions-apiserver/pkg/apiserver/schema/defaulting"
)

func TestExposureDuration(t *testing.T) {
	cases := []struct {
		exposure string
		want     time.Duration
	}{
		{"300s", 300 * time.Second},
		{"500ms", 500 * time.Millisecond},
		{"1m30s", 90 * time.Second},
		{"2.5s", 2500 * time.Millisecond},
	}
	for _, c := range cases {
		t.Run(c.exposure, func(t *testing.T) {
			got, err := Sequence{Exposure: c.exposure}.ExposureDuration()
			if err != nil || got != c.want {
				t.Errorf("ExposureDuration = %v, %v; want %v", got, err, c.want)
			}
		})
	}
}

// defaulted answers a Target as the API server stores it, with the
// CRD's defaults filled in.
func defaulted(t *testing.T, spec map[string]any) Target {
	t.Helper()
	structural, err := structuralschema.NewStructural(internalSchema(t, loadCRD(t, TargetKind)))
	if err != nil {
		t.Fatal(err)
	}
	object := resource(TargetKind, spec)
	defaulting.Default(object, structural)
	raw, err := json.Marshal(object)
	if err != nil {
		t.Fatal(err)
	}
	var target Target
	if err := json.Unmarshal(raw, &target); err != nil {
		t.Fatal(err)
	}
	return target
}

// The design gives a default for each of these fields, and the
// operator reads the stored value, so the CRD must fill each one.
func TestTheCRDFillsTheDefaultsOfPlans02And03(t *testing.T) {
	target := defaulted(t, m42(map[string]any{"centering": map[string]any{"solver": "lab"}}))
	cases := []struct {
		field     string
		got, want any
	}{
		{"centering.tolerance", target.Spec.Centering.Tolerance, 30.0},
		{"centering.exposure", target.Spec.Centering.Exposure, "5s"},
		{"centering.attempts", target.Spec.Centering.Attempts, int32(5)},
		{"centering.filter", target.Spec.Centering.Filter, ""},
		{"archive.path", target.Spec.Archive.Path, ""},
		{"archive.format", target.Spec.Archive.Format, "{object}/{night}/{filter}/{object}_{filter}_{exposure}_{datetime}"},
		{"archive.promotion", target.Spec.Archive.Promotion, PromotionContinuous},
	}
	for _, c := range cases {
		t.Run(c.field, func(t *testing.T) {
			if c.got != c.want {
				t.Errorf("%s = %v, want %v", c.field, c.got, c.want)
			}
		})
	}
}

// The operator orders an unset priority as 0. The CRD stores that 0,
// so the Priority printer column shows it instead of a blank.
func TestTheCRDStoresAnUnsetPriorityAsZero(t *testing.T) {
	structural, err := structuralschema.NewStructural(internalSchema(t, loadCRD(t, TargetKind)))
	if err != nil {
		t.Fatal(err)
	}
	object := resource(TargetKind, m42(nil))
	defaulting.Default(object, structural)
	if got := object["spec"].(map[string]any)["priority"]; got != int64(0) {
		t.Errorf("spec.priority is stored as %#v, want 0", got)
	}
}
