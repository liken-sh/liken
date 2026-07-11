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

// formatSlot formats a system slot's partition, labeled for the role
// it serves so the slot identifies itself in any directory listing.
// The volume ID (FAT's only identity field; there are no UUIDs) is
// derived from a timestamp, the traditional choice.
func formatSlot(devPath string, sizeBytes uint64, role machine.StorageRoleName) error {
	f, err := os.OpenFile(devPath, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	defer f.Close()
	label := "LIKEN-SYS-A"
	if role == machine.SystemBRole {
		label = "LIKEN-SYS-B"
	}
	return disks.FormatFAT32(f, sizeBytes, label, uint32(time.Now().Unix()))
}
