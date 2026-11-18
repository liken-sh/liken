package main

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"

	"github.com/liken-sh/liken/liken/disks"
	"github.com/liken-sh/liken/liken/machine"
)

func TestFormatSlotLeavesARecognizableFilesystem(t *testing.T) {
	path := filepath.Join(t.TempDir(), "slot")
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("creating the slot file: %v", err)
	}
	// The file is sparse, and the format writes only the reserved
	// region and the FATs, so it uses little disk.
	if err := f.Truncate(512 << 20); err != nil {
		t.Fatalf("sizing the slot file: %v", err)
	}
	f.Close()
	if err := formatSlot(path, 512<<20, machine.SystemBRole); err != nil {
		t.Fatalf("formatting the slot: %v", err)
	}
	if !disks.HasFAT32(path) {
		t.Error("disks.HasFAT32 is false for the formatted slot, want true")
	}
}

// blankSlotFile creates a sparse 512 MiB file to use in place of a
// partition.
func blankSlotFile(t *testing.T, name string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("creating the slot file: %v", err)
	}
	defer f.Close()
	if err := f.Truncate(512 << 20); err != nil {
		t.Fatalf("sizing the slot file: %v", err)
	}
	return path
}

// volumeID reads the FAT32 volume ID at byte offset 67 of the boot
// sector.
func volumeID(t *testing.T, path string) uint32 {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("opening the slot file: %v", err)
	}
	defer f.Close()
	var b [4]byte
	if _, err := f.ReadAt(b[:], 67); err != nil {
		t.Fatalf("reading the volume ID: %v", err)
	}
	return binary.LittleEndian.Uint32(b[:])
}

func TestFormatSlotGivesEachCallItsOwnVolumeID(t *testing.T) {
	// When two partitions have the same volume ID, mergeDiskLinks
	// gives /dev/disk/by-uuid a link for neither, and the IDs never
	// change.
	pathA := blankSlotFile(t, "a")
	pathB := blankSlotFile(t, "b")
	if err := formatSlot(pathA, 512<<20, machine.SystemARole); err != nil {
		t.Fatalf("formatting slot a: %v", err)
	}
	if err := formatSlot(pathB, 512<<20, machine.SystemBRole); err != nil {
		t.Fatalf("formatting slot b: %v", err)
	}
	if idA, idB := volumeID(t, pathA), volumeID(t, pathB); idA == idB {
		t.Errorf("both slots have the volume ID %#08x, want different IDs", idA)
	}
}
