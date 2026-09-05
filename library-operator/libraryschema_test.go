package main

// What these tests read: the Library CRD as the cluster applies it,
// against the operator that reconciles it. The two hold one vocabulary,
// so a spec the API server admits is a spec this operator can serve.

import (
	"encoding/json"
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
			Franchises: &LibrarySettings{}}).settings(); settings == nil {
			t.Errorf("the operator resolves no settings block for the kind %s", name)
		}
	}
	if len(kinds) != 3 {
		t.Errorf("the enum names %v, want the three kinds the operator serves", kinds)
	}
}

// Every kind names a claim, so the storage block requires it. The git block is
// the franchises addition beside the claim, and it requires a url and a ref.
func TestTheSchemaTakesAClaimForEveryKind(t *testing.T) {
	storage := schemaField(t, librarySchema(t), "schema", "openAPIV3Schema", "properties",
		"spec", "properties", "storage")

	if required := requiredOf(t, storage); !slices.Equal(required, []string{"claim"}) {
		t.Errorf("storage requires %v, want the claim every kind names", required)
	}
	git := storage.(map[string]any)["properties"].(map[string]any)["git"]
	if required := requiredOf(t, git); !slices.Equal(required, []string{"url", "ref"}) {
		t.Errorf("git requires %v, want the url and the ref", required)
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

// The kind rule names one clause per settings block. A franchises library
// names a git repository beside its claim, and every other kind names a claim
// alone.
func TestTheSchemaTiesTheKindToItsBlockAndItsStorage(t *testing.T) {
	spec := schemaField(t, librarySchema(t), "schema", "openAPIV3Schema", "properties", "spec")

	rules := rulesOf(t, spec)
	for _, want := range []string{
		"has(self.movies) == (self.kind == 'movies') && has(self.series) == (self.kind == 'series') && has(self.franchises) == (self.kind == 'franchises')",
		"has(self.storage.git) == (self.kind == 'franchises')",
	} {
		if !slices.Contains(rules, want) {
			t.Errorf("the spec rules are %v, want %q among them", rules, want)
		}
	}
}

// The storage the API server admits is the one the operator reads back: a
// franchises library carries a claim, a url, and a ref.
func TestTheOperatorReadsAGitStorageTheSchemaAdmits(t *testing.T) {
	spec := LibrarySpec{}
	body := `{"kind":"franchises","franchises":{},` +
		`"storage":{"claim":"franchise-art","root":"/",` +
		`"git":{"url":"https://tangled.org/guid.foo/fiction-franchises","ref":"main"}}}`
	if err := json.Unmarshal([]byte(body), &spec); err != nil {
		t.Fatal(err)
	}

	if !spec.fromGit() {
		t.Fatalf("spec.fromGit() = false, want a library that reads a repository")
	}
	if spec.Storage.Git.Ref != "main" || spec.Storage.Claim != "franchise-art" {
		t.Errorf("storage = %+v, want the ref main beside the claim", spec.Storage)
	}
}
