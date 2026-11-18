package main

// This file moves init from the kernel's rootfs onto the real root:
// the read-only system image under a tmpfs overlay of fixed size.
//
// The kernel unpacks the initramfs into rootfs, the filesystem at the
// root of the mount tree. The kernel cannot unmount rootfs, and
// pivot_root refuses to run on it. rootfs is RAM with no size limit,
// and nothing reports how much of it is in use, so kubelet's
// ephemeral-storage accounting reports nothing for it. An OS that
// stays on rootfs keeps its whole image in RAM until it reboots.
//
// So the real root is an overlay. Its lower layer is the system image,
// which rootimage.go finds and loop-mounts read-only. Its upper layer
// is a tmpfs of writesSize. The image's files then use page cache,
// which the kernel can free, in place of a permanent copy in RAM.
//
// rootfs also holds the deployment layer: the manifests, the
// identity, the declared modules, and their index. init copies these
// files onto the overlay, where they replace the image's copies, as a
// later initramfs archive replaces the files of an earlier one.
//
// The steps follow util-linux's switch_root:
//
//  1. mount the system image and the overlay on it,
//  2. copy the deployment layer from rootfs onto the overlay,
//  3. delete the files on rootfs so the kernel frees their RAM,
//  4. move the mounts into the new root, move the new root onto /,
//     and chroot into it,
//  5. exec init again from the new root.
//
// The exec is needed because the running init is mapped from rootfs,
// and the kernel cannot free those pages until another program
// replaces it. exec keeps PID 1. So "hello from userspace" shows
// twice on the console.
//
// An install boot (liken.install) and a hardware report boot
// (liken.report) stay on rootfs. The installer's release payload is
// far bigger than writesSize.

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

// switchedMarker is the argument that tells init it runs a second
// time, after the switch. The kernel also passes a boot parameter that
// is not key=value to init as an argument, but no parameter on liken's
// command line starts with dashes.
const switchedMarker = "--switched"

const newRoot = "/newroot"

// stagingDir is a tmpfs with the overlay's upper and work directories,
// the system image's mount, and the slot's mount when the image comes
// from a slot. One MS_MOVE moves all of it to bootMountsDir in the new
// root, where the running system can read what it booted from.
const stagingDir = "/liken-boot"

// bootMountsDir is not /var/lib/liken/boot, because the bootHome role
// mounts there on a BIOS machine and would hide the slot and the image.
const bootMountsDir = "/var/lib/liken/boot-mounts"

// writesSize limits everything the running system writes under /:
// k3s config drop-ins, resolv.conf, and the deployment layer. Data that
// grows with use belongs on a disk role. When this tmpfs fills, the fix
// is in the program that writes under /, not a larger size.
const writesSize = "128m"

// maybeSwitchRoot runs first in init, and does nothing after the
// switch. It must run before init mounts anything else, because the
// switch moves only the staging tree into the new root, and before
// init starts any process. It mounts /proc to read the liken.*
// parameters from /proc/cmdline: the kernel treats a parameter with a
// dot in its name as a module parameter, and passes it to init neither
// as an argument nor in the environment. /proc is unmounted again on
// every path, and mountEssentials mounts it later.
func maybeSwitchRoot() {
	if slices.Contains(os.Args[1:], switchedMarker) {
		return
	}
	if err := os.MkdirAll("/proc", 0o755); err == nil {
		if err := unix.Mount("proc", "/proc", "proc", unix.MS_NOSUID|unix.MS_NOEXEC|unix.MS_NODEV, ""); err != nil {
			fmt.Fprintf(os.Stderr, "liken: mounting /proc for the switch: %v\n", err)
		}
	}

	// Install and report boots need these modules too: an install boot
	// mounts FAT slots, and vfat's encoding table is one of them.
	if names, err := readModuleList(filepath.Join(bootModulesDir, "boot-modules.conf")); err == nil {
		loadBootModules(names...)
	} else {
		fmt.Fprintf(os.Stderr, "liken: reading the boot module list: %v\n", err)
	}

	if bootParam(installParam) || bootParam(reportParam) {
		fmt.Println("liken: install or report boot, so init stays on rootfs")
		_ = unix.Unmount("/proc", unix.MNT_DETACH)
		return
	}
	if err := switchRoot(); err != nil {
		// The machine still works on rootfs, without the size limit.
		fmt.Fprintf(os.Stderr, "liken: switching root: %v, continuing on rootfs\n", err)
		_ = unix.Unmount("/proc", unix.MNT_DETACH)
	}
}

func switchRoot() error {
	// The loop device and the slot's partition need device nodes.
	if err := os.MkdirAll("/dev", 0o755); err != nil {
		return err
	}
	if err := unix.Mount("devtmpfs", "/dev", "devtmpfs", unix.MS_NOSUID, ""); err != nil {
		return fmt.Errorf("mounting devtmpfs: %w", err)
	}

	// findSystemImage reads the GPT partition names under /sys/block.
	if err := os.MkdirAll("/sys", 0o755); err != nil {
		return err
	}
	if err := unix.Mount("sysfs", "/sys", "sysfs", unix.MS_NOSUID|unix.MS_NOEXEC|unix.MS_NODEV, ""); err != nil {
		return fmt.Errorf("mounting sysfs: %w", err)
	}

	if err := os.MkdirAll(stagingDir, 0o755); err != nil {
		return err
	}
	if err := unix.Mount("tmpfs", stagingDir, "tmpfs",
		unix.MS_NOSUID, "mode=0755,size="+writesSize); err != nil {
		return fmt.Errorf("mounting the tmpfs for the overlay's writes: %w", err)
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

	// The system image has its own init. The boot archive's module tree
	// stays off the real root, so its partial modules.dep cannot replace
	// the image's complete one.
	if err := copyTree("/", newRoot, []string{
		newRoot, "/dev", "/proc", "/sys", stagingDir,
		"/liken", ramImage, bootModulesDir,
	}); err != nil {
		return fmt.Errorf("copying the deployment layer onto the overlay: %w", err)
	}

	// Delete rootfs's files now, because after the move below no path
	// reaches the old root. Otherwise the deployment layer stays in RAM
	// twice. A system image that came in RAM (ramImage) stays in RAM
	// anyway, because its loop device keeps the deleted file open.
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
			return fmt.Errorf("deleting the files of rootfs: %w", err)
		}
	}

	target := filepath.Join(newRoot, bootMountsDir)
	if err := os.MkdirAll(target, 0o755); err != nil {
		return err
	}
	if err := unix.Mount(stagingDir, target, "", unix.MS_MOVE, ""); err != nil {
		return fmt.Errorf("moving boot mounts into the new root: %w", err)
	}

	// A mount left on the old root keeps it in use. mountEssentials
	// mounts these again in the new root. The console keeps working
	// because init's stdio descriptors are already open.
	_ = unix.Unmount("/dev", unix.MNT_DETACH)
	_ = unix.Unmount("/proc", unix.MNT_DETACH)
	_ = unix.Unmount("/sys", unix.MNT_DETACH)

	// pivot_root refuses to run when / is rootfs, so move the overlay
	// onto / and chroot into it. The chdir makes "." the new root for
	// both calls.
	if err := os.Chdir(newRoot); err != nil {
		return err
	}
	if err := unix.Mount(".", "/", "", unix.MS_MOVE, ""); err != nil {
		return fmt.Errorf("moving the overlay onto /: %w", err)
	}
	if err := unix.Chroot("."); err != nil {
		return fmt.Errorf("changing root into the overlay: %w", err)
	}
	if err := os.Chdir("/"); err != nil {
		return err
	}

	fmt.Println("liken: re-executing from the system image")
	return unix.Exec("/liken", []string{"/liken", switchedMarker}, os.Environ())
}

// copyTree copies the tree under src into dst, except the paths in
// skip. An initramfs that liken builds has only directories, symlinks,
// and regular files, so any other type is an error. It replaces a file
// that dst already has, because the overlay's lower layer has the
// image's copy of each file that the deployment layer replaces, such
// as modules.dep.
func copyTree(src, dst string, skip []string) error {
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
			// An existing directory keeps the image's permissions.
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
			return fmt.Errorf("%s has file type %s, and an initramfs holds only directories, symlinks, and regular files", path, d.Type())
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
	// OpenFile applies the umask, and leaves the mode of a file that
	// exists, so set the mode here.
	if err := out.Chmod(perm); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}
