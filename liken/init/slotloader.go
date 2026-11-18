package main

// A fallback boot loader on the proven slot, for firmware that has
// lost its boot entries.
//
// bootentries.go writes this machine's boot entries into NVRAM, and
// the kernel command line of an installed machine is only in those
// entries. A firmware update, a dead NVRAM battery, or "load defaults"
// in the setup menu erases them, and the firmware then has nothing to
// start. When nothing else is bootable, UEFI firmware runs
// \EFI\BOOT\BOOTX64.EFI from a device, as it does for an installation
// stick. So liken copies systemd-boot, which every release has, to
// that path on the proven slot, with a Boot Loader Specification entry
// that has the same command line as the firmware's entry. The machine
// then boots the proven release, and that boot writes the firmware
// entries again.
//
// Only the proven slot gets the loader, because firmware at its
// defaults runs the first loader it finds, and the other slot has an
// older release or nothing.

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"
)

// bootMenuName is the file name of systemd-boot in a release.
const bootMenuName = "systemd-bootx64.efi"

// defaultLoaderPath returns the path where UEFI firmware with no boot
// entries looks for an x86-64 loader.
func defaultLoaderPath(mount string) string {
	return filepath.Join(mount, "EFI", "BOOT", "BOOTX64.EFI")
}

func otherSlot(slot string) string {
	if slot == "B" {
		return "A"
	}
	return "B"
}

// writeSlotLoader writes the fallback loader to a slot: the program
// at the firmware's default path, loader.conf, and one entry that
// boots this slot. It skips a file whose contents already match, so
// that a boot does not rewrite the FAT filesystem for nothing. It
// compares contents, not existence, because a power cut can leave a
// file with its name and no data.
//
// It writes nothing when the slot has no systemd-boot, and the caller
// then keeps the other slot's loader.
func writeSlotLoader(mount, slot, machineName string) error {
	program, err := os.ReadFile(filepath.Join(mount, bootMenuName))
	if err != nil {
		return fmt.Errorf("reading %s from slot %s: %w",
			bootMenuName, slot, err)
	}

	files := map[string][]byte{
		defaultLoaderPath(mount):                                         program,
		filepath.Join(mount, "loader", "loader.conf"):                    slotLoaderConfText(),
		filepath.Join(mount, "loader", "entries", "liken-"+slot+".conf"): slotLoaderEntryText(mount, slot, machineName),
	}
	wrote := false
	for path, want := range files {
		if current, err := os.ReadFile(path); err == nil && bytes.Equal(current, want) {
			continue
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		if err := writeFileDurably(path, want); err != nil {
			return fmt.Errorf("writing %s: %w", path, err)
		}
		wrote = true
	}
	if wrote {
		if err := flushSlot(mount); err != nil {
			return err
		}
		fmt.Printf("liken: system: wrote the fallback loader to slot %s\n", slot)
	}
	return nil
}

// flushSlot writes a slot's directory entries to the disk.
// writeFileDurably syncs each file's contents, but on FAT the
// directory entry that its rename writes, with the file's size and
// first cluster, stays in the block device's buffers. If power is lost
// then, the file has its name and no data, and the firmware starts a
// loader that cannot boot. unix.Sync writes those buffers to the
// drive, and syncDirectory then flushes the drive's write cache.
func flushSlot(mount string) error {
	unix.Sync()
	if err := syncDirectory(mount); err != nil {
		return fmt.Errorf("flushing the directory entries of %s to the disk: %w", mount, err)
	}
	return nil
}

// removeSlotLoader deletes the loader program from a slot and leaves
// its entry, which nothing reads without the program.
func removeSlotLoader(mount string) {
	path := defaultLoaderPath(mount)
	if _, err := os.Stat(path); err != nil {
		return
	}
	if err := os.Remove(path); err != nil {
		fmt.Fprintf(os.Stderr, "liken: system: removing the fallback loader at %s: %v\n", path, err)
		return
	}
	if err := flushSlot(mount); err != nil {
		fmt.Fprintf(os.Stderr, "liken: system: %v\n", err)
	}
	fmt.Printf("liken: system: removed the fallback loader at %s\n", path)
}

// slotLoaderConfText returns loader.conf with no menu timeout, because
// the loader runs only when the firmware has lost its entries, and
// nobody is at the console to choose.
func slotLoaderConfText() []byte {
	return []byte(`# liken wrote this loader on the slot it has proven, for one purpose:
# a firmware that lost its boot variables finds \EFI\BOOT\BOOTX64.EFI
# here and boots this slot. The entries directory holds one entry, and
# no person is here to pick it.
timeout 0
`)
}

// slotLoaderEntryText returns the Boot Loader Specification entry that
// boots this slot. The options line is the firmware entry's command
// line without its initrd= parameters, because the initrd lines
// already load each archive.
//
// The entry lists only the archives that the slot has, because an
// older release can lack one and systemd-boot refuses an entry whose
// initrd file is missing.
func slotLoaderEntryText(mount, slot, machineName string) []byte {
	var b strings.Builder
	fmt.Fprintf(&b, "title    %s\n", slotEntryDescription(slot))
	fmt.Fprint(&b, "linux    /vmlinuz\n")
	for _, name := range slotInitrds() {
		if _, err := os.Stat(filepath.Join(mount, name)); err != nil {
			continue
		}
		fmt.Fprintf(&b, "initrd   /%s\n", name)
	}
	fmt.Fprintf(&b, "options  %s\n", strings.Join(slotArgs(machineName, slot, nil), " "))
	return []byte(b.String())
}
