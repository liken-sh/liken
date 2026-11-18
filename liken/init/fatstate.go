package main

// The FAT32 boot sector has a dirty bit. The kernel's vfat driver sets
// it when it mounts the volume for writing and clears it on a clean
// unmount, so a volume found with the bit set was not unmounted
// cleanly. FAT has no journal, so the directory entries and the
// allocation table of such a volume can disagree. The system slots and
// the boot home are FAT32 because FAT is the only filesystem that UEFI
// firmware must read.
//
// Each boot reads the bit of each FAT role before it mounts the volume
// for writing, because the mount sets the bit. The result goes to
// Machine status as lastStopUnclean, so you can see that a machine did
// not stop cleanly without a serial console.
//
// When the driver mounts a volume whose bit is already set, it leaves
// the bit set at unmount. So without a clear, the bit stays set after
// one unclean stop until the volume is formatted again. liken clears
// the bit only when it can verify the contents:
//
//   - The slot this machine booted is mounted for writing, and the
//     running root is a loop device over a file on it, so liken leaves
//     its bit set. The next boot from the other slot checks and clears
//     it.
//   - For the other slot, liken reads every artifact that the slot's
//     release document lists and compares it with the document's
//     digest. fsck.fat checks less: it can show that the cluster chains
//     are well formed, but not that the bytes are the kernel that liken
//     meant to boot.
//   - The boot home holds GRUB's configuration and environment, which
//     liken writes again from the manifest on every boot, so liken
//     clears its bit.

import (
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"

	"github.com/liken-sh/liken/liken/disks"
	"github.com/liken-sh/liken/liken/machine"
)

// fatCheckMount is where checkSlotArtifacts mounts a slot read-only.
// Tests set it to a temporary directory.
var fatCheckMount = "/run/liken/fat-check"

// bootedSlotStopEnv holds the booted slot's dirty bit as the initramfs
// read it, before it mounted the slot for writing to reach the system
// image. By the time storage reconciles, that mount has set the bit, so
// a read of the device then would report every boot as unclean. The
// value is in the environment because init execs itself from the new
// root at the end of switch_root (switchroot.go), and the environment
// survives the exec.
const bootedSlotStopEnv = "LIKEN_BOOTED_SLOT_STOP"

const (
	lastStopClean   = "clean"
	lastStopUnclean = "unclean"
)

// recordBootedSlotStop stores a slot's dirty bit in bootedSlotStopEnv.
// Call it before the slot is mounted for writing.
//
// When the read fails, nothing is stored, and fatDirtyAtBoot reads the
// device later. By then this boot's mount has set the bit, so status
// can show an unclean stop that did not happen.
func recordBootedSlotStop(dev string) {
	unclean, err := disks.FAT32Dirty(dev)
	if err != nil {
		fmt.Fprintf(os.Stderr, "liken: storage: reading the dirty bit of the booted slot: %v\n", err)
		return
	}
	stop := lastStopClean
	if unclean {
		stop = lastStopUnclean
	}
	if err := os.Setenv(bootedSlotStopEnv, stop); err != nil {
		fmt.Fprintf(os.Stderr, "liken: storage: setting %s in the environment: %v\n", bootedSlotStopEnv, err)
	}
}

// fatDirtyAtBoot reports whether a FAT role's volume had its dirty bit
// set when this boot started. For the booted slot it returns what the
// initramfs recorded, and for every other volume it reads the device.
func fatDirtyAtBoot(name machine.StorageRoleName, dev string) (bool, error) {
	if isSystemSlot(name) && string(name) == bootedSlotRole() {
		switch os.Getenv(bootedSlotStopEnv) {
		case lastStopUnclean:
			return true, nil
		case lastStopClean:
			return false, nil
		}
		// Nothing was recorded when the initramfs mounted no slot, as
		// in a boot that loads the system image into RAM. Then the slot
		// is not mounted and the device is correct.
	}
	return disks.FAT32Dirty(dev)
}

// readFATStop returns whether the previous stop left a FAT role's
// volume unclean, for Machine status, and when it did, calls
// clearVerifiedFATDirty. It returns true even when the clear succeeds,
// because the value describes the previous stop.
//
// It returns false for a volume that this boot formatted, and for a
// read error, so an unreadable boot sector does not stop the boot. A
// volume that cannot be read fails storage reconciliation later.
func readFATStop(name machine.StorageRoleName, dev string, created bool) bool {
	if created {
		return false
	}
	unclean, err := fatDirtyAtBoot(name, dev)
	if err != nil {
		fmt.Fprintf(os.Stderr, "liken: storage: reading the dirty bit of %s: %v\n", name, err)
		return false
	}
	if !unclean {
		return false
	}
	fmt.Printf("liken: storage: %s had its dirty bit set at the last stop\n", name)
	clearVerifiedFATDirty(name, dev)
	return true
}

// clearVerifiedFATDirty clears a volume's dirty bit when liken can
// verify the volume's contents, as the file comment describes. A
// failure only logs, because a volume that keeps its bit is no worse
// off than before.
func clearVerifiedFATDirty(name machine.StorageRoleName, dev string) {
	switch {
	case isSystemSlot(name) && string(name) == bootedSlotRole():
		fmt.Printf("liken: storage: %s is the slot this machine booted, so its dirty bit stays set until a boot from the other slot\n", name)
		return
	case isSystemSlot(name):
		if err := checkSlotArtifacts(dev); err != nil {
			fmt.Fprintf(os.Stderr, "liken: storage: %s keeps its dirty bit: %v\n", name, err)
			return
		}
		fmt.Printf("liken: storage: every artifact in the release document of %s matches its digest, so clearing its dirty bit\n", name)
	case name == machine.BootHomeRole:
		// This boot writes GRUB's configuration and environment again,
		// so it reads nothing that the last stop left here.
		fmt.Printf("liken: storage: %s is rewritten from the manifest on every boot, so clearing its dirty bit\n", name)
	default:
		return
	}
	if err := disks.ClearFAT32Dirty(dev); err != nil {
		fmt.Fprintf(os.Stderr, "liken: storage: clearing the dirty bit of %s: %v\n", name, err)
	}
}

// bootedSlotRole returns the role of the slot that the liken.slot boot
// parameter selects, or "" when the parameter is absent, as in a boot
// from RAM.
func bootedSlotRole() string {
	switch bootParamValue("liken.slot") {
	case "A":
		return string(machine.SystemARole)
	case "B":
		return string(machine.SystemBRole)
	}
	return ""
}

// checkSlotArtifacts mounts a slot read-only, which does not set the
// dirty bit, and verifies its artifacts with verifySlotContents. It
// unmounts the slot before it returns, so the caller can write the boot
// sector.
func checkSlotArtifacts(dev string) error {
	if err := os.MkdirAll(fatCheckMount, 0o755); err != nil {
		return err
	}
	if err := mountFilesystem(dev, fatCheckMount, "vfat", unix.MS_RDONLY, ""); err != nil {
		return fmt.Errorf("mounting %s read-only to check its artifacts: %w", dev, err)
	}
	err := verifySlotContents(fatCheckMount)
	if unmountErr := unmountFilesystem(fatCheckMount, 0); unmountErr != nil {
		// The caller must not write the boot sector of a mounted
		// volume, so the check fails even after a lazy detach.
		_ = unmountFilesystem(fatCheckMount, unix.MNT_DETACH)
		return fmt.Errorf("unmounting %s after checking its artifacts: %w", dev, unmountErr)
	}
	return err
}

// verifySlotContents checks each artifact that release.yaml at the
// root of a mounted slot lists against its digest. A slot with no
// release.yaml fails.
func verifySlotContents(mount string) error {
	raw, err := os.ReadFile(filepath.Join(mount, "release.yaml"))
	if err != nil {
		return fmt.Errorf("reading the release document of the slot: %w", err)
	}
	release, err := machine.ParseRelease(raw)
	if err != nil {
		return fmt.Errorf("parsing the release document of the slot: %w", err)
	}
	for _, artifact := range release.Artifacts {
		if err := verifyFile(artifact, filepath.Join(mount, artifact.Name)); err != nil {
			return fmt.Errorf("verifying %s against the release document: %w", artifact.Name, err)
		}
	}
	return nil
}
