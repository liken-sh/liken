package main

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"golang.org/x/sys/unix"

	"github.com/liken-sh/liken/liken/disks"
	"github.com/liken-sh/liken/liken/machine"
)

// bootedSlot sets the liken.slot boot parameter to slot and
// bootedSlotStopEnv to recorded. An empty recorded leaves the variable
// unset, as when the initramfs mounted no slot.
func bootedSlot(t *testing.T, slot, recorded string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "cmdline")
	if err := os.WriteFile(path, []byte("liken.slot="+slot+"\n"), 0o644); err != nil {
		t.Fatalf("writing the cmdline file: %v", err)
	}
	old := cmdlinePath
	cmdlinePath = path
	t.Cleanup(func() { cmdlinePath = old })
	if recorded == "" {
		t.Setenv(bootedSlotStopEnv, "")
		os.Unsetenv(bootedSlotStopEnv)
		return
	}
	t.Setenv(bootedSlotStopEnv, recorded)
}

// cleanVolume returns the path of a new FAT32 image with the dirty bit
// clear.
func cleanVolume(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "volume.img")
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("creating the volume image: %v", err)
	}
	defer f.Close()
	if err := f.Truncate(64 << 20); err != nil {
		t.Fatalf("sizing the volume image: %v", err)
	}
	if err := disks.FormatFAT32(f, 64<<20, "TEST", 0x12345678); err != nil {
		t.Fatalf("formatting the volume image: %v", err)
	}
	return path
}

func TestFATDirtyAtBootUsesTheInitramfsReadingForTheBootedSlot(t *testing.T) {
	// The image is clean and the recorded value is unclean, so the result
	// shows which of the two was read.
	bootedSlot(t, "A", lastStopUnclean)
	unclean, err := fatDirtyAtBoot(machine.SystemARole, cleanVolume(t))
	if err != nil {
		t.Fatalf("reading the dirty bit of the booted slot: %v", err)
	}
	if !unclean {
		t.Error("fatDirtyAtBoot reported the device's state, want the unclean state that the initramfs recorded")
	}
}

func TestFATDirtyAtBootReportsACleanBootedSlot(t *testing.T) {
	bootedSlot(t, "A", lastStopClean)
	unclean, err := fatDirtyAtBoot(machine.SystemARole, cleanVolume(t))
	if err != nil {
		t.Fatalf("reading the dirty bit of the booted slot: %v", err)
	}
	if unclean {
		t.Error("fatDirtyAtBoot reported an unclean stop, want clean as the initramfs recorded")
	}
}

func TestFATDirtyAtBootReadsTheOtherSlotFromItsDevice(t *testing.T) {
	bootedSlot(t, "A", lastStopUnclean)
	unclean, err := fatDirtyAtBoot(machine.SystemBRole, cleanVolume(t))
	if err != nil {
		t.Fatalf("reading the dirty bit of the other slot: %v", err)
	}
	if unclean {
		t.Error("fatDirtyAtBoot reported an unclean stop for the other slot, want clean as its device says")
	}
}

func TestFATDirtyAtBootReadsTheBootHomeFromItsDevice(t *testing.T) {
	bootedSlot(t, "A", lastStopUnclean)
	unclean, err := fatDirtyAtBoot(machine.BootHomeRole, cleanVolume(t))
	if err != nil {
		t.Fatalf("reading the dirty bit of the boot home: %v", err)
	}
	if unclean {
		t.Error("fatDirtyAtBoot reported an unclean stop for the boot home, want clean as its device says")
	}
}

func TestFATDirtyAtBootReadsTheDeviceWhenNothingWasRecorded(t *testing.T) {
	bootedSlot(t, "A", "")
	unclean, err := fatDirtyAtBoot(machine.SystemARole, cleanVolume(t))
	if err != nil {
		t.Fatalf("reading the dirty bit with nothing recorded: %v", err)
	}
	if unclean {
		t.Error("fatDirtyAtBoot reported an unclean stop with nothing recorded, want clean as the device says")
	}
}

func TestRecordBootedSlotStopRecordsTheDevicesDirtyBit(t *testing.T) {
	t.Setenv(bootedSlotStopEnv, "")
	recordBootedSlotStop(cleanVolume(t))
	if got := os.Getenv(bootedSlotStopEnv); got != lastStopClean {
		t.Errorf("%s is %q, want %q", bootedSlotStopEnv, got, lastStopClean)
	}
}

func TestRecordBootedSlotStopRecordsNothingForAnUnreadableDevice(t *testing.T) {
	t.Setenv(bootedSlotStopEnv, "")
	os.Unsetenv(bootedSlotStopEnv)
	recordBootedSlotStop(filepath.Join(t.TempDir(), "absent"))
	if got, ok := os.LookupEnv(bootedSlotStopEnv); ok {
		t.Errorf("%s is %q, want it unset", bootedSlotStopEnv, got)
	}
}

// slotWith returns a directory laid out like a mounted slot: a
// release.yaml that lists each artifact with its digest, and a file for
// each artifact whose content is not nil.
func slotWith(t *testing.T, artifacts map[string][]byte) string {
	t.Helper()
	dir := t.TempDir()
	entries := ""
	for name, content := range artifacts {
		sum := sha256.Sum256(content)
		entries += fmt.Sprintf("  - name: %s\n    sha256: %s\n    size: %d\n",
			name, hex.EncodeToString(sum[:]), len(content))
		if content == nil {
			continue
		}
		if err := os.WriteFile(filepath.Join(dir, name), content, 0o644); err != nil {
			t.Fatalf("writing the artifact: %v", err)
		}
	}
	doc := "apiVersion: liken.sh/v1alpha1\nkind: Release\nmetadata:\n  name: 2026.07.26-001\nartifacts:\n" + entries
	if err := os.WriteFile(filepath.Join(dir, "release.yaml"), []byte(doc), 0o644); err != nil {
		t.Fatalf("writing release.yaml: %v", err)
	}
	return dir
}

func TestVerifySlotContentsAcceptsAnIntactSlot(t *testing.T) {
	slot := slotWith(t, map[string][]byte{"vmlinuz": []byte("kernel bytes")})
	if err := verifySlotContents(slot); err != nil {
		t.Errorf("verifySlotContents returned %v for an intact slot, want nil", err)
	}
}

func TestVerifySlotContentsRejectsAChangedArtifact(t *testing.T) {
	// The size matches and only the digest differs.
	slot := slotWith(t, map[string][]byte{"vmlinuz": []byte("kernel bytes")})
	if err := os.WriteFile(filepath.Join(slot, "vmlinuz"), []byte("kernel bytez"), 0o644); err != nil {
		t.Fatalf("changing the artifact: %v", err)
	}
	if err := verifySlotContents(slot); err == nil {
		t.Error("verifySlotContents returned nil for an artifact that does not match its digest, want an error")
	}
}

func TestVerifySlotContentsRejectsATruncatedArtifact(t *testing.T) {
	slot := slotWith(t, map[string][]byte{"liken.sqfs": []byte("a whole system image")})
	if err := os.WriteFile(filepath.Join(slot, "liken.sqfs"), []byte("a whole"), 0o644); err != nil {
		t.Fatalf("truncating the artifact: %v", err)
	}
	if err := verifySlotContents(slot); err == nil {
		t.Error("verifySlotContents returned nil for a truncated artifact, want an error")
	}
}

func TestVerifySlotContentsRejectsAMissingArtifact(t *testing.T) {
	slot := slotWith(t, map[string][]byte{"vmlinuz": []byte("kernel bytes")})
	if err := os.Remove(filepath.Join(slot, "vmlinuz")); err != nil {
		t.Fatalf("removing the artifact: %v", err)
	}
	if err := verifySlotContents(slot); err == nil {
		t.Error("verifySlotContents returned nil for a missing artifact, want an error")
	}
}

func TestVerifySlotContentsRejectsASlotWithNoReleaseDocument(t *testing.T) {
	if err := verifySlotContents(t.TempDir()); err == nil {
		t.Error("verifySlotContents returned nil for a slot with no release.yaml, want an error")
	}
}

func TestVerifySlotContentsRejectsAnUnreadableReleaseDocument(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "release.yaml"), []byte("{{ not yaml"), 0o644); err != nil {
		t.Fatalf("writing release.yaml: %v", err)
	}
	if err := verifySlotContents(dir); err == nil {
		t.Error("verifySlotContents returned nil for a release.yaml that does not parse, want an error")
	}
}

func TestReadFATStopReportsNothingForAVolumeThisBootCreated(t *testing.T) {
	bootedSlot(t, "A", "")
	dev := dirtyVolume(t)
	if readFATStop(machine.SystemBRole, dev, true) {
		t.Error("readFATStop returned true for a volume this boot created, want false")
	}
	if !isDirty(t, dev) {
		t.Error("readFATStop cleared the dirty bit of a volume this boot created, want it left set")
	}
}

func TestReadFATStopTreatsAnUnreadableBootSectorAsClean(t *testing.T) {
	bootedSlot(t, "A", "")
	if readFATStop(machine.SystemBRole, filepath.Join(t.TempDir(), "absent"), false) {
		t.Error("readFATStop returned true for a dirty bit it could not read, want false")
	}
}

func TestReadFATStopReportsACleanVolume(t *testing.T) {
	bootedSlot(t, "A", "")
	if readFATStop(machine.BootHomeRole, cleanVolume(t), false) {
		t.Error("readFATStop returned true for a volume with a clear dirty bit, want false")
	}
}

// fakeFATCheckMount points fatCheckMount at a temporary directory and
// returns it.
func fakeFATCheckMount(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "fat-check")
	old := fatCheckMount
	fatCheckMount = dir
	t.Cleanup(func() { fatCheckMount = old })
	return dir
}

// tamperedSlot is a slot whose kernel does not match the digest in its
// release document.
func tamperedSlot(t *testing.T) string {
	t.Helper()
	slot := slotWith(t, map[string][]byte{"vmlinuz": []byte("kernel bytes")})
	if err := os.WriteFile(filepath.Join(slot, "vmlinuz"), []byte("kernel bytez"), 0o644); err != nil {
		t.Fatalf("changing the artifact: %v", err)
	}
	return slot
}

// readFATStop reports every unclean stop, and clears the bit only for
// the boot home and for the other slot when its artifacts match.
func TestReadFATStopClearsTheDirtyBitOnlyAfterVerifying(t *testing.T) {
	cases := []struct {
		name      string
		role      machine.StorageRoleName
		slot      func(t *testing.T) string
		wantDirty bool
	}{
		{"the booted slot", machine.SystemARole, nil, true},
		{"the other slot when it matches its release", machine.SystemBRole, func(t *testing.T) string {
			return slotWith(t, map[string][]byte{"vmlinuz": []byte("kernel bytes")})
		}, false},
		{"the other slot with a changed artifact", machine.SystemBRole, tamperedSlot, true},
		{"the other slot with no release document", machine.SystemBRole, func(t *testing.T) string {
			return t.TempDir()
		}, true},
		{"the boot home", machine.BootHomeRole, nil, false},
		{"a role that cannot be verified", machine.MachineStateRole, nil, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			bootedSlot(t, "A", lastStopUnclean)
			mounts := fakeStorageMounts(t)
			fakeFATCheckMount(t)
			dev := dirtyVolume(t)
			if c.slot != nil {
				mounts.contents[dev] = c.slot(t)
			}

			if !readFATStop(c.role, dev, false) {
				t.Error("readFATStop returned false for an unclean stop, want true whether or not the dirty bit is cleared")
			}
			if got := isDirty(t, dev); got != c.wantDirty {
				t.Errorf("the dirty bit is set = %v after readFATStop, want %v", got, c.wantDirty)
			}
		})
	}
}

func TestCheckSlotArtifactsReadsTheSlotReadOnlyAndReleasesIt(t *testing.T) {
	mounts := fakeStorageMounts(t)
	check := fakeFATCheckMount(t)
	dev := dirtyVolume(t)
	mounts.contents[dev] = slotWith(t, map[string][]byte{"vmlinuz": []byte("kernel bytes")})

	if err := checkSlotArtifacts(dev); err != nil {
		t.Fatalf("checking the slot: %v", err)
	}
	want := []storageMount{{source: dev, target: check, fstype: "vfat", flags: unix.MS_RDONLY}}
	if !slices.Equal(mounts.mounts, want) {
		t.Errorf("mounts = %+v, want %+v", mounts.mounts, want)
	}
	if !slices.Equal(mounts.unmountedPaths(), []string{check}) {
		t.Errorf("unmounts = %+v, want only %s", mounts.unmounts, check)
	}
}

func TestCheckSlotArtifactsReportsASlotItCannotMount(t *testing.T) {
	mounts := fakeStorageMounts(t)
	fakeFATCheckMount(t)
	mounts.mountErr = unix.EINVAL

	err := checkSlotArtifacts("/dev/vdz9")
	if !errors.Is(err, unix.EINVAL) {
		t.Errorf("checkSlotArtifacts returned %v, want an error that wraps unix.EINVAL", err)
	}
}

// A failed unmount fails the check even when the contents match.
func TestCheckSlotArtifactsFailsWhenTheSlotWillNotRelease(t *testing.T) {
	mounts := fakeStorageMounts(t)
	check := fakeFATCheckMount(t)
	dev := dirtyVolume(t)
	mounts.contents[dev] = slotWith(t, map[string][]byte{"vmlinuz": []byte("kernel bytes")})
	mounts.unmountErrs[check] = unix.EBUSY

	err := checkSlotArtifacts(dev)
	if !errors.Is(err, unix.EBUSY) {
		t.Errorf("checkSlotArtifacts returned %v, want an error that wraps unix.EBUSY", err)
	}
	want := []storageUnmount{{target: check, flags: 0}, {target: check, flags: unix.MNT_DETACH}}
	if !slices.Equal(mounts.unmounts, want) {
		t.Errorf("unmounts = %+v, want %+v", mounts.unmounts, want)
	}
}

func TestBootedSlotRoleReadsTheSlotParameter(t *testing.T) {
	cases := []struct {
		cmdline string
		want    string
	}{
		{"liken.slot=A\n", string(machine.SystemARole)},
		{"liken.slot=B\n", string(machine.SystemBRole)},
		{"rdinit=/liken\n", ""},
	}
	for _, c := range cases {
		t.Run(c.cmdline, func(t *testing.T) {
			fakeCmdline(t, c.cmdline)
			if got := bootedSlotRole(); got != c.want {
				t.Errorf("bootedSlotRole() = %q, want %q", got, c.want)
			}
		})
	}
}
