package main

// What a machine does about a FAT volume that was never released.
//
// The system slots and the boot home are FAT32, because firmware reads
// FAT32 and reads almost nothing else. FAT keeps no journal, so its
// whole account of its own health is one bit in the boot sector: on
// while the volume is mounted for writing, off once it is released.
// A volume found with the bit on was not released, and its directory
// entries and allocation table may disagree.
//
// Two things follow, and this file does both.
//
// The machine reports it. A boot reads the bit before it mounts the
// volume, because mounting for writing sets the bit and a later read
// would only say that the volume is in use now. The answer reaches
// Machine status as lastStopUnclean, so an operator sees that a
// machine did not stop cleanly without reading a serial console.
//
// "Before it mounts the volume" is earlier than it sounds for one of
// the volumes. The initramfs mounts the slot it boots from to reach
// the system image, so that slot's answer has to be read there and
// carried forward, rather than read where the other roles are read.
//
// The machine clears it, where it can say honestly that the volume is
// good. This is necessary rather than tidy: the driver will not clear
// a bit it found set, so one unclean stop marks a volume for the rest
// of its life and the warning stops meaning anything. liken clears the
// bit only after it has read every artifact the slot claims to hold
// and checked each one against the digest in the slot's own release
// document. That is a stronger check than a filesystem repair, which
// can prove that a chain of clusters is well formed but cannot tell
// whether the bytes in it are the kernel liken meant to boot.
//
// Three volumes cannot be treated the same way:
//
//   - The slot this machine booted is already mounted for writing by
//     the time storage reconciles, and the running root is a loop
//     device over a file on it. Its bit is reported and left alone.
//     It clears on the next boot from the other slot, when this one is
//     no longer in use.
//   - The other slot is not in use. It is checked and cleared here.
//   - The boot home holds GRUB's configuration and environment, which
//     liken rewrites from the manifest on every boot. It has no
//     release document to check against, so liken clears its bit on
//     the strength of rewriting its contents, and says so.

import (
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"

	"github.com/liken-sh/liken/disks"
	"github.com/liken-sh/liken/machine"
)

// fatCheckMount is where a slot is mounted read-only while its
// contents are checked. A read-only mount does not set the volume's
// mark, so the check leaves nothing behind to clean up.
const fatCheckMount = "/run/liken/fat-check"

// bootedSlotStopEnv carries one fact from the initramfs to the rest of
// the boot: what the slot this machine booted said about its last stop.
// The initramfs mounts that slot for writing to reach the system image,
// long before storage reconciles, and the mount sets the mark. So by
// the time reconciliation asks, the device only says that the volume is
// in use now, which is true of every boot and worth reporting on none.
//
// The fact travels in the environment because init re-executes itself
// from the new root at the end of switch_root (switchroot.go), which
// ends the process that read it. The environment survives that exec.
const bootedSlotStopEnv = "LIKEN_BOOTED_SLOT_STOP"

const (
	stopMarkClean   = "clean"
	stopMarkUnclean = "unclean"
)

// recordBootedSlotStop reads a slot's mark and keeps the answer for the
// rest of the boot. Call it before mounting the slot for writing, which
// is the act that destroys the answer.
//
// A read that fails records nothing. The fallback is a read of the
// device later, which reports a mark this boot set, and that is wrong
// in the safe direction: a machine whose boot sector cannot be read is
// about to fail its storage reconciliation for the same reason.
func recordBootedSlotStop(dev string) {
	unclean, err := disks.FAT32Dirty(dev)
	if err != nil {
		fmt.Fprintf(os.Stderr, "liken: storage: reading the booted slot's stop mark: %v\n", err)
		return
	}
	mark := stopMarkClean
	if unclean {
		mark = stopMarkUnclean
	}
	if err := os.Setenv(bootedSlotStopEnv, mark); err != nil {
		fmt.Fprintf(os.Stderr, "liken: storage: keeping the booted slot's stop mark: %v\n", err)
	}
}

// fatStopMark answers whether a FAT role's volume already carried its
// mark when this boot found it.
//
// Every role but one still holds the answer on the device, because
// nothing has mounted it yet. The exception is the slot this machine
// booted, whose answer the initramfs read and recorded.
func fatStopMark(name machine.StorageRoleName, dev string) (bool, error) {
	if isSystemSlot(name) && string(name) == bootedSlotRole() {
		switch os.Getenv(bootedSlotStopEnv) {
		case stopMarkUnclean:
			return true, nil
		case stopMarkClean:
			return false, nil
		}
		// Nothing was recorded, so the initramfs mounted no slot. A boot
		// that takes the system image from RAM does this, and then the
		// device is still the place to ask.
	}
	return disks.FAT32Dirty(dev)
}

// readFATStop reports whether a FAT role's volume still carries the
// mark from an earlier stop, and heals the volume when it can. It
// returns what belongs in status: whether the previous stop left this
// volume unreleased. Healing does not change that answer, because the
// answer describes the stop that already happened.
//
// A role that this boot claimed and formatted is new, so it is not
// asked. Anything that goes wrong here is reported and treated as no
// mark: a machine must boot even when it cannot read one byte of a
// boot sector, and storage reconciliation will fail for a real reason
// a moment later if the volume is truly unreadable.
func readFATStop(name machine.StorageRoleName, dev string, created bool) bool {
	if created {
		return false
	}
	unclean, err := fatStopMark(name, dev)
	if err != nil {
		fmt.Fprintf(os.Stderr, "liken: storage: reading %s's stop mark: %v\n", name, err)
		return false
	}
	if !unclean {
		return false
	}
	fmt.Printf("liken: storage: %s was not released at the last stop\n", name)
	healFATRole(name, dev)
	return true
}

// healFATRole clears a volume's mark when liken can vouch for what is
// on it. It never reports failure to its caller: a volume that keeps
// its mark is the state the machine is already in, and the fact
// reaches status either way.
func healFATRole(name machine.StorageRoleName, dev string) {
	switch {
	case isSystemSlot(name) && string(name) == bootedSlotRole():
		fmt.Printf("liken: storage: %s is the slot this machine booted, so its mark stays until a boot from the other slot\n", name)
		return
	case isSystemSlot(name):
		if err := checkSlotArtifacts(dev); err != nil {
			fmt.Fprintf(os.Stderr, "liken: storage: %s keeps its mark: %v\n", name, err)
			return
		}
		fmt.Printf("liken: storage: %s holds every artifact its release document names; clearing its mark\n", name)
	case name == machine.BootHomeRole:
		// This boot rewrites GRUB's configuration and environment from
		// the manifest, so whatever the last stop left here is
		// replaced rather than trusted.
		fmt.Printf("liken: storage: %s is rewritten from the manifest on every boot; clearing its mark\n", name)
	default:
		return
	}
	if err := disks.ClearFAT32Dirty(dev); err != nil {
		fmt.Fprintf(os.Stderr, "liken: storage: clearing %s's mark: %v\n", name, err)
	}
}

// bootedSlotRole names the role of the slot this machine booted, so
// the caller can tell it apart from the one that is idle. A boot with
// no slot parameter booted no slot at all, and then neither slot is
// in use.
func bootedSlotRole() string {
	switch bootParamValue("liken.slot") {
	case "A":
		return string(machine.SystemARole)
	case "B":
		return string(machine.SystemBRole)
	}
	return ""
}

// checkSlotArtifacts reads a slot and checks every artifact its
// release document names against that document's digests. It mounts
// the slot read-only, which leaves the volume's mark untouched, and
// unmounts before returning so that the caller can write to the
// device.
//
// A slot with no release document has never been written by liken and
// is not something to vouch for.
func checkSlotArtifacts(dev string) error {
	if err := os.MkdirAll(fatCheckMount, 0o755); err != nil {
		return err
	}
	if err := unix.Mount(dev, fatCheckMount, "vfat", unix.MS_RDONLY, ""); err != nil {
		return fmt.Errorf("mounting %s to check it: %w", dev, err)
	}
	err := verifySlotContents(fatCheckMount)
	if unmountErr := unix.Unmount(fatCheckMount, 0); unmountErr != nil {
		// The check is worthless if the volume is still mounted, since
		// the device cannot be written underneath it.
		_ = unix.Unmount(fatCheckMount, unix.MNT_DETACH)
		return fmt.Errorf("releasing %s after checking it: %w", dev, unmountErr)
	}
	return err
}

// verifySlotContents hashes every artifact on a mounted slot against
// the release document that the slot carries.
func verifySlotContents(mount string) error {
	raw, err := os.ReadFile(filepath.Join(mount, "release.yaml"))
	if err != nil {
		return fmt.Errorf("this slot carries no release document: %w", err)
	}
	release, err := machine.ParseRelease(raw)
	if err != nil {
		return fmt.Errorf("this slot's release document does not parse: %w", err)
	}
	for _, artifact := range release.Artifacts {
		if err := verifyFile(artifact, filepath.Join(mount, artifact.Name)); err != nil {
			return fmt.Errorf("%s does not match the release document: %w", artifact.Name, err)
		}
	}
	return nil
}
