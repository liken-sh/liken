package main

// Finding and mounting the system image: the read-only squashfs file
// that becomes the root filesystem.
//
// The boot loader stages almost nothing in RAM: the kernel, the small
// boot archive that holds this program, and the deployment layer. The
// operating system itself is liken.sqfs. It arrives in one of two
// ways:
//
//   - From a boot slot. On an installed machine, the kernel command
//     line carries liken.slot=A (or B). This is the installer's
//     record of which slot the boot entry belongs to. The slot is a
//     FAT32 partition, recognized by the GPT name written when it
//     was claimed (liken:systemA). The image is a file on that
//     partition.
//
//   - From RAM. A boot with no disk, such as the lab's
//     from-blank-disks drills or QEMU -kernel boots, wraps the image
//     in a cpio archive. The kernel unpacks this archive into rootfs
//     at /liken.sqfs, and init mounts the image from there. The loop
//     device holds the file's memory in place for as long as it
//     stays mounted. This is the RAM cost of having no disk. Only
//     boots that run without a disk pay this cost.
//
// Either way, init loop-mounts the image read-only, with exactly the
// bytes the release published. The running root is the
// digest-verified artifact. Nothing can change it, because of how
// the system builds and mounts it. A bounded tmpfs sits above it for
// the runtime's writes (see switchroot.go for that part).
//
// This file's work happens first, at the start of boot, when almost
// nothing else is mounted. The caller must mount /proc before it
// calls into this file, because the liken.* parameters live only in
// /proc/cmdline. The kernel treats dotted parameter names as module
// parameters and never passes them to init directly.

import (
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"

	"github.com/liken-sh/liken/machine"
)

// ramImage is where a cpio-wrapped system image lands in rootfs. If
// this file exists, init uses the from-RAM path. This is a package
// variable, not a constant, so tests can point the search at a file
// of their own.
var ramImage = "/liken.sqfs"

// bootModulesDir is the boot archive's module tree. The directory
// name does not include the kernel release, on purpose. When init
// carries the boot-time files onto the real root, nothing under this
// name can hide the system image's full index at
// /lib/modules/<release>. This is a package variable, for the same
// testing reason as ramImage.
var bootModulesDir = "/lib/modules/boot"

// slotImageName is the image's file name on a boot slot. It is one
// of the artifacts that the installer copies, and the release
// document names it.
const slotImageName = "liken.sqfs"

// loadBootModules loads the boot archive's few modules, such as
// overlayfs and vfat's encoding table. It resolves each module
// through the archive's own depmod index. A failure to load a module
// is reported, not fatal. Any mount that needs a missing module
// reports its own failure.
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

// findSystemImage finds liken.sqfs and returns its path. It mounts
// the boot slot when the image is not already in rootfs. slotMount
// is where the slot mounts when init uses one (use "" for the RAM
// path).
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
	// This mount is read-write, but not because it writes anything
	// here. The early boot only reads the image. Storage
	// reconciliation later mounts the same partition at its role
	// path, and both mounts share one superblock. A superblock cannot
	// be read-only in one mount and read-write in the other. The slot
	// must stay writable, because the fetcher writes downloaded
	// releases into slots.
	if err := unix.Mount(device, slotMount, "vfat", 0, ""); err != nil {
		return "", fmt.Errorf("mounting slot %s (%s): %w", slotParam, device, err)
	}
	fmt.Printf("liken: system image on slot %s (%s)\n", slotParam, device)
	return filepath.Join(slotMount, slotImageName), nil
}

// slotDevice picks the partition that holds the named slot, from the
// machine's discovered partitions. It recognizes the slot the same
// way storage roles are recognized: by the GPT partition name that
// the claim wrote, wherever the disk enumerated during this boot.
// Device paths in specs are only hints from claim time. Names are
// the identity. If two partitions claim one name, slotDevice refuses
// to guess and returns an error.
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

// loopMount attaches path to a free loop device, read-only, and
// mounts it at target as squashfs. The autoclear flag detaches the
// loop device when the mount goes away, so nothing needs to be torn
// down by hand.
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
