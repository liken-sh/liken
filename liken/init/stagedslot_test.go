package main

// Tests that the reboot path arms a trial only of a slot that holds
// exactly the release its staged record names.

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/liken-sh/liken/liken/machine"
)

// slotHoldingRelease writes a release's artifacts and then its
// document onto a temporary directory, the way the operator's fetcher
// leaves a slot, and mounts it as the slot given. It answers the
// slot's directory and the digest of the document, which is the
// digest a staged record of the release names.
func slotHoldingRelease(t *testing.T, slot, version string) (string, string) {
	t.Helper()
	mount := t.TempDir()
	document := "apiVersion: liken.sh/v1alpha1\nkind: Release\nmetadata:\n  name: " + version + "\nartifacts:\n"
	for _, name := range []string{"vmlinuz", "liken.cpio"} {
		contents := releaseArtifact(name, version)
		if err := os.WriteFile(filepath.Join(mount, name), contents, 0o644); err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(contents)
		document += fmt.Sprintf("  - name: %s\n    sha256: %s\n    size: %d\n", name, hex.EncodeToString(sum[:]), len(contents))
	}
	if err := os.WriteFile(filepath.Join(mount, "release.yaml"), []byte(document), 0o644); err != nil {
		t.Fatal(err)
	}

	role := map[string]machine.StorageRoleName{"A": machine.SystemARole, "B": machine.SystemBRole}[slot]
	old := roleMounts[role]
	rm := old
	rm.path = mount
	roleMounts[role] = rm
	t.Cleanup(func() { roleMounts[role] = old })

	sum := sha256.Sum256([]byte(document))
	return mount, "sha256:" + hex.EncodeToString(sum[:])
}

// The bytes of one artifact of a release.
func releaseArtifact(name, version string) []byte {
	return []byte("pretend " + name + " " + version)
}

// A trial arms over a slot that holds what its record names, and
// refuses every slot that the record does not describe.
func TestArmProvingBootChecksTheSlotAgainstTheRecord(t *testing.T) {
	cases := []struct {
		name  string
		slot  func(t *testing.T, mount string)
		armed bool
	}{
		{name: "the slot holds the staged release", slot: func(*testing.T, string) {}, armed: true},
		{name: "another release's document", slot: func(t *testing.T, mount string) {
			other, _ := slotHoldingRelease(t, "A", "0.3.0")
			if err := copyFile(filepath.Join(other, "release.yaml"), filepath.Join(mount, "release.yaml"), 0o644); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "the document of the release beside another release's kernel", slot: func(t *testing.T, mount string) {
			if err := os.WriteFile(filepath.Join(mount, "vmlinuz"), releaseArtifact("vmlinuz", "0.3.0"), 0o644); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "no document, in the middle of a download", slot: func(t *testing.T, mount string) {
			os.Remove(filepath.Join(mount, "release.yaml"))
		}},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			root := t.TempDir()
			provenRelease(t, root, "0.1.0", "A")
			stagedRelease(t, root, "0.2.0", "B")
			one.slot(t, slotMountPath("B"))
			dir := slotFirmware(t, 0x0002, 0x0003)

			armProvingBoot(efiActuator{dir: dir}, root, "A")

			attempted, _ := machine.SystemReleases(root).LoadAttempted()
			_, err := readEFIVar(dir, "BootNext")
			if armed := attempted != "" && err == nil; armed != one.armed {
				t.Errorf("armed = %v (marker %q, BootNext %v), want %v", armed, attempted, err, one.armed)
			}
		})
	}
}
