package main

// Tests for the role-specific half of slot formatting; the FAT32
// format itself is tested in the disks package.

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/liken-sh/liken/disks"
	"github.com/liken-sh/liken/machine"
)

func TestFormatSlotLeavesARecognizableFilesystem(t *testing.T) {
	// formatSlot against a file standing in for the partition: the
	// same open-and-write path the claim takes, minus the disk.
	path := filepath.Join(t.TempDir(), "slot")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	// Sparse: FAT32 needs at least ~260Mi of clusters, but the format
	// only writes the reserved region and the tables.
	if err := f.Truncate(512 << 20); err != nil {
		t.Fatal(err)
	}
	f.Close()
	if err := formatSlot(path, 512<<20, machine.SystemBRole); err != nil {
		t.Fatal(err)
	}
	if !disks.HasFAT32(path) {
		t.Error("a formatted slot must recognize itself")
	}
}
