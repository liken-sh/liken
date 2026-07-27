package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/liken-sh/liken/disks"
	"github.com/liken-sh/liken/machine"
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
