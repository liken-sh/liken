package machine

import (
	"os"
	"regexp"
	"strings"
	"testing"

	"sigs.k8s.io/yaml"
)

// readNodeLabels reads the schema of spec.nodeLabels from the Machine
// CRD.
func readNodeLabels(t *testing.T) crdMapField {
	t.Helper()
	raw, err := os.ReadFile("manifests/machines-crd.yaml")
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
		t.Fatal(err)
	}
	return crd.Spec.Versions[0].Schema.OpenAPIV3Schema.Properties.Spec.Properties.NodeLabels
}

// reservedLabelPattern pulls the regular expression out of the rule
// that keeps the operating system's own label space. The rule has the
// form self.all(key, !key.matches('...')), so a key that the pattern
// matches is a key that the API server refuses.
func reservedLabelPattern(t *testing.T) *regexp.Regexp {
	t.Helper()
	for _, v := range readNodeLabels(t).Validations {
		if !strings.Contains(v.Message, "liken.sh") {
			continue
		}
		_, rest, found := strings.Cut(v.Rule, "!key.matches('")
		if !found {
			t.Fatalf("the liken.sh rule must refuse the keys that one pattern matches: %q", v.Rule)
		}
		expression, _, _ := strings.Cut(rest, "')")
		return regexp.MustCompile(expression)
	}
	t.Fatal("spec.nodeLabels must reserve the liken.sh label space")
	return nil
}

// The operating system sets its own labels in liken.sh/ itself, so a
// Machine cannot declare a key there. Each operator owns a subdomain
// of liken.sh, and its DaemonSet stays off a node labeled
// <group>/<hardware>: none. The Machine is the record of the machine,
// so a Machine must be able to declare that label.
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

// The label that keeps equipment-operator off a node with no CEC
// adapter passes the key shape rule, the kubernetes.io rule, and the
// value pattern too, so a Machine that declares it is accepted. The
// kubernetes.io rule passes any key that its first pattern does not
// match.
func TestMachineCRDAcceptsAnOperatorsNoneLabel(t *testing.T) {
	labels := readNodeLabels(t)
	if !keyRulePattern(t, labels.Validations).MatchString("equipment.liken.sh/cec") {
		t.Error("equipment.liken.sh/cec is a well-formed label key")
	}
	for _, v := range labels.Validations {
		if !strings.Contains(v.Message, "kubernetes.io") {
			continue
		}
		if keyRulePattern(t, []crdValidation{v}).MatchString("equipment.liken.sh/cec") {
			t.Error("equipment.liken.sh/cec is not in the kubernetes.io or k8s.io namespaces")
		}
	}
	if !regexp.MustCompile(labels.AdditionalProperties.Pattern).MatchString("none") {
		t.Error("none is a well-formed label value")
	}
}
