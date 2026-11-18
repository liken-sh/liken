package main

// manifestpeek_test.go tests reading the manifests from the
// partition. Reconciling storage under a declared manifest needs real
// disks and runs only under QEMU.

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/liken-sh/liken/liken/machine"
)

func TestFindMachineStatePartition(t *testing.T) {
	sys, dev := fakeMachine(t)
	addDisk(t, sys, dev, "vda", 1<<30, nil)
	addPartition(t, sys, "vda", "vda1", "liken:machineState", 64<<20)
	addPartition(t, sys, "vda", "vda2", "liken:clusterState", 1<<29)

	p, err := findMachineStatePartition()
	if err != nil {
		t.Fatalf("finding the machineState partition: %v", err)
	}
	if p == nil || p.name != "vda1" {
		t.Errorf("the partition is %+v, want vda1", p)
	}
}

func TestFindMachineStatePartitionAbsentIsAFirstBoot(t *testing.T) {
	fakeMachine(t)
	p, err := findMachineStatePartition()
	if err != nil || p != nil {
		t.Errorf("findMachineStatePartition returned %v and %v, want nil and nil with no partition", p, err)
	}
}

func TestFindMachineStatePartitionRefusesDuplicates(t *testing.T) {
	// A cloned disk gives two partitions the same name, and a guess
	// could boot the machine under another machine's configuration.
	sys, dev := fakeMachine(t)
	addDisk(t, sys, dev, "vda", 1<<30, nil)
	addDisk(t, sys, dev, "vdb", 1<<30, nil)
	addPartition(t, sys, "vda", "vda1", "liken:machineState", 64<<20)
	addPartition(t, sys, "vdb", "vdb1", "liken:machineState", 64<<20)

	_, err := findMachineStatePartition()
	if err == nil {
		t.Fatal("the error is nil, want one for the duplicate machineState partitions")
	}
	for _, want := range []string{"vda1", "vdb1"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the error is %q, want it to name %q", err, want)
		}
	}
}

func TestAttemptOrder(t *testing.T) {
	staged := &manifestChoice{source: machine.ManifestSourceStaged}
	proven := &manifestChoice{source: machine.ManifestSourceProven}

	cases := []struct {
		name string
		c    manifestCandidates
		want []machine.ManifestSource
	}{
		{"proven only", manifestCandidates{proven: proven}, []machine.ManifestSource{"Proven"}},
		{"staged then proven", manifestCandidates{staged: staged, proven: proven}, []machine.ManifestSource{"Staged", "Proven"}},
		{"staged alone", manifestCandidates{staged: staged}, []machine.ManifestSource{"Staged"}},
		{"first boot", manifestCandidates{}, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var got []machine.ManifestSource
			for _, choice := range attemptOrder(c.c) {
				got = append(got, choice.source)
			}
			if len(got) != len(c.want) {
				t.Fatalf("the order is %v, want %v", got, c.want)
			}
			for i := range got {
				if got[i] != c.want[i] {
					t.Errorf("attempt %d is %s, want %s", i, got[i], c.want[i])
				}
			}
		})
	}
}

func seedDir(t *testing.T, names ...string) string {
	t.Helper()
	dir := t.TempDir()
	for _, name := range names {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("{}"), 0o644); err != nil {
			t.Fatalf("writing a seed manifest: %v", err)
		}
	}
	return dir
}

func TestSeedPathSelection(t *testing.T) {
	cases := []struct {
		name      string
		files     []string
		requested string
		want      string // file name of the selected path, or "" for none
		wantErr   string // text the error must contain, or "" for success
	}{
		{"explicit selection", []string{"node-1.yaml", "node-2.yaml"}, "node-2", "node-2.yaml", ""},
		{"explicit selection among one", []string{"node-1.yaml"}, "node-1", "node-1.yaml", ""},
		{"a lone manifest needs no name", []string{"node-1.yaml"}, "", "node-1.yaml", ""},
		{"no manifests is a valid machine", nil, "", "", ""},
		{"a name that matches nothing", []string{"node-1.yaml"}, "node-9", "", "liken.machine=node-9"},
		{"many manifests and no name", []string{"node-1.yaml", "node-2.yaml"}, "", "", "liken.machine=<name>"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := seedDir(t, c.files...)
			got, err := seedPath(dir, c.requested)
			if c.wantErr != "" {
				if err == nil {
					t.Fatalf("the path is %q and the error is nil, want an error naming %q", got, c.wantErr)
				}
				if !errors.Is(err, errIdentity) {
					t.Errorf("the error is %v, want errIdentity", err)
				}
				if !strings.Contains(err.Error(), c.wantErr) {
					t.Errorf("the error is %q, want it to name %q", err, c.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("selecting the seed path: %v", err)
			}
			want := ""
			if c.want != "" {
				want = filepath.Join(dir, c.want)
			}
			if got != want {
				t.Errorf("the selected file is %q, want %q", got, want)
			}
		})
	}
}

func TestSeedPathMissingDirectoryIsNoManifest(t *testing.T) {
	got, err := seedPath(filepath.Join(t.TempDir(), "does-not-exist"), "")
	if err != nil || got != "" {
		t.Errorf("seedPath returned %q and %v, want no path and no error for a missing directory", got, err)
	}
}

func TestLoadSeedReadsTheNamedManifest(t *testing.T) {
	dir := t.TempDir()
	doc := "apiVersion: liken.sh/v1alpha1\nkind: Machine\nmetadata:\n  name: node-2\n"
	if err := os.WriteFile(filepath.Join(dir, "node-2.yaml"), []byte(doc), 0o644); err != nil {
		t.Fatalf("writing the seed manifest: %v", err)
	}
	choice, err := loadSeed(dir, "node-2")
	if err != nil {
		t.Fatalf("loading the seed: %v", err)
	}
	if choice.m.Metadata.Name != "node-2" || choice.source != machine.ManifestSourceSeed {
		t.Errorf("the choice is %+v, want the seed of node-2", choice)
	}
	if choice.hash == "" || string(choice.raw) != doc {
		t.Errorf("the choice has the hash %q and the bytes %q, want the file's bytes %q and their hash", choice.hash, choice.raw, doc)
	}
}

func TestLoadSeedWithNoManifestIsAnEmptyMachine(t *testing.T) {
	choice, err := loadSeed(t.TempDir(), "")
	if err != nil {
		t.Fatalf("loading the seed: %v", err)
	}
	if choice.m.Metadata.Name != "" || choice.raw != nil {
		t.Errorf("the choice is %+v, want a machine with no name, no bytes, and an empty spec", choice)
	}
}

func TestLoadSeedRefusesAnUnknownName(t *testing.T) {
	if _, err := loadSeed(t.TempDir(), "node-9"); !errors.Is(err, errIdentity) {
		t.Errorf("loadSeed returned %v, want errIdentity for a name that matches no manifest", err)
	}
}

func TestLoadSeedRefusesAManifestThatWontParse(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "node-1.yaml"), []byte("{not yaml"), 0o644); err != nil {
		t.Fatalf("writing the seed manifest: %v", err)
	}
	if _, err := loadSeed(dir, "node-1"); !errors.Is(err, errIdentity) {
		t.Errorf("loadSeed returned %v, want errIdentity for a seed that does not parse", err)
	}
}

func TestSettleStorageFirstBootRunsTheSeed(t *testing.T) {
	// With no machineState partition and no manifest in the image,
	// the seed is an empty machine and every role stays in memory.
	fakeMachine(t)
	fakeCmdline(t, "console=ttyS0\n")

	choice, status, boot, err := settleStorage()
	if err != nil {
		t.Fatalf("settling storage: %v", err)
	}
	if choice.source != machine.ManifestSourceSeed {
		t.Errorf("the manifest source is %s, want the seed on a first boot", choice.source)
	}
	if status != machine.AllRolesInMemory() {
		t.Errorf("the storage status is %+v, want every role in memory when nothing is declared", status)
	}
	if boot.ManifestSource != machine.ManifestSourceSeed || boot.ManifestHash != "" {
		t.Errorf("the boot record is %+v, want the seed as its source and no hash", boot)
	}
}

func TestSettleStorageRecordsTheBootsNetworkEvenWhenItIsEmpty(t *testing.T) {
	// A manifest that declares no interface still gets a network
	// record. The operator treats a missing record as a boot it
	// cannot check for drift, and an empty record as a manifest that
	// declared no network settings.
	fakeMachine(t)
	fakeCmdline(t, "console=ttyS0\n")

	_, _, boot, err := settleStorage()
	if err != nil {
		t.Fatalf("settling storage: %v", err)
	}
	if boot.Network == nil {
		t.Fatal("the boot record has no network, want the network that the manifest declares")
	}
	if len(boot.Network.Interfaces) != 0 {
		t.Errorf("the boot network is %+v, want no interface, because the manifest declares none", boot.Network)
	}
}

func TestSettleManifestsPromotesTheStagedWinner(t *testing.T) {
	store := machine.MachineManifests(t.TempDir())
	raw := []byte("kind: Machine\n")
	if err := store.WriteStaged(raw); err != nil {
		t.Fatalf("staging the manifest: %v", err)
	}
	status := machine.AllRolesInMemory()
	status.MachineState = machine.StorageRoleStatus{Backing: machine.BackingPartition}
	boot := machine.BootStatus{ManifestSource: machine.ManifestSourceStaged, Rejection: &machine.Rejection{Reason: "old news"}}
	choice := &manifestChoice{raw: raw, source: machine.ManifestSourceStaged, hash: machine.ManifestHash(raw)}

	settleManifests(store, choice, status, &boot)

	if proven, _ := store.LoadProven(); string(proven) != string(raw) {
		t.Errorf("the proven manifest is %q, want the staged manifest that booted, %q", proven, raw)
	}
	if staged, _ := store.LoadStaged(); staged != nil {
		t.Errorf("the staged manifest is %q, want none after the promotion", staged)
	}
	if boot.ManifestSource != machine.ManifestSourceProven || boot.Rejection != nil {
		t.Errorf("the boot record is %+v, want proven as its source and no rejection", boot)
	}
}

func TestSettleManifestsRecordsTheSeedAsFirstProven(t *testing.T) {
	store := machine.MachineManifests(t.TempDir())
	raw := []byte("kind: Machine\n")
	status := machine.AllRolesInMemory()
	status.MachineState = machine.StorageRoleStatus{Backing: machine.BackingPartition}
	boot := machine.BootStatus{ManifestSource: machine.ManifestSourceSeed}
	choice := &manifestChoice{raw: raw, source: machine.ManifestSourceSeed, hash: machine.ManifestHash(raw)}

	settleManifests(store, choice, status, &boot)

	if proven, _ := store.LoadProven(); string(proven) != string(raw) {
		t.Errorf("the proven manifest is %q, want the seed %q after its first successful boot", proven, raw)
	}
	if boot.ManifestSource != machine.ManifestSourceProven {
		t.Errorf("the manifest source is %s, want proven", boot.ManifestSource)
	}
}

func TestSettleManifestsWithNoDurableStorageDoesNothing(t *testing.T) {
	store := machine.MachineManifests(t.TempDir())
	boot := machine.BootStatus{ManifestSource: machine.ManifestSourceSeed}
	choice := &manifestChoice{raw: []byte("kind: Machine\n"), source: machine.ManifestSourceSeed}

	settleManifests(store, choice, machine.AllRolesInMemory(), &boot)

	if proven, _ := store.LoadProven(); proven != nil {
		t.Errorf("the proven manifest is %q, want none on a machine whose roles are all in memory", proven)
	}
}

func TestLoadManifestCandidatesBeforeTheFilesystemExists(t *testing.T) {
	// A boot that died between partitioning and mkfs leaves a
	// machineState partition with no filesystem.
	sys, dev := fakeMachine(t)
	addDisk(t, sys, dev, "vda", 2<<30, make([]byte, 4096))
	addPartition(t, sys, "vda", "vda1", "liken:machineState", 1<<30)
	if err := os.WriteFile(filepath.Join(dev, "vda1"), make([]byte, 4096), 0o600); err != nil {
		t.Fatalf("writing the partition: %v", err)
	}
	c, err := loadManifestCandidates()
	if err != nil {
		t.Fatalf("loading the manifest candidates: %v", err)
	}
	if c.part == nil {
		t.Error("the machineState partition was not found, want it found by its name even with no filesystem")
	}
	if c.staged != nil || c.proven != nil {
		t.Errorf("the candidates are staged %v and proven %v, want none on a partition with no filesystem", c.staged, c.proven)
	}
}

func TestSettleManifestsPromoteFailureIsLoudButNotFatal(t *testing.T) {
	// Nothing is staged, so promotion fails.
	store := machine.MachineManifests(t.TempDir())
	status := machine.AllRolesInMemory()
	status.MachineState = machine.StorageRoleStatus{Backing: machine.BackingPartition}
	boot := machine.BootStatus{ManifestSource: machine.ManifestSourceStaged}
	choice := &manifestChoice{source: machine.ManifestSourceStaged}

	settleManifests(store, choice, status, &boot)

	if boot.ManifestSource != machine.ManifestSourceStaged {
		t.Errorf("the boot record is %+v, want staged as its source when the promotion failed", boot)
	}
}

func TestSettleManifestsSeedWithNoBytesRecordsNothing(t *testing.T) {
	// The seed is empty because no manifest exists, so there are no
	// bytes to write as proven.
	root := t.TempDir()
	store := machine.MachineManifests(root)
	status := machine.AllRolesInMemory()
	status.MachineState = machine.StorageRoleStatus{Backing: machine.BackingPartition}
	boot := machine.BootStatus{ManifestSource: machine.ManifestSourceSeed}

	settleManifests(store, &manifestChoice{source: machine.ManifestSourceSeed}, status, &boot)

	if proven, _ := store.LoadProven(); proven != nil {
		t.Errorf("the proven manifest is %q, want none when the choice has no bytes", proven)
	}
}

func TestSettleManifestsReportsAFailedProvenWrite(t *testing.T) {
	sealed := t.TempDir()
	if err := os.Chmod(sealed, 0o555); err != nil {
		t.Fatalf("making the directory read-only: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(sealed, 0o755) })
	store := machine.MachineManifests(filepath.Join(sealed, "state"))
	status := machine.AllRolesInMemory()
	status.MachineState = machine.StorageRoleStatus{Backing: machine.BackingPartition}
	boot := machine.BootStatus{ManifestSource: machine.ManifestSourceSeed}
	choice := &manifestChoice{raw: []byte("kind: Machine\n"), source: machine.ManifestSourceSeed}

	settleManifests(store, choice, status, &boot)

	if boot.ManifestSource != machine.ManifestSourceSeed {
		t.Errorf("the boot record is %+v, want the seed as its source when the seed could not be recorded", boot)
	}
}

func TestSettleStorageRefusesDuplicateMachineStatePartitions(t *testing.T) {
	sys, dev := fakeMachine(t)
	addDisk(t, sys, dev, "vda", 2<<30, nil)
	addDisk(t, sys, dev, "vdb", 2<<30, nil)
	addPartition(t, sys, "vda", "vda1", "liken:machineState", 1<<30)
	addPartition(t, sys, "vdb", "vdb1", "liken:machineState", 1<<30)
	fakeCmdline(t, "console=ttyS0\n")

	_, _, _, err := settleStorage()
	if err == nil || !strings.Contains(err.Error(), "vda1") {
		t.Errorf("the error is %v, want one that names the partitions vda1 and vdb1", err)
	}
}

// After a rollback, a proven manifest that a newer release wrote is
// still a candidate, without the fields this release does not know.
func TestAProvenManifestFromANewerReleaseStaysACandidate(t *testing.T) {
	cases := []struct {
		name      string
		raw       string
		candidate bool
	}{
		{name: "a manifest this release knows", raw: "kind: Machine\nmetadata:\n  name: node-1\n", candidate: true},
		{name: "a field a newer release added", raw: "kind: Machine\nmetadata:\n  name: node-1\nspec:\n  futureDevices: [adapter]\n", candidate: true},
		{name: "a corrupted manifest", raw: "kind: Machine\nmetadata: [node-1\n"},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			choice := provenCandidate([]byte(one.raw))

			if (choice != nil) != one.candidate {
				t.Fatalf("candidate = %v, want %v", choice != nil, one.candidate)
			}
			if choice != nil && (choice.m.Metadata.Name != "node-1" || choice.hash != machine.ManifestHash([]byte(one.raw))) {
				t.Errorf("the candidate is %+v, want node-1 with the bytes' own hash", choice)
			}
		})
	}
}
