package main

// Tests for the feature pass: what init reports about the cluster's
// opt-ins, and what actuating a vendored payload does. The
// disable-list rendering the bundled features act through is pinned
// in k3s_test.go. Module verdicts use a fabricated tree the way the
// module tests do, leaning on builtins, because only a real kernel
// can truly load anything.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chrisguidry/liken/machine"
)

func TestActuateFeaturesWithNoClusterReportsNothing(t *testing.T) {
	if got := actuateFeatures(nil, "node-1"); got != nil {
		t.Errorf("a machine alone enables nothing: %v", got)
	}
}

func TestActuateFeaturesWithNoFeaturesReportsNothing(t *testing.T) {
	if got := actuateFeatures(labCluster(), "node-1"); got != nil {
		t.Errorf("a cluster with no opt-ins reports nothing: %v", got)
	}
}

func TestActuateFeaturesReportsBundledFeaturesActive(t *testing.T) {
	c := labCluster()
	c.Spec.Features = map[string]*machine.FeatureConfig{
		"metrics-server": {},
		"traefik":        {},
	}
	got := actuateFeatures(c, "node-1")
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

// featureFixture points the package's path variables at temporary
// directories and builds a module tree where the feature's one module
// is compiled into the kernel, so the healthy path needs no syscalls.
// It returns the fabricated module tree's base.
func featureFixture(t *testing.T) string {
	t.Helper()
	featuresDir = t.TempDir()
	k3sManifestsDir = filepath.Join(t.TempDir(), "manifests")
	iscsiDir = filepath.Join(t.TempDir(), "iscsi")
	t.Cleanup(func() {
		featuresDir = "/etc/liken/features"
		k3sManifestsDir = "/var/lib/rancher/k3s/server/manifests"
		iscsiDir = "/etc/iscsi"
	})

	base := t.TempDir()
	writeTreeFile(t, filepath.Join(base, "modules.dep"), "")
	writeTreeFile(t, filepath.Join(base, "modules.builtin"), "kernel/drivers/scsi/iscsi_tcp.ko\n")
	return base
}

func writeTreeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestVendoredFeatureMissingFromAnOlderImage(t *testing.T) {
	base := featureFixture(t)
	got := actuateVendoredFeature(base, "iscsi", "node-1")
	if got.State != machine.FeatureMissing {
		t.Fatalf("no staged payload means the image predates the feature: %+v", got)
	}
	if !strings.Contains(got.Message, "upgrade to a release") {
		t.Errorf("the message should name the fix: %q", got.Message)
	}
}

func TestVendoredFeatureActuatesEndToEnd(t *testing.T) {
	base := featureFixture(t)
	writeTreeFile(t, filepath.Join(featuresDir, "iscsi", "modules.conf"),
		"# the transport\niscsi_tcp\n")
	writeTreeFile(t, filepath.Join(featuresDir, "iscsi", "manifests", "iscsid.yaml"),
		"kind: DaemonSet\n")

	got := actuateVendoredFeature(base, "iscsi", "node-1")
	if got.State != machine.FeatureActive || got.Message != "" {
		t.Fatalf("got %+v", got)
	}

	iqn, err := os.ReadFile(filepath.Join(iscsiDir, "initiatorname.iscsi"))
	if err != nil {
		t.Fatal(err)
	}
	if string(iqn) != "InitiatorName=iqn.2026-07.sh.liken:node-1\n" {
		t.Errorf("initiator name: got %q", iqn)
	}

	seeded, err := os.ReadFile(filepath.Join(k3sManifestsDir, "iscsid.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if string(seeded) != "kind: DaemonSet\n" {
		t.Errorf("seeded manifest: got %q", seeded)
	}
}

func TestVendoredFeatureFailsWhenAModuleIsUnloadable(t *testing.T) {
	base := featureFixture(t)
	writeTreeFile(t, filepath.Join(featuresDir, "iscsi", "modules.conf"), "not_a_module\n")

	got := actuateVendoredFeature(base, "iscsi", "node-1")
	if got.State != machine.FeatureFailed {
		t.Fatalf("an unloadable module fails the whole feature: %+v", got)
	}
	if !strings.Contains(got.Message, "not_a_module") {
		t.Errorf("the message should name the module: %q", got.Message)
	}
}

func TestVendoredFeatureWithoutManifestsSeedsNothing(t *testing.T) {
	base := featureFixture(t)
	writeTreeFile(t, filepath.Join(featuresDir, "nfs", "modules.conf"), "iscsi_tcp\n")

	got := actuateVendoredFeature(base, "nfs", "node-1")
	if got.State != machine.FeatureActive {
		t.Fatalf("a payload with no workload is still a whole feature: %+v", got)
	}
	if _, err := os.Stat(k3sManifestsDir); !os.IsNotExist(err) {
		t.Errorf("nothing should have been seeded: %v", err)
	}
}
