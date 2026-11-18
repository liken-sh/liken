package main

// Tests of reconcileStorage on a disk whose roles already exist, and of
// the wait for declared disks to attach. The partitions are plain files
// under a fake /dev, and fakeStorageMounts in mounttable_test.go
// records the mount calls.

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
	"testing/synctest"
	"time"

	"github.com/liken-sh/liken/liken/machine"
)

// bootRolesDisk builds the disk of a BIOS machine whose boot roles
// already exist: biosBoot as vda1, the boot home as vda2, and slot A as
// vda3. The boot home's FAT volume has its dirty bit set,
// and slot A's is clean. It returns the fake /dev directory.
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

// When every declared role already exists, reconcileStorage creates
// and grows nothing. It mounts each FAT role, leaves the raw biosBoot
// partition unmounted, and records each role's device in the status.
// The boot home's unclean unmount goes into the status, because
// otherwise only the serial console shows it. The booted slot's stop
// state comes from bootedSlotStopEnv, which bootedSlot sets to clean.
func TestReconcileStorageRecordsTheDeviceOfEachExistingRole(t *testing.T) {
	dev := bootRolesDisk(t)
	mounts := fakeStorageMounts(t)
	bootedSlot(t, "A", lastStopClean)
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
		t.Fatalf("reconcileStorage: %v", err)
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

// A SATA or USB disk can attach seconds after its driver loads. The
// wait must end at the first poll after the disk appears, not at the
// 30-second deadline.
func TestAwaitStorageDevicesWaitsForALateDisk(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		sys, dev := fakeMachine(t)
		// Build the disk in another directory, so that attaching it is
		// one rename.
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
			t.Fatalf("attaching the disk: %v", err)
		}
		if elapsed != 2*time.Second {
			t.Errorf("the wait took %s, want the first poll after the disk attached, at 2s", elapsed)
		}
	})
}

// A disk that never attaches must not block the boot. At the deadline
// awaitStorageDevices returns, and reconcileStorage then reports the
// missing disk.
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
			t.Errorf("the console shows %q, want the line that says the declared disks did not attach", got)
		}
	})
}
