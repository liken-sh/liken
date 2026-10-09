package main

// Tests for reconcileStorage as a whole on a disk whose roles are
// already claimed, and for the bounded wait for declared disks. The
// mount syscalls are replaced by the recorder in mounttable_test.go,
// and the partitions are plain files under a fake /dev.

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
	"testing/synctest"
	"time"

	"github.com/liken-sh/liken/liken/machine"
)

// bootRolesDisk builds the disk of a BIOS machine whose boot roles are
// claimed: biosBoot as vda1, the boot home as vda2, and slot A as vda3.
// The boot home's volume still carries the mark of a stop that did not
// release it, and slot A's volume is clean. It returns the fake /dev.
func bootRolesDisk(t *testing.T) string {
	t.Helper()
	sys, dev := fakeMachine(t)
	addDisk(t, sys, dev, "vda", 1<<30, nil)
	addPartition(t, sys, "vda", "vda1", "liken:biosBoot", 1<<20)
	addPartition(t, sys, "vda", "vda2", "liken:bootHome", 64<<20)
	addPartition(t, sys, "vda", "vda3", "liken:systemA", 64<<20)
	fatVolumeAt(t, filepath.Join(dev, "vda2"), true)
	fatVolumeAt(t, filepath.Join(dev, "vda3"), false)
	return dev
}

// A boot that recognizes every declared role claims nothing and grows
// nothing. It mounts each FAT role at its path and leaves the raw
// biosBoot partition unmounted, and the status records where each role
// landed. The boot home's unclean stop reaches status, because a fact
// that only the serial console printed is invisible to an operator.
// The booted slot reports what the initramfs read before it mounted the
// slot.
func TestReconcileStorageRecordsWhereEachRecognizedRoleLanded(t *testing.T) {
	dev := bootRolesDisk(t)
	mounts := fakeStorageMounts(t)
	bootedSlot(t, "A", stopMarkClean)
	home := fakeRoleMountPath(t, machine.BootHomeRole)
	slotA := fakeRoleMountPath(t, machine.SystemARole)
	disk := filepath.Join(dev, "vda")
	spec := machine.StorageSpec{
		BIOSBoot: &machine.StorageRole{Device: disk, Size: "1Mi"},
		BootHome: &machine.StorageRole{Device: disk, Size: "64Mi"},
		SystemA:  &machine.StorageRole{Device: disk, Size: "64Mi"},
	}

	var status machine.StorageStatus
	var err error
	capturedStdout(t, func() { status, err = reconcileStorage(spec) })
	if err != nil {
		t.Fatal(err)
	}

	want := machine.AllRolesInMemory()
	want.BIOSBoot = machine.StorageRoleStatus{Backing: machine.BackingPartition, Device: "vda1",
		Partition: "liken:biosBoot", CapacityBytes: 1 << 20}
	want.BootHome = machine.StorageRoleStatus{Backing: machine.BackingPartition, Device: "vda2",
		Partition: "liken:bootHome", CapacityBytes: 64 << 20, LastStopUnclean: true}
	want.SystemA = machine.StorageRoleStatus{Backing: machine.BackingPartition, Device: "vda3",
		Partition: "liken:systemA", CapacityBytes: 64 << 20}
	if status != want {
		t.Errorf("status = %+v\nwant     %+v", status, want)
	}
	wantMounts := []storageMount{
		{source: dev + "/vda2", target: home, fstype: "vfat", flags: slotMountFlags},
		{source: dev + "/vda3", target: slotA, fstype: "vfat", flags: slotMountFlags},
	}
	if !slices.Equal(mounts.mounts, wantMounts) {
		t.Errorf("mounts = %+v\nwant     %+v", mounts.mounts, wantMounts)
	}
}

// A declared disk can attach seconds after storage first looks,
// because a SATA link trains and a USB device negotiates after their
// drivers load. The wait must end at the first poll after the disk
// appears, not at its 30-second deadline.
func TestAwaitStorageDevicesWaitsForALateDisk(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		sys, dev := fakeMachine(t)
		// The disk is built out of the way first, so the goroutine that
		// attaches it does one rename and reports one error.
		staged := t.TempDir()
		addDisk(t, staged, dev, "vdb", 1<<30, nil)
		attached := make(chan error, 1)
		go func() {
			time.Sleep(1800 * time.Millisecond)
			attached <- os.Rename(filepath.Join(staged, "vdb"), filepath.Join(sys, "vdb"))
		}()
		roles := []machine.DeclaredRole{declared(machine.ClusterStateRole, filepath.Join(dev, "vdb"), "")}

		begin := time.Now()
		awaitStorageDevices(roles)
		elapsed := time.Since(begin)

		if err := <-attached; err != nil {
			t.Fatal(err)
		}
		if elapsed != 2*time.Second {
			t.Errorf("the wait took %s, want the first poll after the disk attached, at 2s", elapsed)
		}
	})
}

// A disk that never attaches must not hold the boot forever. At the
// deadline the wait ends, and the ordinary errors of planning report
// what is missing.
func TestAwaitStorageDevicesGivesUpAtTheDeadline(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		_, dev := fakeMachine(t)
		roles := []machine.DeclaredRole{declared(machine.ClusterStateRole, filepath.Join(dev, "vdb"), "")}
		stderr := fakeLogStream(t)

		begin := time.Now()
		awaitStorageDevices(roles)

		if elapsed := time.Since(begin); elapsed != 30*time.Second {
			t.Errorf("the wait took %s, want its 30s deadline", elapsed)
		}
		if got := stderr(); got != "liken: storage: the declared disks did not all attach within 30s\n" {
			t.Errorf("the console must say the disks did not attach: %q", got)
		}
	})
}
