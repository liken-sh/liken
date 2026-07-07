package main

// Tests for the cluster document's boot-time selection: staged over
// proven over seed, vetting before the trial, and the boot record
// accurately recording which copy won.

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/chrisguidry/liken/machine"
)

const sampleCluster = `apiVersion: liken.sh/v1alpha1
kind: Cluster
metadata:
  name: lab
spec:
  leaders: [node-1]
  endpoint: https://10.10.0.1:6443
`

// a second, distinguishable document
const editedCluster = sampleCluster + `  time:
    upstreams: [time.cloudflare.com]
`

func writeSeed(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "cluster.yaml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestChooseClusterFromTheSeed(t *testing.T) {
	boot := &machine.BootStatus{}
	c, _, err := chooseCluster(t.TempDir(), writeSeed(t, sampleCluster), true, boot)
	if err != nil {
		t.Fatal(err)
	}
	if c == nil || c.Metadata.Name != "lab" {
		t.Fatalf("expected the seed cluster, got %+v", c)
	}
	if boot.ClusterManifestSource != machine.ManifestSourceSeed {
		t.Errorf("source: got %q", boot.ClusterManifestSource)
	}
	if boot.ClusterManifestHash != machine.ManifestHash([]byte(sampleCluster)) {
		t.Errorf("hash: got %q", boot.ClusterManifestHash)
	}
}

func TestChooseClusterWithNoManifestAnywhereIsAMachineAlone(t *testing.T) {
	boot := &machine.BootStatus{}
	c, _, err := chooseCluster(t.TempDir(), filepath.Join(t.TempDir(), "absent.yaml"), true, boot)
	if err != nil || c != nil {
		t.Fatalf("no manifest anywhere should be a valid lone machine: %v %v", c, err)
	}
	if boot.ClusterManifestSource != "" {
		t.Errorf("no document, no source: got %q", boot.ClusterManifestSource)
	}
}

func TestChooseClusterPrefersProvenOverSeed(t *testing.T) {
	root := t.TempDir()
	if err := machine.ClusterManifests(root).WriteProven([]byte(editedCluster)); err != nil {
		t.Fatal(err)
	}
	boot := &machine.BootStatus{}
	c, _, err := chooseCluster(root, writeSeed(t, sampleCluster), true, boot)
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Spec.Time.Upstreams) != 1 {
		t.Errorf("expected the proven document, got %+v", c.Spec)
	}
	if boot.ClusterManifestSource != machine.ManifestSourceProven {
		t.Errorf("source: got %q", boot.ClusterManifestSource)
	}
}

func TestChooseClusterPrefersStagedOverProven(t *testing.T) {
	root := t.TempDir()
	store := machine.ClusterManifests(root)
	if err := store.WriteProven([]byte(sampleCluster)); err != nil {
		t.Fatal(err)
	}
	if err := store.WriteStaged([]byte(editedCluster)); err != nil {
		t.Fatal(err)
	}
	boot := &machine.BootStatus{}
	c, _, err := chooseCluster(root, writeSeed(t, sampleCluster), true, boot)
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Spec.Time.Upstreams) != 1 {
		t.Errorf("expected the staged document, got %+v", c.Spec)
	}
	if boot.ClusterManifestSource != machine.ManifestSourceStaged {
		t.Errorf("source: got %q", boot.ClusterManifestSource)
	}
}

func TestChooseClusterRejectsAStagedDocumentThatWontParse(t *testing.T) {
	root := t.TempDir()
	store := machine.ClusterManifests(root)
	if err := store.WriteProven([]byte(sampleCluster)); err != nil {
		t.Fatal(err)
	}
	garbage := []byte("not: [valid")
	if err := store.WriteStaged(garbage); err != nil {
		t.Fatal(err)
	}
	boot := &machine.BootStatus{}
	c, _, err := chooseCluster(root, writeSeed(t, sampleCluster), true, boot)
	if err != nil {
		t.Fatal(err)
	}
	if c == nil || boot.ClusterManifestSource != machine.ManifestSourceProven {
		t.Fatalf("a rejected staged document should fall back to proven: %v %q", c, boot.ClusterManifestSource)
	}
	if boot.ClusterRejection == nil || boot.ClusterRejection.Hash != machine.ManifestHash(garbage) {
		t.Errorf("the rejection should identify the refused bytes: %+v", boot.ClusterRejection)
	}
	if raw, _ := store.LoadStaged(); raw != nil {
		t.Error("the rejected document should be quarantined, not left staged")
	}
}

func TestChooseClusterRejectsAStagedDocumentOfTheWrongKind(t *testing.T) {
	root := t.TempDir()
	store := machine.ClusterManifests(root)
	if err := store.WriteStaged([]byte("apiVersion: liken.sh/v1alpha1\nkind: Machine\nmetadata:\n  name: lab\n")); err != nil {
		t.Fatal(err)
	}
	boot := &machine.BootStatus{}
	c, _, err := chooseCluster(root, writeSeed(t, sampleCluster), true, boot)
	if err != nil {
		t.Fatal(err)
	}
	if c == nil || boot.ClusterManifestSource != machine.ManifestSourceSeed {
		t.Fatalf("with nothing proven, the fallback is the seed: %v %q", c, boot.ClusterManifestSource)
	}
	if boot.ClusterRejection == nil {
		t.Error("the wrong-kind rejection should be recorded")
	}
}

func TestChooseClusterOnAMemoryBackedMachineIsSeedOnly(t *testing.T) {
	root := t.TempDir()
	if err := machine.ClusterManifests(root).WriteStaged([]byte(editedCluster)); err != nil {
		t.Fatal(err)
	}
	boot := &machine.BootStatus{}
	c, _, err := chooseCluster(root, writeSeed(t, sampleCluster), false, boot)
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Spec.Time.Upstreams) != 0 || boot.ClusterManifestSource != machine.ManifestSourceSeed {
		t.Errorf("a memory-backed machine reads only the seed: %+v %q", c.Spec, boot.ClusterManifestSource)
	}
}

func TestChooseClusterRepublishesAStandingRejection(t *testing.T) {
	root := t.TempDir()
	store := machine.ClusterManifests(root)
	if err := store.WriteStaged([]byte("not: [valid")); err != nil {
		t.Fatal(err)
	}
	boot := &machine.BootStatus{}
	if _, _, err := chooseCluster(root, writeSeed(t, sampleCluster), true, boot); err != nil {
		t.Fatal(err)
	}
	// The next boot finds no staged document, but the quarantine
	// record still stands and must be reported again.
	nextBoot := &machine.BootStatus{}
	if _, _, err := chooseCluster(root, writeSeed(t, sampleCluster), true, nextBoot); err != nil {
		t.Fatal(err)
	}
	if nextBoot.ClusterRejection == nil {
		t.Error("a standing rejection must be republished every boot")
	}
}

func TestChooseClusterMarksTheStagedDocumentAttempted(t *testing.T) {
	root := t.TempDir()
	store := machine.ClusterManifests(root)
	if err := store.WriteStaged([]byte(editedCluster)); err != nil {
		t.Fatal(err)
	}
	boot := &machine.BootStatus{}
	if _, _, err := chooseCluster(root, writeSeed(t, sampleCluster), true, boot); err != nil {
		t.Fatal(err)
	}
	h, err := store.LoadAttempted()
	if err != nil || h != machine.ManifestHash([]byte(editedCluster)) {
		t.Errorf("booting a staged document must mark it attempted: %q %v", h, err)
	}
}

func TestChooseClusterRejectsAStagedDocumentThatWasNeverProven(t *testing.T) {
	root := t.TempDir()
	store := machine.ClusterManifests(root)
	if err := store.WriteProven([]byte(sampleCluster)); err != nil {
		t.Fatal(err)
	}
	if err := store.WriteStaged([]byte(editedCluster)); err != nil {
		t.Fatal(err)
	}
	// The previous boot tried this exact document and nobody promoted
	// it: the machine never joined its cluster under it.
	if err := store.WriteAttempted(machine.ManifestHash([]byte(editedCluster))); err != nil {
		t.Fatal(err)
	}
	boot := &machine.BootStatus{}
	c, _, err := chooseCluster(root, writeSeed(t, sampleCluster), true, boot)
	if err != nil {
		t.Fatal(err)
	}
	if boot.ClusterManifestSource != machine.ManifestSourceProven || len(c.Spec.Time.Upstreams) != 0 {
		t.Fatalf("the unproven staged document should be rejected: %q %+v", boot.ClusterManifestSource, c.Spec)
	}
	if boot.ClusterRejection == nil || boot.ClusterRejection.Hash != machine.ManifestHash([]byte(editedCluster)) {
		t.Errorf("the rejection should identify the unproven document: %+v", boot.ClusterRejection)
	}
	if h, _ := store.LoadAttempted(); h != "" {
		t.Errorf("the trial is over; the marker should be gone, got %q", h)
	}
}

func TestChooseClusterFailsOnAnUnparseableSeed(t *testing.T) {
	boot := &machine.BootStatus{}
	if _, _, err := chooseCluster(t.TempDir(), writeSeed(t, "not: [valid"), true, boot); err == nil {
		t.Fatal("an unparseable seed leaves the machine unable to know its role; that is an error")
	}
}

func TestChooseClusterFallsThroughACorruptProvenDocument(t *testing.T) {
	// A proven document that won't parse is a corrupted
	// last-known-good: reported, then the seed carries the boot,
	// because a recovery file must never be the thing a boot dies on.
	root := t.TempDir()
	if err := machine.ClusterManifests(root).WriteProven([]byte("{corrupt")); err != nil {
		t.Fatal(err)
	}
	seed := filepath.Join(t.TempDir(), "cluster.yaml")
	doc := "apiVersion: liken.sh/v1alpha1\nkind: Cluster\nmetadata:\n  name: lab\nspec:\n  leaders: [node-1]\n"
	if err := os.WriteFile(seed, []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
	boot := machine.BootStatus{}
	c, _, err := chooseCluster(root, seed, true, &boot)
	if err != nil {
		t.Fatal(err)
	}
	if c == nil || boot.ClusterManifestSource != machine.ManifestSourceSeed {
		t.Errorf("the seed carries a boot whose proven copy is corrupt: %+v", boot)
	}
}
