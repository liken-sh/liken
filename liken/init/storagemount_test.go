package main

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

// setMachineStateWritable sets machineStateWritable for one test.
func setMachineStateWritable(t *testing.T, value bool) {
	t.Helper()
	old := machineStateWritable
	machineStateWritable = value
	t.Cleanup(func() { machineStateWritable = old })
}

// Teardown unmounts in reverse of the mount order, so a mount on top of
// a role's file system comes off before that file system. biosBoot is
// never mounted. After teardown, failBoot writes no record until a
// later attempt mounts machineState again.
func TestTeardownStorageUnmountsEveryRoleInReverseOrder(t *testing.T) {
	mounts := fakeStorageMounts(t)
	setMachineStateWritable(t, true)

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
		t.Error("machineStateWritable is true after teardown, want false")
	}
}

// The reboot path passes MNT_DETACH, because a container that was just
// killed can still keep a mount namespace open, and a plain unmount
// would fail with EBUSY.
func TestUnmountRoleMountsPassesTheCallersFlags(t *testing.T) {
	mounts := fakeStorageMounts(t)

	capturedStdout(t, func() { unmountRoleMounts(unix.MNT_DETACH, false) })

	if len(mounts.unmounts) == 0 {
		t.Fatal("unmountRoleMounts unmounted nothing, want each role mount unmounted")
	}
	for _, u := range mounts.unmounts {
		if u.flags != unix.MNT_DETACH {
			t.Errorf("%s was unmounted with flags %#x, want MNT_DETACH", u.target, u.flags)
		}
	}
}

// EINVAL and ENOENT mean there was nothing to unmount, so detachMount
// prints nothing for them. It prints any other failure only when the
// caller asks, because the reboot path expects some failures.
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

// ext4Device writes an ext4 superblock with the given number of 4 KiB
// blocks to a partition under the fake /dev. It returns the partition
// sized to exactly fit the file system, so nothing grows.
func ext4Device(t *testing.T, dev, name string, role machine.StorageRoleName, blocks uint32) partition {
	t.Helper()
	image := make([]byte, 2048)
	copy(image[ext4SuperblockOffset:], superblock(blocks, 2, 0, 0))
	if err := os.WriteFile(filepath.Join(dev, name), image, 0o600); err != nil {
		t.Fatalf("writing the partition device: %v", err)
	}
	return partition{name: name, disk: "vda", partName: "liken:" + string(role), sizeBytes: uint64(blocks) * 4096}
}

// Only the machineState role sets machineStateWritable, because failBoot
// writes its record only to that partition.
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
			setMachineStateWritable(t, false)
			target := fakeRoleMountPath(t, c.role)
			p := ext4Device(t, dev, "vda1", c.role, 100)

			var err error
			capturedStdout(t, func() { err = mountRole(declared(c.role, "/dev/vda", ""), p, false) })
			if err != nil {
				t.Fatalf("mounting the role: %v", err)
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

// The root of a new ext4 file system is mode 0755, so the mount sets
// /tmp to 1777: writable by every user, and with the sticky bit, so
// only a file's owner can delete or rename it.
func TestMountRoleOpensTmpToEveryUser(t *testing.T) {
	_, dev := fakeMachine(t)
	fakeStorageMounts(t)
	target := fakeRoleMountPath(t, machine.MachineEphemeralRole)
	p := ext4Device(t, dev, "vda1", machine.MachineEphemeralRole, 100)

	var err error
	capturedStdout(t, func() { err = mountRole(declared(machine.MachineEphemeralRole, "/dev/vda", ""), p, false) })
	if err != nil {
		t.Fatalf("mounting the role: %v", err)
	}
	if got, want := modeOf(t, target), fs.ModeSticky|0o777; got != want {
		t.Errorf("mode of /tmp = %v, want %v", got, want)
	}
}

// No program runs from a slot, so the slot flags allow no executables,
// no device files, and no setuid.
func TestMountRoleMountsARecognizedSlotAsFAT(t *testing.T) {
	_, dev := fakeMachine(t)
	mounts := fakeStorageMounts(t)
	target := fakeRoleMountPath(t, machine.SystemBRole)
	fatVolumeAt(t, filepath.Join(dev, "vda3"), false)
	p := partition{name: "vda3", disk: "vda", partName: "liken:systemB", sizeBytes: 64 << 20}

	var err error
	capturedStdout(t, func() { err = mountRole(declared(machine.SystemBRole, "/dev/vda", ""), p, false) })
	if err != nil {
		t.Fatalf("mounting the role: %v", err)
	}
	want := []storageMount{{source: dev + "/vda3", target: target, fstype: "vfat", flags: slotMountFlags}}
	if !slices.Equal(mounts.mounts, want) {
		t.Errorf("mounts = %+v, want %+v", mounts.mounts, want)
	}
}

func volumeLabel(t *testing.T, path string) string {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("opening the volume: %v", err)
	}
	defer f.Close()
	label := make([]byte, 11)
	if _, err := f.ReadAt(label, 71); err != nil {
		t.Fatalf("reading the volume label: %v", err)
	}
	return strings.TrimSpace(string(label))
}

// A partition that this boot created is formatted even when it still
// holds an old file system. A reinstall writes the same layout at the
// same offsets, so the old file system would bring the previous
// install's data into the new one.
func TestMountRoleFormatsASlotThisBootCreated(t *testing.T) {
	_, dev := fakeMachine(t)
	fakeStorageMounts(t)
	fakeRoleMountPath(t, machine.SystemBRole)
	device := fatVolumeAt(t, filepath.Join(dev, "vda3"), false)
	p := partition{name: "vda3", disk: "vda", partName: "liken:systemB", sizeBytes: 64 << 20}

	var err error
	capturedStdout(t, func() { err = mountRole(declared(machine.SystemBRole, "/dev/vda", ""), p, true) })
	if err != nil {
		t.Fatalf("mounting the role: %v", err)
	}
	if got := volumeLabel(t, device); got != "LIKEN-SYS-B" {
		t.Errorf("the volume label is %q, want LIKEN-SYS-B because this boot formatted the slot", got)
	}
}

// When the machineState mount fails, machineStateWritable stays false,
// so failBoot writes no record.
func TestMountRoleReportsAMountThatFails(t *testing.T) {
	_, dev := fakeMachine(t)
	mounts := fakeStorageMounts(t)
	mounts.mountErr = unix.EPERM
	setMachineStateWritable(t, false)
	fakeRoleMountPath(t, machine.MachineStateRole)
	p := ext4Device(t, dev, "vda1", machine.MachineStateRole, 100)

	err := mountRole(declared(machine.MachineStateRole, "/dev/vda", ""), p, false)

	if err == nil || !strings.Contains(err.Error(), dev+"/vda1") || !strings.Contains(err.Error(), "machineState") {
		t.Errorf("the error is %v, want one that names the device %s/vda1 and the role machineState", err, dev)
	}
	if machineStateWritable {
		t.Error("machineStateWritable is true after the mount failed, want false")
	}
}
