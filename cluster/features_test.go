package cluster

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
		if f.Kind != FeatureBundled && f.Kind != FeatureEmbedded &&
			f.Kind != FeatureVendored && f.Kind != FeatureWorkload {
			t.Errorf("feature %q has kind %q", f.Slug, f.Kind)
		}
	}
}

// A feature's requirements must name slugs that the vocabulary
// itself carries. Otherwise the closure in EnabledFeatures would
// enable a feature that no table entry defines.
func TestFeatureRequirementsAreInTheVocabulary(t *testing.T) {
	for _, f := range Features {
		for _, req := range f.Requires {
			if FeatureBySlug(req) == nil {
				t.Errorf("feature %q requires %q, which is not in the vocabulary", f.Slug, req)
			}
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

// The Cluster CRD is hand-written, so its schema can teach the API.
// This means nothing mechanical keeps its feature validation aligned
// with the vocabulary in features.go. This test provides that
// alignment. A slug added to either side without the other fails
// here, in both directions, because the test compares the CEL rule's
// vocabulary list against the table exactly.
func TestClusterCRDMatchesTheVocabulary(t *testing.T) {
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
									Features struct {
										Validations []struct {
											Rule    string `json:"rule"`
											Message string `json:"message"`
										} `json:"x-kubernetes-validations"`
										AdditionalProperties struct {
											Type                 string `json:"type"`
											Nullable             bool   `json:"nullable"`
											AdditionalProperties struct {
												Type string `json:"type"`
											} `json:"additionalProperties"`
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
	// table order. Otherwise an admission error would name a
	// vocabulary that the file parser disagrees with.
	wantRule := fmt.Sprintf("self.all(slug, slug in ['%s'])",
		strings.Join(FeatureSlugs(), "', '"))
	var rules []string
	for _, v := range features.Validations {
		rules = append(rules, v.Rule)
	}
	if !slices.Contains(rules, wantRule) {
		t.Errorf("the CRD's vocabulary rule must be exactly %q; its rules are %q", wantRule, rules)
	}

	// The null refusal depends on two things that work together: a
	// rule that names the mistake, and nullable values. Without
	// nullable values, the decoder drops a null before validation
	// can see it.
	if !slices.Contains(rules, "self.all(slug, dyn(self[slug]) != null)") {
		t.Errorf("the CRD must refuse null feature values by rule; its rules are %q", rules)
	}
	if features.AdditionalProperties.Type != "object" || !features.AdditionalProperties.Nullable {
		t.Errorf("feature values must be nullable objects so a null survives to validation, got %+v",
			features.AdditionalProperties)
	}

	// A feature's configuration is a map of string parameters, never
	// a schema of named properties. Map keys are never pruned, so a
	// guessed parameter survives to validation, and the rules below
	// refuse it by name instead of quietly flipping the feature on
	// as {}.
	if features.AdditionalProperties.AdditionalProperties.Type != "string" {
		t.Errorf("feature parameters must be a map of strings, got %+v",
			features.AdditionalProperties)
	}

	// A parameterless feature's configuration must stay exactly {}.
	// This rule is built from the table, so a feature that grows
	// parameters must declare them in Params, in the same change.
	var parameterized []string
	for _, f := range Features {
		if len(f.Params) > 0 {
			parameterized = append(parameterized, f.Slug)
		}
	}
	wantEmptyRule := fmt.Sprintf(
		"self.all(slug, dyn(self[slug]) == null || slug in ['%s'] || self[slug].size() == 0)",
		strings.Join(parameterized, "', '"))
	if !slices.Contains(rules, wantEmptyRule) {
		t.Errorf("the CRD must hold parameterless features to {} with exactly %q; its rules are %q",
			wantEmptyRule, rules)
	}

	// Each parameterized feature gets its own shape rule, holding
	// its parameters to the table's names.
	for _, f := range Features {
		if len(f.Params) == 0 {
			continue
		}
		wantShapeRule := fmt.Sprintf(
			"!('%[1]s' in self) || dyn(self['%[1]s']) == null || ('%[2]s' in self['%[1]s'] && self['%[1]s'].all(param, param in ['%[3]s']))",
			f.Slug, f.Params[0], strings.Join(f.Params, "', '"))
		if !slices.Contains(rules, wantShapeRule) {
			t.Errorf("the CRD must hold %s's parameters to the table with exactly %q; its rules are %q",
				f.Slug, wantShapeRule, rules)
		}
	}
}

func TestFluxConfigIsAbsentUntilDeclared(t *testing.T) {
	var none *Cluster
	if cfg, err := none.FluxConfig(); cfg != nil || err != nil {
		t.Errorf("nil cluster: got %v, %v", cfg, err)
	}
	lean := &Cluster{}
	if cfg, err := lean.FluxConfig(); cfg != nil || err != nil {
		t.Errorf("undeclared: got %v, %v", cfg, err)
	}
}

func TestFluxConfigAppliesDefaults(t *testing.T) {
	c := &Cluster{Spec: ClusterSpec{Features: map[string]*FeatureConfig{
		"flux": {"repository": "ssh://git@forge.example/fleet.git"},
	}}}
	cfg, err := c.FluxConfig()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Repository != "ssh://git@forge.example/fleet.git" {
		t.Errorf("repository: got %q", cfg.Repository)
	}
	if cfg.Path != "." || cfg.Branch != "main" {
		t.Errorf("defaults: got path %q, branch %q", cfg.Path, cfg.Branch)
	}
}

func TestFluxConfigReadsEveryParameter(t *testing.T) {
	c := &Cluster{Spec: ClusterSpec{Features: map[string]*FeatureConfig{
		"flux": {
			"repository": "ssh://git@forge.example/fleet.git",
			"path":       "clusters/lab",
			"branch":     "trunk",
			"knownHosts": "forge.example ssh-ed25519 AAAA",
		},
	}}}
	cfg, err := c.FluxConfig()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Path != "clusters/lab" || cfg.Branch != "trunk" {
		t.Errorf("got path %q, branch %q", cfg.Path, cfg.Branch)
	}
	if cfg.KnownHosts != "forge.example ssh-ed25519 AAAA" {
		t.Errorf("got knownHosts %q", cfg.KnownHosts)
	}
}

func TestFluxConfigRequiresARepository(t *testing.T) {
	for name, features := range map[string]map[string]*FeatureConfig{
		"empty configuration": {"flux": {}},
		"null configuration":  {"flux": nil},
		"empty repository":    {"flux": {"repository": ""}},
	} {
		c := &Cluster{Spec: ClusterSpec{Features: features}}
		if _, err := c.FluxConfig(); err == nil ||
			!strings.Contains(err.Error(), "repository") {
			t.Errorf("%s: the error must name the missing parameter, got %v", name, err)
		}
	}
}

func TestFluxConfigRefusesANonStringParameter(t *testing.T) {
	c := &Cluster{Spec: ClusterSpec{Features: map[string]*FeatureConfig{
		"flux": {"repository": 7},
	}}}
	if _, err := c.FluxConfig(); err == nil ||
		!strings.Contains(err.Error(), "repository") {
		t.Errorf("the error must name the parameter, got %v", err)
	}
}

// An unknown parameter has the same two causes as an unknown slug: a
// newer vocabulary defined it, or a hand-written seed misspelled it.
// The error must name both, because the reader cannot tell which one
// happened from where they sit.
func TestValidateFeatureParamsNamesBothCauses(t *testing.T) {
	def := FeatureBySlug("flux")
	err := def.ValidateParams(&FeatureConfig{
		"repository": "ssh://git@forge.example/fleet.git",
		"pathh":      "clusters/lab",
	})
	if err == nil || !strings.Contains(err.Error(), "pathh") ||
		!strings.Contains(err.Error(), "misspelling") {
		t.Errorf("the error must name the parameter and both causes, got %v", err)
	}
}

func TestValidateFeatureParamsHoldsParameterlessFeaturesToEmpty(t *testing.T) {
	def := FeatureBySlug("metrics-server")
	if err := def.ValidateParams(&FeatureConfig{"replicas": 2}); err == nil {
		t.Error("a parameterless feature must refuse any parameter")
	}
	if err := def.ValidateParams(&FeatureConfig{}); err != nil {
		t.Errorf("{} is every feature's zero configuration: %v", err)
	}
	if err := def.ValidateParams(nil); err != nil {
		t.Errorf("an absent configuration validates like {}: %v", err)
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
	// traefik requires helm, so helm joins the enabled set without
	// being declared.
	if got := c.EnabledFeatures(); !slices.Equal(got, []string{"helm", "metrics-server", "traefik"}) {
		t.Errorf("got %v", got)
	}
}

func TestFeatureEnabled(t *testing.T) {
	var none *Cluster
	if none.FeatureEnabled("helm") {
		t.Error("a nil cluster enables nothing")
	}
	lean := &Cluster{}
	if lean.FeatureEnabled("helm") {
		t.Error("no features means no helm")
	}
	explicit := &Cluster{Spec: ClusterSpec{Features: map[string]*FeatureConfig{"helm": {}}}}
	if !explicit.FeatureEnabled("helm") {
		t.Error("helm can be enabled on its own")
	}
	implied := &Cluster{Spec: ClusterSpec{Features: map[string]*FeatureConfig{"traefik": {}}}}
	if !implied.FeatureEnabled("helm") {
		t.Error("traefik requires helm, so declaring traefik enables it")
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
