package main

// Tests for the feature pass: what init reports about the cluster's
// opt-ins, and what actuating a vendored payload does. k3s_test.go
// pins the disable-list rendering that bundled features act through.
// Module verdicts use a fabricated tree, the same as the module
// tests, and lean on builtins, because only a real kernel can
// actually load anything.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/liken-sh/liken/cluster"
	"github.com/liken-sh/liken/machine"
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

func TestActuateFeaturesReportsSlugsFromANewerVocabulary(t *testing.T) {
	c := labCluster()
	c.Spec.Features = map[string]*cluster.FeatureConfig{"from-the-future": {}}
	got := actuateFeatures(c, "node-1")
	if len(got) != 1 || got[0].State != machine.FeatureMissing {
		t.Fatalf("a slug this binary predates reports Missing: %+v", got)
	}
	if !strings.Contains(got[0].Message, "upgrade to a release") ||
		!strings.Contains(got[0].Message, "misspelling") {
		t.Errorf("the message should name both causes: %q", got[0].Message)
	}
}

func TestActuateFeaturesReportsBundledFeaturesActive(t *testing.T) {
	c := labCluster()
	c.Spec.Features = map[string]*cluster.FeatureConfig{
		"metrics-server": {},
		"traefik":        {},
	}
	got := actuateFeatures(c, "node-1")
	// Three statuses come from two declarations: traefik requires
	// helm, and an implied feature reports its standing the same as
	// a declared one, so status.features tells the whole truth about
	// what runs.
	if len(got) != 3 {
		t.Fatalf("expected three feature statuses, got %v", got)
	}
	// Sorted by slug, the same as every rendering of the same spec.
	if got[0].Name != "helm" || got[1].Name != "metrics-server" || got[2].Name != "traefik" {
		t.Errorf("expected sorted slugs, got %v", got)
	}
	for _, s := range got {
		if s.State != machine.FeatureActive || s.Message != "" {
			t.Errorf("a bundled feature has nothing that can be missing: %+v", s)
		}
	}
}

// featureFixture points the package's path variables at temporary
// directories, and builds a module tree where the feature's one
// module is compiled into the kernel, so the healthy path needs no
// syscalls. It returns the fabricated module tree's base directory.
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

// An unknown parameter on a known feature has the same two causes as
// an unknown slug: a newer vocabulary defined it, or a hand-written
// seed misspelled it. The pass reports it the same way, degraded
// with both causes named, rather than quietly ignoring the key and
// actuating half a declaration.
func TestActuateFeaturesReportsUnknownParameters(t *testing.T) {
	c := labCluster()
	c.Spec.Features = map[string]*cluster.FeatureConfig{
		"metrics-server": {"replicas": "2"},
	}
	got := actuateFeatures(c, "node-1")
	if len(got) != 1 || got[0].State != machine.FeatureFailed {
		t.Fatalf("an unknown parameter reports Failed: %+v", got)
	}
	if !strings.Contains(got[0].Message, "replicas") ||
		!strings.Contains(got[0].Message, "misspelling") {
		t.Errorf("the message should name the parameter and both causes: %q", got[0].Message)
	}
}

func TestWorkloadFeatureSeedsItsManifests(t *testing.T) {
	featureFixture(t)
	writeTreeFile(t, filepath.Join(featuresDir, "flux", "manifests", "flux-system.yaml"),
		"kind: Namespace\n")
	c := labCluster()
	c.Spec.Features = map[string]*cluster.FeatureConfig{
		"flux": {"repository": "ssh://git@forge.example/fleet.git"},
	}
	got := actuateFeatures(c, "node-1")
	if len(got) != 1 || got[0].State != machine.FeatureActive {
		t.Fatalf("a declared workload feature reports Active: %+v", got)
	}
	seeded := filepath.Join(k3sManifestsDir, "flux-system.yaml")
	if _, err := os.Stat(seeded); err != nil {
		t.Errorf("the manifest should be seeded for k3s: %v", err)
	}
}

func TestWorkloadFeatureFailsOnABadConfiguration(t *testing.T) {
	featureFixture(t)
	c := labCluster()
	c.Spec.Features = map[string]*cluster.FeatureConfig{"flux": {}}
	got := actuateFeatures(c, "node-1")
	if len(got) != 1 || got[0].State != machine.FeatureFailed {
		t.Fatalf("flux without a repository reports Failed: %+v", got)
	}
	if !strings.Contains(got[0].Message, "repository") {
		t.Errorf("the message should name the missing parameter: %q", got[0].Message)
	}
	if entries, _ := os.ReadDir(k3sManifestsDir); len(entries) != 0 {
		t.Errorf("a failed feature must not seed workloads: %v", entries)
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

func TestVendoredFeatureFailsOnAnUnreadableModuleList(t *testing.T) {
	base := featureFixture(t)
	conf := filepath.Join(featuresDir, "iscsi", "modules.conf")
	writeTreeFile(t, conf, "iscsi_tcp\n")
	if err := os.Chmod(conf, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(conf, 0o644) })

	got := actuateVendoredFeature(base, "iscsi", "node-1")
	if got.State != machine.FeatureFailed {
		t.Errorf("a payload that can't be read is a failure, not an absence: %+v", got)
	}
}

func TestVendoredFeatureFailsWhenItsBootHookFails(t *testing.T) {
	base := featureFixture(t)
	writeTreeFile(t, filepath.Join(featuresDir, "iscsi", "modules.conf"), "iscsi_tcp\n")
	// A plain file where the hook expects to create its directory.
	writeTreeFile(t, filepath.Join(filepath.Dir(iscsiDir), "blocker"), "")
	iscsiDir = filepath.Join(filepath.Dir(iscsiDir), "blocker", "iscsi")

	got := actuateVendoredFeature(base, "iscsi", "node-1")
	if got.State != machine.FeatureFailed || got.Message == "" {
		t.Errorf("a hook that can't write its file fails the feature: %+v", got)
	}
}

func TestVendoredFeatureFailsWhenItsManifestIsUnreadable(t *testing.T) {
	base := featureFixture(t)
	writeTreeFile(t, filepath.Join(featuresDir, "nfs", "modules.conf"), "iscsi_tcp\n")
	manifest := filepath.Join(featuresDir, "nfs", "manifests", "nfs.yaml")
	writeTreeFile(t, manifest, "kind: DaemonSet\n")
	if err := os.Chmod(manifest, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(manifest, 0o644) })

	got := actuateVendoredFeature(base, "nfs", "node-1")
	if got.State != machine.FeatureFailed {
		t.Errorf("a workload that can't be seeded fails the feature: %+v", got)
	}
}

func TestSeedFeatureManifestsReportsAnUnwritableDeployDir(t *testing.T) {
	featureFixture(t)
	writeTreeFile(t, filepath.Join(featuresDir, "nfs", "manifests", "nfs.yaml"), "kind: DaemonSet\n")
	// The auto-deploy path is blocked by a plain file, so MkdirAll
	// can't create the directory.
	blocked := filepath.Join(t.TempDir(), "blocker")
	writeTreeFile(t, blocked, "")
	k3sManifestsDir = filepath.Join(blocked, "manifests")

	if err := seedFeatureManifests("nfs"); err == nil {
		t.Error("an unwritable deploy directory is an error")
	}
}
