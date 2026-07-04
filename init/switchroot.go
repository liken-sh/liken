package main

// Trading the kernel's rootfs for a real root filesystem.
//
// When the kernel unpacks the initramfs, it doesn't mount a filesystem
// to hold the files. They land in rootfs: the filesystem instance the
// kernel creates as the root of the mount tree before any userspace
// exists. rootfs is the bottom of the stack by construction — it can
// never be unmounted, and pivot_root refuses to operate on it, because
// both would leave the mount tree with no root at all.
//
// A machine can run this way — but rootfs makes a poor permanent home.
// It appears in the mount table as device "rootfs", a name nothing can
// resolve to a measurable filesystem, so anything that wants to account
// for the root filesystem comes up empty. kubelet minds the most: it
// meters node ephemeral storage against the root filesystem, and on
// rootfs that bookkeeping has nothing to stand on.
//
// The escape is a userspace ritual, named for the util-linux tool that
// performs it on conventional systems (switch_root):
//
//  1. mount a fresh tmpfs,
//  2. copy the entire operating system into it,
//  3. delete the originals, handing their RAM back to the kernel,
//  4. move the tmpfs mount onto / and chroot into it,
//  5. re-exec init from the new root.
//
// The re-exec is not ceremony. The running program is the last thing
// pinning the old world: its executable is mapped from rootfs, so those
// pages can't be freed until something else is running in their place.
// exec replaces the process image while keeping PID 1 — the second
// "hello from userspace" on the console is the same program taking its
// first breath in its new home.
//
// Why tmpfs and not just staying on rootfs? Even when rootfs is backed
// by tmpfs (the kernel's default when no root= is given), the mount
// entry still says "rootfs" and still can't be unmounted or measured.
// An ordinary tmpfs mount is a first-class citizen: it has a size (half
// of RAM by default — an honest cap for an OS that lives in memory),
// statfs reports real numbers against it, and df would show it if this
// machine had a df.

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

// switchedMarker is how init tells its post-switch self apart from its
// first incarnation: the re-exec passes it as an argument. The kernel
// hands unrecognized non-key=value boot parameters to init as arguments
// too, but nothing on our command line starts with dashes.
const switchedMarker = "--switched"

const newRoot = "/newroot"

// maybeSwitchRoot runs once, before init touches anything else: the
// switch must happen while the mount tree is empty (one moved mount is
// simple; a forest of them is not) and before any child processes exist.
func maybeSwitchRoot() {
	if slices.Contains(os.Args[1:], switchedMarker) {
		return
	}
	if err := switchRoot(); err != nil {
		// A machine still on rootfs is degraded but alive, and the
		// console works either way; carry on and let the report show
		// the state of things.
		fmt.Fprintf(os.Stderr, "liken: switch_root: %v (continuing on rootfs)\n", err)
	}
}

func switchRoot() error {
	var stat unix.Statfs_t
	if err := unix.Statfs("/", &stat); err == nil {
		fmt.Printf("liken: root is the kernel's rootfs (backed by %s); switching to a real tmpfs\n",
			fsTypeName(stat.Type))
	}

	// Whatever the kernel put at /dev belongs to the old root: at
	// minimum a /dev/console node it creates itself when the image
	// ships none (so init can have stdio), possibly a whole automounted
	// devtmpfs. Detach any mount so the delete below can't reach into
	// the kernel's shared device catalog; copyTree skips the directory
	// entirely, and mountEssentials mounts a fresh devtmpfs in the new
	// root. The console keeps working throughout — our stdio
	// descriptors were opened before any of this.
	_ = unix.Unmount("/dev", unix.MNT_DETACH)

	if err := os.MkdirAll(newRoot, 0o755); err != nil {
		return err
	}
	if err := unix.Mount("tmpfs", newRoot, "tmpfs", 0, "mode=0755"); err != nil {
		return fmt.Errorf("mounting tmpfs: %w", err)
	}
	if err := copyTree("/", newRoot); err != nil {
		return fmt.Errorf("copying the system: %w", err)
	}

	// Delete the originals while they're still reachable — after the
	// move below, the old root has no path. This is the step that
	// actually reclaims the RAM; skipping it would leave the machine
	// carrying two copies of the OS forever.
	entries, err := os.ReadDir("/")
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if "/"+entry.Name() == newRoot {
			continue
		}
		if err := os.RemoveAll("/" + entry.Name()); err != nil {
			return fmt.Errorf("clearing old root: %w", err)
		}
	}

	// The move-and-chroot dance. MS_MOVE grafts the tmpfs onto / (legal
	// where pivot_root is not, because nothing has to be unmounted), and
	// chroot(".") repoints this process's idea of "/" at it. Order
	// matters: chdir first so "." names the new root throughout.
	if err := os.Chdir(newRoot); err != nil {
		return err
	}
	if err := unix.Mount(".", "/", "", unix.MS_MOVE, ""); err != nil {
		return fmt.Errorf("moving tmpfs onto /: %w", err)
	}
	if err := unix.Chroot("."); err != nil {
		return fmt.Errorf("chroot: %w", err)
	}
	if err := os.Chdir("/"); err != nil {
		return err
	}

	fmt.Println("liken: re-executing from the new root")
	return unix.Exec("/liken", []string{"/liken", switchedMarker}, os.Environ())
}

// fsTypeName decodes the statfs filesystem magic numbers this boot can
// actually encounter: rootfs is ramfs unless the kernel was built with
// tmpfs support, in which case tmpfs quietly backs it.
func fsTypeName(magic int64) string {
	switch magic {
	case unix.TMPFS_MAGIC:
		return "tmpfs"
	case unix.RAMFS_MAGIC:
		return "ramfs"
	default:
		return fmt.Sprintf("magic %#x", magic)
	}
}

// copyTree replicates the filesystem tree at src into dst: directories,
// symlinks, and regular files, with permissions intact. That's the
// complete inventory of an initramfs built by our image build — anything
// else showing up is a surprise worth failing loudly over.
func copyTree(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if path == src {
			return nil
		}
		if path == dst {
			return fs.SkipDir
		}
		target := filepath.Join(dst, strings.TrimPrefix(path, src))
		// /dev is the kernel's business, not the image's: it holds
		// device nodes, not files. The new root gets the empty
		// directory and a fresh devtmpfs mounted over it.
		if path == filepath.Join(src, "dev") {
			if err := os.Mkdir(target, 0o755); err != nil {
				return err
			}
			return fs.SkipDir
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		switch {
		case d.IsDir():
			if err := os.Mkdir(target, 0o755); err != nil {
				return err
			}
			// Chmod after the fact: mkdir's mode argument is filtered
			// through the umask, and these must match the originals bit
			// for bit.
			return os.Chmod(target, info.Mode().Perm())
		case d.Type()&fs.ModeSymlink != 0:
			link, err := os.Readlink(path)
			if err != nil {
				return err
			}
			return os.Symlink(link, target)
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
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, perm)
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
