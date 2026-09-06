package main

// What these tests read: the Library CRD as the cluster applies it,
// against the operator that reconciles it. The two hold one vocabulary,
// so a spec the API server admits is a spec this operator can serve.

import (
	"encoding/json"
	"maps"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

// The schema of the one version this CRD serves.
func librarySchema(t *testing.T) map[string]any {
	t.Helper()
	body, err := os.ReadFile("deploy/libraries-crd.yaml")
	if err != nil {
		t.Fatal(err)
	}
	document := map[string]any{}
	if err := yaml.Unmarshal(body, &document); err != nil {
		t.Fatal(err)
	}
	versions := document["spec"].(map[string]any)["versions"].([]any)
	return versions[0].(map[string]any)
}

// The names a CEL list holds, in the order the rule states them.
func namesInRule(t *testing.T, rule string) []string {
	t.Helper()
	start, end := strings.Index(rule, "["), strings.LastIndex(rule, "]")
	if start < 0 || end < start {
		t.Fatalf("the rule holds no list: %s", rule)
	}
	names := []string{}
	for _, name := range strings.Split(rule[start+1:end], ",") {
		names = append(names, strings.Trim(strings.TrimSpace(name), "'"))
	}
	return names
}

// Spec.refresh takes one RFC 3339 time per fact, and the rule that
// guards its keys names every fact the operator runs, so a person cannot
// ask for a refresh of a fact no container fills.
func TestTheRefreshMapTakesOneTimePerFact(t *testing.T) {
	refresh := schemaField(t, librarySchema(t), "schema", "openAPIV3Schema", "properties",
		"spec", "properties", "refresh").(map[string]any)

	values := refresh["additionalProperties"].(map[string]any)
	if values["type"] != "string" || values["format"] != "date-time" {
		t.Errorf("a value reads %+v, want an RFC 3339 string", values)
	}
	rules := refresh["x-kubernetes-validations"].([]any)
	if len(rules) != 1 {
		t.Fatalf("rules = %+v, want the one that guards the keys", rules)
	}
	names := namesInRule(t, rules[0].(map[string]any)["rule"].(string))
	if !slices.Equal(names, factVocabulary) {
		t.Errorf("the rule names %v, want %v", names, factVocabulary)
	}
}

// The spec the API server admits is the spec the operator reads: one
// time per fact, under the field name the schema states.
func TestTheOperatorReadsARefreshTheSchemaAdmits(t *testing.T) {
	spec := LibrarySpec{}
	if err := json.Unmarshal([]byte(`{"refresh":{"credits":"2026-09-03T21:00:00Z"}}`), &spec); err != nil {
		t.Fatal(err)
	}

	want := time.Date(2026, 9, 3, 21, 0, 0, 0, time.UTC)
	if at := spec.Refresh[factCredits]; !at.Equal(want) {
		t.Errorf("refresh[%s] = %v, want %v", factCredits, at, want)
	}
}

// rulesOf is the rules one field of the schema carries, as the CRD states
// them.
func rulesOf(t *testing.T, field any) []string {
	t.Helper()
	held, _ := field.(map[string]any)["x-kubernetes-validations"].([]any)
	rules := []string{}
	for _, rule := range held {
		text, _ := rule.(map[string]any)["rule"].(string)
		rules = append(rules, strings.Join(strings.Fields(text), " "))
	}
	return rules
}

// The kind enum and the settings blocks of the CRD are the kinds the operator
// serves, and a kind the schema admits resolves to a settings block in Go.
func TestTheSchemaAdmitsTheKindsTheOperatorServes(t *testing.T) {
	spec := schemaField(t, librarySchema(t), "schema", "openAPIV3Schema", "properties",
		"spec", "properties").(map[string]any)
	kinds, _ := spec["kind"].(map[string]any)["enum"].([]any)

	for _, kind := range kinds {
		name := kind.(string)
		if _, held := spec[name]; !held {
			t.Errorf("the kind %s names no settings block", name)
		}
		if settings := (LibrarySpec{Kind: name, Movies: &LibrarySettings{}, Series: &LibrarySettings{},
			Franchises: &LibraryFranchises{}}).settings(); settings == nil {
			t.Errorf("the operator resolves no settings block for the kind %s", name)
		}
	}
	if len(kinds) != 3 {
		t.Errorf("the enum names %v, want the three kinds the operator serves", kinds)
	}
}

// Every kind names a claim, so the storage block requires it, and it names
// nothing else: a franchises library's checkout is a claim like any other.
func TestTheSchemaTakesAClaimForEveryKind(t *testing.T) {
	storage := schemaField(t, librarySchema(t), "schema", "openAPIV3Schema", "properties",
		"spec", "properties", "storage")

	if required := requiredOf(t, storage); !slices.Equal(required, []string{"claim"}) {
		t.Errorf("storage requires %v, want the claim every kind names", required)
	}
	fields := storage.(map[string]any)["properties"].(map[string]any)
	if len(fields) != 2 || fields["claim"] == nil || fields["root"] == nil {
		t.Errorf("storage holds %v, want the claim and the root alone", slices.Sorted(maps.Keys(fields)))
	}
}

// The art claim is required inside the franchises block, so a franchises
// library that names none is refused at apply.
func TestTheSchemaRequiresTheArtClaimOfAFranchisesLibrary(t *testing.T) {
	franchises := schemaField(t, librarySchema(t), "schema", "openAPIV3Schema", "properties",
		"spec", "properties", "franchises")

	if required := requiredOf(t, franchises); !slices.Equal(required, []string{"art"}) {
		t.Errorf("the franchises block requires %v, want the art block", required)
	}
	art := franchises.(map[string]any)["properties"].(map[string]any)["art"]
	if required := requiredOf(t, art); !slices.Equal(required, []string{"claim"}) {
		t.Errorf("art requires %v, want the claim", required)
	}
}

// requiredOf is the fields one object of the schema requires, in the order it
// names them.
func requiredOf(t *testing.T, field any) []string {
	t.Helper()
	held, _ := field.(map[string]any)["required"].([]any)
	names := []string{}
	for _, name := range held {
		names = append(names, name.(string))
	}
	return names
}

// The kind rule names one clause per settings block, and a franchises library
// names the art claim beside it.
func TestTheSchemaTiesTheKindToItsBlockAndItsArtClaim(t *testing.T) {
	spec := schemaField(t, librarySchema(t), "schema", "openAPIV3Schema", "properties", "spec")

	rules := rulesOf(t, spec)
	for _, want := range []string{
		"has(self.movies) == (self.kind == 'movies') && has(self.series) == (self.kind == 'series') && has(self.franchises) == (self.kind == 'franchises')",
		"self.kind != 'franchises' || (has(self.franchises) && has(self.franchises.art))",
	} {
		if !slices.Contains(rules, want) {
			t.Errorf("the spec rules are %v, want %q among them", rules, want)
		}
	}
}

// The spec the API server admits is the one the operator reads back: a
// franchises library carries a storage claim for the checkout and an art
// claim of its own.
func TestTheOperatorReadsAFranchisesSpecTheSchemaAdmits(t *testing.T) {
	spec := LibrarySpec{}
	body := `{"kind":"franchises","franchises":{"art":{"claim":"franchise-art"}},` +
		`"storage":{"claim":"franchises","root":"/"}}`
	if err := json.Unmarshal([]byte(body), &spec); err != nil {
		t.Fatal(err)
	}

	if spec.Storage.Claim != "franchises" || spec.Franchises.Art.Claim != "franchise-art" {
		t.Errorf("spec = %+v, want the checkout claim beside the art claim", spec)
	}
	if spec.screenClaim() != "franchise-art" {
		t.Errorf("screenClaim = %q, want the art claim", spec.screenClaim())
	}
}
