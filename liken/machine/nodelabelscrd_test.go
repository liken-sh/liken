package machine

import (
	"os"
	"regexp"
	"strings"
	"testing"

	"sigs.k8s.io/yaml"
)

func readNodeLabels(t *testing.T) crdMapField {
	t.Helper()
	raw, err := os.ReadFile("manifests/machines-crd.yaml")
	if err != nil {
		t.Fatalf("reading the Machine CRD: %v", err)
	}
	var crd struct {
		Spec struct {
			Versions []struct {
				Schema struct {
					OpenAPIV3Schema struct {
						Properties struct {
							Spec struct {
								Properties struct {
									NodeLabels crdMapField `json:"nodeLabels"`
								} `json:"properties"`
							} `json:"spec"`
						} `json:"properties"`
					} `json:"openAPIV3Schema"`
				} `json:"schema"`
			} `json:"versions"`
		} `json:"spec"`
	}
	if err := yaml.Unmarshal(raw, &crd); err != nil {
		t.Fatalf("parsing the Machine CRD: %v", err)
	}
	return crd.Spec.Versions[0].Schema.OpenAPIV3Schema.Properties.Spec.Properties.NodeLabels
}

// reservedLabelPattern returns the regular expression of the rule
// self.all(key, !key.matches('...')), which refuses the label keys
// under liken.sh/.
func reservedLabelPattern(t *testing.T) *regexp.Regexp {
	t.Helper()
	for _, v := range readNodeLabels(t).Validations {
		if !strings.Contains(v.Message, "liken.sh") {
			continue
		}
		_, rest, found := strings.Cut(v.Rule, "!key.matches('")
		if !found {
			t.Fatalf("the rule is %q, want one that refuses the keys that a single pattern matches", v.Rule)
		}
		expression, _, _ := strings.Cut(rest, "')")
		return regexp.MustCompile(expression)
	}
	t.Fatal("spec.nodeLabels has no rule that names liken.sh, want one that reserves its label keys")
	return nil
}

// The OS sets the labels under liken.sh/, so a Machine cannot declare
// one. An operator's DaemonSet stays off a node labeled
// <group>/<hardware>: none, where the group is a subdomain of
// liken.sh, and a Machine must be able to declare that label.
func TestMachineCRDReservesOnlyTheOperatingSystemsLabels(t *testing.T) {
	pattern := reservedLabelPattern(t)
	cases := []struct {
		key     string
		refused bool
	}{
		{"liken.sh/machine", true},
		{"liken.sh/node-labels", true},
		{"equipment.liken.sh/cec", false},
		{"bluetooth.liken.sh/bluetooth", false},
		{"audio.liken.sh/sound-card", false},
		{"guid.foo/gpu", false},
		{"notliken.sh/x", false},
	}
	for _, tc := range cases {
		t.Run(tc.key, func(t *testing.T) {
			if got := pattern.MatchString(tc.key); got != tc.refused {
				t.Errorf("%q refused = %v, want %v", tc.key, got, tc.refused)
			}
		})
	}
}

// equipment.liken.sh/cec: none passes the key shape rule, the
// kubernetes.io rule, and the value pattern of spec.nodeLabels. The
// kubernetes.io rule refuses a key that its first pattern matches.
func TestMachineCRDAcceptsAnOperatorsNoneLabel(t *testing.T) {
	labels := readNodeLabels(t)
	if !keyRulePattern(t, labels.Validations).MatchString("equipment.liken.sh/cec") {
		t.Error("the key rule refuses equipment.liken.sh/cec, want it accepted")
	}
	for _, v := range labels.Validations {
		if !strings.Contains(v.Message, "kubernetes.io") {
			continue
		}
		if keyRulePattern(t, []crdValidation{v}).MatchString("equipment.liken.sh/cec") {
			t.Error("the kubernetes.io rule refuses equipment.liken.sh/cec, want it accepted")
		}
	}
	if !regexp.MustCompile(labels.AdditionalProperties.Pattern).MatchString("none") {
		t.Error("the value pattern refuses none, want it accepted")
	}
}
