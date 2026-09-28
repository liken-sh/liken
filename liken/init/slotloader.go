package main

// The loader that a firmware with no boot preferences finds by itself.
//
// bootentries.go writes this machine's boot menu into NVRAM, and that
// menu is the only place a UEFI machine's kernel command line lives.
// Some firmware resets its variables to defaults: an update does it, a
// dead NVRAM battery does it, and the setup menu's own "load defaults"
// does it. That erases every entry and every command line at once, and
// leaves a complete installed disk that nothing can start.
//
// The UEFI specification answers this with one fallback. A firmware
// with nothing to boot searches each device for one fixed path,
// \EFI\BOOT\BOOTX64.EFI, and runs whatever it finds. This is how an
// installation stick boots a machine that has never seen it. So liken
// puts a loader at that path on the slot it has proven, with a Boot
// Loader Specification entry beside it that carries the same command
// line the firmware's own entry would have carried. A machine that
// loses its variables then boots the proven release, and that boot
// writes the entries again.
//
// The loader lives on the proven slot alone. A firmware at its defaults
// takes the first answer it finds, and the other slot holds either an
// older release or nothing at all. One answer means such a firmware
// cannot boot the wrong half of the pair.
//
// The program itself is not vendored twice. systemd-bootx64.efi is a
// release artifact, so every slot already carries the menu that an
// installation stick boots (the systemd-boot domain explains why liken
// ships one). Writing the fallback is a copy inside one slot.

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"
)

// bootMenuName is the boot menu program as a release names it. Both an
// installation stick and a slot's fallback loader are copies of this
// one file.
const bootMenuName = "systemd-bootx64.efi"

// defaultLoaderPath is the one path a firmware with no boot
// preferences looks for. The UEFI specification fixes this name per
// architecture, so it is not a choice liken makes.
func defaultLoaderPath(mount string) string {
	return filepath.Join(mount, "EFI", "BOOT", "BOOTX64.EFI")
}

// otherSlot names the other half of the blue-green pair.
func otherSlot(slot string) string {
	if slot == "B" {
		return "A"
	}
	return "B"
}

// writeSlotLoader puts the fallback loader on one slot: the loader
// program at the firmware's default path, the loader's one setting, and
// one entry that boots this slot. Each file is compared before it is
// written, so a slot that already agrees costs no writes. A slot is FAT
// on a real disk, and a rewrite on every boot buys nothing.
//
// A slot with no menu program cannot carry a loader. This returns an
// error in that case and writes nothing at all, because the caller
// removes the other slot's loader only after this one succeeds.
func writeSlotLoader(mount, slot, machineName string) error {
	program, err := os.ReadFile(filepath.Join(mount, bootMenuName))
	if err != nil {
		return fmt.Errorf("slot %s carries no %s, so it cannot answer the firmware's default boot path: %w",
			slot, bootMenuName, err)
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
		fmt.Printf("liken: system: slot %s now answers the firmware's default boot path\n", slot)
	}
	return nil
}

// flushSlot forces a slot's directory updates all the way out to the
// disk. Two calls, because they do different halves of the job, and the
// installer's power-off path needs the same pair for the same reason.
//
// Each file above became durable through a rename, and on FAT a rename
// is the only record of a file's name, size, and first cluster. The
// per-file fsync inside writeFileDurably covers the file's own bytes.
// It does not cover the directory entry that the rename wrote, and an
// fsync of the directory does not reach it either: a FAT directory's
// entries live in buffers attached to the block device, not in the
// directory's own pages. A machine cut off in that state holds a loader
// entry with the right name, no size, and no data, which is a fallback
// that a firmware will start and then fail to boot from.
//
// unix.Sync walks every mounted filesystem and writes its dirty state
// back to the drivers. syncDirectory then asks this slot's drive to
// empty its own write cache.
func flushSlot(mount string) error {
	unix.Sync()
	if err := syncDirectory(mount); err != nil {
		return fmt.Errorf("flushing slot %s to the disk: %w", mount, err)
	}
	return nil
}

// removeSlotLoader takes the fallback loader off a slot. Only the
// loader program goes. Its entry stays, because the program is what a
// firmware looks for, and an entry with no program beside it boots
// nothing.
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
	fmt.Printf("liken: system: %s no longer answers the firmware's default boot path\n", path)
}

// slotLoaderConfText is the loader's one setting. A boot that reaches
// this loader reached it because the firmware had no preference left to
// read, and no person is standing at the machine. So it must never
// wait.
func slotLoaderConfText() []byte {
	return []byte(`# liken wrote this loader on the slot it has proven, for one purpose:
# a firmware that lost its boot variables finds \EFI\BOOT\BOOTX64.EFI
# here and boots this slot. The entries directory holds one entry, and
# no person is here to pick it.
timeout 0
`)
}

// slotLoaderEntryText renders the entry that boots this slot, in
// systemd-boot's Boot Loader Specification form. The options line is
// the command line the firmware's own entry carries, minus the initrd=
// parameters: this loader stages the archives from its own initrd
// lines, so naming them twice would load each one twice.
//
// The entry names only the archives that this slot holds. systemd-boot
// refuses an entry whose initrd is missing, and a release older than an
// archive does not carry it. Such an entry would be worse than no
// fallback, because it fails after the firmware has already committed
// to it.
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
