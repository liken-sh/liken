package main

// What these tests read: the cap each CRD puts on a name, against the
// longest name the operator builds from it.

import (
	"fmt"
	"os"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// The first rule at the root of one CRD's schema.
func nameRule(t *testing.T, path string) string {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	document := map[string]any{}
	if err := yaml.Unmarshal(body, &document); err != nil {
		t.Fatal(err)
	}
	version := document["spec"].(map[string]any)["versions"].([]any)[0].(map[string]any)
	rules := schemaField(t, version, "schema", "openAPIV3Schema", "x-kubernetes-validations").([]any)
	return rules[0].(map[string]any)["rule"].(string)
}

// Each CRD whose name the operator builds names from caps the name.
func TestEachCRDCapsItsName(t *testing.T) {
	want := fmt.Sprintf("size(self.metadata.name) <= %d", maxNameLength)
	for _, path := range []string{"deploy/libraries-crd.yaml", "deploy/catalogs-crd.yaml",
		"deploy/metadataproviders-crd.yaml"} {
		if rule := nameRule(t, path); rule != want {
			t.Errorf("%s's first rule is %q, want %q", path, rule, want)
		}
	}
}

// The longest Catalog, MetadataProvider, and Player names the CRDs
// admit give every name built from them a length Kubernetes accepts: 63
// for a Job, a Service, and a claim whose name a per-node volume's label
// carries.
func TestTheLongestNamesLeaveEveryBuiltNameValid(t *testing.T) {
	name := strings.Repeat("n", maxNameLength)
	built := []string{
		jellyfinBackfillJobName(name), jellyfinName(name),
		catalogStoreName(name), progressStoreName(name),
		datasetsClaimName(name),
		screenClaimName(name), screenArtClaimName(name),
	}
	for _, one := range built {
		if len(one) > 63 {
			t.Errorf("%s is %d characters, past the 63 Kubernetes allows", one, len(one))
		}
	}
}
