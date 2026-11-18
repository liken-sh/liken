package main

// A UEFI machine has one firmware boot entry for each system slot.
//
// A UEFI machine stores its boot menu in NVRAM, one variable per
// entry. efi.go reads and writes the variables, and loadoption.go
// encodes an entry. liken writes one entry per system slot. Each entry
// loads \vmlinuz from the slot's partition through the kernel's EFI
// stub, with the whole kernel command line in the entry's optional
// data.
//
// An ordinary boot reads the command line from that entry and from
// nowhere else. When the firmware resets its variables to defaults,
// the disk is intact but nothing starts it. So this file renders each
// entry from facts on the disk, and any later boot can write a lost
// entry again. For a machine whose entries are already gone,
// slotloader.go puts a fallback loader on the proven slot.

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"os"
	"slices"
	"strings"

	"github.com/liken-sh/liken/liken/machine"
)

// slotEntryDescription is the description liken writes on a slot's
// entry. Code finds an entry by this description and never by its
// number, because the firmware can assign a different number.
func slotEntryDescription(slot string) string { return "liken slot " + slot }

// registerSlotEntries writes the boot entries of both slots at install
// time. Slot B's files do not exist until the first upgrade. That is
// harmless, because a firmware that cannot load an entry's file tries
// the next entry in BootOrder.
func registerSlotEntries(slotA, slotB *slotPartition, machineName string) error {
	entryA, err := writeSlotBootEntry(efiVarsDir, "A", slotA, machineName)
	if err != nil {
		return err
	}
	entryB, err := writeSlotBootEntry(efiVarsDir, "B", slotB, machineName)
	if err != nil {
		return err
	}
	if err := writeBootOrder(efiVarsDir, bootOrderWith(efiVarsDir, []uint16{entryA, entryB})); err != nil {
		return fmt.Errorf("writing BootOrder: %w", err)
	}
	// Without BootNext, a firmware that loses the BootOrder write at
	// reset would boot whatever it booted before the install. See
	// assertBootNext.
	assertBootNext(efiVarsDir, entryA, "A")
	fmt.Printf("liken: install: wrote boot entries %s and %s, and BootOrder starts with slot A\n",
		bootEntryID(entryA), bootEntryID(entryB))
	return nil
}

// bootOrderWith returns BootOrder with leaders first, then the entries
// that BootOrder already had, such as setup menus and shells, in their
// previous order.
func bootOrderWith(dir string, leaders []uint16) []uint16 {
	order := slices.Clone(leaders)
	for _, n := range readBootOrder(dir) {
		if !slices.Contains(order, n) {
			order = append(order, n)
		}
	}
	return order
}

func writeSlotBootEntry(dir, slot string, part *slotPartition, machineName string) (uint16, error) {
	number, err := setBootEntry(dir, slotBootOption(slot, part, machineName))
	if err != nil {
		return 0, fmt.Errorf("writing the %s entry: %w", slotEntryDescription(slot), err)
	}
	return number, nil
}

// slotBootOption renders one slot's boot entry. Its device path starts
// at the partition, which it names by its GPT unique GUID, with no PCI
// or SATA path before it. So the entry stays correct when the disk
// moves to another controller or port.
func slotBootOption(slot string, part *slotPartition, machineName string) loadOption {
	return loadOption{
		attributes:  loadOptionActive,
		description: slotEntryDescription(slot),
		hardDrive: &hardDriveNode{
			partitionNumber: part.number,
			firstLBA:        part.firstLBA,
			sectors:         part.lastLBA - part.firstLBA + 1,
			partitionGUID:   part.guid,
		},
		filePath: `\vmlinuz`,
		// The EFI stub reads its command line as UTF-16.
		optionalData: encodeUTF16Z(strings.Join(slotKernelArgs(machineName, slot), " ")),
	}
}

// slotInitrds returns the archives that make up the kernel's initrd,
// in order. Microcode is first because the kernel looks for a
// microcode update in an uncompressed archive at the start of the
// initrd. boot.cpio holds init and the early-boot modules, and the
// deployment layer holds the files written for this deployment. The
// kernel joins the archives at boot, so an upgrade replaces boot.cpio
// without touching the deployment layer. The system image, liken.sqfs,
// is not an initrd. init mounts it from the slot, so the firmware
// loads only these small archives into memory.
func slotInitrds() []string {
	return []string{"microcode.cpio", "boot.cpio", machine.LayerName}
}

// slotKernelArgs renders the command line of a firmware boot entry.
// The EFI stub loads each initrd= file, in order, from the filesystem
// that holds the kernel, so the entry lists them on the command line.
func slotKernelArgs(machineName, slot string) []string {
	var initrds []string
	for _, name := range slotInitrds() {
		initrds = append(initrds, `initrd=\`+name)
	}
	return slotArgs(machineName, slot, initrds)
}

// slotArgs builds a kernel command line from fixed parts, so no
// argument of the current boot is inherited except the console:
//
//	console=...      copied from this boot, so the installed system
//	                 uses the console that was configured for it
//	rdinit=/liken    run liken's init as PID 1
//	liken.machine=   the machine's name
//	liken.slot=      the slot this entry boots, so the running system
//	                 can report which slot it is on
//	panic=10         reboot 10 seconds after a kernel panic. The
//	                 firmware deleted BootNext when it started a trial,
//	                 so the reset boots the proven slot from BootOrder
//
// afterRdinit is inserted after rdinit=. slotKernelArgs passes the
// initrd= parameters that an EFI stub needs. The loader entry in
// slotloader.go passes none, because that loader stages the archives
// itself.
func slotArgs(machineName, slot string, afterRdinit []string) []string {
	args := append(consoleArgs(), "rdinit=/liken")
	args = append(args, afterRdinit...)
	return append(args, "liken.machine="+machineName, "liken.slot="+slot, "panic=10")
}

func consoleArgs() []string {
	var consoles []string
	for _, field := range cmdlineFields() {
		if strings.HasPrefix(field, "console=") {
			consoles = append(consoles, field)
		}
	}
	return consoles
}

// repairBootEntries makes the firmware's boot menu match this machine's
// slots. It renders each slot's entry from the GPT data on the disk,
// writes the entries that differ, and puts both slots first in
// BootOrder, the preferred slot first.
//
// init runs it at every boot and before every reboot, so a machine
// that still boots repairs a menu that a firmware update, a dead NVRAM
// battery, "load defaults" in the setup menu, or a full variable store
// damaged. A machine with no bootable entry never runs it, and
// slotloader.go covers that case.
//
// NVRAM accepts a limited number of writes, so each variable is read
// and compared before it is written. When the entries and BootOrder
// already match, nothing is written and nothing prints. assertBootNext
// explains the one BootNext write per boot.
//
// Without a machine name it writes no entries, because each entry's
// command line has the name, and a boot from external media or a
// direct-kernel boot has none. It still reorders the entries that
// exist.
//
// pinNext is false on a reboot that leaves BootNext as a Disruption
// set it (handover.go).
func repairBootEntries(dir, machineName, preferred string, pinNext bool) {
	if machineName != "" {
		writeSlotEntries(dir, machineName, preferred)
	}
	var leaders []uint16
	preferredEntry, havePreferred := uint16(0), false
	for _, slot := range slotOrder(preferred) {
		number, ok := findSlotEntry(dir, slot)
		if !ok {
			continue
		}
		leaders = append(leaders, number)
		if slot == preferred {
			preferredEntry, havePreferred = number, true
		}
	}
	if len(leaders) == 0 {
		fmt.Fprintf(os.Stderr, "liken: system: no boot entry exists for slot A or slot B and this boot cannot write one, so BootOrder is unchanged\n")
		return
	}
	assertBootOrder(dir, leaders, preferred, havePreferred)
	// Without the preferred slot's own entry, BootNext would boot the
	// other slot.
	if havePreferred && pinNext {
		assertBootNext(dir, preferredEntry, preferred)
	}
}

// writeSlotEntries writes each slot's entry that differs from the
// firmware's. A slot whose partition cannot be found is skipped, and
// the other slot's entry is still written.
func writeSlotEntries(dir, machineName, preferred string) {
	parts := discoverPartitions()
	for _, slot := range slotOrder(preferred) {
		part, err := findSlotPartition(parts, slotStorageRole(slot))
		if err != nil {
			fmt.Fprintf(os.Stderr, "liken: system: checking the %s entry: %v\n",
				err, slotEntryDescription(slot))
			continue
		}
		want := slotBootOption(slot, part, machineName)
		if number, ok := findSlotEntry(dir, slot); ok {
			current, err := readEFIVar(dir, bootEntryID(number))
			if err == nil && bytes.Equal(current, encodeLoadOption(want)) {
				continue // the firmware's entry already matches
			}
		}
		number, err := setBootEntry(dir, want)
		if err != nil {
			fmt.Fprintf(os.Stderr, "liken: system: writing the %s entry: %v\n", slotEntryDescription(slot), err)
			continue
		}
		fmt.Printf("liken: system: the %s entry differed from this machine's disk, so wrote it as %s\n",
			slotEntryDescription(slot), bootEntryID(number))
	}
}

// assertBootOrder makes BootOrder start with the given entries. It
// checks the result by reading BootOrder back, because some firmware
// accepts a write and does not keep it. preferredLeads is false when
// the preferred slot has no entry and leaders[0] is the other slot's,
// so that the log line does not claim the preferred slot leads.
func assertBootOrder(dir string, leaders []uint16, preferred string, preferredLeads bool) {
	want := bootOrderWith(dir, leaders)
	if slices.Equal(want, readBootOrder(dir)) {
		return // BootOrder already matches
	}
	if err := writeBootOrder(dir, want); err != nil {
		fmt.Fprintf(os.Stderr, "liken: system: writing BootOrder: %v\n", err)
		return
	}
	if readback := readBootOrder(dir); !slices.Equal(readback, want) {
		fmt.Fprintln(os.Stderr, "liken: system: BootOrder reads back different from what was written, so the firmware did not keep it")
		return
	}
	if !preferredLeads {
		fmt.Printf("liken: system: BootOrder now starts with %s, because no entry exists for slot %s, the proven slot\n",
			bootEntryID(leaders[0]), preferred)
		return
	}
	fmt.Printf("liken: system: BootOrder now starts with %s, the entry of slot %s, the proven slot\n",
		bootEntryID(leaders[0]), preferred)
}

// assertBootNext sets BootNext to the proven slot's entry. The
// firmware boots BootNext once, before it reads BootOrder, and deletes
// it when it does. Some firmware accepts a BootOrder write and reads it
// back correctly, but returns to its old order at reset and still
// honors BootNext. On that firmware, BootNext is what boots the proven
// slot. Because the firmware deletes it at each boot, this costs one
// NVRAM write per boot: the call at boot writes it, and the call
// before a reboot finds it already set.
//
// On the reboot path, armTrial runs after this and overwrites
// BootNext when a trial is staged. When no trial is staged, this call
// replaces a BootNext that a withdrawn trial left, as the GRUB
// actuator clears try_slot.
//
// It compares entry numbers and not bytes, because a firmware can
// store the variable wider than two bytes.
func assertBootNext(dir string, entry uint16, preferred string) {
	if current, err := readEFIVar(dir, "BootNext"); err == nil && bootNextEntry(current) == int32(entry) {
		return // BootNext already points at the proven slot
	}
	if err := writeEFIVar(dir, "BootNext", bootNextPayload(entry)); err != nil {
		fmt.Fprintf(os.Stderr, "liken: system: writing BootNext for slot %s: %v\n", preferred, err)
		return
	}
	if readback, err := readEFIVar(dir, "BootNext"); err != nil || bootNextEntry(readback) != int32(entry) {
		fmt.Fprintln(os.Stderr, "liken: system: BootNext reads back different from what was written, so the firmware did not keep it")
	}
}

// bootNextEntry returns the entry number in a BootNext payload, or -1
// when the payload is too short. The result is signed so that -1 never
// equals a real entry number.
func bootNextEntry(payload []byte) int32 {
	if len(payload) < 2 {
		return -1
	}
	return int32(binary.LittleEndian.Uint16(payload))
}

// bootNextPayload encodes an entry number as BootNext stores it, a
// 16-bit little-endian number, the same as each element of BootOrder.
func bootNextPayload(entry uint16) []byte {
	return []byte{byte(entry), byte(entry >> 8)}
}

// slotOrder returns the preferred slot first and the other slot
// second. Both slots go at the head of BootOrder, so a firmware that
// cannot load the preferred slot's kernel tries the other slot before
// its own setup menu. A machine with no record of a proven slot uses
// the installer's order, A then B.
func slotOrder(preferred string) []string {
	if preferred == "B" {
		return []string{"B", "A"}
	}
	return []string{"A", "B"}
}

func slotStorageRole(slot string) machine.StorageRoleName {
	if slot == "B" {
		return machine.SystemBRole
	}
	return machine.SystemARole
}

// findSlotEntry returns the number of a slot's firmware entry, found
// by its description, and false when there is none.
//
// Some firmware adds a copy of a boot entry it did not write, with the
// same description and command line and a full PCI and SATA path
// before the partition. So two entries can have one slot's
// description. findSlotEntry returns the lowest matching number, and
// setBootEntry overwrites the lowest match, so every caller reads the
// entry that liken writes. Otherwise fallbackLeads could compare the
// copy with the head of BootOrder, and armProvingBoot would refuse a
// trial on a machine whose fallback works.
func findSlotEntry(efiDir, slot string) (uint16, bool) {
	found, ok := uint16(0), false
	for number, option := range listBootEntries(efiDir) {
		if option.description != slotEntryDescription(slot) {
			continue
		}
		if !ok || number < found {
			found, ok = number, true
		}
	}
	return found, ok
}
