package machine

import (
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"

	"sigs.k8s.io/yaml"
)

func TestFeatureSlugsAreUnique(t *testing.T) {
	seen := map[string]bool{}
	for _, f := range Features {
		if seen[f.Slug] {
			t.Errorf("slug %q appears twice in the vocabulary", f.Slug)
		}
		seen[f.Slug] = true
	}
}

func TestFeatureKindsAreValid(t *testing.T) {
	for _, f := range Features {
		if f.Kind != FeatureBundled && f.Kind != FeatureVendored {
			t.Errorf("feature %q has kind %q", f.Slug, f.Kind)
		}
	}
}

func TestFeatureBySlug(t *testing.T) {
	if def := FeatureBySlug("traefik"); def == nil || def.Kind != FeatureBundled {
		t.Errorf("traefik: got %+v", def)
	}
	if def := FeatureBySlug("not-a-feature"); def != nil {
		t.Errorf("unknown slug: got %+v", def)
	}
}

// The Cluster CRD is hand-written so its schema can teach the API,
// which means nothing mechanical keeps its feature validation aligned
// with the vocabulary in features.go. This test is that alignment: a
// slug added to either side without the other fails here, in both
// directions, because the CEL rule's vocabulary list is compared
// against the table exactly.
func TestClusterCRDMatchesTheVocabulary(t *testing.T) {
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
									Features struct {
										Validations []struct {
											Rule    string `json:"rule"`
											Message string `json:"message"`
										} `json:"x-kubernetes-validations"`
										AdditionalProperties struct {
											Type          string `json:"type"`
											Nullable      bool   `json:"nullable"`
											MaxProperties *int   `json:"maxProperties"`
											Preserve      bool   `json:"x-kubernetes-preserve-unknown-fields"`
										} `json:"additionalProperties"`
									} `json:"features"`
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
	if len(crd.Spec.Versions) != 1 {
		t.Fatalf("expected one CRD version, got %d", len(crd.Spec.Versions))
	}
	features := crd.Spec.Versions[0].Schema.OpenAPIV3Schema.Properties.Spec.Properties.Features

	// The vocabulary rule must name exactly the table's slugs, in
	// table order, or an admission error would name a vocabulary the
	// file parser disagrees with.
	wantRule := fmt.Sprintf("self.all(slug, slug in ['%s'])",
		strings.Join(FeatureSlugs(), "', '"))
	var rules []string
	for _, v := range features.Validations {
		rules = append(rules, v.Rule)
	}
	if !slices.Contains(rules, wantRule) {
		t.Errorf("the CRD's vocabulary rule must be exactly %q; its rules are %q", wantRule, rules)
	}

	// The null refusal depends on two things standing together: a
	// rule that names the mistake, and nullable values, without which
	// the decoder drops a null before validation can see it.
	if !slices.Contains(rules, "self.all(slug, self[slug] != null)") {
		t.Errorf("the CRD must refuse null feature values by rule; its rules are %q", rules)
	}
	if features.AdditionalProperties.Type != "object" || !features.AdditionalProperties.Nullable {
		t.Errorf("feature values must be nullable objects so a null survives to validation, got %+v",
			features.AdditionalProperties)
	}

	// No feature has parameters yet, and the guard is a pair that
	// stands together: preserving unknown fields stops the API server
	// pruning a guessed parameter (which would quietly flip the
	// feature on as {}), and a maximum of zero properties refuses the
	// preserved value by name. When the first parameter arrives, this
	// assertion is what changes.
	if features.AdditionalProperties.MaxProperties == nil ||
		*features.AdditionalProperties.MaxProperties != 0 ||
		!features.AdditionalProperties.Preserve {
		t.Errorf("feature values must preserve unknown fields and cap properties at zero, got %+v",
			features.AdditionalProperties)
	}
}

func TestEnabledFeatures(t *testing.T) {
	var none *Cluster
	if got := none.EnabledFeatures(); got != nil {
		t.Errorf("nil cluster: got %v", got)
	}
	c := &Cluster{Spec: ClusterSpec{Features: map[string]*FeatureConfig{
		"traefik":        {},
		"metrics-server": {},
	}}}
	if got := c.EnabledFeatures(); !slices.Equal(got, []string{"metrics-server", "traefik"}) {
		t.Errorf("got %v", got)
	}
}

func TestDisabledComponents(t *testing.T) {
	for _, tc := range []struct {
		name    string
		cluster *Cluster
		want    []string
	}{
		{"nil cluster disables everything bundled", nil,
			[]string{"metrics-server", "servicelb", "traefik"}},
		{"no features disables everything bundled", &Cluster{},
			[]string{"metrics-server", "servicelb", "traefik"}},
		{"an opt-in leaves the disable list",
			&Cluster{Spec: ClusterSpec{Features: map[string]*FeatureConfig{"metrics-server": {}}}},
			[]string{"servicelb", "traefik"}},
		{"all opt-ins empty the list",
			&Cluster{Spec: ClusterSpec{Features: map[string]*FeatureConfig{
				"traefik": {}, "servicelb": {}, "metrics-server": {},
			}}},
			nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.cluster.DisabledComponents(); !slices.Equal(got, tc.want) {
				t.Errorf("got %v, want %v", got, tc.want)
			}
		})
	}
}
