package main

// Tests for the peek at machineState: the read-only look that finds
// the staged and proven manifests before storage reconciliation runs,
// and the quarantine of a staged manifest that cannot boot. A test
// process cannot mount ext4, so a directory stands in for the mounted
// partition, and `machineStateMounts` records each mount and unmount.

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"golang.org/x/sys/unix"

	"github.com/liken-sh/liken/liken/machine"
)

// machineStateMounts records what the peek mounted. The peek point is
// a directory that already holds the partition's files, so a mount
// only records its flags.
type machineStateMounts struct {
	mu         sync.Mutex
	flags      []uintptr
	mounted    int
	mountErr   error
	unmountErr error
}

func (m *machineStateMounts) mount(source, target, fstype string, flags uintptr, data string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.mountErr != nil {
		return m.mountErr
	}
	m.flags = append(m.flags, flags)
	m.mounted++
	return nil
}

func (m *machineStateMounts) unmount(target string, flags int) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.unmountErr != nil {
		return m.unmountErr
	}
	m.mounted--
	return nil
}

// machineStateDisk builds a machine with one disk whose first
// partition is named machineState and carries an ext4 superblock. It
// returns the manifest store on that partition, for the test to fill,
// and the record of mounts.
func machineStateDisk(t *testing.T) (machine.ManifestStore, *machineStateMounts) {
	t.Helper()
	sys, dev := fakeMachine(t)
	addDisk(t, sys, dev, "vda", 2<<30, nil)
	addPartition(t, sys, "vda", "vda1", "liken:machineState", 1<<30)
	if err := os.WriteFile(filepath.Join(dev, "vda1"), ext4DeviceWithUUID(make([]byte, 16)), 0o600); err != nil {
		t.Fatal(err)
	}

	mounts := &machineStateMounts{}
	savedPoint, savedMount, savedUnmount := manifestPeekPoint, mountFilesystem, unmountFilesystem
	t.Cleanup(func() {
		manifestPeekPoint, mountFilesystem, unmountFilesystem = savedPoint, savedMount, savedUnmount
	})
	manifestPeekPoint = filepath.Join(t.TempDir(), "peek")
	mountFilesystem, unmountFilesystem = mounts.mount, mounts.unmount
	if err := os.MkdirAll(filepath.Join(manifestPeekPoint, "manifests"), 0o755); err != nil {
		t.Fatal(err)
	}
	return machine.MachineManifests(manifestPeekPoint), mounts
}

const stagedWithMachineState = `apiVersion: liken.sh/v1alpha1
kind: Machine
metadata:
  name: node-1
spec:
  storage:
    machineState:
      device: /dev/vda
      size: 64Mi
`

const provenNode1 = "kind: Machine\nmetadata:\n  name: node-1\n"

// The peek finds both manifests through a read-only mount, and leaves
// nothing mounted. A mount that stays in place would make the kernel
// refuse to re-read this disk's partition table when storage grows a
// partition minutes later.
func TestLoadManifestCandidatesPeeksReadOnlyAndUnmounts(t *testing.T) {
	store, mounts := machineStateDisk(t)
	if err := store.WriteStaged([]byte(stagedWithMachineState)); err != nil {
		t.Fatal(err)
	}
	if err := store.WriteProven([]byte(provenNode1)); err != nil {
		t.Fatal(err)
	}

	c, err := loadManifestCandidates()
	if err != nil {
		t.Fatal(err)
	}
	if c.staged == nil || c.staged.hash != machine.ManifestHash([]byte(stagedWithMachineState)) {
		t.Errorf("the staged manifest is a candidate with its own hash: %+v", c.staged)
	}
	if c.proven == nil || c.proven.hash != machine.ManifestHash([]byte(provenNode1)) {
		t.Errorf("the proven manifest is a candidate with its own hash: %+v", c.proven)
	}
	if len(mounts.flags) != 1 || mounts.flags[0] != unix.MS_RDONLY {
		t.Errorf("the peek mounts once, read-only: %v", mounts.flags)
	}
	if mounts.mounted != 0 {
		t.Errorf("the peek leaves nothing mounted: %d mounts remain", mounts.mounted)
	}
}

// A staged manifest that would fail every boot the same way is
// rejected without a trial. The rejection is recorded on the partition
// with its reason and the staged copy moves aside, so the next boot
// does not try it again. The proven manifest still carries this boot.
func TestLoadManifestCandidatesRejectsAStagedManifestThatCannotBoot(t *testing.T) {
	cases := []struct {
		name   string
		staged string
		reason string
	}{
		{"a manifest that does not parse", "kind: Machine\nmetadata: [node-1\n", "does not parse"},
		{"a manifest with no machineState role", provenNode1, "does not declare the machineState role"},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			store, mounts := machineStateDisk(t)
			if err := store.WriteStaged([]byte(one.staged)); err != nil {
				t.Fatal(err)
			}
			if err := store.WriteProven([]byte(provenNode1)); err != nil {
				t.Fatal(err)
			}

			c, err := loadManifestCandidates()
			if err != nil {
				t.Fatal(err)
			}

			if c.staged != nil || c.proven == nil {
				t.Errorf("only the proven manifest is a candidate: staged %+v, proven %+v", c.staged, c.proven)
			}
			if c.rejection == nil || !strings.Contains(c.rejection.Reason, one.reason) {
				t.Errorf("this boot's facts carry the rejection: %+v", c.rejection)
			}
			recorded, _ := store.LoadRejection()
			if recorded == nil || recorded.Reason != c.rejection.Reason {
				t.Errorf("the rejection is recorded on the partition: %+v", recorded)
			}
			if staged, _ := store.LoadStaged(); staged != nil {
				t.Errorf("the rejected manifest moves aside: %q", staged)
			}
			if len(mounts.flags) != 2 || mounts.flags[1] != 0 || mounts.mounted != 0 {
				t.Errorf("only the rejection mounts read-write, and nothing stays mounted: %v, %d", mounts.flags, mounts.mounted)
			}
		})
	}
}

// A staged manifest that cannot be read is rejected the same way, with
// the read error as its reason.
func TestLoadManifestCandidatesRejectsAnUnreadableStagedManifest(t *testing.T) {
	store, _ := machineStateDisk(t)
	if err := os.Mkdir(filepath.Join(manifestPeekPoint, "manifests", "staged.yaml"), 0o755); err != nil {
		t.Fatal(err)
	}

	c, err := loadManifestCandidates()
	if err != nil {
		t.Fatal(err)
	}

	if c.staged != nil || c.rejection == nil || !strings.Contains(c.rejection.Reason, "unreadable") {
		t.Errorf("an unreadable staged manifest is rejected: staged %+v, rejection %+v", c.staged, c.rejection)
	}
	if recorded, _ := store.LoadRejection(); recorded == nil {
		t.Error("the rejection is recorded on the partition")
	}
}

// An unreadable proven manifest leaves no proven candidate, and the
// peek still succeeds. The file exists only for recovery, so a damaged
// copy must not stop the boot.
func TestLoadManifestCandidatesSkipsAnUnreadableProvenManifest(t *testing.T) {
	machineStateDisk(t)
	if err := os.Mkdir(filepath.Join(manifestPeekPoint, "manifests", "proven.yaml"), 0o755); err != nil {
		t.Fatal(err)
	}

	c, err := loadManifestCandidates()
	if err != nil {
		t.Fatal(err)
	}
	if c.proven != nil {
		t.Errorf("an unreadable proven manifest is no candidate: %+v", c.proven)
	}
}

// A rejection from an earlier boot stands until a promotion clears it.
// The peek reads it, so each boot's facts report it again.
func TestLoadManifestCandidatesCarriesAStandingRejection(t *testing.T) {
	store, _ := machineStateDisk(t)
	if err := store.WriteStaged([]byte(provenNode1)); err != nil {
		t.Fatal(err)
	}
	if err := store.Reject(machine.Rejection{Reason: "storage failed to reconcile"}); err != nil {
		t.Fatal(err)
	}

	c, err := loadManifestCandidates()
	if err != nil {
		t.Fatal(err)
	}
	if c.rejection == nil || c.rejection.Reason != "storage failed to reconcile" {
		t.Errorf("the standing rejection is reported again: %+v", c.rejection)
	}
}

// A partition that will not mount stops the peek with an error that
// names the device, because the boot cannot tell which manifest is
// this machine's own.
func TestLoadManifestCandidatesReportsAFailedMount(t *testing.T) {
	_, mounts := machineStateDisk(t)
	mounts.mountErr = unix.EIO

	_, err := loadManifestCandidates()
	if err == nil || !strings.Contains(err.Error(), "vda1") {
		t.Errorf("the error names the device: %v", err)
	}
}

// A rejection that cannot mount the partition is still reported for
// this boot. Only the durable record is lost, so the next boot rejects
// the same manifest again.
func TestRejectStagedReportsTheRejectionWhenTheRecordFails(t *testing.T) {
	_, mounts := machineStateDisk(t)
	mounts.mountErr = unix.EIO

	rejection := rejectStaged(&partition{name: "vda1"}, []byte(provenNode1), "no machineState role")

	if rejection == nil || rejection.Reason != "no machineState role" {
		t.Errorf("the rejection comes back for this boot's facts: %+v", rejection)
	}
}

// A peek whose unmount fails still returns what it read, and a
// rejection whose unmount fails is still recorded. The failure is
// reported on the console, and a later rewrite of this disk's
// partition table fails with `EBUSY` and names the problem.
func TestLoadManifestCandidatesContinuesPastAFailedUnmount(t *testing.T) {
	store, mounts := machineStateDisk(t)
	mounts.unmountErr = unix.EBUSY
	if err := store.WriteStaged([]byte(provenNode1)); err != nil {
		t.Fatal(err)
	}
	if err := store.WriteProven([]byte(provenNode1)); err != nil {
		t.Fatal(err)
	}

	c, err := loadManifestCandidates()
	if err != nil {
		t.Fatal(err)
	}

	if c.proven == nil {
		t.Error("the proven manifest is still a candidate")
	}
	if recorded, _ := store.LoadRejection(); recorded == nil {
		t.Error("the rejection of the staged manifest is still recorded")
	}
}
