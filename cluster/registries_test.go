package cluster

import (
	"os"
	"slices"
	"strings"
	"testing"

	"sigs.k8s.io/yaml"
)

func TestParseClusterRegistries(t *testing.T) {
	c, err := ParseCluster([]byte(`
apiVersion: liken.sh/v1alpha1
kind: Cluster
metadata:
  name: lab
spec:
  registries:
    mirrors:
      docker.io:
        - https://mirror.example:5000
        - http://10.10.0.100:5000
    embedded: true
`))
	if err != nil {
		t.Fatal(err)
	}
	endpoints := c.Spec.Registries.Mirrors["docker.io"]
	if len(endpoints) != 2 || endpoints[0] != "https://mirror.example:5000" {
		t.Errorf("mirrors did not round-trip: %v", c.Spec.Registries.Mirrors)
	}
	if !c.Spec.Registries.Embedded {
		t.Error("embedded did not round-trip")
	}
}

func TestParseClusterRegistriesNullIsAbsent(t *testing.T) {
	// registries: null decodes to the zero struct, which genuinely
	// means "no registries configuration". This differs from a null
	// feature, where null could be mistaken for an opt-in.
	c, err := ParseCluster([]byte(`
apiVersion: liken.sh/v1alpha1
kind: Cluster
metadata:
  name: lab
spec:
  registries: null
`))
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Spec.Registries.Mirrors) != 0 || c.Spec.Registries.Embedded {
		t.Errorf("null registries should parse as the zero value: %+v", c.Spec.Registries)
	}
}

func TestValidateRegistriesRefusals(t *testing.T) {
	cases := map[string]struct {
		manifest string
		wants    string
	}{
		"null endpoint list": {
			manifest: `
spec:
  registries:
    mirrors:
      docker.io: null
`,
			wants: "docker.io",
		},
		"empty endpoint list": {
			manifest: `
spec:
  registries:
    mirrors:
      docker.io: []
`,
			wants: "docker.io",
		},
		"endpoint without a scheme": {
			manifest: `
spec:
  registries:
    mirrors:
      docker.io:
        - mirror.example:5000
`,
			wants: "http",
		},
		"endpoint with a bad scheme": {
			manifest: `
spec:
  registries:
    mirrors:
      docker.io:
        - ftp://mirror.example:5000
`,
			wants: "http",
		},
		"empty mirror host": {
			manifest: `
spec:
  registries:
    mirrors:
      "":
        - https://mirror.example:5000
`,
			wants: "host",
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			doc := "apiVersion: liken.sh/v1alpha1\nkind: Cluster\nmetadata:\n  name: lab\n" + tc.manifest
			_, err := ParseCluster([]byte(doc))
			if err == nil {
				t.Fatalf("expected a validation error")
			}
			if !strings.Contains(err.Error(), tc.wants) {
				t.Errorf("error %q should mention %q", err, tc.wants)
			}
		})
	}
}

// The registries schema's pruning defense uses the same
// nullable-plus-rule pair that the features map introduced first,
// and this test pins it the same way. A well-meaning cleanup that
// drops either half would turn `docker.io:` in hand-written YAML
// into a quiet nothing. The dyn() cast in the rule matters just as
// much. CEL types a nullable array as list(string) and refuses a
// bare null comparison at compile time, so removing the cast would
// break the CRD at apply time (the scratch drill caught this).
func TestClusterCRDRegistriesSchema(t *testing.T) {
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
									Registries struct {
										Properties struct {
											Mirrors struct {
												Validations []struct {
													Rule string `json:"rule"`
												} `json:"x-kubernetes-validations"`
												AdditionalProperties struct {
													Type     string `json:"type"`
													Nullable bool   `json:"nullable"`
												} `json:"additionalProperties"`
											} `json:"mirrors"`
											Embedded struct {
												Type string `json:"type"`
											} `json:"embedded"`
										} `json:"properties"`
									} `json:"registries"`
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
	registries := crd.Spec.Versions[0].Schema.OpenAPIV3Schema.Properties.Spec.Properties.Registries

	mirrors := registries.Properties.Mirrors
	if mirrors.AdditionalProperties.Type != "array" || !mirrors.AdditionalProperties.Nullable {
		t.Errorf("mirror values must be nullable arrays so a null survives to validation, got %+v",
			mirrors.AdditionalProperties)
	}
	wantRule := "self.all(host, dyn(self[host]) != null && self[host].size() > 0)"
	var rules []string
	for _, v := range mirrors.Validations {
		rules = append(rules, v.Rule)
	}
	if !slices.Contains(rules, wantRule) {
		t.Errorf("the CRD must refuse null and empty endpoint lists by rule %q; its rules are %q", wantRule, rules)
	}

	if registries.Properties.Embedded.Type != "boolean" {
		t.Errorf("embedded must be a boolean, got %+v", registries.Properties.Embedded)
	}
}
