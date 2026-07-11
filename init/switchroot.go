package main

// Trading the kernel's rootfs for the real root: the system image,
// read-only, with a bounded overlay for the runtime's writes.
//
// When the kernel unpacks the initramfs, it doesn't mount a
// filesystem to hold the files. They land in rootfs: the filesystem
// instance the kernel creates as the root of the mount tree before
// any userspace exists. rootfs is the bottom of the stack by
// construction: it can never be unmounted, and pivot_root refuses to
// operate on it, because both would leave the mount tree with no root
// at all. It is also RAM with no bound and no accounting — device
// "rootfs" in the mount table, which nothing can measure — so an OS
// that stays there pays for itself in memory, forever, and kubelet's
// node ephemeral-storage accounting comes up empty.
//
// liken's root is elsewhere: the system image (rootimage.go finds and
// loop-mounts it) is the lower, read-only layer of an overlay, and a
// small fixed-size tmpfs is the upper, writable one. The OS costs
// page cache instead of a permanent copy of itself, and everything
// that grows with use lives on disk roles, not under /.
//
// What rides in rootfs is only what the boot loader delivered beyond
// the OS: the boot archive (this program and the early modules) and
// the deployment layer. The layer's files — manifests, identity,
// declared modules and their index — are carried onto the overlay
// before the switch, which preserves the initramfs contract that
// later archives override earlier ones: the layer's files win over
// the image's.
//
// The procedure, named for the util-linux tool that performs it on
// conventional systems (switch_root):
//
//  1. mount the system image and the overlay above it,
//  2. carry rootfs's extra files (the layer) onto the overlay,
//  3. delete the originals, handing their RAM back to the kernel,
//  4. move the mounts into the new root, move it onto /, chroot,
//  5. re-exec init from the new root.
//
// The re-exec is required. The running program is the last thing
// pinning the old root: its executable is mapped from rootfs, so
// those pages can't be freed until something else is running in their
// place. exec replaces the process image while keeping PID 1; the
// second "hello from userspace" on the console is the same program
// restarting from the new root.
//
// An install boot (liken.install) skips all of this: the installer
// runs from rootfs, copies the release payload it carried to disk,
// and powers off. Nothing it does needs the overlay, and its payload
// is far bigger than the overlay's bound.

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"golang.org/x/sys/unix"
)

// switchedMarker is how init tells its post-switch run apart from its
// first: the re-exec passes it as an argument. The kernel hands
// unrecognized non-key=value boot parameters to init as arguments
// too, but nothing on our command line starts with dashes.
const switchedMarker = "--switched"

const newRoot = "/newroot"

// stagingDir is where the boot's mounts assemble before the switch:
// a tmpfs holding the overlay's upper and work directories, with the
// system image and (when a slot boot) the slot mounted beneath it.
// One MS_MOVE carries the whole subtree into the new root at
// bootMountsDir, so the running system can see what it booted from.
const stagingDir = "/liken-boot"

// bootMountsDir is the staging tree's home inside the new root.
const bootMountsDir = "/var/lib/liken/boot"

// writesSize bounds the overlay's upper layer: the root filesystem's
// entire write budget. The runtime's writes under / are small and
// fixed (k3s config drop-ins, resolv.conf, the layer's seeds);
// everything that grows with use belongs to a disk role. A budget
// this size is deliberate: filling it is a bug report about something
// writing to / that shouldn't, not a reason to raise it.
const writesSize = "128m"

// maybeSwitchRoot runs once, before init touches anything else: the
// switch must happen while the mount tree is empty (one moved mount
// is simple; many of them are not) and before any child processes
// exist. The liken.* boot parameters it needs live in /proc/cmdline
// and nowhere else — the kernel treats any parameter with a dot in
// its name as a module parameter and passes it to init neither as an
// argument nor in the environment — so /proc is mounted here, before
// anything else, and detached again before the switch.
func maybeSwitchRoot() {
	if slices.Contains(os.Args[1:], switchedMarker) {
		return
	}
	if err := os.MkdirAll("/proc", 0o755); err == nil {
		if err := unix.Mount("proc", "/proc", "proc", unix.MS_NOSUID|unix.MS_NOEXEC|unix.MS_NODEV, ""); err != nil {
			fmt.Fprintf(os.Stderr, "liken: mounting /proc for the switch: %v\n", err)
		}
	}

	// The boot archive's few modules load on every first run, not just
	// switching ones: an install boot stays on rootfs but still mounts
	// FAT slots, and vfat's encoding table is one of these.
	if names, err := readModuleList(filepath.Join(bootModulesDir, "boot-modules.conf")); err == nil {
		loadBootModules(names...)
	} else {
		fmt.Fprintf(os.Stderr, "liken: boot modules: %v\n", err)
	}

	if bootParam(installParam) {
		// The installer runs from rootfs, copies its payload to disk,
		// and powers off; nothing it does needs the overlay, and its
		// payload is far bigger than the overlay's bound.
		fmt.Println("liken: install boot; staying on rootfs")
		_ = unix.Unmount("/proc", unix.MNT_DETACH)
		return
	}
	if err := switchRoot(); err != nil {
		// A machine still on rootfs is degraded but alive, and the
		// console works either way; carry on and let the report show
		// the state of things.
		fmt.Fprintf(os.Stderr, "liken: switch_root: %v (continuing on rootfs)\n", err)
		_ = unix.Unmount("/proc", unix.MNT_DETACH)
	}
}

func switchRoot() error {
	// Devices first: the loop device and the slot's partition both
	// live in devtmpfs, the kernel's own device catalog. The new root
	// gets a fresh mount of the same catalog later (mountEssentials);
	// this one is detached before the switch.
	if err := os.MkdirAll("/dev", 0o755); err != nil {
		return err
	}
	if err := unix.Mount("devtmpfs", "/dev", "devtmpfs", unix.MS_NOSUID, ""); err != nil {
		return fmt.Errorf("mounting devtmpfs: %w", err)
	}

	// Slot recognition walks /sys/block for the GPT names the kernel
	// read, so sysfs joins the early mounts; like /dev and /proc it is
	// detached before the switch and remounted fresh in the new root.
	if err := os.MkdirAll("/sys", 0o755); err != nil {
		return err
	}
	if err := unix.Mount("sysfs", "/sys", "sysfs", unix.MS_NOSUID|unix.MS_NOEXEC|unix.MS_NODEV, ""); err != nil {
		return fmt.Errorf("mounting sysfs: %w", err)
	}

	// The staging tmpfs: the overlay's writable layer, and the parent
	// of every mount this boot assembles. Its size is the bound on
	// what the running system can ever write under /.
	if err := os.MkdirAll(stagingDir, 0o755); err != nil {
		return err
	}
	if err := unix.Mount("tmpfs", stagingDir, "tmpfs",
		unix.MS_NOSUID, "mode=0755,size="+writesSize); err != nil {
		return fmt.Errorf("mounting the writes tmpfs: %w", err)
	}
	for _, dir := range []string{"upper", "work", "system"} {
		if err := os.MkdirAll(filepath.Join(stagingDir, dir), 0o755); err != nil {
			return err
		}
	}

	imagePath, err := findSystemImage(bootParamValue("liken.slot"), filepath.Join(stagingDir, "slot"))
	if err != nil {
		return err
	}
	if err := loopMount(imagePath, filepath.Join(stagingDir, "system")); err != nil {
		return err
	}

	if err := os.MkdirAll(newRoot, 0o755); err != nil {
		return err
	}
	overlay := fmt.Sprintf("lowerdir=%s,upperdir=%s,workdir=%s",
		filepath.Join(stagingDir, "system"),
		filepath.Join(stagingDir, "upper"),
		filepath.Join(stagingDir, "work"))
	if err := unix.Mount("overlay", newRoot, "overlay", 0, overlay); err != nil {
		return fmt.Errorf("mounting the overlay: %w", err)
	}

	// Carry the boot loader's extra cargo — the deployment layer —
	// onto the overlay. Everything the boot archive itself brought is
	// excluded: the system image already carries init, and the boot
	// module tree is deliberately kept off the real root so its
	// partial index can never shadow the image's complete one.
	if err := carryTree("/", newRoot, []string{
		newRoot, "/dev", "/proc", "/sys", stagingDir,
		"/liken", ramImage, bootModulesDir,
	}); err != nil {
		return fmt.Errorf("carrying the layer: %w", err)
	}

	// Delete the originals while they're still reachable; after the
	// move below, the old root has no path. This is the step that
	// actually reclaims the RAM; skipping it would leave the machine
	// carrying the layer twice. (A RAM-delivered system image is the
	// one exception: the loop device pins it, deleted or not, for as
	// long as it stays mounted.)
	entries, err := os.ReadDir("/")
	if err != nil {
		return err
	}
	for _, entry := range entries {
		path := "/" + entry.Name()
		if path == newRoot || path == "/dev" || path == "/proc" ||
			path == "/sys" || path == stagingDir {
			continue
		}
		if err := os.RemoveAll(path); err != nil {
			return fmt.Errorf("clearing old root: %w", err)
		}
	}

	// The staging tree moves into the new root in one piece — the
	// system image and slot mounts ride along as children — so the
	// running system sees its own foundations at a real path.
	target := filepath.Join(newRoot, bootMountsDir)
	if err := os.MkdirAll(target, 0o755); err != nil {
		return err
	}
	if err := unix.Mount(stagingDir, target, "", unix.MS_MOVE, ""); err != nil {
		return fmt.Errorf("moving boot mounts into the new root: %w", err)
	}

	// Whatever the kernel put at /dev belongs to the old root, and the
	// /proc mounted for parameter reading has served its purpose;
	// detach both so nothing pins the old tree once it moves. The new
	// root gets fresh mounts of each (mountEssentials), and the
	// console keeps working throughout, because our stdio descriptors
	// were opened before any of this.
	_ = unix.Unmount("/dev", unix.MNT_DETACH)
	_ = unix.Unmount("/proc", unix.MNT_DETACH)
	_ = unix.Unmount("/sys", unix.MNT_DETACH)

	// Now the move and the chroot. MS_MOVE grafts the overlay onto /
	// (legal where pivot_root is not, because nothing has to be
	// unmounted), and chroot(".") repoints this process's idea of "/"
	// at it. Order matters: chdir first so "." names the new root
	// throughout.
	if err := os.Chdir(newRoot); err != nil {
		return err
	}
	if err := unix.Mount(".", "/", "", unix.MS_MOVE, ""); err != nil {
		return fmt.Errorf("moving the overlay onto /: %w", err)
	}
	if err := unix.Chroot("."); err != nil {
		return fmt.Errorf("chroot: %w", err)
	}
	if err := os.Chdir("/"); err != nil {
		return err
	}

	fmt.Println("liken: re-executing from the system image")
	return unix.Exec("/liken", []string{"/liken", switchedMarker}, os.Environ())
}

// carryTree replicates the filesystem tree at src into dst —
// directories, symlinks, and regular files, with permissions intact —
// skipping the subtrees in skip. That inventory is the complete
// contents of an initramfs built by our image build; any other file
// type is unexpected and fails the carry. Existing files in dst are
// overwritten: the destination is the overlay, whose lower layer
// already carries the system's own copy of anything the deployment
// layer overrides (the depmod index, most importantly).
func carryTree(src, dst string, skip []string) error {
	return filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if path == src {
			return nil
		}
		if slices.Contains(skip, path) {
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		target := filepath.Join(dst, strings.TrimPrefix(path, src))
		info, err := d.Info()
		if err != nil {
			return err
		}
		switch {
		case d.IsDir():
			// The overlay's lower layer already has most directories;
			// creating one again must not fail, and an existing
			// directory keeps the image's permissions.
			if err := os.Mkdir(target, 0o755); err != nil {
				if os.IsExist(err) {
					return nil
				}
				return err
			}
			return os.Chmod(target, info.Mode().Perm())
		case d.Type()&fs.ModeSymlink != 0:
			link, err := os.Readlink(path)
			if err != nil {
				return err
			}
			if err := os.Symlink(link, target); err != nil && !os.IsExist(err) {
				return err
			}
			return nil
		case d.Type().IsRegular():
			return copyFile(path, target, info.Mode().Perm())
		default:
			return fmt.Errorf("%s: unexpected file type %s", path, d.Type())
		}
	})
}

func copyFile(src, dst string, perm fs.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, perm)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	if err := out.Chmod(perm); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}
