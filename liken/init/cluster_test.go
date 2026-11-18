package main

// Tests for choosing the Cluster document at boot: staged over proven
// over seed, vetting before the trial, and the boot record naming the
// copy that the boot used.

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/liken-sh/liken/liken/machine"
)

const sampleCluster = `apiVersion: liken.sh/v1alpha1
kind: Cluster
metadata:
  name: lab
spec:
  leaders: [node-1]
  endpoint: https://10.10.0.1:6443
`

// editedCluster differs from sampleCluster in spec.time.
const editedCluster = sampleCluster + `  time:
    upstreams: [time.cloudflare.com]
`

func writeSeed(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "cluster.yaml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("writing the seed document: %v", err)
	}
	return path
}

func TestCheckSeedClusterAcceptsAGoodDocument(t *testing.T) {
	if err := checkSeedCluster(writeSeed(t, sampleCluster)); err != nil {
		t.Errorf("checkSeedCluster = %v for a document that parses, want nil", err)
	}
}

func TestCheckSeedClusterAcceptsAMissingDocument(t *testing.T) {
	// A machine with no Cluster document is its own cluster, so a
	// missing document is not an error.
	if err := checkSeedCluster(filepath.Join(t.TempDir(), "absent.yaml")); err != nil {
		t.Errorf("checkSeedCluster = %v for a missing document, want nil", err)
	}
}

func TestCheckSeedClusterRefusesTheDocumentAMachineWouldRefuse(t *testing.T) {
	// A registry mirror with no endpoints is a document that installs
	// and then fails every boot from the disk. Refuse it here, while
	// a person is at the machine.
	seed := writeSeed(t, sampleCluster+`  registries:
    mirrors:
      docker.io: []
`)
	err := checkSeedCluster(seed)
	if err == nil {
		t.Fatal("checkSeedCluster = nil for a document that the machine would refuse, want an error")
	}
	if !errors.Is(err, errIdentity) {
		t.Errorf("the error is %v, want errIdentity", err)
	}
	if !strings.Contains(err.Error(), seed) {
		t.Errorf("the error is %v, want it to name %s", err, seed)
	}
}

func TestChooseClusterFromTheSeed(t *testing.T) {
	boot := &machine.BootStatus{}
	c, _, err := chooseCluster(t.TempDir(), writeSeed(t, sampleCluster), true, boot)
	if err != nil {
		t.Fatalf("choosing the cluster document: %v", err)
	}
	if c == nil || c.Metadata.Name != "lab" {
		t.Fatalf("the cluster is %+v, want the one named lab", c)
	}
	if boot.ClusterManifestSource != machine.ManifestSourceSeed {
		t.Errorf("the source is %q, want the one for this document", boot.ClusterManifestSource)
	}
	if boot.ClusterManifestHash != machine.ManifestHash([]byte(sampleCluster)) {
		t.Errorf("the hash is %q, want the hash of the seed", boot.ClusterManifestHash)
	}
}

func TestChooseClusterWithNoDocumentReturnsNil(t *testing.T) {
	boot := &machine.BootStatus{}
	c, _, err := chooseCluster(t.TempDir(), filepath.Join(t.TempDir(), "absent.yaml"), true, boot)
	if err != nil || c != nil {
		t.Fatalf("chooseCluster with no document = %v, %v, want nil and no error", c, err)
	}
	if boot.ClusterManifestSource != "" {
		t.Errorf("the source is %q, want empty when no document exists", boot.ClusterManifestSource)
	}
}

func TestChooseClusterPrefersProvenOverSeed(t *testing.T) {
	root := t.TempDir()
	if err := machine.ClusterManifests(root).WriteProven([]byte(editedCluster)); err != nil {
		t.Fatalf("writing the proven document: %v", err)
	}
	boot := &machine.BootStatus{}
	c, _, err := chooseCluster(root, writeSeed(t, sampleCluster), true, boot)
	if err != nil {
		t.Fatalf("choosing the cluster document: %v", err)
	}
	if len(c.Spec.Time.Upstreams) != 1 {
		t.Errorf("the spec is %+v, want the proven document, which has one time upstream", c.Spec)
	}
	if boot.ClusterManifestSource != machine.ManifestSourceProven {
		t.Errorf("the source is %q, want the one for this document", boot.ClusterManifestSource)
	}
}

func TestChooseClusterPrefersStagedOverProven(t *testing.T) {
	root := t.TempDir()
	store := machine.ClusterManifests(root)
	if err := store.WriteProven([]byte(sampleCluster)); err != nil {
		t.Fatalf("writing the proven document: %v", err)
	}
	if err := store.WriteStaged([]byte(editedCluster)); err != nil {
		t.Fatalf("writing the staged document: %v", err)
	}
	boot := &machine.BootStatus{}
	c, _, err := chooseCluster(root, writeSeed(t, sampleCluster), true, boot)
	if err != nil {
		t.Fatalf("choosing the cluster document: %v", err)
	}
	if len(c.Spec.Time.Upstreams) != 1 {
		t.Errorf("the spec is %+v, want the staged document, which has one time upstream", c.Spec)
	}
	if boot.ClusterManifestSource != machine.ManifestSourceStaged {
		t.Errorf("the source is %q, want the one for this document", boot.ClusterManifestSource)
	}
}

func TestChooseClusterRejectsAStagedDocumentThatWontParse(t *testing.T) {
	root := t.TempDir()
	store := machine.ClusterManifests(root)
	if err := store.WriteProven([]byte(sampleCluster)); err != nil {
		t.Fatalf("writing the proven document: %v", err)
	}
	garbage := []byte("not: [valid")
	if err := store.WriteStaged(garbage); err != nil {
		t.Fatalf("writing the staged document: %v", err)
	}
	boot := &machine.BootStatus{}
	c, _, err := chooseCluster(root, writeSeed(t, sampleCluster), true, boot)
	if err != nil {
		t.Fatalf("choosing the cluster document: %v", err)
	}
	if c == nil || boot.ClusterManifestSource != machine.ManifestSourceProven {
		t.Fatalf("after a rejected staged document, the cluster is %v from %q, want the proven document", c, boot.ClusterManifestSource)
	}
	if boot.ClusterRejection == nil || boot.ClusterRejection.Hash != machine.ManifestHash(garbage) {
		t.Errorf("the rejection is %+v, want the hash of the refused bytes", boot.ClusterRejection)
	}
	if raw, _ := store.LoadStaged(); raw != nil {
		t.Error("the rejected document is still staged, want it quarantined")
	}
}

func TestChooseClusterRejectsAStagedDocumentOfTheWrongKind(t *testing.T) {
	root := t.TempDir()
	store := machine.ClusterManifests(root)
	if err := store.WriteStaged([]byte("apiVersion: liken.sh/v1alpha1\nkind: Machine\nmetadata:\n  name: lab\n")); err != nil {
		t.Fatalf("writing the staged document: %v", err)
	}
	boot := &machine.BootStatus{}
	c, _, err := chooseCluster(root, writeSeed(t, sampleCluster), true, boot)
	if err != nil {
		t.Fatalf("choosing the cluster document: %v", err)
	}
	if c == nil || boot.ClusterManifestSource != machine.ManifestSourceSeed {
		t.Fatalf("with no proven document, the cluster is %v from %q, want the seed", c, boot.ClusterManifestSource)
	}
	if boot.ClusterRejection == nil {
		t.Error("the boot record has no rejection, want one for the document of the wrong kind")
	}
}

func TestChooseClusterOnAMemoryBackedMachineIsSeedOnly(t *testing.T) {
	root := t.TempDir()
	if err := machine.ClusterManifests(root).WriteStaged([]byte(editedCluster)); err != nil {
		t.Fatalf("writing the staged document: %v", err)
	}
	boot := &machine.BootStatus{}
	c, _, err := chooseCluster(root, writeSeed(t, sampleCluster), false, boot)
	if err != nil {
		t.Fatalf("choosing the cluster document: %v", err)
	}
	if len(c.Spec.Time.Upstreams) != 0 || boot.ClusterManifestSource != machine.ManifestSourceSeed {
		t.Errorf("a memory-backed machine used spec %+v from %q, want the seed only", c.Spec, boot.ClusterManifestSource)
	}
}

func TestChooseClusterReportsAnEarlierRejectionAgain(t *testing.T) {
	root := t.TempDir()
	store := machine.ClusterManifests(root)
	if err := store.WriteStaged([]byte("not: [valid")); err != nil {
		t.Fatalf("writing the staged document: %v", err)
	}
	boot := &machine.BootStatus{}
	if _, _, err := chooseCluster(root, writeSeed(t, sampleCluster), true, boot); err != nil {
		t.Fatalf("choosing the cluster document: %v", err)
	}
	// The next boot finds no staged document, but the rejection record
	// is still on disk. It must be reported again.
	nextBoot := &machine.BootStatus{}
	if _, _, err := chooseCluster(root, writeSeed(t, sampleCluster), true, nextBoot); err != nil {
		t.Fatalf("choosing the cluster document: %v", err)
	}
	if nextBoot.ClusterRejection == nil {
		t.Error("the next boot has no rejection, want the record that is still on disk")
	}
}

func TestChooseClusterMarksTheStagedDocumentAttempted(t *testing.T) {
	root := t.TempDir()
	store := machine.ClusterManifests(root)
	if err := store.WriteStaged([]byte(editedCluster)); err != nil {
		t.Fatalf("writing the staged document: %v", err)
	}
	boot := &machine.BootStatus{}
	if _, _, err := chooseCluster(root, writeSeed(t, sampleCluster), true, boot); err != nil {
		t.Fatalf("choosing the cluster document: %v", err)
	}
	h, err := store.LoadAttempted()
	if err != nil || h != machine.ManifestHash([]byte(editedCluster)) {
		t.Errorf("the attempted marker is %q (%v), want the hash of the staged document", h, err)
	}
}

func TestChooseClusterRejectsAStagedDocumentThatWasNeverProven(t *testing.T) {
	root := t.TempDir()
	store := machine.ClusterManifests(root)
	if err := store.WriteProven([]byte(sampleCluster)); err != nil {
		t.Fatalf("writing the proven document: %v", err)
	}
	if err := store.WriteStaged([]byte(editedCluster)); err != nil {
		t.Fatalf("writing the staged document: %v", err)
	}
	// The previous boot tried this document and nothing promoted it,
	// so the machine did not join its cluster under it.
	if err := store.WriteAttempted(machine.ManifestHash([]byte(editedCluster))); err != nil {
		t.Fatalf("writing the attempted marker: %v", err)
	}
	boot := &machine.BootStatus{}
	c, _, err := chooseCluster(root, writeSeed(t, sampleCluster), true, boot)
	if err != nil {
		t.Fatalf("choosing the cluster document: %v", err)
	}
	if boot.ClusterManifestSource != machine.ManifestSourceProven || len(c.Spec.Time.Upstreams) != 0 {
		t.Fatalf("the source is %q with spec %+v, want the proven document", boot.ClusterManifestSource, c.Spec)
	}
	if boot.ClusterRejection == nil || boot.ClusterRejection.Hash != machine.ManifestHash([]byte(editedCluster)) {
		t.Errorf("the rejection is %+v, want the hash of the document that never joined", boot.ClusterRejection)
	}
	if h, _ := store.LoadAttempted(); h != "" {
		t.Errorf("the attempted marker is %q after the rejection, want none", h)
	}
}

func TestChooseClusterFailsOnAnUnparseableSeed(t *testing.T) {
	boot := &machine.BootStatus{}
	if _, _, err := chooseCluster(t.TempDir(), writeSeed(t, "not: [valid"), true, boot); err == nil {
		t.Fatal("chooseCluster = nil for a seed that does not parse, want an error")
	}
}

func TestChooseClusterFallsThroughACorruptProvenDocument(t *testing.T) {
	// A proven document that does not parse is a damaged copy. The boot
	// reports it and uses the seed, because a recovery file must not
	// make a boot fail.
	root := t.TempDir()
	if err := machine.ClusterManifests(root).WriteProven([]byte("{corrupt")); err != nil {
		t.Fatalf("writing the proven document: %v", err)
	}
	seed := filepath.Join(t.TempDir(), "cluster.yaml")
	doc := "apiVersion: liken.sh/v1alpha1\nkind: Cluster\nmetadata:\n  name: lab\nspec:\n  leaders: [node-1]\n"
	if err := os.WriteFile(seed, []byte(doc), 0o644); err != nil {
		t.Fatalf("writing the seed document: %v", err)
	}
	boot := machine.BootStatus{}
	c, _, err := chooseCluster(root, seed, true, &boot)
	if err != nil {
		t.Fatalf("choosing the cluster document: %v", err)
	}
	if c == nil || boot.ClusterManifestSource != machine.ManifestSourceSeed {
		t.Errorf("the boot record is %+v, want the seed as the source after a corrupt proven document", boot)
	}
}

func TestChooseClusterToleratesUnreadableStoreFiles(t *testing.T) {
	// Both store files exist but cannot be read, as on a failing disk.
	// The boot reports each one and uses the seed.
	root := t.TempDir()
	store := machine.ClusterManifests(root)
	if err := store.WriteStaged([]byte("staged")); err != nil {
		t.Fatalf("writing the staged document: %v", err)
	}
	if err := store.WriteProven([]byte("proven")); err != nil {
		t.Fatalf("writing the proven document: %v", err)
	}
	for _, name := range []string{"staged.yaml", "proven.yaml"} {
		path := filepath.Join(root, "cluster", name)
		if err := os.Chmod(path, 0o000); err != nil {
			t.Fatalf("removing the read permission: %v", err)
		}
		t.Cleanup(func() { _ = os.Chmod(path, 0o644) })
	}

	boot := machine.BootStatus{}
	seed := writeSeed(t, "kind: Cluster\nmetadata:\n  name: lab\nspec:\n  leaders: [node-1]\n")
	c, _, err := chooseCluster(root, seed, true, &boot)
	if err != nil {
		t.Fatalf("choosing the cluster document: %v", err)
	}
	if c == nil || boot.ClusterManifestSource != machine.ManifestSourceSeed {
		t.Errorf("the boot record is %+v, want the seed as the source after unreadable store files", boot)
	}
}

func TestChooseClusterFailsOnAnUnreadableSeed(t *testing.T) {
	seed := writeSeed(t, "kind: Cluster\n")
	if err := os.Chmod(seed, 0o000); err != nil {
		t.Fatalf("removing the read permission: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(seed, 0o644) })

	boot := machine.BootStatus{}
	if _, _, err := chooseCluster(t.TempDir(), seed, false, &boot); err == nil {
		t.Error("chooseCluster = nil for a seed that exists and cannot be read, want an error")
	}
}
