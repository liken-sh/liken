package main

import (
	"testing"

	"golang.org/x/sys/unix"
)

// The mount table of a lab machine at the moment it reboots, trimmed
// to the shapes that matter. It carries every case quiesceDisks has to
// judge: the staging tmpfs, the booting slot under two mounts, the
// read-only squashfs that is the running root, the role filesystems,
// the kernel's own filesystems, and containerd's leftover overlays.
const shutdownMountTable = `tmpfs /var/lib/liken/boot tmpfs rw,nosuid,relatime,size=131072k,mode=755,inode64 0 0
/dev/vdc3 /var/lib/liken/boot/slot vfat rw,relatime,fmask=0022,dmask=0022 0 0
/dev/loop0 /var/lib/liken/boot/system squashfs ro,relatime,errors=continue 0 0
overlay / overlay rw,relatime,lowerdir=/liken-boot/system 0 0
proc /proc proc rw,nosuid,nodev,noexec,relatime 0 0
sysfs /sys sysfs rw,nosuid,nodev,noexec,relatime 0 0
devtmpfs /dev devtmpfs rw,nosuid,relatime,size=486472k 0 0
/dev/vdc2 /var/lib/liken/boot vfat rw,nosuid,nodev,noexec,relatime,fmask=0022 0 0
/dev/vdc3 /var/lib/liken/system/a vfat rw,nosuid,nodev,noexec,relatime,fmask=0022 0 0
/dev/vda2 /var/lib/rancher ext4 rw,relatime 0 0
/dev/vdb3 /var/lib/kubelet ext4 rw,relatime 0 0
none /sys/fs/pstore pstore rw,nosuid,nodev,noexec,relatime 0 0
cgroup2 /sys/fs/cgroup cgroup2 rw,nsdelegate,memory_recursiveprot 0 0
tmpfs /run tmpfs rw,nosuid,nodev,mode=755,inode64 0 0
/dev/vdb3 /var/log/pods ext4 rw,relatime 0 0
overlay /run/k3s/containerd/io.containerd.runtime.v2.task/k8s.io/abc/rootfs overlay rw,relatime,lowerdir=/var/lib/rancher/k3s/agent/containerd/x 0 0
`

func TestWritableDiskMountsPicksOnlyWritableDisks(t *testing.T) {
	var targets []string
	for _, m := range writableDiskMounts(shutdownMountTable) {
		targets = append(targets, m.target)
	}
	// Newest first, so a stacked mount comes off before the mount it
	// covers. Every entry names a block device that is mounted
	// read-write; nothing virtual and nothing already read-only.
	want := []string{
		"/var/log/pods",
		"/var/lib/kubelet",
		"/var/lib/rancher",
		"/var/lib/liken/system/a",
		"/var/lib/liken/boot",
		"/var/lib/liken/boot/slot",
	}
	if len(targets) != len(want) {
		t.Fatalf("got %v, want %v", targets, want)
	}
	for i := range want {
		if targets[i] != want[i] {
			t.Errorf("position %d: got %q, want %q", i, targets[i], want[i])
		}
	}
}

func TestWritableDiskMountsSkipsTheReadOnlyRootImage(t *testing.T) {
	// The squashfs that carries the running system is already
	// read-only, so it has no clean record to write.
	for _, m := range writableDiskMounts(shutdownMountTable) {
		if m.target == "/var/lib/liken/boot/system" {
			t.Fatal("the read-only root image must not be remounted")
		}
	}
}

func TestWritableDiskMountsCoversBothMountsOfTheBootingSlot(t *testing.T) {
	// The booting slot answers to two paths. Either one reaches the
	// superblock, and the loop device that holds the running root
	// keeps the slot busy, so the list must not drop either.
	var count int
	for _, m := range writableDiskMounts(shutdownMountTable) {
		if m.target == "/var/lib/liken/system/a" || m.target == "/var/lib/liken/boot/slot" {
			count++
		}
	}
	if count != 2 {
		t.Errorf("got %d mounts of the booting slot, want 2", count)
	}
}

func TestWritableDiskMountsKeepsMountFlags(t *testing.T) {
	// A remount replaces the flag word, so a flag the caller does not
	// name is dropped. The slots are nosuid, nodev, and noexec, and
	// they must stay that way for the rest of the shutdown.
	for _, m := range writableDiskMounts(shutdownMountTable) {
		if m.target != "/var/lib/liken/system/a" {
			continue
		}
		want := uintptr(unix.MS_NOSUID | unix.MS_NODEV | unix.MS_NOEXEC | unix.MS_RELATIME)
		if m.flags != want {
			t.Errorf("got flags %#x, want %#x", m.flags, want)
		}
		return
	}
	t.Fatal("the booting slot's role mount is missing from the list")
}

func TestWritableDiskMountsDecodesEscapedPaths(t *testing.T) {
	// The kernel escapes a space, a tab, a newline, and a backslash in
	// a mount path as octal, so a path has to be decoded before it can
	// be used in a syscall.
	table := `/dev/sda1 /mnt/two\040words ext4 rw,relatime 0 0
`
	got := writableDiskMounts(table)
	if len(got) != 1 {
		t.Fatalf("got %d mounts, want 1", len(got))
	}
	if got[0].target != "/mnt/two words" {
		t.Errorf("got %q, want %q", got[0].target, "/mnt/two words")
	}
}

func TestWritableDiskMountsIgnoresShortAndEmptyLines(t *testing.T) {
	// A truncated read of the mount table must not panic the shutdown.
	table := "\n/dev/sda1 /mnt\n\n/dev/sdb1 /other ext4 rw 0 0\n"
	got := writableDiskMounts(table)
	if len(got) != 1 || got[0].target != "/other" {
		t.Errorf("got %v, want one mount of /other", got)
	}
}
