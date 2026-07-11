package main

// Finding and mounting the system image: the read-only squashfs that
// becomes the root filesystem.
//
// The boot loader stages almost nothing in RAM — the kernel, the
// small boot archive this program rides in, and the deployment layer.
// The operating system itself is liken.sqfs, and it arrives one of
// two ways:
//
//   - From a boot slot. An installed machine's kernel command line
//     carries liken.slot=A (or B), the installer's record of which
//     slot the boot entry belongs to. The slot is a FAT32 partition
//     recognized by the GPT name written when it was claimed
//     (liken:systemA), and the image is a file on it.
//
//   - From RAM. A boot with no disk (the lab's from-blank-disks
//     drills, QEMU -kernel boots) wraps the image in a cpio archive
//     so the kernel unpacks it into rootfs at /liken.sqfs, and init
//     mounts it from there. The loop device pins the file's memory
//     for as long as it is mounted; that is the RAM cost of not
//     having a disk, paid only by boots that chose it.
//
// Either way the image is loop-mounted read-only, exactly the bytes
// the release published: the running root is the digest-verified
// artifact, immutable by construction. A bounded tmpfs is overlaid
// above it for the runtime's writes (switchroot.go tells that half).
//
// This file's work happens first thing at boot, when almost nothing
// is mounted; the caller mounts /proc before asking, because the
// liken.* parameters live in /proc/cmdline and nowhere else (the
// kernel treats dotted parameter names as module parameters and
// never passes them to init directly).

import (
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"

	"github.com/liken-sh/liken/machine"
)

// ramImage is where a cpio-wrapped system image lands in rootfs; its
// presence selects the from-RAM path. A package variable rather than
// a constant so tests can point the search at a file of their own
// making.
var ramImage = "/liken.sqfs"

// bootModulesDir is the boot archive's module tree. The directory is
// deliberately not named for the kernel release: when init carries
// the boot-time files onto the real root, nothing under this name can
// shadow the system image's complete index at /lib/modules/<release>.
// A package variable for the same testing reason as ramImage.
var bootModulesDir = "/lib/modules/boot"

// slotImageName is the image's filename on a boot slot, one of the
// artifacts the installer copies and the release document names.
const slotImageName = "liken.sqfs"

// loadBootModules loads the boot archive's few modules (overlayfs,
// vfat's encoding table), resolved through the archive's own depmod
// index. Failures are reported, not fatal: the mounts that need a
// missing module will say so themselves.
func loadBootModules(names ...string) {
	deps, err := readModulesDep(filepath.Join(bootModulesDir, "modules.dep"))
	if err != nil {
		fmt.Fprintf(os.Stderr, "liken: boot modules: %v\n", err)
		return
	}
	loaded := map[string]bool{}
	for _, name := range names {
		if _, err := loadModule(bootModulesDir, name, deps, loaded); err != nil {
			fmt.Fprintf(os.Stderr, "liken: boot modules: %s: %v\n", name, err)
		}
	}
}

// findSystemImage locates liken.sqfs and returns its path, mounting
// the boot slot when the image isn't already in rootfs. slotMount is
// where the slot lands when one is used ("" for the RAM path).
func findSystemImage(slotParam, slotMount string) (imagePath string, err error) {
	if _, err := os.Stat(ramImage); err == nil {
		fmt.Println("liken: system image found in RAM (no disk needed for this boot)")
		return ramImage, nil
	}

	if slotParam == "" {
		return "", fmt.Errorf("no %s in rootfs and no liken.slot= boot parameter", ramImage)
	}
	device, err := slotDevice(discoverPartitions(), slotParam)
	if err != nil {
		return "", err
	}

	if err := os.MkdirAll(slotMount, 0o755); err != nil {
		return "", err
	}
	// Read-write not because this mount writes anything — the early
	// boot only reads the image — but because storage reconciliation
	// later mounts the same partition at its role path, sharing the
	// one superblock, and a superblock cannot be read-only here and
	// read-write there. The slot must stay writable for the machine:
	// the fetcher writes downloaded releases into slots.
	if err := unix.Mount(device, slotMount, "vfat", 0, ""); err != nil {
		return "", fmt.Errorf("mounting slot %s (%s): %w", slotParam, device, err)
	}
	fmt.Printf("liken: system image on slot %s (%s)\n", slotParam, device)
	return filepath.Join(slotMount, slotImageName), nil
}

// slotDevice picks the partition holding the named slot from the
// machine's discovered partitions. The slot is recognized exactly the
// way storage roles are: by the GPT partition name the claim wrote,
// wherever the disk enumerated this boot. Device paths in specs are
// claim-time hints; names are identity — and two partitions claiming
// one name is a refusal, never a guess.
func slotDevice(parts []partition, slotParam string) (string, error) {
	role := machine.SystemARole
	if slotParam == "B" {
		role = machine.SystemBRole
	}
	want := machine.PartitionPrefix + string(role)

	var device string
	for _, p := range parts {
		if p.partName == want {
			if device != "" {
				return "", fmt.Errorf("two partitions carry %s; refusing to guess", want)
			}
			device = "/dev/" + p.name
		}
	}
	if device == "" {
		return "", fmt.Errorf("no partition carries %s", want)
	}
	return device, nil
}

// loopMount attaches path to a free loop device read-only and mounts
// it at target as squashfs. Autoclear detaches the loop device when
// the mount goes away, so nothing needs tearing down by hand.
func loopMount(path, target string) error {
	ctl, err := os.OpenFile("/dev/loop-control", os.O_RDWR, 0)
	if err != nil {
		return fmt.Errorf("opening loop-control: %w", err)
	}
	defer ctl.Close()
	n, err := unix.IoctlRetInt(int(ctl.Fd()), unix.LOOP_CTL_GET_FREE)
	if err != nil {
		return fmt.Errorf("allocating a loop device: %w", err)
	}

	backing, err := os.Open(path)
	if err != nil {
		return err
	}
	defer backing.Close()
	loopPath := fmt.Sprintf("/dev/loop%d", n)
	loop, err := os.OpenFile(loopPath, os.O_RDONLY, 0)
	if err != nil {
		return fmt.Errorf("opening %s: %w", loopPath, err)
	}
	defer loop.Close()
	if err := unix.IoctlSetInt(int(loop.Fd()), unix.LOOP_SET_FD, int(backing.Fd())); err != nil {
		return fmt.Errorf("attaching %s to %s: %w", path, loopPath, err)
	}
	status := unix.LoopInfo64{
		Flags: unix.LO_FLAGS_READ_ONLY | unix.LO_FLAGS_AUTOCLEAR,
	}
	copy(status.File_name[:], loopPath)
	if err := unix.IoctlLoopSetStatus64(int(loop.Fd()), &status); err != nil {
		return fmt.Errorf("configuring %s: %w", loopPath, err)
	}

	if err := os.MkdirAll(target, 0o755); err != nil {
		return err
	}
	if err := unix.Mount(loopPath, target, "squashfs", unix.MS_RDONLY, ""); err != nil {
		return fmt.Errorf("mounting %s at %s: %w", path, target, err)
	}
	return nil
}
