package main

// Tests for locating the system image. Slot selection is pure logic
// over discovered partitions, and the RAM path is just a file check,
// so both are tested against fixtures. The loop device and the
// mounts are tested only under QEMU.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSlotDeviceFindsTheNamedSlot(t *testing.T) {
	parts := []partition{
		{name: "sda1", disk: "sda", partName: "liken:systemA"},
		{name: "sda2", disk: "sda", partName: "liken:systemB"},
		{name: "sda3", disk: "sda", partName: "liken:machineState"},
	}
	if got, err := slotDevice(parts, "A"); err != nil || got != "/dev/sda1" {
		t.Errorf("slot A: %q, %v", got, err)
	}
	if got, err := slotDevice(parts, "B"); err != nil || got != "/dev/sda2" {
		t.Errorf("slot B: %q, %v", got, err)
	}
}

func TestSlotDeviceMatchesByNameNotPosition(t *testing.T) {
	// A disk may enumerate at a different position between boots. The
	// GPT name is the identity, the same way storage recognition
	// treats it.
	parts := []partition{
		{name: "vdc7", disk: "vdc", partName: "liken:systemA"},
	}
	if got, err := slotDevice(parts, "A"); err != nil || got != "/dev/vdc7" {
		t.Errorf("got %q, %v", got, err)
	}
}

func TestSlotDeviceRefusesAmbiguity(t *testing.T) {
	// Two partitions that claim one slot's name is exactly the state
	// that a torn claim can leave behind. Recognition refuses to
	// guess in that case.
	parts := []partition{
		{name: "sda1", disk: "sda", partName: "liken:systemA"},
		{name: "sdb1", disk: "sdb", partName: "liken:systemA"},
	}
	if _, err := slotDevice(parts, "A"); err == nil || !strings.Contains(err.Error(), "refusing to guess") {
		t.Errorf("ambiguity must be refused: %v", err)
	}
}

func TestSlotDeviceReportsAMissingSlot(t *testing.T) {
	if _, err := slotDevice(nil, "A"); err == nil || !strings.Contains(err.Error(), "liken:systemA") {
		t.Errorf("a missing slot names what it looked for: %v", err)
	}
}

// slotWaitTiming shortens the slot wait for a test, and restores the boot's
// own timing when the test ends. Because slotPoll and slotDeadline are package
// variables, tests in this package must not run in parallel.
func slotWaitTiming(t *testing.T, poll, deadline time.Duration) {
	t.Helper()
	oldPoll, oldDeadline := slotPoll, slotDeadline
	slotPoll, slotDeadline = poll, deadline
	t.Cleanup(func() { slotPoll, slotDeadline = oldPoll, oldDeadline })
}

// mmc card detection runs on a workqueue after the host controller
// registers, so on an eMMC machine the slot partition appears tens
// of milliseconds after init first looks for it. liken.slot= on the
// command line means the boot loader already read this slot, so the
// partition is on this
// machine and waiting for it is always correct.
func TestAwaitSlotDeviceWaitsForALateSlot(t *testing.T) {
	sys, dev := fakeMachine(t)
	slotWaitTiming(t, time.Millisecond, 5*time.Second)
	addDisk(t, sys, dev, "mmcblk0", 58<<30, nil)
	// The partition is built out of the way first, so the goroutine that
	// publishes it does one rename and reports one error.
	addPartition(t, sys, "staged", "mmcblk0p1", "liken:systemA", 1<<30)
	addPartitionNode(t, dev, "mmcblk0p1")

	published := make(chan error, 1)
	go func() {
		time.Sleep(20 * time.Millisecond)
		published <- os.Rename(filepath.Join(sys, "staged", "mmcblk0p1"),
			filepath.Join(sys, "mmcblk0", "mmcblk0p1"))
	}()

	got, err := awaitSlotDevice("A")
	if err := <-published; err != nil {
		t.Fatal(err)
	}
	if err != nil || got != dev+"/mmcblk0p1" {
		t.Errorf("got %q, %v, want %s/mmcblk0p1", got, err, dev)
	}
}

// The kernel publishes a partition's sysfs entry before devtmpfs
// creates its node, so a poll landing in that window sees a
// partition it cannot mount. The wait must keep waiting for the
// node too.
func TestAwaitSlotDeviceWaitsForTheDeviceNode(t *testing.T) {
	sys, dev := fakeMachine(t)
	slotWaitTiming(t, time.Millisecond, 5*time.Second)
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
	if elapsed := time.Since(begin); elapsed < nodeDelay {
		t.Errorf("the wait returned after %s, before the node existed", elapsed)
	}
	if err := <-published; err != nil {
		t.Fatal(err)
	}
	if err != nil || got != dev+"/mmcblk0p1" {
		t.Errorf("got %q, %v, want %s/mmcblk0p1", got, err, dev)
	}
}

// A node that never appears reports the node it waited for, not
// the partition, because the partition is right there in sysfs.
func TestAwaitSlotDeviceReportsAMissingNodeAtTheDeadline(t *testing.T) {
	sys, dev := fakeMachine(t)
	slotWaitTiming(t, time.Millisecond, 20*time.Millisecond)
	addDisk(t, sys, dev, "mmcblk0", 58<<30, nil)
	addPartition(t, sys, "mmcblk0", "mmcblk0p1", "liken:systemA", 1<<30)

	if _, err := awaitSlotDevice("A"); err == nil ||
		!strings.Contains(err.Error(), dev+"/mmcblk0p1") {
		t.Errorf("a node that never appears names itself: %v", err)
	}
}

// A slot that is already there costs the boot nothing. The poll
// here is longer than the test tolerates, so a sleep before the
// first search would fail this test on elapsed time.
func TestAwaitSlotDeviceReturnsAtOnceWhenTheSlotIsPresent(t *testing.T) {
	sys, dev := fakeMachine(t)
	slotWaitTiming(t, 2*time.Second, 10*time.Second)
	addDisk(t, sys, dev, "sda", 8<<30, nil)
	addPartition(t, sys, "sda", "sda1", "liken:systemA", 1<<30)
	addPartitionNode(t, dev, "sda1")

	begin := time.Now()
	got, err := awaitSlotDevice("A")
	if elapsed := time.Since(begin); elapsed > time.Second {
		t.Errorf("a slot already present took %s; the first search must not wait", elapsed)
	}
	if err != nil || got != dev+"/sda1" {
		t.Errorf("got %q, %v, want %s/sda1", got, err, dev)
	}
}

// Waiting cannot fix an ambiguity: a partition that attaches later
// cannot un-name the two that already carry the slot's name, so the
// refusal stands as soon as the search sees it.
func TestAwaitSlotDeviceRefusesAmbiguityAtOnce(t *testing.T) {
	sys, dev := fakeMachine(t)
	slotWaitTiming(t, time.Millisecond, 5*time.Second)
	addDisk(t, sys, dev, "sda", 8<<30, nil)
	addDisk(t, sys, dev, "sdb", 8<<30, nil)
	addPartition(t, sys, "sda", "sda1", "liken:systemA", 1<<30)
	addPartition(t, sys, "sdb", "sdb1", "liken:systemA", 1<<30)

	begin := time.Now()
	_, err := awaitSlotDevice("A")
	if elapsed := time.Since(begin); elapsed > time.Second {
		t.Errorf("the refusal took %s; an ambiguity must not wait out the deadline", elapsed)
	}
	if err == nil || !strings.Contains(err.Error(), "refusing to guess") {
		t.Errorf("ambiguity must be refused: %v", err)
	}
}

// At the deadline the search reports what it looked for, and the
// boot continues on rootfs, exactly as it did before the wait
// existed.
func TestAwaitSlotDeviceReportsAMissingSlotAtTheDeadline(t *testing.T) {
	sys, dev := fakeMachine(t)
	slotWaitTiming(t, time.Millisecond, 20*time.Millisecond)
	addDisk(t, sys, dev, "sda", 8<<30, nil)

	if _, err := awaitSlotDevice("A"); err == nil ||
		!strings.Contains(err.Error(), "no partition carries liken:systemA") {
		t.Errorf("a slot that never appears reports what it looked for: %v", err)
	}
}

func TestFindSystemImagePrefersTheRAMImage(t *testing.T) {
	// If the loader delivers the image into rootfs, the boot mounts
	// the image from there. It needs no disk or slot parameter.
	dir := t.TempDir()
	path := filepath.Join(dir, "liken.sqfs")
	if err := os.WriteFile(path, []byte("squash"), 0o644); err != nil {
		t.Fatal(err)
	}
	old := ramImage
	ramImage = path
	t.Cleanup(func() { ramImage = old })

	got, err := findSystemImage("", filepath.Join(dir, "slot"))
	if err != nil || got != path {
		t.Errorf("got %q, %v", got, err)
	}
}

func TestFindSystemImageNeedsAnImageOrASlot(t *testing.T) {
	old := ramImage
	ramImage = filepath.Join(t.TempDir(), "absent.sqfs")
	t.Cleanup(func() { ramImage = old })

	if _, err := findSystemImage("", t.TempDir()); err == nil ||
		!strings.Contains(err.Error(), "liken.slot") {
		t.Errorf("a boot with neither image nor slot must say so: %v", err)
	}
}

func TestFindSystemImageSearchesTheSlotByName(t *testing.T) {
	// With no RAM image, the search checks the machine's real
	// partitions. On the machine that runs this test, none of the
	// partitions carry liken GPT names, so the error names the slot
	// that the search looked for.
	//
	// The slot never appears in this test, so it shortens the wait
	// rather than paying the boot's whole deadline.
	slotWaitTiming(t, time.Millisecond, 20*time.Millisecond)
	old := ramImage
	ramImage = filepath.Join(t.TempDir(), "absent.sqfs")
	t.Cleanup(func() { ramImage = old })

	if _, err := findSystemImage("A", t.TempDir()); err == nil ||
		!strings.Contains(err.Error(), "liken:systemA") {
		t.Errorf("the slot search must report what it looked for: %v", err)
	}
}

func TestLoadBootModulesReportsAMissingIndex(t *testing.T) {
	// A boot archive without its depmod index has a build bug. The
	// loader reports the error, and the boot continues. It may still
	// fail later, at the mounts that needed the missing modules.
	old := bootModulesDir
	bootModulesDir = t.TempDir()
	t.Cleanup(func() { bootModulesDir = old })

	loadBootModules("overlay") // must not panic; it reports the error on stderr
}

func TestLoadBootModulesReportsAModuleItCannotLoad(t *testing.T) {
	// The index may name a file that the archive does not carry. Or
	// the test may simply not run as root. Either way, the loader
	// reports each failure per module and keeps going.
	dir := t.TempDir()
	dep := "kernel/fs/overlayfs/overlay.ko.zst:\n"
	if err := os.WriteFile(filepath.Join(dir, "modules.dep"), []byte(dep), 0o644); err != nil {
		t.Fatal(err)
	}
	old := bootModulesDir
	bootModulesDir = dir
	t.Cleanup(func() { bootModulesDir = old })

	loadBootModules("overlay", "no-such-module")
}

func TestMaybeSwitchRootIsIdempotent(t *testing.T) {
	// The re-exec passes the marker. The second run must do nothing,
	// because the switch already happened and the mount tree is no
	// longer empty.
	oldArgs := os.Args
	os.Args = []string{"/liken", switchedMarker}
	t.Cleanup(func() { os.Args = oldArgs })

	maybeSwitchRoot() // must return without touching any mounts
}

// fakeEarlyBoot points the early boot's inputs at fixtures: a command
// line chosen by the test, a boot module directory with no index,
// and no RAM image. This only matters for a non-root test process.
// In that case, every mount attempt fails with EPERM, which is
// exactly the degraded path that these tests exercise.
func fakeEarlyBoot(t *testing.T, cmdline string) {
	t.Helper()
	if os.Geteuid() == 0 {
		t.Skip("as root the mounts would succeed against the real machine")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "cmdline")
	if err := os.WriteFile(path, []byte(cmdline), 0o644); err != nil {
		t.Fatal(err)
	}
	oldCmdline, oldModules, oldImage, oldArgs := cmdlinePath, bootModulesDir, ramImage, os.Args
	cmdlinePath, bootModulesDir, ramImage = path, dir, filepath.Join(dir, "absent.sqfs")
	os.Args = []string{"/liken"}
	t.Cleanup(func() {
		cmdlinePath, bootModulesDir, ramImage, os.Args = oldCmdline, oldModules, oldImage, oldArgs
	})
}

func TestMaybeSwitchRootStaysPutForInstallBoots(t *testing.T) {
	// The installer runs from rootfs and then powers off. If it
	// switched root, its large payload would exceed the overlay's
	// size limit.
	fakeEarlyBoot(t, "console=ttyS0 rdinit=/liken liken.install\n")
	maybeSwitchRoot() // must take the install branch and return
}

func TestMaybeSwitchRootDegradesWhenTheSwitchFails(t *testing.T) {
	// A failed switch leaves a degraded machine, not a dead one. The
	// error is reported, and the boot continues from rootfs.
	//
	// The slot never appears in this test either, so the wait is
	// shortened for the same reason.
	slotWaitTiming(t, time.Millisecond, 20*time.Millisecond)
	fakeEarlyBoot(t, "console=ttyS0 rdinit=/liken liken.slot=A\n")
	maybeSwitchRoot() // the switch fails (no privileges) and returns
}
