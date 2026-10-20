package main

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"strings"
)

const (
	efiDir     = "/sys/firmware/efi"
	efivarsDir = "/sys/firmware/efi/efivars"
)

// mounter holds the three facts that mounting efivarfs depends on, as
// functions, so a test needs no machine with UEFI and no privilege.
type mounter struct {
	exists func(path string) bool
	mounts func() (string, error)
	mount  func(target string) error
}

// ensureEFIVars gives fwupd read access to the machine's UEFI
// variables, which the uefi_capsule plugin needs to see the firmware
// resource table. The mount is read-only because the scan never
// writes a variable.
//
// A machine that booted in BIOS mode has no /sys/firmware/efi. fwupd
// then finds no UEFI devices, which is a valid answer, so the scan
// continues without the mount.
func (m mounter) ensureEFIVars() error {
	if !m.exists(efiDir) {
		return nil
	}
	table, err := m.mounts()
	if err != nil {
		return err
	}
	if mountedAt(table, efivarsDir) {
		return nil
	}
	if err := m.mount(efivarsDir); err != nil {
		return fmt.Errorf("mounting efivarfs at %s: %w", efivarsDir, err)
	}
	return nil
}

// mountedAt reports whether /proc/mounts lists a mount at target.
func mountedAt(table, target string) bool {
	scanner := bufio.NewScanner(strings.NewReader(table))
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) > 1 && fields[1] == target {
			return true
		}
	}
	return false
}

func osMounter() mounter {
	return mounter{
		exists: func(path string) bool {
			_, err := os.Stat(path)
			return !errors.Is(err, os.ErrNotExist)
		},
		mounts: func() (string, error) {
			data, err := os.ReadFile("/proc/mounts")
			return string(data), err
		},
		mount: mountEFIVars,
	}
}
