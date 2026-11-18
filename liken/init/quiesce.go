package main

// Init remounts the disks read-only before the machine stops.
//
// A filesystem writes its clean-shutdown record when the kernel
// releases its superblock. vfat clears the dirty bit in the boot
// sector. ext4 flushes the journal and clears the flag that makes the
// next mount replay it. Until then the filesystem is marked in use on
// disk, and the next boot treats that as a crash.
//
// An unmount releases the superblock, but the booting slot and
// clusterState cannot be unmounted. The running root is a squashfs
// image stored as a file on the booting slot and attached through a
// loop device, so the slot stays busy while the machine runs.
// clusterState stays busy because containerd's overlay mounts use
// directories inside it, and those mounts outlive their containers. A
// lazy unmount of either removes it from the mount table but leaves the
// superblock unreleased, so no clean record is written.
//
// A read-only remount works on a busy filesystem. It does not wait for
// references to drop: it flushes the filesystem and writes the clean
// record while the mount stays in place. So shutdown remounts every
// writable disk read-only first and unmounts afterwards, and the
// next boot reads a clean record even when the unmount fails.
//
// The callers run this after the last write. On reboot that is after
// assertAndArmForReboot, which writes to the slots and to machineState.
//
// A FAT volume that is left dirty once stays dirty. When vfat mounts a
// volume whose dirty bit is already set, it stops managing the bit for
// that mount, so neither an unmount nor a read-only remount clears it.
// Only fsck clears it, so one unclean stop makes the slot report a
// crash on every later boot. That is why power-off runs this as well
// as reboot.

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"
)

// diskMount is a mounted filesystem that quiesceDisks remounts: its
// mount point and the mount flags to keep through the remount.
type diskMount struct {
	target string
	flags  uintptr
}

// mountOptionFlags maps the option names in /proc/self/mounts to the
// mount flags that set them. A remount replaces all the mount flags,
// so any flag not passed again is cleared. The table lists only
// per-mount flags. Filesystem-specific options are kept by a remount
// without being passed.
var mountOptionFlags = map[string]uintptr{
	"nosuid":     unix.MS_NOSUID,
	"nodev":      unix.MS_NODEV,
	"noexec":     unix.MS_NOEXEC,
	"noatime":    unix.MS_NOATIME,
	"nodiratime": unix.MS_NODIRATIME,
	"relatime":   unix.MS_RELATIME,
	"sync":       unix.MS_SYNCHRONOUS,
	"dirsync":    unix.MS_DIRSYNC,
	"lazytime":   unix.MS_LAZYTIME,
	"mand":       unix.MS_MANDLOCK,
}

// writableDiskMounts parses a mount table and returns the read-write
// mounts of block devices, in reverse table order.
//
// Other mounts need no remount. A tmpfs, an overlay, or a kernel
// filesystem has no disk, and a filesystem that is already read-only
// wrote its clean record when it became read-only.
//
// Reverse order puts a mount before any mount it covers. A filesystem
// mounted at more than one path is listed once for each path. The
// booting slot is mounted twice, once for early boot and once as its
// role, and a remount through either path reaches the same superblock.
func writableDiskMounts(mountTable string) []diskMount {
	lines := strings.Split(strings.TrimSpace(mountTable), "\n")
	mounts := make([]diskMount, 0, len(lines))
	for i := len(lines) - 1; i >= 0; i-- {
		fields := strings.Fields(lines[i])
		if len(fields) < 4 || !strings.HasPrefix(fields[0], "/dev/") {
			continue
		}
		options := strings.Split(fields[3], ",")
		var flags uintptr
		var writable bool
		for _, option := range options {
			if option == "rw" {
				writable = true
			}
			flags |= mountOptionFlags[option]
		}
		if !writable {
			continue
		}
		mounts = append(mounts, diskMount{target: unescapeMountField(fields[1]), flags: flags})
	}
	return mounts
}

// unescapeMountField decodes the octal escapes in a mount table field.
// The kernel writes a space, tab, newline, or backslash in a path as a
// backslash and three octal digits, because the table separates its
// fields with spaces and its entries with newlines.
func unescapeMountField(field string) string {
	if !strings.Contains(field, `\`) {
		return field
	}
	var out strings.Builder
	for i := 0; i < len(field); i++ {
		if field[i] == '\\' && i+3 < len(field) {
			if b, err := strconv.ParseUint(field[i+1:i+4], 8, 8); err == nil {
				out.WriteByte(byte(b))
				i += 3
				continue
			}
		}
		out.WriteByte(field[i])
	}
	return out.String()
}

// quiesceDisks remounts every writable disk read-only, so each one has
// a clean record before the machine stops. It prints each failure and
// continues, because nothing else would finish the shutdown if it
// stopped here.
func quiesceDisks() {
	table, err := os.ReadFile("/proc/self/mounts")
	if err != nil {
		fmt.Fprintf(os.Stderr, "liken: storage: reading the mount table: %v\n", err)
		return
	}
	for _, m := range writableDiskMounts(string(table)) {
		err := unix.Mount("", m.target, "", unix.MS_REMOUNT|unix.MS_RDONLY|m.flags, "")
		switch {
		case err == nil:
			fmt.Printf("liken: storage: %s is read-only\n", m.target)
		case errors.Is(err, fs.ErrNotExist):
			// ENOENT means a later mount hides this mount point, so
			// no path reaches it. No liken mount hides another, so the
			// mount belongs to another program. It is skipped without
			// a message, on the expectation that the loop already
			// remounted the filesystem through another entry.
		default:
			fmt.Fprintf(os.Stderr, "liken: storage: remounting %s read-only: %v\n", m.target, err)
		}
	}
}
