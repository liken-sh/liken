package main

import (
	"os"
	"strings"
	"testing"

	"sigs.k8s.io/yaml"
)

// maxNameLength is the longest name a Player or a Remote may have.
// library-operator also builds names from a Player's name, and its tests
// check the same limit.
const maxNameLength = 32

// nameRule returns the first validation rule at the root of the CRD's
// schema, read from the file at path.
func nameRule(t *testing.T, path string) string {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading the CRD %s: %v", path, err)
	}
	var crd struct {
		Spec struct {
			Versions []struct {
				Schema struct {
					OpenAPIV3Schema struct {
						Validations []struct {
							Rule string `json:"rule"`
						} `json:"x-kubernetes-validations"`
					} `json:"openAPIV3Schema"`
				} `json:"schema"`
			} `json:"versions"`
		} `json:"spec"`
	}
	if err := yaml.Unmarshal(body, &crd); err != nil {
		t.Fatalf("parsing the CRD %s: %v", path, err)
	}
	validations := crd.Spec.Versions[0].Schema.OpenAPIV3Schema.Validations
	if len(validations) == 0 {
		t.Fatalf("the CRD %s has no validation rule at the root of its schema, want %q", path, "size(self.metadata.name) <= 32")
	}
	return validations[0].Rule
}

func TestEachCRDCapsItsName(t *testing.T) {
	for _, path := range []string{"deploy/players-crd.yaml", "deploy/remotes-crd.yaml"} {
		if rule := nameRule(t, path); rule != "size(self.metadata.name) <= 32" {
			t.Errorf("the first rule of %s is %q, want a name cap of %d", path, rule, maxNameLength)
		}
	}
}

// The device request name in a Remote's ResourceClaim is built from the
// Remote's name. It must stay a valid DNS label, at most 63 characters,
// for the longest name the CRD admits.
func TestTheLongestRemoteNameLeavesItsRequestValid(t *testing.T) {
	request := remoteRequestName(strings.Repeat("n", maxNameLength))

	if len(request) > 63 {
		t.Errorf("the request name %s is %d characters, want at most 63 for a DNS label", request, len(request))
	}
}
