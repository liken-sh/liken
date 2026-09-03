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
