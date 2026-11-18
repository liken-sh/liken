package main

// Before storage reconciliation runs, loadManifestCandidates mounts
// machineState read-only to read the staged and proven manifests. A
// test process cannot mount ext4, so a directory takes the place of the
// mounted partition.

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"golang.org/x/sys/unix"

	"github.com/liken-sh/liken/liken/machine"
)

// machineStateMounts records the flags of each mount and counts the
// mounts still in place. The mount point already holds the partition's
// files, so a mount does nothing else.
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
// partition is an ext4 machineState. It returns the manifest store on
// that partition, for the test to fill, and the mount recorder.
func machineStateDisk(t *testing.T) (machine.ManifestStore, *machineStateMounts) {
	t.Helper()
	sys, dev := fakeMachine(t)
	addDisk(t, sys, dev, "vda", 2<<30, nil)
	addPartition(t, sys, "vda", "vda1", "liken:machineState", 1<<30)
	if err := os.WriteFile(filepath.Join(dev, "vda1"), ext4DeviceWithUUID(make([]byte, 16)), 0o600); err != nil {
		t.Fatalf("writing the machineState device: %v", err)
	}

	mounts := &machineStateMounts{}
	savedPoint, savedMount, savedUnmount := manifestPeekPoint, mountFilesystem, unmountFilesystem
	t.Cleanup(func() {
		manifestPeekPoint, mountFilesystem, unmountFilesystem = savedPoint, savedMount, savedUnmount
	})
	manifestPeekPoint = filepath.Join(t.TempDir(), "peek")
	mountFilesystem, unmountFilesystem = mounts.mount, mounts.unmount
	if err := os.MkdirAll(filepath.Join(manifestPeekPoint, "manifests"), 0o755); err != nil {
		t.Fatalf("creating the manifests directory: %v", err)
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

// The mount must not stay in place, because the kernel refuses to
// re-read the partition table of a disk with a mounted partition, and
// storage grows partitions later in the boot.
func TestLoadManifestCandidatesPeeksReadOnlyAndUnmounts(t *testing.T) {
	store, mounts := machineStateDisk(t)
	if err := store.WriteStaged([]byte(stagedWithMachineState)); err != nil {
		t.Fatalf("writing the staged manifest: %v", err)
	}
	if err := store.WriteProven([]byte(provenNode1)); err != nil {
		t.Fatalf("writing the proven manifest: %v", err)
	}

	c, err := loadManifestCandidates()
	if err != nil {
		t.Fatalf("loading the manifest candidates: %v", err)
	}
	if c.staged == nil || c.staged.hash != machine.ManifestHash([]byte(stagedWithMachineState)) {
		t.Errorf("the staged candidate is %+v, want one with the hash of the staged manifest", c.staged)
	}
	if c.proven == nil || c.proven.hash != machine.ManifestHash([]byte(provenNode1)) {
		t.Errorf("the proven candidate is %+v, want one with the hash of the proven manifest", c.proven)
	}
	if len(mounts.flags) != 1 || mounts.flags[0] != unix.MS_RDONLY {
		t.Errorf("the mount flags are %v, want one read-only mount", mounts.flags)
	}
	if mounts.mounted != 0 {
		t.Errorf("%d mounts remain after the peek, want none", mounts.mounted)
	}
}

// A staged manifest that would fail every boot is rejected without a
// trial boot. The rejection is recorded on the partition and the staged
// copy is moved aside, so the next boot does not try it again. This
// boot uses the proven manifest.
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
				t.Fatalf("writing the staged manifest: %v", err)
			}
			if err := store.WriteProven([]byte(provenNode1)); err != nil {
				t.Fatalf("writing the proven manifest: %v", err)
			}

			c, err := loadManifestCandidates()
			if err != nil {
				t.Fatalf("loading the manifest candidates: %v", err)
			}

			if c.staged != nil || c.proven == nil {
				t.Errorf("the candidates are staged %+v and proven %+v, want only the proven one", c.staged, c.proven)
			}
			if c.rejection == nil || !strings.Contains(c.rejection.Reason, one.reason) {
				t.Errorf("the rejection in this boot's facts is %+v, want a reason containing %q", c.rejection, one.reason)
			}
			recorded, _ := store.LoadRejection()
			if recorded == nil || recorded.Reason != c.rejection.Reason {
				t.Errorf("the rejection recorded on the partition is %+v, want the reason of this boot's rejection", recorded)
			}
			if staged, _ := store.LoadStaged(); staged != nil {
				t.Errorf("the staged manifest is %q after the rejection, want none", staged)
			}
			if len(mounts.flags) != 2 || mounts.flags[1] != 0 || mounts.mounted != 0 {
				t.Errorf("the mount flags are %v with %d mounts remaining, want a read-only peek, a read-write mount for the rejection, and none remaining", mounts.flags, mounts.mounted)
			}
		})
	}
}

func TestLoadManifestCandidatesRejectsAnUnreadableStagedManifest(t *testing.T) {
	store, _ := machineStateDisk(t)
	if err := os.Mkdir(filepath.Join(manifestPeekPoint, "manifests", "staged.yaml"), 0o755); err != nil {
		t.Fatalf("creating the staged.yaml directory: %v", err)
	}

	c, err := loadManifestCandidates()
	if err != nil {
		t.Fatalf("loading the manifest candidates: %v", err)
	}

	if c.staged != nil || c.rejection == nil || !strings.Contains(c.rejection.Reason, "unreadable") {
		t.Errorf("staged candidate %+v and rejection %+v, want no candidate and a rejection with the reason \"unreadable\"", c.staged, c.rejection)
	}
	if recorded, _ := store.LoadRejection(); recorded == nil {
		t.Error("the partition has no recorded rejection, want one")
	}
}

// The proven manifest is only for recovery, so a damaged copy must not
// stop the boot.
func TestLoadManifestCandidatesSkipsAnUnreadableProvenManifest(t *testing.T) {
	machineStateDisk(t)
	if err := os.Mkdir(filepath.Join(manifestPeekPoint, "manifests", "proven.yaml"), 0o755); err != nil {
		t.Fatalf("creating the proven.yaml directory: %v", err)
	}

	c, err := loadManifestCandidates()
	if err != nil {
		t.Fatalf("loading the manifest candidates: %v", err)
	}
	if c.proven != nil {
		t.Errorf("the proven candidate is %+v, want none because the manifest is unreadable", c.proven)
	}
}

// A rejection from an earlier boot stays on the partition until a
// promotion clears it, so each boot reports it again in its facts.
func TestLoadManifestCandidatesReturnsAnEarlierRejection(t *testing.T) {
	store, _ := machineStateDisk(t)
	if err := store.WriteStaged([]byte(provenNode1)); err != nil {
		t.Fatalf("writing the staged manifest: %v", err)
	}
	if err := store.Reject(machine.Rejection{Reason: "storage failed to reconcile"}); err != nil {
		t.Fatalf("recording the rejection: %v", err)
	}

	c, err := loadManifestCandidates()
	if err != nil {
		t.Fatalf("loading the manifest candidates: %v", err)
	}
	if c.rejection == nil || c.rejection.Reason != "storage failed to reconcile" {
		t.Errorf("the rejection in this boot's facts is %+v, want the reason of the earlier boot", c.rejection)
	}
}

func TestLoadManifestCandidatesReportsAFailedMount(t *testing.T) {
	_, mounts := machineStateDisk(t)
	mounts.mountErr = unix.EIO

	_, err := loadManifestCandidates()
	if err == nil || !strings.Contains(err.Error(), "vda1") {
		t.Errorf("the error is %v, want one that names vda1", err)
	}
}

// When the partition will not mount to record a rejection, the
// rejection still reaches this boot's facts. The staged manifest stays
// in place, so the next boot rejects it again.
func TestRejectStagedReportsTheRejectionWhenTheRecordFails(t *testing.T) {
	_, mounts := machineStateDisk(t)
	mounts.mountErr = unix.EIO

	rejection := rejectStaged(&partition{name: "vda1"}, []byte(provenNode1), "no machineState role")

	if rejection == nil || rejection.Reason != "no machineState role" {
		t.Errorf("rejectStaged returned %+v, want a rejection with the reason \"no machineState role\"", rejection)
	}
}

// A failed unmount is printed on the console and does not stop the
// read or the rejection. A later rewrite of the disk's partition table
// then fails with EBUSY.
func TestLoadManifestCandidatesContinuesPastAFailedUnmount(t *testing.T) {
	store, mounts := machineStateDisk(t)
	mounts.unmountErr = unix.EBUSY
	if err := store.WriteStaged([]byte(provenNode1)); err != nil {
		t.Fatalf("writing the staged manifest: %v", err)
	}
	if err := store.WriteProven([]byte(provenNode1)); err != nil {
		t.Fatalf("writing the proven manifest: %v", err)
	}

	c, err := loadManifestCandidates()
	if err != nil {
		t.Fatalf("loading the manifest candidates: %v", err)
	}

	if c.proven == nil {
		t.Error("there is no proven candidate, want one despite the failed unmount")
	}
	if recorded, _ := store.LoadRejection(); recorded == nil {
		t.Error("the partition has no recorded rejection, want one despite the failed unmount")
	}
}
