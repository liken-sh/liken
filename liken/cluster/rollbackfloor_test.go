package cluster

import (
	"os"
	"regexp"
	"testing"

	"sigs.k8s.io/yaml"
)

// rollbackFloor is the oldest release that spec.version may name.
// 2026.09.28-001 is the first release whose cluster-operator manifest
// grants the create and update of the cluster operator's leader
// election Lease. A copy that the API server refuses on that Lease
// waits, so a fleet on an older release has no acting cluster operator.
const rollbackFloor = "2026.09.28-001"

// versionRules reads the CEL rules on spec.version from the Cluster
// CRD.
func versionRules(t *testing.T) []string {
	t.Helper()
	raw, err := os.ReadFile("manifests/clusters-crd.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var crd struct {
		Spec struct {
			Versions []struct {
				Schema struct {
					OpenAPIV3Schema struct {
						Properties struct {
							Spec struct {
								Properties struct {
									Version struct {
										Validations []struct {
											Rule string `json:"rule"`
										} `json:"x-kubernetes-validations"`
									} `json:"version"`
								} `json:"properties"`
							} `json:"spec"`
						} `json:"properties"`
					} `json:"openAPIV3Schema"`
				} `json:"schema"`
			} `json:"versions"`
		} `json:"spec"`
	}
	if err := yaml.Unmarshal(raw, &crd); err != nil {
		t.Fatal(err)
	}
	var rules []string
	for _, v := range crd.Spec.Versions[0].Schema.OpenAPIV3Schema.Properties.Spec.Properties.Version.Validations {
		rules = append(rules, v.Rule)
	}
	return rules
}

// The CRD carries the floor as the one rule on spec.version, a string
// comparison with the floor's version.
func TestTheClusterCRDNamesTheRollbackFloor(t *testing.T) {
	rules := versionRules(t)

	if len(rules) != 1 || rules[0] != "self >= '"+rollbackFloor+"'" {
		t.Errorf("rules on spec.version = %q, want only self >= '%s'", rules, rollbackFloor)
	}
}

// The API server compares the strings, and the fixed-width grammar
// makes string order the version order, so the rule admits the floor
// and every later release, and refuses every earlier one.
func TestTheRollbackFloorAdmitsOnlyItsReleaseAndLater(t *testing.T) {
	cases := []struct {
		version  string
		admitted bool
	}{
		{"2026.07.11-001", false},
		{"2026.09.27-001", false},
		{"2026.09.28-001", true},
		{"2026.09.28-002", true},
		{"2026.10.10-001", true},
		{"2027.01.01-001", true},
	}
	floor := regexp.MustCompile(`^self >= '(.+)'$`).FindStringSubmatch(versionRules(t)[0])[1]
	for _, c := range cases {
		t.Run(c.version, func(t *testing.T) {
			if admitted := c.version >= floor; admitted != c.admitted {
				t.Errorf("admitted = %v, want %v", admitted, c.admitted)
			}
		})
	}
}
