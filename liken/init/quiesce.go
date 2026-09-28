package main

// Leaving the disks in a finished state.
//
// A filesystem records whether it was shut down properly, and it
// records it at one moment: when the kernel releases its superblock.
// vfat clears the dirty bit in the boot sector there. ext4 flushes the
// journal and clears the flag that tells the next mount to replay it.
// Until that release happens, every disk on the machine still says
// that it is in use, and the next boot reads that as a crash.
//
// Unmounting is the ordinary way to reach that release, and two disks
// here are never free to be unmounted. The running root is a squashfs
// image that lives as a file on the booting slot, attached through a
// loop device, so that slot stays busy for as long as the machine
// runs. clusterState stays busy because containerd's overlay mounts
// name directories inside it, and those outlive the containers they
// belonged to. The shutdown detaches both lazily and reboots, which
// takes the mount out of the table and leaves the busy superblock
// unreleased and its record unwritten.
//
// A read-only remount is the operation that fits a busy filesystem. It
// waits for no reference to drop: it flushes the filesystem and writes
// the clean record while the mount stays where it is. So the shutdown
// remounts every writable disk read-only first, and unmounts
// afterwards. After the remount, a disk is correct on its own, and
// whether its unmount can finish no longer determines what the next
// boot reads.
//
// This runs after the last write. On the reboot path that means after
// the boot actuator has asserted the proven slot and armed any trial,
// because both of those write to the slots and to machineState.
//
// Getting this wrong once on a FAT filesystem is permanent. When vfat
// mounts a volume whose dirty bit is already set, it stops managing
// that bit for the life of the mount: it does not clear the bit at
// unmount, and a read-only remount does not clear it either. The bit
// is meant to survive until a person runs fsck, so one unclean stop
// leaves a slot reporting a crash on every boot after it, however
// cleanly the machine stops from then on. A volume that has never been
// stopped uncleanly stays clean, which is why this runs on every path
// that ends a boot rather than only on the one that reboots.

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"
)

// diskMount is one filesystem that quiesceDisks must finish: where it
// is mounted, and the flags to carry across the remount.
type diskMount struct {
	target string
	flags  uintptr
}

// mountOptionFlags maps the option names the kernel prints in
// /proc/self/mounts back to the flags that set them. A remount
// replaces the whole flag word rather than adding to it, so an option
// that is not named again is switched off. Only the flags that the
// kernel reports per mount belong here; the rest are filesystem
// options, which a remount keeps on its own.
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

// writableDiskMounts reads a mount table and returns the mounts that
// have a clean record to write, newest first.
//
// A mount qualifies when it is backed by a block device and mounted
// read-write. Everything else is skipped for a reason: a tmpfs, an
// overlay, or one of the kernel's own filesystems has no disk behind
// it, and a filesystem that is already read-only wrote its clean
// record when it became read-only.
//
// The order is the reverse of the table, so a mount that covers
// another is handled before the one underneath it. A filesystem that
// answers to more than one path appears more than once, which is what
// the booting slot needs: it is mounted once for the early boot and
// once as its role, and either path reaches the same superblock.
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

// unescapeMountField decodes the octal escapes that the kernel writes
// into a mount table. A space, a tab, a newline, and a backslash in a
// path each appear as a backslash and three octal digits, because the
// table itself is separated by spaces and newlines.
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

// quiesceDisks makes every writable disk on the machine read-only, so
// that each one carries a clean record before the machine stops. It
// reports what it could not finish and returns either way: a shutdown
// that stops here would leave the machine running with nothing left to
// run it.
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
			// A remount reaches a filesystem through its path, and a
			// mount that another mount covers has no path left to name
			// it by. No liken mount covers another, so this is a
			// filesystem that something outside liken stacked on. It is
			// skipped rather than reported: there is no path to remount
			// it by, and the mount underneath may well be one of the
			// entries this loop has already finished.
		default:
			fmt.Fprintf(os.Stderr, "liken: storage: %s stays writable: %v\n", m.target, err)
		}
	}
}
