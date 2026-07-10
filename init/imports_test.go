package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/chrisguidry/liken/machine"
)

// importsFixture points the settle pass at a throwaway tree: a seed
// images directory standing in for what the boot copied onto
// clusterState, and an agent directory standing in for containerd's
// world. It returns the machineState root the store lives under.
type importsFixture struct {
	root      string
	imagesDir string
	agentDir  string
}

func newImportsFixture(t *testing.T) importsFixture {
	t.Helper()
	f := importsFixture{
		root:      t.TempDir(),
		imagesDir: t.TempDir(),
		agentDir:  t.TempDir(),
	}
	origImages, origAgent := k3sImagesDir, k3sAgentDir
	k3sImagesDir, k3sAgentDir = f.imagesDir, f.agentDir
	t.Cleanup(func() { k3sImagesDir, k3sAgentDir = origImages, origAgent })
	return f
}

func (f importsFixture) writeTarball(t *testing.T, name, contents string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(f.imagesDir, name), []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}
}

func (f importsFixture) writeAgentState(t *testing.T, rel, contents string) string {
	t.Helper()
	path := filepath.Join(f.agentDir, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func (f importsFixture) store() machine.ManifestStore {
	return machine.ImportedImagesStore(f.root)
}

func (f importsFixture) provenRecord(t *testing.T) {
	t.Helper()
	digests, err := machine.HashImageTarballs(f.imagesDir)
	if err != nil {
		t.Fatal(err)
	}
	raw, _, err := machine.RenderImportedImages(digests)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.store().WriteProven(raw); err != nil {
		t.Fatal(err)
	}
}

func TestImportsFirstBootStagesATrial(t *testing.T) {
	f := newImportsFixture(t)
	f.writeTarball(t, "liken-machine-operator.tar", "operator")

	boot := machine.BootStatus{}
	settleImageImports(f.root, true, true, &boot)

	if boot.ImportsSource != machine.ManifestSourceStaged {
		t.Fatalf("a first boot's imports are a trial, got %q", boot.ImportsSource)
	}
	if boot.ImportsDiscarded {
		t.Fatal("a first boot has no store to distrust")
	}
	staged, err := f.store().LoadStaged()
	if err != nil || staged == nil {
		t.Fatalf("the trial record was not staged: %v", err)
	}
	if machine.ManifestHash(staged) != boot.ImportsHash {
		t.Fatal("the staged record and the boot record disagree about identity")
	}
}

func TestImportsMatchingProvenRecordIsTheQuietPath(t *testing.T) {
	f := newImportsFixture(t)
	f.writeTarball(t, "liken-machine-operator.tar", "operator")
	f.provenRecord(t)
	victim := f.writeAgentState(t, "containerd/io.containerd.content.v1.content/blob", "precious")

	boot := machine.BootStatus{}
	settleImageImports(f.root, true, true, &boot)

	if boot.ImportsSource != machine.ManifestSourceProven {
		t.Fatalf("matching digests boot under the proven record, got %q", boot.ImportsSource)
	}
	if boot.ImportsDiscarded {
		t.Fatal("a proven store must not be discarded")
	}
	if staged, _ := f.store().LoadStaged(); staged != nil {
		t.Fatal("the quiet path stages nothing")
	}
	if _, err := os.Stat(victim); err != nil {
		t.Fatalf("the container store was disturbed: %v", err)
	}
}

func TestImportsNewDigestsStageWithoutDiscarding(t *testing.T) {
	f := newImportsFixture(t)
	f.writeTarball(t, "liken-machine-operator.tar", "operator v1")
	f.provenRecord(t)
	f.writeTarball(t, "liken-machine-operator.tar", "operator v2")
	victim := f.writeAgentState(t, "containerd/db", "the old, proven unpacks")

	boot := machine.BootStatus{}
	settleImageImports(f.root, true, true, &boot)

	if boot.ImportsSource != machine.ManifestSourceStaged {
		t.Fatalf("new digests are a trial, got %q", boot.ImportsSource)
	}
	if boot.ImportsDiscarded {
		t.Fatal("an upgrade on a proven store keeps the store: only unproven trials are distrusted")
	}
	if _, err := os.Stat(victim); err != nil {
		t.Fatalf("the proven store was discarded: %v", err)
	}
}

func TestImportsStandingTrialDiscardsTheStore(t *testing.T) {
	f := newImportsFixture(t)
	f.writeTarball(t, "liken-machine-operator.tar", "operator")
	digests, _ := machine.HashImageTarballs(f.imagesDir)
	raw, _, _ := machine.RenderImportedImages(digests)
	if err := f.store().WriteStaged(raw); err != nil {
		t.Fatal(err)
	}
	torn := f.writeAgentState(t, "containerd/io.containerd.snapshotter.v1.overlayfs/snapshots/3/fs/bin", "torn")
	tarballCopy := f.writeAgentState(t, "images/liken-machine-operator.tar", "operator")

	boot := machine.BootStatus{}
	settleImageImports(f.root, true, true, &boot)

	if !boot.ImportsDiscarded {
		t.Fatal("a standing trial means the store took writes that were never proven")
	}
	if _, err := os.Stat(torn); !os.IsNotExist(err) {
		t.Fatal("the container store survived the discard")
	}
	if _, err := os.Stat(tarballCopy); err != nil {
		t.Fatalf("the discard must spare the tarballs k3s is about to import: %v", err)
	}
	if boot.ImportsSource != machine.ManifestSourceStaged {
		t.Fatalf("the retry is itself a trial, got %q", boot.ImportsSource)
	}
	staged, _ := f.store().LoadStaged()
	if machine.ManifestHash(staged) != boot.ImportsHash {
		t.Fatal("the staged record does not match this boot's tarballs")
	}
}

func TestImportsStandingTrialWithNewDigestsStagesTheNewOnes(t *testing.T) {
	f := newImportsFixture(t)
	f.writeTarball(t, "liken-machine-operator.tar", "operator v2")
	raw, _, _ := machine.RenderImportedImages(map[string]string{"liken-machine-operator.tar": "digest of v1"})
	if err := f.store().WriteStaged(raw); err != nil {
		t.Fatal(err)
	}

	boot := machine.BootStatus{}
	settleImageImports(f.root, true, true, &boot)

	if !boot.ImportsDiscarded {
		t.Fatal("the v1 trial never proved; the store is not to be trusted")
	}
	staged, _ := f.store().LoadStaged()
	record, err := machine.ParseImportedImages(staged)
	if err != nil {
		t.Fatal(err)
	}
	if record.Images["liken-machine-operator.tar"] == "digest of v1" {
		t.Fatal("the staged record still names the digests of the dead trial")
	}
}

func TestImportsEphemeralMachineStateSkipsTheLifecycle(t *testing.T) {
	f := newImportsFixture(t)
	f.writeTarball(t, "liken-machine-operator.tar", "operator")

	boot := machine.BootStatus{}
	settleImageImports(f.root, false, true, &boot)

	if boot.ImportsSource != "" {
		t.Fatalf("no durable machineState means no record to boot under, got %q", boot.ImportsSource)
	}
	if staged, _ := f.store().LoadStaged(); staged != nil {
		t.Fatal("nothing should be staged without a durable store")
	}
}

func TestImportsEphemeralClusterStateSkipsTheLifecycle(t *testing.T) {
	f := newImportsFixture(t)
	f.writeTarball(t, "liken-machine-operator.tar", "operator")

	boot := machine.BootStatus{}
	settleImageImports(f.root, true, false, &boot)

	if boot.ImportsSource != "" {
		t.Fatalf("an ephemeral container store resets with every boot and cannot wedge, got %q", boot.ImportsSource)
	}
}

func TestImportsUnreadableStagedRecordStillDiscards(t *testing.T) {
	f := newImportsFixture(t)
	f.writeTarball(t, "liken-machine-operator.tar", "operator")
	if err := f.store().WriteStaged([]byte("{{{{ not a record")); err != nil {
		t.Fatal(err)
	}
	torn := f.writeAgentState(t, "containerd/db", "suspect")

	boot := machine.BootStatus{}
	settleImageImports(f.root, true, true, &boot)

	if !boot.ImportsDiscarded {
		t.Fatal("a staged file that won't even parse still marks an unproven trial")
	}
	if _, err := os.Stat(torn); !os.IsNotExist(err) {
		t.Fatal("the store survived")
	}
}
