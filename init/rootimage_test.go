package main

// Tests for locating the system image: the slot selection is pure
// logic over discovered partitions, and the RAM path is a file
// check, so both test against fixtures. The loop device and the
// mounts are QEMU territory.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
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
	// The disk may enumerate anywhere between boots; the GPT name is
	// the identity, exactly as storage recognition treats it.
	parts := []partition{
		{name: "vdc7", disk: "vdc", partName: "liken:systemA"},
	}
	if got, err := slotDevice(parts, "A"); err != nil || got != "/dev/vdc7" {
		t.Errorf("got %q, %v", got, err)
	}
}

func TestSlotDeviceRefusesAmbiguity(t *testing.T) {
	// Two partitions claiming one slot's name is exactly the state a
	// torn claim can leave behind; recognition refuses to guess.
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

func TestFindSystemImagePrefersTheRAMImage(t *testing.T) {
	// A boot whose loader delivered the image into rootfs mounts it
	// from there, no disk or slot parameter needed.
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
	// With no RAM image, the search goes to the machine's real
	// partitions — which on the machine running this test carry no
	// liken GPT names, so the error names the slot it looked for.
	old := ramImage
	ramImage = filepath.Join(t.TempDir(), "absent.sqfs")
	t.Cleanup(func() { ramImage = old })

	if _, err := findSystemImage("A", t.TempDir()); err == nil ||
		!strings.Contains(err.Error(), "liken:systemA") {
		t.Errorf("the slot search must report what it looked for: %v", err)
	}
}

func TestLoadBootModulesReportsAMissingIndex(t *testing.T) {
	// A boot archive without its depmod index is a build bug; the
	// loader reports it and the boot carries on to fail (or not) at
	// the mounts that wanted the modules.
	old := bootModulesDir
	bootModulesDir = t.TempDir()
	t.Cleanup(func() { bootModulesDir = old })

	loadBootModules("overlay") // must not panic; the error is reported on stderr
}

func TestLoadBootModulesReportsAModuleItCannotLoad(t *testing.T) {
	// The index may name a file the archive doesn't carry (or the
	// test may simply not run as root); either way each failure is
	// reported per module and the loader keeps going.
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
	// The re-exec passes the marker; the second run must do nothing,
	// because the switch already happened and the mount tree is no
	// longer empty.
	oldArgs := os.Args
	os.Args = []string{"/liken", switchedMarker}
	t.Cleanup(func() { os.Args = oldArgs })

	maybeSwitchRoot() // must return without touching any mounts
}

// fakeEarlyBoot points the early boot's inputs at fixtures: a command
// line of the test's choosing, a boot module dir with no index, and
// no RAM image. Only meaningful for a non-root test process, whose
// mount attempts all fail with EPERM — which is exactly the degraded
// path these tests exercise.
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
	// The installer runs from rootfs and powers off; a switch would
	// only stuff its oversized payload into the overlay's bound.
	fakeEarlyBoot(t, "console=ttyS0 rdinit=/liken liken.install\n")
	maybeSwitchRoot() // must take the install branch and return
}

func TestMaybeSwitchRootDegradesWhenTheSwitchFails(t *testing.T) {
	// A failed switch is a degraded machine, not a dead one: the
	// error is reported and the boot carries on from rootfs.
	fakeEarlyBoot(t, "console=ttyS0 rdinit=/liken liken.slot=A\n")
	maybeSwitchRoot() // the switch fails (no privileges) and returns
}
