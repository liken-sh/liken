package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"golang.org/x/sys/unix"

	"github.com/liken-sh/liken/liken/disks"
	"github.com/liken-sh/liken/liken/machine"
)

// bootedSlot points the boot parameters at a slot and puts the mark
// that the initramfs recorded for it into the environment. An empty
// mark stands for a boot whose initramfs recorded nothing.
func bootedSlot(t *testing.T, slot, mark string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "cmdline")
	if err := os.WriteFile(path, []byte("liken.slot="+slot+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	old := cmdlinePath
	cmdlinePath = path
	t.Cleanup(func() { cmdlinePath = old })
	if mark == "" {
		t.Setenv(bootedSlotStopEnv, "")
		os.Unsetenv(bootedSlotStopEnv)
		return
	}
	t.Setenv(bootedSlotStopEnv, mark)
}

// cleanVolume writes a freshly formatted FAT32 volume, which carries no
// mark, and returns its path.
func cleanVolume(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "volume.img")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := f.Truncate(64 << 20); err != nil {
		t.Fatal(err)
	}
	if err := disks.FormatFAT32(f, 64<<20, "TEST", 0x12345678); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestFATStopMarkTakesTheBootedSlotsAnswerFromTheInitramfs(t *testing.T) {
	// The volume itself is clean, because this test runs against a file
	// that no kernel has mounted. On a machine it would read as marked,
	// since the initramfs mounted it for writing to reach the system
	// image. Either way the recorded answer is the one that counts.
	bootedSlot(t, "A", stopMarkUnclean)
	unclean, err := fatStopMark(machine.SystemARole, cleanVolume(t))
	if err != nil {
		t.Fatal(err)
	}
	if !unclean {
		t.Error("the booted slot must report what the initramfs read, not what the device says now")
	}
}

func TestFATStopMarkReportsACleanBootedSlot(t *testing.T) {
	bootedSlot(t, "A", stopMarkClean)
	unclean, err := fatStopMark(machine.SystemARole, cleanVolume(t))
	if err != nil {
		t.Fatal(err)
	}
	if unclean {
		t.Error("a slot the initramfs found released must not report an unclean stop")
	}
}

func TestFATStopMarkReadsTheIdleSlotFromItsDevice(t *testing.T) {
	// The other slot is untouched this boot, so its device still holds
	// the answer, and the recorded one belongs to a different volume.
	bootedSlot(t, "A", stopMarkUnclean)
	unclean, err := fatStopMark(machine.SystemBRole, cleanVolume(t))
	if err != nil {
		t.Fatal(err)
	}
	if unclean {
		t.Error("the idle slot must be read from its own device")
	}
}

func TestFATStopMarkReadsTheBootHomeFromItsDevice(t *testing.T) {
	bootedSlot(t, "A", stopMarkUnclean)
	unclean, err := fatStopMark(machine.BootHomeRole, cleanVolume(t))
	if err != nil {
		t.Fatal(err)
	}
	if unclean {
		t.Error("the boot home must be read from its own device")
	}
}

func TestFATStopMarkFallsBackToTheDeviceWhenNothingWasRecorded(t *testing.T) {
	// A boot that takes the system image from RAM mounts no slot in the
	// initramfs, so it records nothing, and the device is still right.
	bootedSlot(t, "A", "")
	unclean, err := fatStopMark(machine.SystemARole, cleanVolume(t))
	if err != nil {
		t.Fatal(err)
	}
	if unclean {
		t.Error("with nothing recorded, the device's answer must stand")
	}
}

func TestRecordBootedSlotStopKeepsWhatTheDeviceSays(t *testing.T) {
	t.Setenv(bootedSlotStopEnv, "")
	recordBootedSlotStop(cleanVolume(t))
	if got := os.Getenv(bootedSlotStopEnv); got != stopMarkClean {
		t.Errorf("got %q, want %q", got, stopMarkClean)
	}
}

func TestRecordBootedSlotStopKeepsNothingItCannotRead(t *testing.T) {
	// Recording a guess here would put a made-up fact into status. An
	// unreadable device is left to the read that comes later.
	t.Setenv(bootedSlotStopEnv, "")
	os.Unsetenv(bootedSlotStopEnv)
	recordBootedSlotStop(filepath.Join(t.TempDir(), "absent"))
	if got, ok := os.LookupEnv(bootedSlotStopEnv); ok {
		t.Errorf("got %q, want nothing recorded", got)
	}
}

// slotWith builds a directory that looks like a mounted slot: a
// release document, and one artifact for each name given. An artifact
// whose content is passed as nil is left off the slot entirely.
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
			t.Fatal(err)
		}
	}
	doc := "apiVersion: liken.sh/v1alpha1\nkind: Release\nmetadata:\n  name: 2026.07.26-001\nartifacts:\n" + entries
	if err := os.WriteFile(filepath.Join(dir, "release.yaml"), []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestVerifySlotContentsAcceptsAnIntactSlot(t *testing.T) {
	slot := slotWith(t, map[string][]byte{"vmlinuz": []byte("kernel bytes")})
	if err := verifySlotContents(slot); err != nil {
		t.Errorf("an intact slot must verify, got %v", err)
	}
}

func TestVerifySlotContentsRejectsAChangedArtifact(t *testing.T) {
	// This is the case the mark exists to warn about: the file is
	// there and the right size, but the bytes are not the ones the
	// release names. Clearing the mark here would be a lie.
	slot := slotWith(t, map[string][]byte{"vmlinuz": []byte("kernel bytes")})
	if err := os.WriteFile(filepath.Join(slot, "vmlinuz"), []byte("kernel bytez"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := verifySlotContents(slot); err == nil {
		t.Error("a slot whose artifact does not match its digest must not verify")
	}
}

func TestVerifySlotContentsRejectsATruncatedArtifact(t *testing.T) {
	slot := slotWith(t, map[string][]byte{"liken.sqfs": []byte("a whole system image")})
	if err := os.WriteFile(filepath.Join(slot, "liken.sqfs"), []byte("a whole"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := verifySlotContents(slot); err == nil {
		t.Error("a truncated artifact must not verify")
	}
}

func TestVerifySlotContentsRejectsAMissingArtifact(t *testing.T) {
	slot := slotWith(t, map[string][]byte{"vmlinuz": []byte("kernel bytes")})
	if err := os.Remove(filepath.Join(slot, "vmlinuz")); err != nil {
		t.Fatal(err)
	}
	if err := verifySlotContents(slot); err == nil {
		t.Error("a slot missing an artifact must not verify")
	}
}

func TestVerifySlotContentsRejectsASlotWithNoReleaseDocument(t *testing.T) {
	// A slot liken has never written cannot be vouched for, so it
	// keeps its mark.
	if err := verifySlotContents(t.TempDir()); err == nil {
		t.Error("a slot with no release document must not verify")
	}
}

func TestVerifySlotContentsRejectsAnUnreadableReleaseDocument(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "release.yaml"), []byte("{{ not yaml"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := verifySlotContents(dir); err == nil {
		t.Error("a slot whose release document does not parse must not verify")
	}
}

// A slot this boot claimed and formatted has no earlier stop to report.
// Its new boot sector carries no mark, and a read of it would only
// waste a device open, so the answer is no without asking the device.
func TestReadFATStopReportsNothingForAVolumeThisBootCreated(t *testing.T) {
	bootedSlot(t, "A", "")
	dev := markedVolume(t)
	if readFATStop(machine.SystemBRole, dev, true) {
		t.Error("a volume this boot created must not report an unclean stop")
	}
	if !isMarked(t, dev) {
		t.Error("readFATStop must not touch a volume it was told is new")
	}
}

// A boot must continue when one boot sector cannot be read, because
// storage reconciliation fails for a real reason a moment later if the
// volume is truly gone. So an unreadable mark counts as no mark.
func TestReadFATStopTreatsAnUnreadableMarkAsClean(t *testing.T) {
	bootedSlot(t, "A", "")
	if readFATStop(machine.SystemBRole, filepath.Join(t.TempDir(), "absent"), false) {
		t.Error("a mark that cannot be read must not be reported as unclean")
	}
}

func TestReadFATStopReportsACleanVolume(t *testing.T) {
	bootedSlot(t, "A", "")
	if readFATStop(machine.BootHomeRole, cleanVolume(t), false) {
		t.Error("a released volume must not report an unclean stop")
	}
}

// fakeFATCheckMount points the slot check at a temporary directory, so
// the check can make its mount point without root.
func fakeFATCheckMount(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "fat-check")
	old := fatCheckMount
	fatCheckMount = dir
	t.Cleanup(func() { fatCheckMount = old })
	return dir
}

// tamperedSlot is a slot whose kernel no longer matches the digest its
// release document names.
func tamperedSlot(t *testing.T) string {
	t.Helper()
	slot := slotWith(t, map[string][]byte{"vmlinuz": []byte("kernel bytes")})
	if err := os.WriteFile(filepath.Join(slot, "vmlinuz"), []byte("kernel bytez"), 0o644); err != nil {
		t.Fatal(err)
	}
	return slot
}

// The status always reports the unclean stop, because it describes the
// stop that already happened. The mark comes off only where liken can
// vouch for the volume: an idle slot whose every artifact matches its
// release document, and the boot home, which this boot rewrites. The
// booted slot is in use and keeps its mark, and so does a slot whose
// contents do not check out, so the warning still means something.
func TestReadFATStopClearsTheMarkOnlyWhereLikenCanVouch(t *testing.T) {
	cases := []struct {
		name       string
		role       machine.StorageRoleName
		slot       func(t *testing.T) string
		wantMarked bool
	}{
		{"the booted slot", machine.SystemARole, nil, true},
		{"an idle slot that matches its release", machine.SystemBRole, func(t *testing.T) string {
			return slotWith(t, map[string][]byte{"vmlinuz": []byte("kernel bytes")})
		}, false},
		{"an idle slot with a changed artifact", machine.SystemBRole, tamperedSlot, true},
		{"an idle slot with no release document", machine.SystemBRole, func(t *testing.T) string {
			return t.TempDir()
		}, true},
		{"the boot home", machine.BootHomeRole, nil, false},
		{"a role with nothing to vouch for it", machine.MachineStateRole, nil, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			bootedSlot(t, "A", stopMarkUnclean)
			mounts := fakeStorageMounts(t)
			fakeFATCheckMount(t)
			dev := markedVolume(t)
			if c.slot != nil {
				mounts.contents[dev] = c.slot(t)
			}

			if !readFATStop(c.role, dev, false) {
				t.Error("the unclean stop must reach status whether or not the mark is cleared")
			}
			if got := isMarked(t, dev); got != c.wantMarked {
				t.Errorf("marked after the boot read it = %v, want %v", got, c.wantMarked)
			}
		})
	}
}

// The check must not set the mark it is about to clear, so it mounts
// the slot read-only, and it must release the slot before the caller
// writes the boot sector underneath it.
func TestCheckSlotArtifactsReadsTheSlotReadOnlyAndReleasesIt(t *testing.T) {
	mounts := fakeStorageMounts(t)
	check := fakeFATCheckMount(t)
	dev := markedVolume(t)
	mounts.contents[dev] = slotWith(t, map[string][]byte{"vmlinuz": []byte("kernel bytes")})

	if err := checkSlotArtifacts(dev); err != nil {
		t.Fatal(err)
	}
	want := []storageMount{{source: dev, target: check, fstype: "vfat", flags: unix.MS_RDONLY}}
	if !slices.Equal(mounts.mounts, want) {
		t.Errorf("mounts = %+v, want %+v", mounts.mounts, want)
	}
	if !slices.Equal(mounts.unmountedPaths(), []string{check}) {
		t.Errorf("the check must release the slot: %+v", mounts.unmounts)
	}
}

func TestCheckSlotArtifactsReportsASlotItCannotMount(t *testing.T) {
	mounts := fakeStorageMounts(t)
	fakeFATCheckMount(t)
	mounts.mountErr = unix.EINVAL

	err := checkSlotArtifacts("/dev/vdz9")
	if err == nil || !strings.Contains(err.Error(), "/dev/vdz9") {
		t.Errorf("the error must name the slot it could not mount: %v", err)
	}
}

// A slot that is still mounted cannot have its boot sector written
// safely, so a failed release fails the check even when the contents
// matched. The check then detaches the mount lazily so it does not
// stay in the way.
func TestCheckSlotArtifactsFailsWhenTheSlotWillNotRelease(t *testing.T) {
	mounts := fakeStorageMounts(t)
	check := fakeFATCheckMount(t)
	dev := markedVolume(t)
	mounts.contents[dev] = slotWith(t, map[string][]byte{"vmlinuz": []byte("kernel bytes")})
	mounts.unmountErrs[check] = unix.EBUSY

	err := checkSlotArtifacts(dev)
	if err == nil || !strings.Contains(err.Error(), "releasing") {
		t.Errorf("a slot that will not release must fail the check: %v", err)
	}
	want := []storageUnmount{{target: check, flags: 0}, {target: check, flags: unix.MNT_DETACH}}
	if !slices.Equal(mounts.unmounts, want) {
		t.Errorf("unmounts = %+v, want %+v", mounts.unmounts, want)
	}
}

// The booted slot is the one volume that is in use, so the boot must
// tell it apart from the idle slot by the kernel command line.
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
				t.Errorf("got %q, want %q", got, c.want)
			}
		})
	}
}
