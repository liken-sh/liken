package main

// Tests for the feature pass: what init reports about the cluster's
// opt-ins. The disable-list rendering the bundled features act
// through is pinned in k3s_test.go.

import (
	"testing"

	"github.com/chrisguidry/liken/machine"
)

func TestActuateFeaturesWithNoClusterReportsNothing(t *testing.T) {
	if got := actuateFeatures(nil); got != nil {
		t.Errorf("a machine alone enables nothing: %v", got)
	}
}

func TestActuateFeaturesWithNoFeaturesReportsNothing(t *testing.T) {
	if got := actuateFeatures(labCluster()); got != nil {
		t.Errorf("a cluster with no opt-ins reports nothing: %v", got)
	}
}

func TestActuateFeaturesReportsBundledFeaturesActive(t *testing.T) {
	c := labCluster()
	c.Spec.Features = map[string]*machine.FeatureConfig{
		"metrics-server": {},
		"traefik":        {},
	}
	got := actuateFeatures(c)
	if len(got) != 2 {
		t.Fatalf("expected two feature statuses, got %v", got)
	}
	// Sorted by slug, like every rendering of the same spec.
	if got[0].Name != "metrics-server" || got[1].Name != "traefik" {
		t.Errorf("expected sorted slugs, got %v", got)
	}
	for _, s := range got {
		if s.State != machine.FeatureActive || s.Message != "" {
			t.Errorf("a bundled feature has nothing that can be missing: %+v", s)
		}
	}
}
