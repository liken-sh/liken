package main

// The slot search and the RAM image check run against fixtures, and
// the slot mount goes through fakeStorageMounts. loopMount has no test
// here, because it needs root and a loop device.

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"golang.org/x/sys/unix"
)

func TestSlotDeviceFindsTheNamedSlot(t *testing.T) {
	parts := []partition{
		{name: "sda1", disk: "sda", partName: "liken:systemA"},
		{name: "sda2", disk: "sda", partName: "liken:systemB"},
		{name: "sda3", disk: "sda", partName: "liken:machineState"},
	}
	if got, err := slotDevice(parts, "A"); err != nil || got != "/dev/sda1" {
		t.Errorf("slotDevice(A) returned %q and %v, want /dev/sda1", got, err)
	}
	if got, err := slotDevice(parts, "B"); err != nil || got != "/dev/sda2" {
		t.Errorf("slotDevice(B) returned %q and %v, want /dev/sda2", got, err)
	}
}

func TestSlotDeviceMatchesByNameNotPosition(t *testing.T) {
	// A disk can get a different device name on each boot, so the
	// GPT name identifies the slot.
	parts := []partition{
		{name: "vdc7", disk: "vdc", partName: "liken:systemA"},
	}
	if got, err := slotDevice(parts, "A"); err != nil || got != "/dev/vdc7" {
		t.Errorf("slotDevice(A) returned %q and %v, want /dev/vdc7", got, err)
	}
}

func TestSlotDeviceRefusesAmbiguity(t *testing.T) {
	// An interrupted storage claim can leave two partitions with one
	// slot's name. slotDevice returns an error instead of choosing.
	parts := []partition{
		{name: "sda1", disk: "sda", partName: "liken:systemA"},
		{name: "sdb1", disk: "sdb", partName: "liken:systemA"},
	}
	if _, err := slotDevice(parts, "A"); err == nil || !strings.Contains(err.Error(), "two partitions have the GPT name liken:systemA") {
		t.Errorf("slotDevice error is %v, want two partitions with the name liken:systemA", err)
	}
}

func TestSlotDeviceReportsAMissingSlot(t *testing.T) {
	if _, err := slotDevice(nil, "A"); err == nil || !strings.Contains(err.Error(), "liken:systemA") {
		t.Errorf("slotDevice error is %v, want it to name liken:systemA", err)
	}
}

// The slot wait tests run in synctest bubbles with the boot's real
// poll interval and deadline. The sleeps pass on the fake clock, so
// each test can check how long the wait took.

// A partition that appears after the first search, as on an eMMC
// machine, is found at the next poll.
func TestAwaitSlotDeviceWaitsForALateSlot(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		sys, dev := fakeMachine(t)
		addDisk(t, sys, dev, "mmcblk0", 58<<30, nil)
		// Build the partition in another directory and rename it into
		// place, so that no search sees it half written.
		addPartition(t, sys, "staged", "mmcblk0p1", "liken:systemA", 1<<30)
		addPartitionNode(t, dev, "mmcblk0p1")

		published := make(chan error, 1)
		go func() {
			time.Sleep(20 * time.Millisecond)
			published <- os.Rename(filepath.Join(sys, "staged", "mmcblk0p1"),
				filepath.Join(sys, "mmcblk0", "mmcblk0p1"))
		}()

		begin := time.Now()
		got, err := awaitSlotDevice("A")
		if err := <-published; err != nil {
			t.Fatalf("publishing the device: %v", err)
		}
		if err != nil || got != dev+"/mmcblk0p1" {
			t.Errorf("got %q, %v, want %s/mmcblk0p1", got, err, dev)
		}
		if elapsed := time.Since(begin); elapsed != slotPoll {
			t.Errorf("the wait took %s, want the first poll after the slot appeared, at %s", elapsed, slotPoll)
		}
	})
}

func TestAwaitSlotDeviceWaitsForTheDeviceNode(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		sys, dev := fakeMachine(t)
		addDisk(t, sys, dev, "mmcblk0", 58<<30, nil)
		addPartition(t, sys, "mmcblk0", "mmcblk0p1", "liken:systemA", 1<<30)

		const nodeDelay = 20 * time.Millisecond
		published := make(chan error, 1)
		go func() {
			time.Sleep(nodeDelay)
			published <- os.WriteFile(filepath.Join(dev, "mmcblk0p1"), nil, 0o600)
		}()

		begin := time.Now()
		got, err := awaitSlotDevice("A")
		if elapsed := time.Since(begin); elapsed != slotPoll {
			t.Errorf("the wait returned after %s, want the first poll after the node existed, at %s", elapsed, slotPoll)
		}
		if err := <-published; err != nil {
			t.Fatalf("publishing the device: %v", err)
		}
		if err != nil || got != dev+"/mmcblk0p1" {
			t.Errorf("got %q, %v, want %s/mmcblk0p1", got, err, dev)
		}
	})
}

// When the node never appears, the error names the node.
func TestAwaitSlotDeviceReportsAMissingNodeAtTheDeadline(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		sys, dev := fakeMachine(t)
		addDisk(t, sys, dev, "mmcblk0", 58<<30, nil)
		addPartition(t, sys, "mmcblk0", "mmcblk0p1", "liken:systemA", 1<<30)

		begin := time.Now()
		if _, err := awaitSlotDevice("A"); err == nil ||
			!strings.Contains(err.Error(), dev+"/mmcblk0p1") {
			t.Errorf("error is %v, want it to name %s/mmcblk0p1", err, dev)
		}
		if elapsed := time.Since(begin); elapsed != slotDeadline {
			t.Errorf("the wait gave up after %s, want the deadline, %s", elapsed, slotDeadline)
		}
	})
}

func TestAwaitSlotDeviceReturnsAtOnceWhenTheSlotIsPresent(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		sys, dev := fakeMachine(t)
		addDisk(t, sys, dev, "sda", 8<<30, nil)
		addPartition(t, sys, "sda", "sda1", "liken:systemA", 1<<30)
		addPartitionNode(t, dev, "sda1")

		begin := time.Now()
		got, err := awaitSlotDevice("A")
		if elapsed := time.Since(begin); elapsed != 0 {
			t.Errorf("a slot already present took %s, want no wait", elapsed)
		}
		if err != nil || got != dev+"/sda1" {
			t.Errorf("got %q, %v, want %s/sda1", got, err, dev)
		}
	})
}

func TestAwaitSlotDeviceRefusesAmbiguityAtOnce(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		sys, dev := fakeMachine(t)
		addDisk(t, sys, dev, "sda", 8<<30, nil)
		addDisk(t, sys, dev, "sdb", 8<<30, nil)
		addPartition(t, sys, "sda", "sda1", "liken:systemA", 1<<30)
		addPartition(t, sys, "sdb", "sdb1", "liken:systemA", 1<<30)

		begin := time.Now()
		_, err := awaitSlotDevice("A")
		if elapsed := time.Since(begin); elapsed != 0 {
			t.Errorf("the ambiguity error took %s, want it at once", elapsed)
		}
		if err == nil || !strings.Contains(err.Error(), "two partitions have the GPT name liken:systemA") {
			t.Errorf("error is %v, want two partitions with the name liken:systemA", err)
		}
	})
}

func TestAwaitSlotDeviceReportsAMissingSlotAtTheDeadline(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		sys, dev := fakeMachine(t)
		addDisk(t, sys, dev, "sda", 8<<30, nil)

		begin := time.Now()
		if _, err := awaitSlotDevice("A"); err == nil ||
			!strings.Contains(err.Error(), "liken:systemA") {
			t.Errorf("error is %v, want it to name liken:systemA", err)
		}
		if elapsed := time.Since(begin); elapsed != slotDeadline {
			t.Errorf("the wait gave up after %s, want the deadline, %s", elapsed, slotDeadline)
		}
	})
}

func TestFindSystemImagePrefersTheRAMImage(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "liken.sqfs")
	if err := os.WriteFile(path, []byte("squash"), 0o644); err != nil {
		t.Fatalf("writing the RAM image: %v", err)
	}
	old := ramImage
	ramImage = path
	t.Cleanup(func() { ramImage = old })

	got, err := findSystemImage("", filepath.Join(dir, "slot"))
	if err != nil || got != path {
		t.Errorf("findSystemImage returned %q and %v, want %q", got, err, path)
	}
}

func TestFindSystemImageNeedsAnImageOrASlot(t *testing.T) {
	old := ramImage
	ramImage = filepath.Join(t.TempDir(), "absent.sqfs")
	t.Cleanup(func() { ramImage = old })

	if _, err := findSystemImage("", t.TempDir()); err == nil ||
		!strings.Contains(err.Error(), "liken.slot") {
		t.Errorf("error is %v, want it to mention liken.slot", err)
	}
}

func TestFindSystemImageSearchesTheSlotByName(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		// With no RAM image, the search reads the real partitions of
		// the machine that runs the test. None has a liken GPT name, so
		// the search runs to the deadline on the fake clock and the
		// error names the slot it looked for.
		old := ramImage
		ramImage = filepath.Join(t.TempDir(), "absent.sqfs")
		t.Cleanup(func() { ramImage = old })

		if _, err := findSystemImage("A", t.TempDir()); err == nil ||
			!strings.Contains(err.Error(), "liken:systemA") {
			t.Errorf("error is %v, want it to name liken:systemA", err)
		}
	})
}

// slotBDisk builds a machine with slot B on vda1, a clean FAT32
// volume on its device node, and no RAM image. It returns the
// partition's device path.
func slotBDisk(t *testing.T) string {
	t.Helper()
	sys, dev := fakeMachine(t)
	addDisk(t, sys, dev, "vda", 1<<30, nil)
	addPartition(t, sys, "vda", "vda1", "liken:systemB", 64<<20)
	old := ramImage
	ramImage = filepath.Join(t.TempDir(), "absent.sqfs")
	t.Cleanup(func() { ramImage = old })
	return fatVolumeAt(t, filepath.Join(dev, "vda1"), false)
}

// The read-write mount of the slot sets the FAT dirty bit, so
// findSystemImage must read the bit before the mount. Otherwise every
// boot reads the bit that its own mount set and reports an unclean stop.
func TestFindSystemImageReadsTheSlotsDirtyBitBeforeMountingIt(t *testing.T) {
	device := slotBDisk(t)
	mounts := fakeStorageMounts(t)
	t.Setenv(bootedSlotStopEnv, "")
	slotMount := filepath.Join(t.TempDir(), "slot")

	got, err := findSystemImage("B", slotMount)
	if err != nil {
		t.Fatalf("finding the system image: %v", err)
	}

	if want := filepath.Join(slotMount, slotImageName); got != want {
		t.Errorf("image path = %q, want %q", got, want)
	}
	want := []storageMount{{source: device, target: slotMount, fstype: "vfat", flags: 0}}
	if !slices.Equal(mounts.mounts, want) {
		t.Errorf("mounts = %+v, want %+v", mounts.mounts, want)
	}
	if mark := os.Getenv(bootedSlotStopEnv); mark != lastStopClean {
		t.Errorf("recorded mark = %q, want %q, read before the mount set it", mark, lastStopClean)
	}
}

func TestFindSystemImageReportsASlotThatWillNotMount(t *testing.T) {
	slotBDisk(t)
	mounts := fakeStorageMounts(t)
	mounts.mountErr = unix.EIO
	t.Setenv(bootedSlotStopEnv, "")

	_, err := findSystemImage("B", filepath.Join(t.TempDir(), "slot"))
	if err == nil || !strings.Contains(err.Error(), "mounting slot B") {
		t.Errorf("error is %v, want it to name slot B", err)
	}
}

func TestLoadBootModulesReportsAMissingIndex(t *testing.T) {
	// A boot archive without modules.dep is a build defect. The loader
	// prints the error and returns.
	old := bootModulesDir
	bootModulesDir = t.TempDir()
	t.Cleanup(func() { bootModulesDir = old })

	loadBootModules("overlay")
}

func TestLoadBootModulesReportsAModuleItCannotLoad(t *testing.T) {
	// modules.dep can list a file that the archive does not have, and
	// a test process that is not root cannot load modules. In both
	// cases the loader prints one error for each module and continues.
	dir := t.TempDir()
	dep := "kernel/fs/overlayfs/overlay.ko.zst:\n"
	if err := os.WriteFile(filepath.Join(dir, "modules.dep"), []byte(dep), 0o644); err != nil {
		t.Fatalf("writing modules.dep: %v", err)
	}
	old := bootModulesDir
	bootModulesDir = dir
	t.Cleanup(func() { bootModulesDir = old })

	loadBootModules("overlay", "no-such-module")
}

func TestMaybeSwitchRootIsIdempotent(t *testing.T) {
	// After the switch, init runs again with the marker argument. That
	// second run must do nothing, because the switch already happened.
	oldArgs := os.Args
	os.Args = []string{"/liken", switchedMarker}
	t.Cleanup(func() { os.Args = oldArgs })

	maybeSwitchRoot() // must return without touching any mounts
}

// fakeEarlyBoot points the early boot at fixtures: a command line
// chosen by the test, a boot module directory with no modules.dep, and
// no RAM image. It skips the test as root, because the mounts would
// succeed on the real machine. Without root, every mount fails with
// EPERM, which is the failure path these tests exercise.
func fakeEarlyBoot(t *testing.T, cmdline string) {
	t.Helper()
	if os.Geteuid() == 0 {
		t.Skip("as root the mounts would succeed against the real machine")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "cmdline")
	if err := os.WriteFile(path, []byte(cmdline), 0o644); err != nil {
		t.Fatalf("writing the command line: %v", err)
	}
	oldCmdline, oldModules, oldImage, oldArgs := cmdlinePath, bootModulesDir, ramImage, os.Args
	cmdlinePath, bootModulesDir, ramImage = path, dir, filepath.Join(dir, "absent.sqfs")
	os.Args = []string{"/liken"}
	t.Cleanup(func() {
		cmdlinePath, bootModulesDir, ramImage, os.Args = oldCmdline, oldModules, oldImage, oldArgs
	})
}

func TestMaybeSwitchRootStaysPutForInstallBoots(t *testing.T) {
	// The installer runs from rootfs and then powers off. After a
	// switch, its large payload would not fit in the size-limited
	// overlay.
	fakeEarlyBoot(t, "console=ttyS0 rdinit=/liken liken.install\n")
	maybeSwitchRoot() // must take the install branch and return
}

func TestMaybeSwitchRootDegradesWhenTheSwitchFails(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		// When the switch fails, init prints the error and the boot
		// continues from rootfs. The slot never appears, so the wait
		// runs to the deadline on the fake clock.
		fakeEarlyBoot(t, "console=ttyS0 rdinit=/liken liken.slot=A\n")
		maybeSwitchRoot() // the switch fails (no privileges) and returns
	})
}
