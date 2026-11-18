package main

// k3s_test.go tests the disable settings that turn on bundled
// features. The module tests list iscsi_tcp as built in, because only
// a real kernel can load a module.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/liken-sh/liken/liken/cluster"
	"github.com/liken-sh/liken/liken/machine"
)

func TestActuateFeaturesWithNoClusterReportsNothing(t *testing.T) {
	if got := actuateFeatures(nil, "node-1"); got != nil {
		t.Errorf("the statuses are %v, want none for a machine that enables nothing", got)
	}
}

func TestActuateFeaturesWithNoFeaturesReportsNothing(t *testing.T) {
	if got := actuateFeatures(labCluster(), "node-1"); got != nil {
		t.Errorf("the statuses are %v, want none for a cluster with no opt-ins", got)
	}
}

func TestActuateFeaturesReportsSlugsFromANewerVocabulary(t *testing.T) {
	c := labCluster()
	c.Spec.Features = map[string]*cluster.FeatureConfig{"from-the-future": {}}
	got := actuateFeatures(c, "node-1")
	if len(got) != 1 || got[0].State != machine.FeatureMissing {
		t.Fatalf("the statuses are %+v, want one Missing for a slug this image does not define", got)
	}
	if !strings.Contains(got[0].Message, "Upgrade to a release") ||
		!strings.Contains(got[0].Message, "misspelled") {
		t.Errorf("the message is %q, want both an upgrade and a misspelling as causes", got[0].Message)
	}
}

func TestActuateFeaturesReportsBundledFeaturesActive(t *testing.T) {
	c := labCluster()
	c.Spec.Features = map[string]*cluster.FeatureConfig{
		"metrics-server": {},
		"traefik":        {},
	}
	got := actuateFeatures(c, "node-1")
	// traefik requires helm, so helm is reported too.
	if len(got) != 3 {
		t.Fatalf("the statuses are %v, want three", got)
	}
	if got[0].Name != "helm" || got[1].Name != "metrics-server" || got[2].Name != "traefik" {
		t.Errorf("the statuses are %v, want helm, metrics-server, traefik in that order", got)
	}
	for _, s := range got {
		if s.State != machine.FeatureActive || s.Message != "" {
			t.Errorf("the status is %+v, want Active with no message", s)
		}
	}
}

// featureFixture points the package's path variables at temporary
// directories and returns a fake module tree in which iscsi_tcp is
// built into the kernel.
func featureFixture(t *testing.T) string {
	t.Helper()
	featuresDir = t.TempDir()
	originalManifests := k3sManifestsDir
	k3sManifestsDir = filepath.Join(t.TempDir(), "manifests", "liken")
	iscsiDir = filepath.Join(t.TempDir(), "iscsi")
	t.Cleanup(func() {
		featuresDir = "/etc/liken/features"
		k3sManifestsDir = originalManifests
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
		t.Fatalf("creating the directory of %s: %v", path, err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("writing %s: %v", path, err)
	}
}

func TestActuateFeaturesReportsUnknownParameters(t *testing.T) {
	c := labCluster()
	c.Spec.Features = map[string]*cluster.FeatureConfig{
		"metrics-server": {"replicas": "2"},
	}
	got := actuateFeatures(c, "node-1")
	if len(got) != 1 || got[0].State != machine.FeatureFailed {
		t.Fatalf("the statuses are %+v, want one Failed for an unknown parameter", got)
	}
	if !strings.Contains(got[0].Message, "replicas") ||
		!strings.Contains(got[0].Message, "misspelling") {
		t.Errorf("the message is %q, want the parameter name and both causes", got[0].Message)
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
		t.Fatalf("the statuses are %+v, want one Active", got)
	}
	seeded := filepath.Join(k3sManifestsDir, "flux-system.yaml")
	if _, err := os.Stat(seeded); err != nil {
		t.Errorf("reading the seeded manifest: %v", err)
	}
}

func TestWorkloadFeatureRendersTheFluxSyncObjects(t *testing.T) {
	featureFixture(t)
	c := labCluster()
	c.Spec.Features = map[string]*cluster.FeatureConfig{
		"flux": {
			"repository": "ssh://git@forge.example/fleet.git",
			"branch":     "trunk",
		},
	}
	got := actuateFeatures(c, "node-1")
	if len(got) != 1 || got[0].State != machine.FeatureActive {
		t.Fatalf("the statuses are %+v, want one Active", got)
	}
	raw, err := os.ReadFile(filepath.Join(k3sManifestsDir, "flux-sync.yaml"))
	if err != nil {
		t.Fatalf("reading flux-sync.yaml: %v", err)
	}
	rendered := string(raw)
	for _, want := range []string{
		"kind: GitRepository",
		"kind: Kustomization",
		`url: "ssh://git@forge.example/fleet.git"`,
		`branch: "trunk"`,
		`path: "./"`,
		"prune: true",
		"name: flux-system",
	} {
		if !strings.Contains(rendered, want) {
			t.Errorf("flux-sync.yaml is\n%s\nwant it to contain %q", rendered, want)
		}
	}
}

func TestWorkloadFeatureRendersTheDeclaredPrune(t *testing.T) {
	for declared, want := range map[string]string{
		"":      "prune: true",
		"true":  "prune: true",
		"false": "prune: false",
	} {
		t.Run(declared, func(t *testing.T) {
			featureFixture(t)
			c := labCluster()
			c.Spec.Features = map[string]*cluster.FeatureConfig{
				"flux": {
					"repository": "ssh://git@forge.example/fleet.git",
					"prune":      declared,
				},
			}
			got := actuateFeatures(c, "node-1")
			if len(got) != 1 || got[0].State != machine.FeatureActive {
				t.Fatalf("the statuses are %+v, want one Active", got)
			}
			raw, err := os.ReadFile(filepath.Join(k3sManifestsDir, "flux-sync.yaml"))
			if err != nil {
				t.Fatalf("reading flux-sync.yaml: %v", err)
			}
			if !strings.Contains(string(raw), want) {
				t.Errorf("flux-sync.yaml is\n%s\nwant it to contain %q", raw, want)
			}
		})
	}
}

func TestWorkloadFeatureFailsOnABadConfiguration(t *testing.T) {
	featureFixture(t)
	c := labCluster()
	c.Spec.Features = map[string]*cluster.FeatureConfig{"flux": {}}
	got := actuateFeatures(c, "node-1")
	if len(got) != 1 || got[0].State != machine.FeatureFailed {
		t.Fatalf("the statuses are %+v, want one Failed for flux with no repository", got)
	}
	if !strings.Contains(got[0].Message, "repository") {
		t.Errorf("the message is %q, want it to name the repository parameter", got[0].Message)
	}
	if entries, _ := os.ReadDir(k3sManifestsDir); len(entries) != 0 {
		t.Errorf("the manifests directory holds %v, want nothing after a Failed feature", entries)
	}
}

func TestVendoredFeatureMissingFromAnOlderImage(t *testing.T) {
	base := featureFixture(t)
	got := actuateVendoredFeature(base, "iscsi", "node-1")
	if got.State != machine.FeatureMissing {
		t.Fatalf("the status is %+v, want Missing when the image has no modules.conf", got)
	}
	if !strings.Contains(got.Message, "Upgrade to a release") {
		t.Errorf("the message is %q, want it to tell the reader to upgrade", got.Message)
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
		t.Fatalf("the status is %+v, want Active with no message", got)
	}

	iqn, err := os.ReadFile(filepath.Join(iscsiDir, "initiatorname.iscsi"))
	if err != nil {
		t.Fatalf("reading the initiator name: %v", err)
	}
	if string(iqn) != "InitiatorName=iqn.2026-07.sh.liken:node-1\n" {
		t.Errorf("the initiator name file is %q, want the machine name in an iqn", iqn)
	}

	seeded, err := os.ReadFile(filepath.Join(k3sManifestsDir, "iscsid.yaml"))
	if err != nil {
		t.Fatalf("reading the seeded manifest: %v", err)
	}
	if string(seeded) != "kind: DaemonSet\n" {
		t.Errorf("the seeded manifest is %q, want a copy of the image's manifest", seeded)
	}
}

func TestVendoredFeatureFailsWhenAModuleIsUnloadable(t *testing.T) {
	base := featureFixture(t)
	writeTreeFile(t, filepath.Join(featuresDir, "iscsi", "modules.conf"), "not_a_module\n")

	got := actuateVendoredFeature(base, "iscsi", "node-1")
	if got.State != machine.FeatureFailed {
		t.Fatalf("the status is %+v, want Failed for an unloadable module", got)
	}
	if !strings.Contains(got.Message, "not_a_module") {
		t.Errorf("the message is %q, want it to name the module", got.Message)
	}
}

func TestVendoredFeatureWithoutManifestsSeedsNothing(t *testing.T) {
	base := featureFixture(t)
	writeTreeFile(t, filepath.Join(featuresDir, "nfs", "modules.conf"), "iscsi_tcp\n")

	got := actuateVendoredFeature(base, "nfs", "node-1")
	if got.State != machine.FeatureActive {
		t.Fatalf("the status is %+v, want Active for a feature with no manifests", got)
	}
	if _, err := os.Stat(k3sManifestsDir); !os.IsNotExist(err) {
		t.Errorf("checking the manifests directory returned %v, want it not to exist", err)
	}
}

func TestVendoredFeatureFailsOnAnUnreadableModuleList(t *testing.T) {
	base := featureFixture(t)
	conf := filepath.Join(featuresDir, "iscsi", "modules.conf")
	writeTreeFile(t, conf, "iscsi_tcp\n")
	if err := os.Chmod(conf, 0o000); err != nil {
		t.Fatalf("making modules.conf unreadable: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(conf, 0o644) })

	got := actuateVendoredFeature(base, "iscsi", "node-1")
	if got.State != machine.FeatureFailed {
		t.Errorf("the status is %+v, want Failed for an unreadable modules.conf", got)
	}
}

func TestVendoredFeatureFailsWhenItsBootHookFails(t *testing.T) {
	base := featureFixture(t)
	writeTreeFile(t, filepath.Join(featuresDir, "iscsi", "modules.conf"), "iscsi_tcp\n")
	// A plain file blocks the boot hook from creating its directory.
	writeTreeFile(t, filepath.Join(filepath.Dir(iscsiDir), "blocker"), "")
	iscsiDir = filepath.Join(filepath.Dir(iscsiDir), "blocker", "iscsi")

	got := actuateVendoredFeature(base, "iscsi", "node-1")
	if got.State != machine.FeatureFailed || got.Message == "" {
		t.Errorf("the status is %+v, want Failed with a message when the boot hook cannot write", got)
	}
}

func TestVendoredFeatureFailsWhenItsManifestIsUnreadable(t *testing.T) {
	base := featureFixture(t)
	writeTreeFile(t, filepath.Join(featuresDir, "nfs", "modules.conf"), "iscsi_tcp\n")
	manifest := filepath.Join(featuresDir, "nfs", "manifests", "nfs.yaml")
	writeTreeFile(t, manifest, "kind: DaemonSet\n")
	if err := os.Chmod(manifest, 0o000); err != nil {
		t.Fatalf("making the manifest unreadable: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(manifest, 0o644) })

	got := actuateVendoredFeature(base, "nfs", "node-1")
	if got.State != machine.FeatureFailed {
		t.Errorf("the status is %+v, want Failed when the manifest cannot be read", got)
	}
}

func TestSeedFeatureManifestsReportsAnUnwritableDeployDir(t *testing.T) {
	featureFixture(t)
	writeTreeFile(t, filepath.Join(featuresDir, "nfs", "manifests", "nfs.yaml"), "kind: DaemonSet\n")
	// A plain file blocks MkdirAll from creating the directory.
	blocked := filepath.Join(t.TempDir(), "blocker")
	writeTreeFile(t, blocked, "")
	k3sManifestsDir = filepath.Join(blocked, "manifests")

	if err := seedFeatureManifests("nfs"); err == nil {
		t.Error("seedFeatureManifests returned no error when it could not create the manifests directory, want one")
	}
}
