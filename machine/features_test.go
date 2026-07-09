package machine

import (
	"maps"
	"os"
	"slices"
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
// which means nothing mechanical keeps its feature properties aligned
// with the vocabulary in features.go. This test is that alignment: a
// slug added to either side without the other fails here, in both
// directions.
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
										Properties map[string]struct {
											Type     string `json:"type"`
											Nullable bool   `json:"nullable"`
										} `json:"properties"`
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
	declared := crd.Spec.Versions[0].Schema.OpenAPIV3Schema.Properties.Spec.Properties.Features.Properties

	want := FeatureSlugs()
	slices.Sort(want)
	got := slices.Sorted(maps.Keys(declared))
	if !slices.Equal(got, want) {
		t.Errorf("CRD feature properties %v, vocabulary %v", got, want)
	}

	// Null rejection at admission depends on every feature being a
	// non-nullable object in the structural schema.
	for slug, schema := range declared {
		if schema.Type != "object" || schema.Nullable {
			t.Errorf("feature %q must be a non-nullable object, got type=%q nullable=%v",
				slug, schema.Type, schema.Nullable)
		}
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
