package main

// Tests for the role-specific half of slot formatting. The disks
// package tests the FAT32 format itself.

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/liken-sh/liken/disks"
	"github.com/liken-sh/liken/machine"
)

func TestFormatSlotLeavesARecognizableFilesystem(t *testing.T) {
	// This test runs formatSlot against a file that stands in for the
	// partition. It follows the same open-and-write path that the
	// claim takes, minus the disk.
	path := filepath.Join(t.TempDir(), "slot")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	// This file is sparse. FAT32 needs at least about 260Mi of
	// clusters, but the format only writes the reserved region and
	// the tables.
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
