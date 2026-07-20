package main

// This file replaces the kernel's rootfs with the real root: the
// read-only system image, with a bounded overlay for the runtime's
// writes.
//
// When the kernel unpacks the initramfs, the kernel does not mount a
// filesystem to hold the files. The files land in rootfs: the
// filesystem instance that the kernel creates as the root of the
// mount tree, before any userspace process exists. rootfs is the
// bottom of the mount stack by construction. The kernel can never
// unmount rootfs, and pivot_root refuses to operate on it, because
// either action would leave the mount tree with no root at all.
// rootfs is also RAM with no bound and no accounting: it appears as
// device "rootfs" in the mount table, and nothing can measure its
// size. So an OS that stays on rootfs pays for itself in memory
// forever, and kubelet's node ephemeral-storage accounting reports
// nothing for it.
//
// liken's root lives elsewhere. The system image, which rootimage.go
// finds and loop-mounts, is the lower, read-only layer of an overlay
// filesystem. A small, fixed-size tmpfs is the upper, writable layer.
// The OS costs page cache instead of a permanent copy of itself.
// Everything that grows with use lives on a disk role, not under /.
//
// rootfs holds only what the boot loader delivered beyond the OS
// itself: the boot archive (this program and the early kernel
// modules) and the deployment layer. Before the switch, this code
// carries the layer's files onto the overlay: manifests, identity,
// declared modules, and their index. This preserves the initramfs
// contract that later archives override earlier ones, so the layer's
// files win over the image's files.
//
// The procedure takes its name from the util-linux tool that
// performs it on conventional systems (switch_root):
//
//  1. mount the system image and the overlay above it,
//  2. carry rootfs's extra files (the layer) onto the overlay,
//  3. delete the originals, and return their RAM to the kernel,
//  4. move the mounts into the new root, move the new root onto /,
//     and chroot into it,
//  5. re-exec init from the new root.
//
// The re-exec is required. The running program is the last thing
// pinning the old root in memory: the kernel maps its executable
// from rootfs, so those pages cannot be freed until a different
// program runs in their place. exec replaces the process image while
// it keeps PID 1. The second "hello from userspace" message on the
// console is the same program, restarting from the new root.
//
// An install boot (liken.install) skips this entire procedure. The
// installer runs from rootfs, copies the release payload it carried
// to disk, and powers off. None of this work needs the overlay, and
// the payload is far bigger than the overlay's size bound.

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

// switchedMarker is how init tells its post-switch run apart from
// its first run: the re-exec passes switchedMarker as an argument.
// The kernel also hands unrecognized non-key=value boot parameters to
// init as arguments, but nothing on liken's command line starts with
// dashes.
const switchedMarker = "--switched"

const newRoot = "/newroot"

// stagingDir is where the boot's mounts assemble before the switch.
// It is a tmpfs holding the overlay's upper and work directories,
// with the system image mounted beneath it, and the slot too, when
// this boot is a slot boot. One MS_MOVE operation carries the whole
// subtree into the new root at bootMountsDir, so the running system
// can see what it booted from.
const stagingDir = "/liken-boot"

// bootMountsDir is the staging tree's location inside the new root.
const bootMountsDir = "/var/lib/liken/boot"

// writesSize bounds the overlay's upper layer, which is the root
// filesystem's entire write budget. The runtime's writes under / are
// small and fixed: k3s config drop-ins, resolv.conf, and the layer's
// seed files. Everything that grows with use belongs to a disk role.
// This budget size is deliberate. If something fills the budget, that
// is a bug report about something writing to / that should not write
// there, not a reason to raise the budget.
const writesSize = "128m"

// maybeSwitchRoot runs once, before init touches anything else. The
// switch must happen while the mount tree is empty, because moving
// one mount is simple and moving many is not, and it must happen
// before any child process exists. The liken.* boot parameters that
// maybeSwitchRoot needs live in /proc/cmdline and nowhere else. The
// kernel treats any parameter with a dot in its name as a module
// parameter, and passes it to init neither as an argument nor in the
// environment. So this function mounts /proc here, before anything
// else, and detaches /proc again before the switch.
func maybeSwitchRoot() {
	if slices.Contains(os.Args[1:], switchedMarker) {
		return
	}
	if err := os.MkdirAll("/proc", 0o755); err == nil {
		if err := unix.Mount("proc", "/proc", "proc", unix.MS_NOSUID|unix.MS_NOEXEC|unix.MS_NODEV, ""); err != nil {
			fmt.Fprintf(os.Stderr, "liken: mounting /proc for the switch: %v\n", err)
		}
	}

	// The boot archive's few modules load on every first run, not
	// only on runs that switch root. An install boot stays on rootfs,
	// but still mounts FAT slots, and vfat's encoding table is one of
	// these modules.
	if names, err := readModuleList(filepath.Join(bootModulesDir, "boot-modules.conf")); err == nil {
		loadBootModules(names...)
	} else {
		fmt.Fprintf(os.Stderr, "liken: boot modules: %v\n", err)
	}

	if bootParam(installParam) {
		// The installer runs from rootfs, copies its payload to disk,
		// and powers off. None of this work needs the overlay, and the
		// payload is far bigger than the overlay's size bound.
		fmt.Println("liken: install boot; staying on rootfs")
		_ = unix.Unmount("/proc", unix.MNT_DETACH)
		return
	}
	if err := switchRoot(); err != nil {
		// A machine still on rootfs is degraded but alive, and the
		// console works either way. Continue, and let the report show
		// the state of the machine.
		fmt.Fprintf(os.Stderr, "liken: switch_root: %v (continuing on rootfs)\n", err)
		_ = unix.Unmount("/proc", unix.MNT_DETACH)
	}
}

func switchRoot() error {
	// Mount devices first. The loop device and the slot's partition
	// both live in devtmpfs, the kernel's own device catalog. The new
	// root gets a fresh mount of the same catalog later
	// (mountEssentials); this mount is detached before the switch.
	if err := os.MkdirAll("/dev", 0o755); err != nil {
		return err
	}
	if err := unix.Mount("devtmpfs", "/dev", "devtmpfs", unix.MS_NOSUID, ""); err != nil {
		return fmt.Errorf("mounting devtmpfs: %w", err)
	}

	// Slot recognition walks /sys/block for the GPT names that the
	// kernel read, so sysfs joins the early mounts. Like /dev and
	// /proc, this mount is detached before the switch, and remounted
	// fresh in the new root.
	if err := os.MkdirAll("/sys", 0o755); err != nil {
		return err
	}
	if err := unix.Mount("sysfs", "/sys", "sysfs", unix.MS_NOSUID|unix.MS_NOEXEC|unix.MS_NODEV, ""); err != nil {
		return fmt.Errorf("mounting sysfs: %w", err)
	}

	// This is the staging tmpfs: the overlay's writable layer, and the
	// parent of every mount that this boot assembles. Its size is the
	// bound on what the running system can ever write under /.
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

	// Carry the boot loader's extra files — the deployment layer —
	// onto the overlay. This excludes everything that the boot archive
	// itself brought: the system image already carries init, and the
	// boot module tree stays off the real root deliberately, so its
	// partial index can never override the image's complete index.
	if err := carryTree("/", newRoot, []string{
		newRoot, "/dev", "/proc", "/sys", stagingDir,
		"/liken", ramImage, bootModulesDir,
	}); err != nil {
		return fmt.Errorf("carrying the layer: %w", err)
	}

	// Delete the originals while they are still reachable by a path.
	// After the move below, the old root has no path at all. This
	// step is what actually reclaims the RAM. Skipping this step
	// would leave the machine carrying the layer twice. A
	// RAM-delivered system image is the one exception: the loop
	// device pins it in memory, deleted or not, for as long as the
	// image stays mounted.
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

	// The staging tree moves into the new root in one piece. The
	// system image and slot mounts move along with it as child
	// mounts, so the running system can see the mounts it booted from
	// at a real path.
	target := filepath.Join(newRoot, bootMountsDir)
	if err := os.MkdirAll(target, 0o755); err != nil {
		return err
	}
	if err := unix.Mount(stagingDir, target, "", unix.MS_MOVE, ""); err != nil {
		return fmt.Errorf("moving boot mounts into the new root: %w", err)
	}

	// Whatever the kernel put at /dev belongs to the old root. The
	// /proc mount, used only for reading boot parameters, is no
	// longer needed. Detach both mounts, so nothing pins the old tree
	// once it moves. The new root gets fresh mounts of /dev and /proc
	// (mountEssentials). The console keeps working throughout,
	// because this program opened its stdio descriptors before any of
	// this.
	_ = unix.Unmount("/dev", unix.MNT_DETACH)
	_ = unix.Unmount("/proc", unix.MNT_DETACH)
	_ = unix.Unmount("/sys", unix.MNT_DETACH)

	// Now the move and the chroot. MS_MOVE attaches the overlay onto
	// /. This is legal where pivot_root is not, because nothing has
	// to be unmounted. chroot(".") changes what this process treats
	// as "/". Order matters here: chdir runs first, so "." names the
	// new root throughout.
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

// carryTree replicates the filesystem tree at src into dst. It
// copies directories, symlinks, and regular files, and keeps their
// permissions intact, while it skips the subtrees named in skip.
// Directories, symlinks, and regular files are the complete contents
// of an initramfs that liken's image build produces. Any other file
// type is unexpected, and fails the carry. carryTree overwrites
// existing files in dst, because dst is the overlay, whose lower
// layer already carries the system's own copy of anything the
// deployment layer overrides. The depmod index is the most important
// example.
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
			// The overlay's lower layer already has most directories.
			// Creating a directory again must not fail. An existing
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
