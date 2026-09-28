package main

// Tests for the role-specific half of slot formatting. The disks
// package tests the FAT32 format itself.

import (
	"encoding/binary"
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

// blankSlotFile creates a sparse file the size of a slot, standing in
// for a partition the way TestFormatSlotLeavesARecognizableFilesystem
// does.
func blankSlotFile(t *testing.T, name string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := f.Truncate(512 << 20); err != nil {
		t.Fatal(err)
	}
	return path
}

// volumeID reads the FAT32 volume id FormatFAT32 writes at byte
// offset 67 of the boot sector.
func volumeID(t *testing.T, path string) uint32 {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var b [4]byte
	if _, err := f.ReadAt(b[:], 67); err != nil {
		t.Fatal(err)
	}
	return binary.LittleEndian.Uint32(b[:])
}

func TestFormatSlotGivesEachCallItsOwnVolumeID(t *testing.T) {
	// A claim formats bootHome and both system slots inside one
	// second. A volume id drawn from the clock would give all three
	// the same id, and the by-uuid collision rule would then suppress
	// every one of them, permanently, because no later walk ever sees
	// the id change.
	pathA := blankSlotFile(t, "a")
	pathB := blankSlotFile(t, "b")
	if err := formatSlot(pathA, 512<<20, machine.SystemARole); err != nil {
		t.Fatal(err)
	}
	if err := formatSlot(pathB, 512<<20, machine.SystemBRole); err != nil {
		t.Fatal(err)
	}
	if idA, idB := volumeID(t, pathA), volumeID(t, pathB); idA == idB {
		t.Errorf("both slots formatted with volume id %#08x", idA)
	}
}
