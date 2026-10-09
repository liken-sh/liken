package main

// Tests for how storage mounts each role and takes the mounts down
// again. The mount syscalls are replaced by the recorder in
// mounttable_test.go, so these tests check which device lands on which
// path with which flags, and what the code does around each mount. The
// file systems are plain files under a fake /dev.

import (
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"golang.org/x/sys/unix"

	"github.com/liken-sh/liken/liken/machine"
)

// keepMachineStateWritable saves the flag that says machineState is on
// disk, starts the test with the given value, and restores the flag
// afterward.
func keepMachineStateWritable(t *testing.T, value bool) {
	t.Helper()
	old := machineStateWritable
	machineStateWritable = value
	t.Cleanup(func() { machineStateWritable = old })
}

// Teardown must take off whatever a failed reconcile left mounted, so
// that the next attempt starts from the RAM root. It works in reverse
// canonical order, so a mount stacked on a role's file system comes off
// before that file system does. biosBoot is never mounted and is not in
// the list. machineState comes off with the rest, so the fail-stop
// record has nowhere durable to go until a later attempt mounts it.
func TestTeardownStorageUnmountsEveryRoleInReverseOrder(t *testing.T) {
	mounts := fakeStorageMounts(t)
	keepMachineStateWritable(t, true)

	capturedStdout(t, teardownStorage)

	want := []string{
		clusterStateStaging,
		podLogsDir,
		"/var/lib/kubelet",
		"/var/lib/liken/pod-storage",
		"/var/lib/rancher",
		"/tmp",
		machine.MachineStateDir,
		machine.SystemSlotDir("B"),
		machine.SystemSlotDir("A"),
		bootHomeDir,
	}
	if got := mounts.unmountedPaths(); !slices.Equal(got, want) {
		t.Errorf("unmounted %v, want %v", got, want)
	}
	if machineStateWritable {
		t.Error("after teardown, machineState is no longer on disk")
	}
}

// The reboot path detaches lazily, because a container that was just
// killed can still hold a mount namespace open for a moment, and a
// plain unmount would fail on it.
func TestUnmountRoleMountsPassesTheCallersFlags(t *testing.T) {
	mounts := fakeStorageMounts(t)

	capturedStdout(t, func() { unmountRoleMounts(unix.MNT_DETACH, false) })

	if len(mounts.unmounts) == 0 {
		t.Fatal("nothing was unmounted")
	}
	for _, u := range mounts.unmounts {
		if u.flags != unix.MNT_DETACH {
			t.Errorf("%s was unmounted with flags %#x, want MNT_DETACH", u.target, u.flags)
		}
	}
}

// The console tells one story about what came apart. A path that was
// never mounted answers EINVAL or ENOENT, which means there was nothing
// to do, so it prints nothing. A real failure prints only when the
// caller asked for errors, because the reboot path expects some.
func TestDetachMountReportsOnlyRealFailures(t *testing.T) {
	const target = "/var/lib/rancher"
	cases := []struct {
		name       string
		err        error
		report     bool
		wantStdout string
		wantStderr string
	}{
		{"a mount that came off", nil, true, "liken: storage: unmounted /var/lib/rancher\n", ""},
		{"a path that is not a mount point", unix.EINVAL, true, "", ""},
		{"a path that does not exist", unix.ENOENT, true, "", ""},
		{"a busy mount at boot", unix.EBUSY, true, "", "liken: storage: unmounting /var/lib/rancher: device or resource busy\n"},
		{"a busy mount on the way down", unix.EBUSY, false, "", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			mounts := fakeStorageMounts(t)
			mounts.unmountErrs[target] = c.err
			stderr := fakeLogStream(t)

			stdout := capturedStdout(t, func() { detachMount(target, 0, c.report) })

			if stdout != c.wantStdout {
				t.Errorf("stdout = %q, want %q", stdout, c.wantStdout)
			}
			if got := stderr(); got != c.wantStderr {
				t.Errorf("stderr = %q, want %q", got, c.wantStderr)
			}
		})
	}
}

// ext4Device writes the start of an ext4 file system to a partition's
// node under the fake /dev: a superblock that records the given count
// of 4 KiB blocks. It returns the partition as discovery would report
// it, sized to exactly fill the file system, so nothing needs to grow.
func ext4Device(t *testing.T, dev, name string, role machine.StorageRoleName, blocks uint32) partition {
	t.Helper()
	image := make([]byte, 2048)
	copy(image[ext4SuperblockOffset:], superblock(blocks, 2, 0, 0))
	if err := os.WriteFile(filepath.Join(dev, name), image, 0o600); err != nil {
		t.Fatal(err)
	}
	return partition{name: name, disk: "vda", partName: "liken:" + string(role), sizeBytes: uint64(blocks) * 4096}
}

// A recognized data role keeps its file system and mounts it as ext4
// with the role's flags. Only machineState's mount makes the fail-stop
// record durable, so only that role sets the flag that says so.
func TestMountRoleMountsARecognizedExt4Role(t *testing.T) {
	cases := []struct {
		role         machine.StorageRoleName
		flags        uintptr
		wantWritable bool
	}{
		{machine.MachineStateRole, 0, true},
		{machine.MachineEphemeralRole, unix.MS_NOSUID | unix.MS_NODEV, false},
		{machine.PodStorageRole, 0, false},
		{machine.PodEphemeralRole, 0, false},
	}
	for _, c := range cases {
		t.Run(string(c.role), func(t *testing.T) {
			_, dev := fakeMachine(t)
			mounts := fakeStorageMounts(t)
			keepMachineStateWritable(t, false)
			target := fakeRoleMountPath(t, c.role)
			p := ext4Device(t, dev, "vda1", c.role, 100)

			var err error
			capturedStdout(t, func() { err = mountRole(declared(c.role, "/dev/vda", ""), p, false) })
			if err != nil {
				t.Fatal(err)
			}

			want := []storageMount{{source: dev + "/vda1", target: target, fstype: "ext4", flags: c.flags}}
			if !slices.Equal(mounts.mounts, want) {
				t.Errorf("mounts = %+v, want %+v", mounts.mounts, want)
			}
			if machineStateWritable != c.wantWritable {
				t.Errorf("machineStateWritable = %v, want %v", machineStateWritable, c.wantWritable)
			}
		})
	}
}

// A new ext4 root is mode 0755 and writable only by root. /tmp must be
// writable by every user, so the mount applies the role's mode to the
// mounted root.
func TestMountRoleOpensTmpToEveryUser(t *testing.T) {
	_, dev := fakeMachine(t)
	fakeStorageMounts(t)
	target := fakeRoleMountPath(t, machine.MachineEphemeralRole)
	p := ext4Device(t, dev, "vda1", machine.MachineEphemeralRole, 100)

	var err error
	capturedStdout(t, func() { err = mountRole(declared(machine.MachineEphemeralRole, "/dev/vda", ""), p, false) })
	if err != nil {
		t.Fatal(err)
	}
	if got, want := modeOf(t, target).Perm(), fs.FileMode(0o777); got != want {
		t.Errorf("mode of /tmp = %v, want %v", got, want)
	}
}

// A slot that already carries FAT32 keeps it, and mounts with the slot
// flags: nothing runs from a slot, so it allows no executables, no
// device files, and no suid bit.
func TestMountRoleMountsARecognizedSlotAsFAT(t *testing.T) {
	_, dev := fakeMachine(t)
	mounts := fakeStorageMounts(t)
	target := fakeRoleMountPath(t, machine.SystemBRole)
	fatVolumeAt(t, filepath.Join(dev, "vda3"), false)
	p := partition{name: "vda3", disk: "vda", partName: "liken:systemB", sizeBytes: 64 << 20}

	var err error
	capturedStdout(t, func() { err = mountRole(declared(machine.SystemBRole, "/dev/vda", ""), p, false) })
	if err != nil {
		t.Fatal(err)
	}
	want := []storageMount{{source: dev + "/vda3", target: target, fstype: "vfat", flags: slotMountFlags}}
	if !slices.Equal(mounts.mounts, want) {
		t.Errorf("mounts = %+v, want %+v", mounts.mounts, want)
	}
}

// volumeLabel reads the label field of a FAT32 boot sector.
func volumeLabel(t *testing.T, path string) string {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	label := make([]byte, 11)
	if _, err := f.ReadAt(label, 71); err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(label))
}

// A partition this boot created gets a new file system, even when the
// bytes underneath still hold an old one. A reinstall writes the same
// layout at the same offsets, and keeping the old volume would carry
// the previous install into the new one.
func TestMountRoleFormatsASlotThisBootCreated(t *testing.T) {
	_, dev := fakeMachine(t)
	fakeStorageMounts(t)
	fakeRoleMountPath(t, machine.SystemBRole)
	device := fatVolumeAt(t, filepath.Join(dev, "vda3"), false)
	p := partition{name: "vda3", disk: "vda", partName: "liken:systemB", sizeBytes: 64 << 20}

	var err error
	capturedStdout(t, func() { err = mountRole(declared(machine.SystemBRole, "/dev/vda", ""), p, true) })
	if err != nil {
		t.Fatal(err)
	}
	if got := volumeLabel(t, device); got != "LIKEN-SYS-B" {
		t.Errorf("label = %q, want the new slot's label LIKEN-SYS-B", got)
	}
}

// A role that will not mount stops the boot, and the error names the
// device and the role so the console says which one. A failed
// machineState mount must leave the fail-stop record pointed at RAM.
func TestMountRoleReportsAMountThatFails(t *testing.T) {
	_, dev := fakeMachine(t)
	mounts := fakeStorageMounts(t)
	mounts.mountErr = unix.EPERM
	keepMachineStateWritable(t, false)
	fakeRoleMountPath(t, machine.MachineStateRole)
	p := ext4Device(t, dev, "vda1", machine.MachineStateRole, 100)

	err := mountRole(declared(machine.MachineStateRole, "/dev/vda", ""), p, false)

	if err == nil || !strings.Contains(err.Error(), dev+"/vda1") || !strings.Contains(err.Error(), "machineState") {
		t.Errorf("the error must name the device and the role: %v", err)
	}
	if machineStateWritable {
		t.Error("a machineState that did not mount is not writable")
	}
}
