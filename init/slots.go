package main

// The role-specific half of slot formatting. The FAT32 format itself
// lives in the disks package (shared with the install-media builder);
// what stays here is init's policy: which label a slot carries, and
// the identity field a machine mints at format time.

import (
	"os"
	"time"

	"github.com/liken-sh/liken/disks"
	"github.com/liken-sh/liken/machine"
)

// formatSlot formats one of the FAT32 roles' partitions, labeled for
// the role it serves so the volume identifies itself in any directory
// listing — and, for these roles, so GRUB can find it: the labels are
// what its search command keys on. The volume ID (FAT's only identity
// field; there are no UUIDs) is derived from a timestamp, the
// traditional choice.
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
