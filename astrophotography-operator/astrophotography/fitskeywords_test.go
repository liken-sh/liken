package astrophotography

// The Target's spec.fitsKeywords is a copy of observatory-operator's
// field with a longer list of refused names. These tests hold the copy
// equal to the field in observatory-operator's CRDs, and its list to
// RefusedFITSKeywords, so a change on either side fails here.

import (
	"maps"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"testing"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
)

// observatoryFITSKeywords is spec.fitsKeywords of observatory-operator's
// Observatory, the field that its other levels hold copies of.
func observatoryFITSKeywords(t *testing.T) apiextensionsv1.JSONSchemaProps {
	t.Helper()
	crd := loadCRDFile(t, filepath.Join("..", "..", "observatory-operator", "deploy", "observatories-crd.yaml"))
	return crd.Spec.Versions[0].Schema.OpenAPIV3Schema.Properties["spec"].Properties["fitsKeywords"]
}

func targetFITSKeywords(t *testing.T) apiextensionsv1.JSONSchemaProps {
	t.Helper()
	return schemaOf(t, TargetKind).Properties["spec"].Properties["fitsKeywords"]
}

// withoutTheLevelsOwnParts answers a copy of the field with the two
// parts that differ between levels cleared: the field's description,
// which names the level, and the rule that refuses names.
func withoutTheLevelsOwnParts(field apiextensionsv1.JSONSchemaProps) apiextensionsv1.JSONSchemaProps {
	field.Description = ""
	item := *field.Items.Schema
	item.Properties = maps.Clone(item.Properties)
	name := item.Properties["name"]
	name.XValidations = name.XValidations[:1]
	item.Properties["name"] = name
	field.Items = &apiextensionsv1.JSONSchemaPropsOrArray{Schema: &item}
	return field
}

func TestTheTargetsFITSKeywordsMatchObservatoryOperators(t *testing.T) {
	got := withoutTheLevelsOwnParts(targetFITSKeywords(t))
	want := withoutTheLevelsOwnParts(observatoryFITSKeywords(t))
	if !reflect.DeepEqual(got, want) {
		t.Error("spec.fitsKeywords of the Target differs from observatory-operator's beyond its description and its refused names")
	}
}

// refusedIn reads the names from the rule `!(self in [...])`.
func refusedIn(t *testing.T, field apiextensionsv1.JSONSchemaProps) []string {
	t.Helper()
	rules := field.Items.Schema.Properties["name"].XValidations
	if len(rules) < 2 {
		t.Fatal("the name has no rule that refuses names")
	}
	var names []string
	for _, match := range regexp.MustCompile(`'([A-Z0-9_-]+)'`).FindAllStringSubmatch(rules[1].Rule, -1) {
		names = append(names, match[1])
	}
	return names
}

func TestTheTargetRefusesTheKeywordsThatTheOperatorWrites(t *testing.T) {
	if got, want := refusedIn(t, targetFITSKeywords(t)), RefusedFITSKeywords(); !slices.Equal(got, want) {
		t.Errorf("the CRD refuses %v, want %v", got, want)
	}
}

func TestRefusedFITSKeywordsHoldsEachKeywordOnce(t *testing.T) {
	refused := RefusedFITSKeywords()
	for _, name := range append([]string{"EXPTIME", "COMMENT"}, OperatorFITSKeywords...) {
		if count := len(slices.DeleteFunc(slices.Clone(refused), func(n string) bool { return n != name })); count != 1 {
			t.Errorf("%s appears %d times", name, count)
		}
	}
}
