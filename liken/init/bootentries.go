package main

// The firmware boot entries that name this machine's system slots.
//
// A UEFI machine keeps its boot menu in NVRAM, as one variable per
// entry (efi.go reads and writes them, loadoption.go describes the
// record). liken writes one entry per system slot. Each entry pins a
// partition by its GPT unique GUID, loads \vmlinuz from it through the
// kernel's EFI stub, and carries the whole kernel command line in the
// entry's own optional data.
//
// That command line is the reason this file matters beyond the
// install. It carries the machine's identity, the slot letter, and the
// console. On a UEFI machine the firmware's entry is the only place
// that holds it, so a firmware whose variables reset to defaults
// leaves a complete installed disk that nothing can start. This file
// renders an entry from facts that live on the disk, so any boot can
// write the entry again. slotloader.go covers the other half: the
// machine that cannot boot at all because the entry is already gone.

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"os"
	"slices"
	"strings"

	"github.com/liken-sh/liken/machine"
)

// slotEntryDescription is the name liken writes on a slot's entry.
// Everything here finds an entry by this description, never by its
// number: the number is a handle the firmware owns, and the
// description is the identity liken owns.
func slotEntryDescription(slot string) string { return "liken slot " + slot }

// registerSlotEntries registers both slots with UEFI firmware. Slot
// B's entry points at files that do not exist yet, because its slot
// stays empty until the first upgrade. This is fine: a firmware that
// cannot load an entry's file moves on to the next entry in BootOrder.
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
	// The one-shot is pinned here for the same reason every later
	// boot pins it (assertBootNext): a firmware that drops the
	// BootOrder write across a reset would otherwise send the
	// machine's very first boot back to whatever it booted before
	// the install.
	assertBootNext(efiVarsDir, entryA, "A")
	fmt.Printf("liken: install: boot entries %s and %s written; BootOrder prefers slot A\n",
		bootEntryID(entryA), bootEntryID(entryB))
	return nil
}

// bootOrderWith puts the given entries at the head of BootOrder, in
// the order given, and keeps whatever the firmware already had after
// them, in its previous order. The firmware's own entries (setup
// menus, shells) stay reachable. They are just never preferred.
func bootOrderWith(dir string, leaders []uint16) []uint16 {
	order := slices.Clone(leaders)
	for _, n := range readBootOrder(dir) {
		if !slices.Contains(order, n) {
			order = append(order, n)
		}
	}
	return order
}

// writeSlotBootEntry writes one slot's firmware entry under the number
// that already carries its description, or under the lowest free
// number.
func writeSlotBootEntry(dir, slot string, part *slotPartition, machineName string) (uint16, error) {
	number, err := setBootEntry(dir, slotBootOption(slot, part, machineName))
	if err != nil {
		return 0, fmt.Errorf("writing the %s entry: %w", slotEntryDescription(slot), err)
	}
	return number, nil
}

// slotBootOption renders one slot's whole boot entry: where the kernel
// is, which partition holds it, and the command line to start it with.
//
// The partition is pinned by its GPT unique GUID, so the entry stays
// correct when the disk moves to another controller or another port.
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
		// The EFI stub reads its command line as UTF-16, the
		// firmware's native string type.
		optionalData: encodeUTF16Z(strings.Join(slotKernelArgs(machineName, slot), " ")),
	}
}

// slotInitrds names the archives that load beside the kernel, in the
// order they must load. Microcode leads, because the kernel scans the
// very start of its initrd for a microcode update before it
// decompresses anything. boot.cpio carries init and the early boot's
// modules. The deployment layer comes last and belongs to this
// deployment alone. The system itself (liken.sqfs) is deliberately
// absent: init mounts it straight from the slot, so the loader stages
// megabytes instead of the whole OS. Composition at load time is what
// lets an upgrade replace the generic half without touching the layer.
func slotInitrds() []string {
	return []string{"microcode.cpio", "boot.cpio", machine.LayerName}
}

// slotKernelArgs renders the command line a firmware boot entry
// carries. The EFI stub loads every initrd= file, in order, from the
// same filesystem it loaded the kernel from, so a firmware entry names
// them on the command line itself.
func slotKernelArgs(machineName, slot string) []string {
	var initrds []string
	for _, name := range slotInitrds() {
		initrds = append(initrds, `initrd=\`+name)
	}
	return slotArgs(machineName, slot, initrds)
}

// slotArgs assembles a command line from scratch, so every argument is
// deliberate and none is inherited by accident:
//
//	console=...      copied from this boot, so the installed system
//	                 keeps using whatever console its operator wired
//	rdinit=/liken    run our program as PID 1
//	liken.machine=   the machine's identity
//	liken.slot=      which slot this entry boots, so a running system
//	                 reports which half of the blue-green pair it is on
//	panic=10         reboot ten seconds after a kernel panic, instead
//	                 of hanging forever. Upgrades depend on this: a
//	                 panicking trial slot must reset, so the firmware's
//	                 consumed BootNext can fall back to the proven slot
//
// The middle argument is what differs between the two things that boot
// a slot. slotKernelArgs supplies the initrd= parameters that an EFI
// stub needs, and the loader entry in slotloader.go supplies none,
// because that loader stages the archives itself.
func slotArgs(machineName, slot string, afterRdinit []string) []string {
	args := append(consoleArgs(), "rdinit=/liken")
	args = append(args, afterRdinit...)
	return append(args, "liken.machine="+machineName, "liken.slot="+slot, "panic=10")
}

// consoleArgs copies every console= argument from the running command
// line. This machine was told where its console is, and everything
// liken writes should keep using the same console.
func consoleArgs() []string {
	var consoles []string
	for _, field := range cmdlineFields() {
		if strings.HasPrefix(field, "console=") {
			consoles = append(consoles, field)
		}
	}
	return consoles
}

// healBootEntries brings the firmware's boot menu into agreement with
// this machine's slots. It renders each slot's entry from the GPT facts
// on the disk, writes back only what drifted, and puts both slots at
// the head of BootOrder with the preferred slot first.
//
// This runs on every boot and again on the way down before every
// reboot, so a machine that boots at all repairs its own boot menu.
// That covers every way a firmware loses an entry: an update that
// resets its variables, a dead NVRAM battery, a setup menu's "load
// defaults", and a variable store that filled up. It cannot cover a
// machine with no entry left to boot, because such a machine never runs
// this. slotloader.go covers that one.
//
// Every comparison happens before any write. NVRAM accepts a limited
// number of writes, and the entries and the order cost a healthy
// machine none of them. BootNext is the one exception, at one write
// per boot cycle: the firmware consumes the variable at power-on, so
// the boot-time assertion always finds it gone and writes it back,
// and the shutdown assertion then finds that write standing and
// writes nothing. assertBootNext says why the variable is written at
// all. A healthy machine's console still stays silent, because
// assertBootNext prints only when a write fails or does not read
// back, so a line from here still means something drifted.
//
// The two halves are separate because a boot that cannot write an entry
// can still order the entries that exist. Without a name there is
// nothing correct to render, because an entry carries identity, and a
// boot from external media or a direct-kernel lab boot has no name to
// render. The ordering still runs, so such a boot leaves the machine's
// fallback exactly as strong as it found it.
func healBootEntries(dir, machineName, preferred string) {
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
		fmt.Fprintf(os.Stderr, "liken: system: no boot entry answers to either slot and this boot cannot write one; leaving BootOrder alone\n")
		return
	}
	assertBootOrder(dir, leaders, preferred, havePreferred)
	// BootNext joins the assertion because a firmware exists that
	// accepts a BootOrder write, reads it back correctly, and still
	// resets to its old order, while it honors BootNext through every
	// reset. The one-shot is the only preference such a firmware
	// keeps, so the proven slot is pinned there too. The pin is written only
	// when the preferred slot's own entry exists: leaders[0] is the
	// other slot's entry when it does not, and a one-shot aimed there
	// would boot the wrong half of the pair.
	if havePreferred {
		assertBootNext(dir, preferredEntry, preferred)
	}
}

// writeSlotEntries renders each slot's entry from the GPT facts on its
// disk and writes back only the ones that drifted. A slot whose
// partition cannot be read is skipped rather than guessed at, and the
// other slot still gets its entry: half a boot menu is worth more than
// none.
func writeSlotEntries(dir, machineName, preferred string) {
	parts := discoverPartitions()
	for _, slot := range slotOrder(preferred) {
		part, err := findSlotPartition(parts, slotStorageRole(slot))
		if err != nil {
			fmt.Fprintf(os.Stderr, "liken: system: %v; the %s entry cannot be checked\n",
				err, slotEntryDescription(slot))
			continue
		}
		want := slotBootOption(slot, part, machineName)
		if number, ok := findSlotEntry(dir, slot); ok {
			current, err := readEFIVar(dir, bootEntryID(number))
			if err == nil && bytes.Equal(current, encodeLoadOption(want)) {
				continue // the firmware already holds this exact entry
			}
		}
		number, err := setBootEntry(dir, want)
		if err != nil {
			fmt.Fprintf(os.Stderr, "liken: system: writing the %s entry: %v\n", slotEntryDescription(slot), err)
			continue
		}
		fmt.Printf("liken: system: %s disagreed with this machine's disk; wrote it as %s\n",
			slotEntryDescription(slot), bootEntryID(number))
	}
}

// assertBootOrder makes the standing preference lead with the given
// entries. It trusts the readback, not the write. Some firmware accepts
// a write and then fails to hold it, and every later report would be
// wrong if this code took the write result at face value.
// preferredLeads says whether leaders[0] is the preferred slot's own
// entry. It is false when no entry answers to that slot, and the
// console line must not then claim the preferred slot leads, because
// what actually leads is the other slot's entry.
func assertBootOrder(dir string, leaders []uint16, preferred string, preferredLeads bool) {
	want := bootOrderWith(dir, leaders)
	if slices.Equal(want, readBootOrder(dir)) {
		return // the firmware already agrees
	}
	if err := writeBootOrder(dir, want); err != nil {
		fmt.Fprintf(os.Stderr, "liken: system: repairing BootOrder: %v\n", err)
		return
	}
	if readback := readBootOrder(dir); !slices.Equal(readback, want) {
		fmt.Fprintln(os.Stderr, "liken: system: BootOrder was written but does not read back; the firmware is not holding it")
		return
	}
	if !preferredLeads {
		fmt.Printf("liken: system: BootOrder now leads with %s; no entry answers to slot %s, which the store calls proven\n",
			bootEntryID(leaders[0]), preferred)
		return
	}
	fmt.Printf("liken: system: BootOrder now leads with %s (slot %s is proven)\n",
		bootEntryID(leaders[0]), preferred)
}

// assertBootNext points the one-shot at the proven slot's entry, so a
// firmware that drops the BootOrder write across a reset still boots
// the proven slot: the one-shot is consumed and honored before the
// standing order is consulted at all. This costs one NVRAM write per
// boot, because the firmware deletes the variable at power-on, and
// the boot-time assertion always finds it gone. armTrial runs after
// this on the reboot path and overwrites the value, which is the
// contract: a staged trial takes the one shot for exactly one boot.
// The same overwrite clears a one-shot left over from a withdrawn
// trial, the way the GRUB dialect clears try_slot.
//
// The comparison decodes both sides to a number instead of comparing
// bytes, because a firmware may store the variable wider than the two
// bytes it needs. A byte comparison would then rewrite NVRAM on every
// call and warn about a firmware that is holding the value correctly.
// Like assertBootOrder, this trusts the readback and not the write.
func assertBootNext(dir string, entry uint16, preferred string) {
	if current, err := readEFIVar(dir, "BootNext"); err == nil && bootNextEntry(current) == int32(entry) {
		return // the firmware already aims at the proven slot
	}
	if err := writeEFIVar(dir, "BootNext", bootNextPayload(entry)); err != nil {
		fmt.Fprintf(os.Stderr, "liken: system: aiming BootNext at slot %s: %v\n", preferred, err)
		return
	}
	if readback, err := readEFIVar(dir, "BootNext"); err != nil || bootNextEntry(readback) != int32(entry) {
		fmt.Fprintln(os.Stderr, "liken: system: BootNext was written but does not read back; the firmware is not holding it")
	}
}

// bootNextEntry decodes the variable's entry number, and returns -1
// for a payload too short to carry one. The result is signed so that
// a missing or truncated variable compares unequal to every real
// entry number, all of which are non-negative.
func bootNextEntry(payload []byte) int32 {
	if len(payload) < 2 {
		return -1
	}
	return int32(binary.LittleEndian.Uint16(payload))
}

// bootNextPayload encodes one entry number the way BootNext carries it:
// a single 16-bit little-endian number, the same encoding BootOrder
// uses for each of its elements.
func bootNextPayload(entry uint16) []byte {
	return []byte{byte(entry), byte(entry >> 8)}
}

// slotOrder puts the preferred slot first and the other slot second.
// Both slots lead the boot order, so a firmware that cannot load the
// preferred slot's kernel tries the other half of the pair before it
// falls back to its own setup menu. A machine with no proven record yet
// takes the installer's order.
func slotOrder(preferred string) []string {
	if preferred == "B" {
		return []string{"B", "A"}
	}
	return []string{"A", "B"}
}

// slotStorageRole names the storage role that holds a slot.
func slotStorageRole(slot string) machine.StorageRoleName {
	if slot == "B" {
		return machine.SystemBRole
	}
	return machine.SystemARole
}

// findSlotEntry locates a slot's firmware entry the same way
// everything in liken finds things: by the name written on it.
//
// Two entries can answer to one slot, because some firmware clones a
// boot option it did not write. The testbed's mini PC keeps liken's
// slot B entry and a copy of its own beside it, with the same
// description and the same command line, differing only in the device
// path: liken names the partition by its GPT GUID alone, and the
// firmware's copy carries the full PCI and SATA path ahead of it, the
// same shape it writes for its own Windows and Ubuntu entries.
//
// So this returns the lowest matching number, and never an arbitrary
// one. setBootEntry writes the lowest match too, so the two agree, and
// every reader that compares a number against BootOrder agrees with
// them. An arbitrary choice would make fallbackLeads compare the wrong
// entry against the head of BootOrder, and armProvingBoot would then
// refuse to arm a trial on a machine whose fallback is in fact real.
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
