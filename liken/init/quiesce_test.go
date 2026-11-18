package main

import (
	"testing"

	"golang.org/x/sys/unix"
)

// The mount table of a lab machine as it reboots, cut down to one line
// of each kind: the boot-mounts tmpfs, the booting slot at two paths,
// the read-only squashfs of the running root, the role filesystems,
// the kernel's filesystems, and a containerd overlay that outlived its
// container.
const shutdownMountTable = `tmpfs /var/lib/liken/boot-mounts tmpfs rw,nosuid,relatime,size=131072k,mode=755,inode64 0 0
/dev/vdc3 /var/lib/liken/boot-mounts/slot vfat rw,relatime,fmask=0022,dmask=0022 0 0
/dev/loop0 /var/lib/liken/boot-mounts/system squashfs ro,relatime,errors=continue 0 0
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
	// Reverse table order, so a mount is remounted before the mount
	// it covers. Only read-write mounts of block devices are listed.
	want := []string{
		"/var/log/pods",
		"/var/lib/kubelet",
		"/var/lib/rancher",
		"/var/lib/liken/system/a",
		"/var/lib/liken/boot",
		"/var/lib/liken/boot-mounts/slot",
	}
	if len(targets) != len(want) {
		t.Fatalf("the targets are %v, want %v", targets, want)
	}
	for i := range want {
		if targets[i] != want[i] {
			t.Errorf("target %d is %q, want %q", i, targets[i], want[i])
		}
	}
}

func TestWritableDiskMountsSkipsTheReadOnlyRootImage(t *testing.T) {
	// The squashfs of the running root is already read-only, so it
	// needs no remount.
	for _, m := range writableDiskMounts(shutdownMountTable) {
		if m.target == "/var/lib/liken/boot-mounts/system" {
			t.Fatal("writableDiskMounts lists the read-only root image, want it left out")
		}
	}
}

func TestWritableDiskMountsCoversBothMountsOfTheBootingSlot(t *testing.T) {
	// The booting slot is mounted at two paths, and the list keeps
	// both.
	var count int
	for _, m := range writableDiskMounts(shutdownMountTable) {
		if m.target == "/var/lib/liken/system/a" || m.target == "/var/lib/liken/boot-mounts/slot" {
			count++
		}
	}
	if count != 2 {
		t.Errorf("the list has %d mounts of the booting slot, want 2", count)
	}
}

func TestWritableDiskMountsKeepsMountFlags(t *testing.T) {
	// A remount clears every flag it does not pass. The slots are
	// nosuid, nodev, and noexec, and must stay so for the rest of the
	// shutdown.
	for _, m := range writableDiskMounts(shutdownMountTable) {
		if m.target != "/var/lib/liken/system/a" {
			continue
		}
		want := uintptr(unix.MS_NOSUID | unix.MS_NODEV | unix.MS_NOEXEC | unix.MS_RELATIME)
		if m.flags != want {
			t.Errorf("the flags are %#x, want %#x", m.flags, want)
		}
		return
	}
	t.Fatal("writableDiskMounts does not list the booting slot's role mount, want it listed")
}

func TestWritableDiskMountsDecodesEscapedPaths(t *testing.T) {
	// The kernel writes a space in a mount path as \040.
	table := `/dev/sda1 /mnt/two\040words ext4 rw,relatime 0 0
`
	got := writableDiskMounts(table)
	if len(got) != 1 {
		t.Fatalf("the list has %d mounts, want 1", len(got))
	}
	if got[0].target != "/mnt/two words" {
		t.Errorf("the target is %q, want %q", got[0].target, "/mnt/two words")
	}
}

func TestWritableDiskMountsIgnoresShortAndEmptyLines(t *testing.T) {
	// A truncated read of the mount table must not panic the shutdown.
	table := "\n/dev/sda1 /mnt\n\n/dev/sdb1 /other ext4 rw 0 0\n"
	got := writableDiskMounts(table)
	if len(got) != 1 || got[0].target != "/other" {
		t.Errorf("the list is %v, want one mount of /other", got)
	}
}
