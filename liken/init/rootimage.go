package main

// Init finds and mounts the system image, liken.sqfs, the read-only
// squashfs file that becomes the root filesystem.
//
// On an installed machine, each boot entry that the installer wrote
// puts liken.slot=A or liken.slot=B on the kernel command line. The
// slot is a FAT32 partition with the GPT name liken:systemA or
// liken:systemB, and the image is a file on it.
//
// A boot with no disk, such as QEMU with -kernel or a drill on blank
// disks, gives the kernel the image in a cpio archive, and the kernel
// unpacks it to /liken.sqfs in rootfs. That file stays in RAM while
// the image is mounted, so only these boots use memory for the image.
//
// Either way, init mounts the image read-only through a loop device,
// so the running root has exactly the bytes that the release
// published. A size-limited tmpfs above it takes the writes
// (switchroot.go).
//
// The caller must mount /proc first. The kernel treats a parameter
// with a dot in its name, such as liken.slot, as a module parameter
// and does not pass it to init, so init reads it from /proc/cmdline.

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/sys/unix"

	"github.com/liken-sh/liken/liken/machine"
)

// ramImage is where the kernel unpacks a system image that came in a
// cpio archive. Tests point it at their own file.
var ramImage = "/liken.sqfs"

// bootModulesDir holds the few modules that the boot archive has, with
// their own modules.dep. Its name is not a kernel release, so it is
// not mistaken for the system image's complete tree at
// /lib/modules/<release>, and the switch to the real root does not
// copy it (switchroot.go). Tests point it at their own directory.
var bootModulesDir = "/lib/modules/boot"

const slotImageName = "liken.sqfs"

// loadBootModules loads modules from bootModulesDir, such as overlayfs
// and the vfat encoding table. A load failure is printed and does not
// stop the boot, because a mount that needs the module reports its own
// error.
func loadBootModules(names ...string) {
	deps, err := readModulesDep(filepath.Join(bootModulesDir, "modules.dep"))
	if err != nil {
		fmt.Fprintf(os.Stderr, "liken: reading the boot modules.dep: %v\n", err)
		return
	}
	loaded := map[string]bool{}
	for _, name := range names {
		if _, err := loadModule(bootModulesDir, name, "", deps, loaded); err != nil {
			fmt.Fprintf(os.Stderr, "liken: loading boot module %s: %v\n", name, err)
		}
	}
}

// findSystemImage returns the path of liken.sqfs. When the image is
// not in rootfs, it mounts the boot slot at slotMount and returns the
// path on that mount. slotMount is unused for the RAM image.
func findSystemImage(slotParam, slotMount string) (imagePath string, err error) {
	if _, err := os.Stat(ramImage); err == nil {
		fmt.Println("liken: system image found in RAM (no disk needed for this boot)")
		return ramImage, nil
	}

	if slotParam == "" {
		return "", fmt.Errorf("%s is not in rootfs and the kernel command line has no liken.slot=", ramImage)
	}
	device, err := awaitSlotDevice(slotParam)
	if err != nil {
		return "", err
	}

	if err := os.MkdirAll(slotMount, 0o755); err != nil {
		return "", err
	}
	// The mount is read-write although init only reads the image.
	// Storage reconciliation later mounts the same partition at its
	// role path, and the fetcher writes downloaded releases there.
	// Both mounts share one superblock, so they cannot differ in
	// read-only.
	//
	// A read-write mount sets the FAT volume's dirty bit, so the bit
	// must be read before the mount to learn how the previous boot of
	// this slot stopped (fatstate.go).
	recordBootedSlotStop(device)
	if err := mountFilesystem(device, slotMount, "vfat", 0, ""); err != nil {
		return "", fmt.Errorf("mounting slot %s (%s): %w", slotParam, device, err)
	}
	fmt.Printf("liken: system image on slot %s (%s)\n", slotParam, device)
	return filepath.Join(slotMount, slotImageName), nil
}

// slotDeadline is how long awaitSlotDevice waits for the partition
// before it returns an error. The short poll lets the boot go on soon
// after the partition appears.
const (
	slotPoll     = 50 * time.Millisecond
	slotDeadline = 10 * time.Second
)

// awaitSlotDevice waits up to slotDeadline for the slot's partition and
// its device node. On an eMMC machine the partition can appear after
// init first looks, because the kernel registers the controller before
// the card and detects the card on a workqueue. The wait is safe
// because liken.slot= means the boot loader read this slot from a disk
// of this machine.
//
// An error that waiting cannot fix, such as two partitions with the
// slot's name, returns at once. At the deadline it returns the last
// error. The function polls instead of reading uevents because the
// wait is short and a walk of /sys/block is cheap.
func awaitSlotDevice(slotParam string) (string, error) {
	device, err := mountableSlotDevice(slotParam)
	if !slotMayStillAppear(err) {
		return device, err
	}
	fmt.Printf("liken: waiting for the disk with slot %s\n", slotParam)
	for begin := time.Now(); time.Since(begin) < slotDeadline; {
		time.Sleep(slotPoll)
		device, err = mountableSlotDevice(slotParam)
		if !slotMayStillAppear(err) {
			return device, err
		}
	}
	return "", err
}

// mountableSlotDevice finds the slot's partition and checks that its
// device node exists, because the kernel adds a partition to sysfs
// before devtmpfs creates its node.
func mountableSlotDevice(slotParam string) (string, error) {
	device, err := slotDevice(discoverPartitions(), slotParam)
	if err != nil {
		return "", err
	}
	if _, err := os.Stat(device); err != nil {
		return "", fmt.Errorf("%w %s", errNoSlotNode, device)
	}
	return device, nil
}

func slotMayStillAppear(err error) bool {
	return errors.Is(err, errNoSlotPartition) || errors.Is(err, errNoSlotNode)
}

// Each sentinel is the start of the printed error.
var errNoSlotPartition = errors.New("no partition has the GPT name")
var errNoSlotNode = errors.New("no device node yet for")

// slotDevice returns the device path of the named slot's partition.
// It matches the GPT partition name, because a disk's device name can
// change from boot to boot. When two partitions have the name, it
// returns an error instead of choosing one.
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
				return "", fmt.Errorf("two partitions have the GPT name %s", want)
			}
			device = devRoot + "/" + p.name
		}
	}
	if device == "" {
		return "", fmt.Errorf("%w %s", errNoSlotPartition, want)
	}
	return device, nil
}

// loopMount mounts the squashfs file at path read-only at target,
// through a free loop device. LO_FLAGS_AUTOCLEAR detaches the loop
// device when the mount is removed.
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
		return fmt.Errorf("setting the status of %s: %w", loopPath, err)
	}

	if err := os.MkdirAll(target, 0o755); err != nil {
		return err
	}
	if err := unix.Mount(loopPath, target, "squashfs", unix.MS_RDONLY, ""); err != nil {
		return fmt.Errorf("mounting %s at %s: %w", path, target, err)
	}
	return nil
}
