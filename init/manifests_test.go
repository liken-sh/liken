package main

// Tests for manifest selection: finding the machineState partition
// before any spec exists, and the attempt-order policy. The peek's
// mount and unmount, and the settle loop's actuation, run only under
// QEMU. The decisions they act on are pinned here.

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/liken-sh/liken/machine"
)

func TestFindMachineStatePartition(t *testing.T) {
	sys, dev := fakeMachine(t)
	addDisk(t, sys, dev, "vda", 1<<30, nil)
	addPartition(t, sys, "vda", "vda1", "liken:machineState", 64<<20)
	addPartition(t, sys, "vda", "vda2", "liken:clusterState", 1<<29)

	p, err := findMachineStatePartition()
	if err != nil {
		t.Fatal(err)
	}
	if p == nil || p.name != "vda1" {
		t.Errorf("expected vda1, got %+v", p)
	}
}

func TestFindMachineStatePartitionAbsentIsAFirstBoot(t *testing.T) {
	fakeMachine(t)
	p, err := findMachineStatePartition()
	if err != nil || p != nil {
		t.Errorf("no partition should mean nil, nil: %v %v", p, err)
	}
}

func TestFindMachineStatePartitionRefusesDuplicates(t *testing.T) {
	// A cloned or transplanted disk: two partitions carry the name.
	// Guessing which one holds the real manifests could boot the
	// machine under the wrong configuration.
	sys, dev := fakeMachine(t)
	addDisk(t, sys, dev, "vda", 1<<30, nil)
	addDisk(t, sys, dev, "vdb", 1<<30, nil)
	addPartition(t, sys, "vda", "vda1", "liken:machineState", 64<<20)
	addPartition(t, sys, "vdb", "vdb1", "liken:machineState", 64<<20)

	_, err := findMachineStatePartition()
	if err == nil {
		t.Fatal("expected a refusal for duplicate machineState partitions")
	}
	for _, want := range []string{"vda1", "vdb1", "refusing"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error should mention %q: %v", want, err)
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
		// The normal steady state: nothing staged, boot the proven.
		{"proven only", manifestCandidates{proven: proven}, []machine.ManifestSource{"Proven"}},
		// An edit awaits its proving boot, with the last-known-good behind it.
		{"staged then proven", manifestCandidates{staged: staged, proven: proven}, []machine.ManifestSource{"Staged", "Proven"}},
		// A staged file with no proven behind it: nothing to fall back to.
		{"staged alone", manifestCandidates{staged: staged}, []machine.ManifestSource{"Staged"}},
		// First boot: no durable candidates at all, which is
		// settleStorage's cue to consult the image's seed.
		{"first boot", manifestCandidates{}, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var got []machine.ManifestSource
			for _, choice := range attemptOrder(c.c) {
				got = append(got, choice.source)
			}
			if len(got) != len(c.want) {
				t.Fatalf("got %v, want %v", got, c.want)
			}
			for i := range got {
				if got[i] != c.want[i] {
					t.Errorf("attempt %d: got %s, want %s", i, got[i], c.want[i])
				}
			}
		})
	}
}

// seedDir builds a manifests directory with the given file names, so
// each selection case reads as data.
func seedDir(t *testing.T, names ...string) string {
	t.Helper()
	dir := t.TempDir()
	for _, name := range names {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("{}"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestSeedPathSelection(t *testing.T) {
	cases := []struct {
		name      string
		files     []string
		requested string
		want      string // the base name of the chosen path; "" for none
		wantErr   string // a fragment the error must carry; "" for success
	}{
		{"explicit selection", []string{"node-1.yaml", "node-2.yaml"}, "node-2", "node-2.yaml", ""},
		{"explicit selection among one", []string{"node-1.yaml"}, "node-1", "node-1.yaml", ""},
		{"a lone manifest needs no name", []string{"node-1.yaml"}, "", "node-1.yaml", ""},
		{"no manifests is a valid machine", nil, "", "", ""},
		{"a name that matches nothing", []string{"node-1.yaml"}, "node-9", "", "liken.machine=node-9"},
		{"many manifests and no name", []string{"node-1.yaml", "node-2.yaml"}, "", "", "refusing to guess"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := seedDir(t, c.files...)
			got, err := seedPath(dir, c.requested)
			if c.wantErr != "" {
				if err == nil {
					t.Fatalf("expected an error mentioning %q, got path %q", c.wantErr, got)
				}
				if !errors.Is(err, errIdentity) {
					t.Errorf("selection failures should be identity errors: %v", err)
				}
				if !strings.Contains(err.Error(), c.wantErr) {
					t.Errorf("error should mention %q: %v", c.wantErr, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			want := ""
			if c.want != "" {
				want = filepath.Join(dir, c.want)
			}
			if got != want {
				t.Errorf("got %q, want %q", got, want)
			}
		})
	}
}

func TestSeedPathMissingDirectoryIsNoManifest(t *testing.T) {
	got, err := seedPath(filepath.Join(t.TempDir(), "does-not-exist"), "")
	if err != nil || got != "" {
		t.Errorf("a missing directory should mean no manifest: %q, %v", got, err)
	}
}

func TestLoadSeedReadsTheNamedManifest(t *testing.T) {
	dir := t.TempDir()
	doc := "apiVersion: liken.sh/v1alpha1\nkind: Machine\nmetadata:\n  name: node-2\n"
	if err := os.WriteFile(filepath.Join(dir, "node-2.yaml"), []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
	choice, err := loadSeed(dir, "node-2")
	if err != nil {
		t.Fatal(err)
	}
	if choice.m.Metadata.Name != "node-2" || choice.source != machine.ManifestSourceSeed {
		t.Errorf("the seed carries the named machine: %+v", choice)
	}
	if choice.hash == "" || string(choice.raw) != doc {
		t.Error("the exact bytes and their hash travel with the choice")
	}
}

func TestLoadSeedWithNoManifestIsAnEmptyMachine(t *testing.T) {
	choice, err := loadSeed(t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	if choice.m.Metadata.Name != "" || choice.raw != nil {
		t.Errorf("no manifest is a valid machine with an empty spec: %+v", choice)
	}
}

func TestLoadSeedRefusesAnUnknownName(t *testing.T) {
	if _, err := loadSeed(t.TempDir(), "node-9"); !errors.Is(err, errIdentity) {
		t.Errorf("a name matching no manifest is an identity failure: %v", err)
	}
}

func TestLoadSeedRefusesAManifestThatWontParse(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "node-1.yaml"), []byte("{not yaml"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := loadSeed(dir, "node-1"); !errors.Is(err, errIdentity) {
		t.Errorf("a seed that won't parse is an identity failure: %v", err)
	}
}

func TestSettleStorageFirstBootRunsTheSeed(t *testing.T) {
	// A machine with no machineState partition, no manifests baked
	// into its image, and no name on its command line: the emptiest
	// possible first boot. The seed choice is an empty machine, no
	// storage is declared, and everything stays on the RAM root.
	fakeMachine(t)
	fakeCmdline(t, "console=ttyS0\n")

	choice, status, boot, err := settleStorage()
	if err != nil {
		t.Fatal(err)
	}
	if choice.source != machine.ManifestSourceSeed {
		t.Errorf("a first boot runs the seed: %s", choice.source)
	}
	if status != machine.AllRolesInMemory() {
		t.Errorf("nothing declared, everything in memory: %+v", status)
	}
	if boot.ManifestSource != machine.ManifestSourceSeed || boot.ManifestHash != "" {
		t.Errorf("the boot record names the seed: %+v", boot)
	}
}

func TestSettleManifestsPromotesTheStagedWinner(t *testing.T) {
	store := machine.MachineManifests(t.TempDir())
	raw := []byte("kind: Machine\n")
	if err := store.WriteStaged(raw); err != nil {
		t.Fatal(err)
	}
	status := machine.AllRolesInMemory()
	status.MachineState = machine.StorageRoleStatus{Backing: machine.BackingPartition}
	boot := machine.BootStatus{ManifestSource: machine.ManifestSourceStaged, Rejection: &machine.Rejection{Reason: "old news"}}
	choice := &manifestChoice{raw: raw, source: machine.ManifestSourceStaged, hash: machine.ManifestHash(raw)}

	settleManifests(store, choice, status, &boot)

	if proven, _ := store.LoadProven(); string(proven) != string(raw) {
		t.Error("the staged manifest that booted becomes proven")
	}
	if staged, _ := store.LoadStaged(); staged != nil {
		t.Error("promotion consumes the staged copy")
	}
	if boot.ManifestSource != machine.ManifestSourceProven || boot.Rejection != nil {
		t.Errorf("the boot record reflects the promotion: %+v", boot)
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
		t.Error("the seed's first success is the first proven manifest")
	}
	if boot.ManifestSource != machine.ManifestSourceProven {
		t.Errorf("the boot record reads proven from here on: %s", boot.ManifestSource)
	}
}

func TestSettleManifestsWithNoDurableStorageDoesNothing(t *testing.T) {
	store := machine.MachineManifests(t.TempDir())
	boot := machine.BootStatus{ManifestSource: machine.ManifestSourceSeed}
	choice := &manifestChoice{raw: []byte("kind: Machine\n"), source: machine.ManifestSourceSeed}

	settleManifests(store, choice, machine.AllRolesInMemory(), &boot)

	if proven, _ := store.LoadProven(); proven != nil {
		t.Error("a memory-backed machine has nowhere to record anything")
	}
}

func TestLoadManifestCandidatesBeforeTheFilesystemExists(t *testing.T) {
	// A machineState partition with no ext4 yet: the boot that
	// claimed it died between partitioning and mkfs. The peek yields
	// no candidates, and the seed carries this boot instead.
	sys, dev := fakeMachine(t)
	addDisk(t, sys, dev, "vda", 2<<30, make([]byte, 4096))
	addPartition(t, sys, "vda", "vda1", "liken:machineState", 1<<30)
	if err := os.WriteFile(filepath.Join(dev, "vda1"), make([]byte, 4096), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := loadManifestCandidates()
	if err != nil {
		t.Fatal(err)
	}
	if c.part == nil {
		t.Error("the partition is recognized by name even without a filesystem")
	}
	if c.staged != nil || c.proven != nil {
		t.Error("no filesystem means no candidates")
	}
}

func TestSettleManifestsPromoteFailureIsLoudButNotFatal(t *testing.T) {
	// Nothing is staged, so promotion fails. The machine is up, and
	// the next boot repeats the step, so the boot record is left alone.
	store := machine.MachineManifests(t.TempDir())
	status := machine.AllRolesInMemory()
	status.MachineState = machine.StorageRoleStatus{Backing: machine.BackingPartition}
	boot := machine.BootStatus{ManifestSource: machine.ManifestSourceStaged}
	choice := &manifestChoice{source: machine.ManifestSourceStaged}

	settleManifests(store, choice, status, &boot)

	if boot.ManifestSource != machine.ManifestSourceStaged {
		t.Errorf("a failed promotion must not claim proven: %+v", boot)
	}
}

func TestSettleManifestsSeedWithNoBytesRecordsNothing(t *testing.T) {
	// The emptiest first boot: no manifest anywhere, so there are no
	// bytes to write down as proven.
	root := t.TempDir()
	store := machine.MachineManifests(root)
	status := machine.AllRolesInMemory()
	status.MachineState = machine.StorageRoleStatus{Backing: machine.BackingPartition}
	boot := machine.BootStatus{ManifestSource: machine.ManifestSourceSeed}

	settleManifests(store, &manifestChoice{source: machine.ManifestSourceSeed}, status, &boot)

	if proven, _ := store.LoadProven(); proven != nil {
		t.Errorf("no bytes, no proven record: %q", proven)
	}
}

func TestSettleManifestsReportsAFailedProvenWrite(t *testing.T) {
	sealed := t.TempDir()
	if err := os.Chmod(sealed, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(sealed, 0o755) })
	store := machine.MachineManifests(filepath.Join(sealed, "state"))
	status := machine.AllRolesInMemory()
	status.MachineState = machine.StorageRoleStatus{Backing: machine.BackingPartition}
	boot := machine.BootStatus{ManifestSource: machine.ManifestSourceSeed}
	choice := &manifestChoice{raw: []byte("kind: Machine\n"), source: machine.ManifestSourceSeed}

	settleManifests(store, choice, status, &boot)

	if boot.ManifestSource != machine.ManifestSourceSeed {
		t.Errorf("a seed that couldn't be recorded is still just the seed: %+v", boot)
	}
}

func TestSettleStorageRefusesDuplicateMachineStatePartitions(t *testing.T) {
	// Two partitions claiming machineState is the cloned-disk
	// ambiguity: the peek refuses to guess and the boot stops.
	sys, dev := fakeMachine(t)
	addDisk(t, sys, dev, "vda", 2<<30, nil)
	addDisk(t, sys, dev, "vdb", 2<<30, nil)
	addPartition(t, sys, "vda", "vda1", "liken:machineState", 1<<30)
	addPartition(t, sys, "vdb", "vdb1", "liken:machineState", 1<<30)
	fakeCmdline(t, "console=ttyS0\n")

	_, _, _, err := settleStorage()
	if err == nil || !strings.Contains(err.Error(), "refusing to guess") {
		t.Errorf("expected the duplicate refusal: %v", err)
	}
}
