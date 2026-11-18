package main

import (
	"crypto/rand"
	"encoding/binary"
	"os"

	"github.com/liken-sh/liken/liken/disks"
	"github.com/liken-sh/liken/liken/machine"
)

// formatSlot writes a FAT32 filesystem on the partition of a system
// slot or bootHome. GRUB's search command finds a system slot by its
// label. FAT32 has no UUID, so the volume ID is the value that
// /dev/disk/by-uuid names the partition by. Init formats all three
// partitions within one second, so an ID taken from the clock would
// repeat, and the ID comes from crypto/rand.
func formatSlot(devPath string, sizeBytes uint64, role machine.StorageRoleName) error {
	f, err := os.OpenFile(devPath, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	defer f.Close()
	label := "LIKEN-SYS-A"
	switch role {
	case machine.SystemBRole:
		label = "LIKEN-SYS-B"
	case machine.BootHomeRole:
		label = "LIKEN-BOOT"
	}
	return disks.FormatFAT32(f, sizeBytes, label, randomVolumeID())
}

// randomVolumeID panics when crypto/rand fails, as disks.RandomGUID
// does, because two partitions with the same ID would hide each other
// from /dev/disk/by-uuid.
func randomVolumeID() uint32 {
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	return binary.LittleEndian.Uint32(b[:])
}
