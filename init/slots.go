package main

// The role-specific half of slot formatting. The FAT32 format itself
// lives in the disks package, shared with the install-media builder.
// This file holds init's policy: which label a slot carries, and the
// identity field a machine creates at format time.

import (
	"os"
	"time"

	"github.com/liken-sh/liken/disks"
	"github.com/liken-sh/liken/machine"
)

// formatSlot formats one of the FAT32 roles' partitions. It labels
// the partition for the role it serves, so the volume shows its role
// in any directory listing. For these roles, the label also lets
// GRUB find the partition, because the label is what GRUB's search
// command uses. The volume ID is FAT's only identity field; FAT32
// has no UUIDs. formatSlot derives the volume ID from a timestamp,
// which is the traditional choice.
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
	return disks.FormatFAT32(f, sizeBytes, label, uint32(time.Now().Unix()))
}
