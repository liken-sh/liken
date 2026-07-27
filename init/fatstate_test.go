package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

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
