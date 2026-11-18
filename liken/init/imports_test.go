package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/liken-sh/liken/liken/machine"
)

// importsFixture points k3sImagesDir and k3sAgentDir at temporary
// directories. root is the machineState directory that holds the
// imported-images record.
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
		t.Fatalf("writing the image tarball: %v", err)
	}
}

func (f importsFixture) writeAgentState(t *testing.T, rel, contents string) string {
	t.Helper()
	path := filepath.Join(f.agentDir, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("creating the agent state directory: %v", err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatalf("writing the agent state file: %v", err)
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
		t.Fatalf("hashing the image tarballs: %v", err)
	}
	raw, _, err := machine.RenderImportedImages(digests)
	if err != nil {
		t.Fatalf("rendering the imported-images record: %v", err)
	}
	if err := f.store().WriteProven(raw); err != nil {
		t.Fatalf("writing the proven record: %v", err)
	}
}

func TestImportsFirstBootStagesATrial(t *testing.T) {
	f := newImportsFixture(t)
	f.writeTarball(t, "liken-machine-operator.tar", "operator")

	boot := machine.BootStatus{}
	settleImageImports(f.root, true, true, &boot)

	if boot.ImportsSource != machine.ManifestSourceStaged {
		t.Fatalf("the first boot's ImportsSource is %q, want staged", boot.ImportsSource)
	}
	if boot.ImportsDiscarded {
		t.Fatal("the first boot discarded the container store, want it kept because no store exists yet")
	}
	staged, err := f.store().LoadStaged()
	if err != nil || staged == nil {
		t.Fatalf("loading the staged record after the first boot: record %v, want a staged record", err)
	}
	if machine.ManifestHash(staged) != boot.ImportsHash {
		t.Fatal("the staged record has a different hash than boot.ImportsHash, want the same")
	}
}

func TestImportsMatchingProvenRecordKeepsTheStore(t *testing.T) {
	f := newImportsFixture(t)
	f.writeTarball(t, "liken-machine-operator.tar", "operator")
	f.provenRecord(t)
	victim := f.writeAgentState(t, "containerd/io.containerd.content.v1.content/blob", "precious")

	boot := machine.BootStatus{}
	settleImageImports(f.root, true, true, &boot)

	if boot.ImportsSource != machine.ManifestSourceProven {
		t.Fatalf("with matching digests ImportsSource is %q, want proven", boot.ImportsSource)
	}
	if boot.ImportsDiscarded {
		t.Fatal("the boot discarded a store that matches the proven record, want it kept")
	}
	if staged, _ := f.store().LoadStaged(); staged != nil {
		t.Fatal("the boot staged a record, want none when the digests match the proven record")
	}
	if _, err := os.Stat(victim); err != nil {
		t.Fatalf("the container store file is missing after the boot: %v, want it kept", err)
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
		t.Fatalf("with new digests ImportsSource is %q, want staged", boot.ImportsSource)
	}
	if boot.ImportsDiscarded {
		t.Fatal("the boot discarded the container store, want it kept because only a staged record left by a previous boot discards it")
	}
	if _, err := os.Stat(victim); err != nil {
		t.Fatalf("the container store file is missing after the boot: %v, want it kept", err)
	}
}

func TestImportsLeftoverStagedRecordDiscardsTheStore(t *testing.T) {
	f := newImportsFixture(t)
	f.writeTarball(t, "liken-machine-operator.tar", "operator")
	digests, _ := machine.HashImageTarballs(f.imagesDir)
	raw, _, _ := machine.RenderImportedImages(digests)
	if err := f.store().WriteStaged(raw); err != nil {
		t.Fatalf("writing the staged record: %v", err)
	}
	torn := f.writeAgentState(t, "containerd/io.containerd.snapshotter.v1.overlayfs/snapshots/3/fs/bin", "torn")
	tarballCopy := f.writeAgentState(t, "images/liken-machine-operator.tar", "operator")

	boot := machine.BootStatus{}
	settleImageImports(f.root, true, true, &boot)

	if !boot.ImportsDiscarded {
		t.Fatal("the boot kept the container store, want it discarded because a staged record remained from a previous boot")
	}
	if _, err := os.Stat(torn); !os.IsNotExist(err) {
		t.Fatal("the torn container store file exists after the discard, want it deleted")
	}
	if _, err := os.Stat(tarballCopy); err != nil {
		t.Fatalf("the image tarball is missing after the discard: %v, want it kept for k3s to import", err)
	}
	if boot.ImportsSource != machine.ManifestSourceStaged {
		t.Fatalf("after the discard ImportsSource is %q, want staged", boot.ImportsSource)
	}
	staged, _ := f.store().LoadStaged()
	if machine.ManifestHash(staged) != boot.ImportsHash {
		t.Fatal("the staged record has a different hash than boot.ImportsHash, want the hash of this boot's tarballs")
	}
}

func TestImportsLeftoverStagedRecordStagesTheNewDigests(t *testing.T) {
	f := newImportsFixture(t)
	f.writeTarball(t, "liken-machine-operator.tar", "operator v2")
	raw, _, _ := machine.RenderImportedImages(map[string]string{"liken-machine-operator.tar": "digest of v1"})
	if err := f.store().WriteStaged(raw); err != nil {
		t.Fatalf("writing the staged record: %v", err)
	}

	boot := machine.BootStatus{}
	settleImageImports(f.root, true, true, &boot)

	if !boot.ImportsDiscarded {
		t.Fatal("the boot kept the container store, want it discarded because the staged record of the v1 tarball was never proven")
	}
	staged, _ := f.store().LoadStaged()
	digests, err := machine.HashImageTarballs(f.imagesDir)
	if err != nil {
		t.Fatalf("hashing the image tarballs: %v", err)
	}
	expected, _, err := machine.RenderImportedImages(digests)
	if err != nil {
		t.Fatalf("rendering the imported-images record: %v", err)
	}
	if string(staged) != string(expected) {
		t.Fatalf("the staged record is\n%s\nwant the record of this boot's tarballs, not the record of the previous boot", staged)
	}
}

func TestImportsEphemeralMachineStateSkipsTheLifecycle(t *testing.T) {
	f := newImportsFixture(t)
	f.writeTarball(t, "liken-machine-operator.tar", "operator")

	boot := machine.BootStatus{}
	settleImageImports(f.root, false, true, &boot)

	if boot.ImportsSource != "" {
		t.Fatalf("with ephemeral machineState ImportsSource is %q, want empty", boot.ImportsSource)
	}
	if staged, _ := f.store().LoadStaged(); staged != nil {
		t.Fatal("the boot staged a record, want none with ephemeral machineState")
	}
}

func TestImportsEphemeralClusterStateSkipsTheLifecycle(t *testing.T) {
	f := newImportsFixture(t)
	f.writeTarball(t, "liken-machine-operator.tar", "operator")

	boot := machine.BootStatus{}
	settleImageImports(f.root, true, false, &boot)

	if boot.ImportsSource != "" {
		t.Fatalf("with ephemeral clusterState ImportsSource is %q, want empty", boot.ImportsSource)
	}
}

func TestImportsUnreadableStagedRecordStillDiscards(t *testing.T) {
	f := newImportsFixture(t)
	f.writeTarball(t, "liken-machine-operator.tar", "operator")
	if err := f.store().WriteStaged([]byte("{{{{ not a record")); err != nil {
		t.Fatalf("writing the staged record: %v", err)
	}
	torn := f.writeAgentState(t, "containerd/db", "suspect")

	boot := machine.BootStatus{}
	settleImageImports(f.root, true, true, &boot)

	if !boot.ImportsDiscarded {
		t.Fatal("the boot kept the container store, want it discarded because an unreadable staged record still means the previous boot did not prove its imports")
	}
	if _, err := os.Stat(torn); !os.IsNotExist(err) {
		t.Fatal("the container store file exists after the boot, want it deleted")
	}
}

func TestImportsReportsUnhashableTarballs(t *testing.T) {
	// A record built from some of the tarballs would be wrong, so an
	// unreadable tarball stops settleImageImports before it decides.
	f := newImportsFixture(t)
	f.writeTarball(t, "k3s-airgap.tar", "layers")
	sealed := filepath.Join(f.imagesDir, "k3s-airgap.tar")
	if err := os.Chmod(sealed, 0o000); err != nil {
		t.Fatalf("making the image tarball unreadable: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(sealed, 0o644) })

	boot := machine.BootStatus{}
	settleImageImports(f.root, true, true, &boot)

	if boot.ImportsSource != "" || boot.ImportsDiscarded {
		t.Errorf("after the hashing failed the boot status is %+v, want no ImportsSource and no discard", boot)
	}
	if staged, _ := f.store().LoadStaged(); staged != nil {
		t.Error("the boot staged a record after the hashing failed, want none")
	}
}

func TestDiscardContainerStoreToleratesAMissingAgentDir(t *testing.T) {
	f := newImportsFixture(t)
	k3sAgentDir = filepath.Join(f.agentDir, "never-created")
	discardContainerStore()
}

func TestDiscardContainerStoreReportsWhatItCannotRemove(t *testing.T) {
	// The agent directory is read-only, so the removal fails and the
	// entry stays for the next boot to try again.
	f := newImportsFixture(t)
	f.writeAgentState(t, "containerd/io.containerd.snapshotter.v1.overlayfs/torn", "")
	if err := os.Chmod(f.agentDir, 0o555); err != nil {
		t.Fatalf("making the agent directory read-only: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(f.agentDir, 0o755) })

	discardContainerStore()

	if _, err := os.Stat(filepath.Join(f.agentDir, "containerd")); err != nil {
		t.Error("the containerd directory is missing, want it kept when its deletion fails")
	}
}
