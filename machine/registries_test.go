package machine

import (
	"os"
	"path/filepath"
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
	// means "no registries configuration" — unlike a null feature,
	// where null could be mistaken for an opt-in.
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

func TestRenderRegistryCredentialsIsDeterministic(t *testing.T) {
	forward, forwardHash, err := RenderRegistryCredentials([]RegistryCredential{
		{Host: "a.example", Username: "a", Password: "pa"},
		{Host: "b.example", Username: "b", Password: "pb"},
	})
	if err != nil {
		t.Fatal(err)
	}
	backward, backwardHash, err := RenderRegistryCredentials([]RegistryCredential{
		{Host: "b.example", Username: "b", Password: "pb"},
		{Host: "a.example", Username: "a", Password: "pa"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if string(forward) != string(backward) || forwardHash != backwardHash {
		t.Errorf("host order must not change the rendering:\n%s\n%s", forward, backward)
	}
}

func TestRenderRegistryCredentialsEmptyIsARealDocument(t *testing.T) {
	// The empty document is the retraction rendering: a Secret that
	// was deleted stages this, and it must have a real hash so the
	// lifecycle can tell it apart from "never had credentials".
	raw, hash, err := RenderRegistryCredentials(nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) == 0 || hash == "" {
		t.Error("the empty credentials document must still render bytes and a hash")
	}
	parsed, err := ParseRegistryCredentials(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(parsed.Hosts) != 0 {
		t.Errorf("the empty document should carry no hosts: %+v", parsed.Hosts)
	}
}

func TestRegistryCredentialsRoundTrip(t *testing.T) {
	raw, _, err := RenderRegistryCredentials([]RegistryCredential{
		{Host: "mirror.example:5000", Username: "puller", Password: "hunter2"},
	})
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := ParseRegistryCredentials(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(parsed.Hosts) != 1 || parsed.Hosts[0].Host != "mirror.example:5000" ||
		parsed.Hosts[0].Username != "puller" || parsed.Hosts[0].Password != "hunter2" {
		t.Errorf("credentials did not round-trip: %+v", parsed.Hosts)
	}
}

func TestParseRegistryCredentialsRefusals(t *testing.T) {
	cases := map[string]struct {
		raw   string
		wants string
	}{
		"wrong kind": {
			raw:   "apiVersion: liken.sh/v1alpha1\nkind: Cluster\n",
			wants: "RegistryCredentials",
		},
		"unknown field": {
			raw:   "apiVersion: liken.sh/v1alpha1\nkind: RegistryCredentials\nsurprise: true\n",
			wants: "surprise",
		},
		"entry without a host": {
			raw:   "apiVersion: liken.sh/v1alpha1\nkind: RegistryCredentials\nhosts:\n  - username: u\n    password: p\n",
			wants: "host",
		},
		"entry without a username": {
			raw:   "apiVersion: liken.sh/v1alpha1\nkind: RegistryCredentials\nhosts:\n  - host: a.example\n    password: p\n",
			wants: "username",
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := ParseRegistryCredentials([]byte(tc.raw))
			if err == nil {
				t.Fatalf("expected a parse error")
			}
			if !strings.Contains(strings.ToLower(err.Error()), strings.ToLower(tc.wants)) {
				t.Errorf("error %q should mention %q", err, tc.wants)
			}
		})
	}
}

func TestRegistryCredentialsStoreIsItsOwnDirectory(t *testing.T) {
	root := t.TempDir()
	store := RegistryCredentialsStore(root)
	if err := store.WriteStaged([]byte("creds")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "registries", "staged.yaml")); err != nil {
		t.Errorf("the credentials lifecycle should live under registries/: %v", err)
	}
}

// The registries schema's pruning defense is the same
// nullable-plus-rule pair the features map pioneered, and this test
// pins it the same way: a well-meaning cleanup that drops either
// half would turn `docker.io:` in hand-written YAML into a quiet
// nothing. The dyn() cast in the rule is load-bearing too — CEL
// types a nullable array as list(string) and refuses a bare null
// comparison at compile time, so removing the cast would break the
// CRD at apply time (the scratch drill caught this).
func TestClusterCRDRegistriesSchema(t *testing.T) {
	raw, err := os.ReadFile("../manifests/clusters-crd.yaml")
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

func TestCredentialsFilesAreOwnerOnly(t *testing.T) {
	// The staged file carries passwords, and the design leans on
	// WriteDurable's CreateTemp giving 0600: this test is the tripwire
	// if anyone ever "fixes" those modes.
	store := RegistryCredentialsStore(t.TempDir())
	if err := store.WriteStaged([]byte("creds")); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(store.dir, "staged.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("credentials must be owner-only, got %v", info.Mode().Perm())
	}
}
