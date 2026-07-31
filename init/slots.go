package main

// The role-specific half of slot formatting. The FAT32 format itself
// lives in the disks package, shared with the install-media builder.
// This file holds init's policy: which label a slot carries, and the
// identity field a machine creates at format time.

import (
	"crypto/rand"
	"encoding/binary"
	"os"

	"github.com/liken-sh/liken/disks"
	"github.com/liken-sh/liken/machine"
)

// formatSlot formats one of the FAT32 roles' partitions. It labels
// the partition for the role it serves, so the volume shows its role
// in any directory listing. For these roles, the label also lets
// GRUB find the partition, because the label is what GRUB's search
// command uses. The volume ID is FAT's only identity field; FAT32
// has no UUIDs. formatSlot draws the volume ID from crypto/rand,
// the same source a claim already uses for each partition's GPT
// unique GUID, because a claim formats bootHome and both system
// slots inside one second, and volumes formatted in the same second
// must still carry ids that tell them apart.
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

// randomVolumeID draws a 32-bit FAT volume id from crypto/rand. A
// failure here means the kernel has no randomness source at all, the
// same fault that RandomGUID treats as fatal for a partition's GPT
// unique GUID, so formatSlot stops rather than mint an id two slots
// might end up sharing.
func randomVolumeID() uint32 {
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	return binary.LittleEndian.Uint32(b[:])
}
