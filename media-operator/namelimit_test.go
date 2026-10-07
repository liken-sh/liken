package main

// What these tests read: the cap the Player and Remote CRDs put on a
// name, against the longest name this operator builds from it.

import (
	"os"
	"strings"
	"testing"

	"sigs.k8s.io/yaml"
)

// The longest name a Player or a Remote may take. library-operator also
// builds names from a Player's, and its own tests hold them to the same
// cap.
const maxNameLength = 32

// The first rule at the root of one CRD's schema.
func nameRule(t *testing.T, path string) string {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
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
		t.Fatal(err)
	}
	validations := crd.Spec.Versions[0].Schema.OpenAPIV3Schema.Validations
	if len(validations) == 0 {
		t.Fatalf("%s holds no rule at the root of its schema", path)
	}
	return validations[0].Rule
}

// The Player and Remote CRDs cap a name at maxNameLength.
func TestEachCRDCapsItsName(t *testing.T) {
	for _, path := range []string{"deploy/players-crd.yaml", "deploy/remotes-crd.yaml"} {
		if rule := nameRule(t, path); rule != "size(self.metadata.name) <= 32" {
			t.Errorf("%s's first rule is %q, want the cap of %d", path, rule, maxNameLength)
		}
	}
}

// The longest Remote name the CRD admits leaves the device request in its
// ResourceClaim a valid DNS label of at most 63 characters.
func TestTheLongestRemoteNameLeavesItsRequestValid(t *testing.T) {
	request := remoteRequestName(strings.Repeat("n", maxNameLength))

	if len(request) > 63 {
		t.Errorf("%s is %d characters, past the 63 a DNS label holds", request, len(request))
	}
}
