package main

// The UEFI implementation of the boot actuator (actuator.go describes
// the interface and why it exists).
//
// UEFI firmware keeps its boot preferences as variables (efi.go), and
// the specification already provides exactly the two mechanisms
// blue-green upgrades need. BootNext is a one-shot: the firmware
// consumes it as it boots, so a slot on trial gets exactly one
// chance, and any reset after that falls back to BootOrder.
// BootOrder is the standing preference list, and keeping the proven
// slot at its head is what makes the fallback real.

import (
	"fmt"
	"os"
)

type efiActuator struct {
	// dir is the efivarfs mount, or a test's stand-in for one.
	dir string
	// machineName is rendered into every boot entry this dialect
	// writes. An entry carries identity, so an anonymous entry would be
	// wrong on every later boot. This value comes from the kernel
	// command line, where an earlier boot's entry put it.
	machineName string
}

func (a efiActuator) canArmTrial(slot string) error {
	if _, ok := findSlotEntry(a.dir, slot); !ok {
		return fmt.Errorf("no boot entry answers to %q", "liken slot "+slot)
	}
	return nil
}

func (a efiActuator) armTrial(slot string) (string, error) {
	entry, ok := findSlotEntry(a.dir, slot)
	if !ok {
		return "", fmt.Errorf("no boot entry answers to %q", "liken slot "+slot)
	}
	if err := writeEFIVar(a.dir, "BootNext", bootNextPayload(entry)); err != nil {
		return "", fmt.Errorf("arming BootNext: %w", err)
	}
	return "BootNext armed at " + bootEntryID(entry), nil
}

func (a efiActuator) fallbackLeads(slot string) bool {
	leader, ok := findSlotEntry(a.dir, slot)
	if !ok {
		return false
	}
	order := readBootOrder(a.dir)
	return len(order) > 0 && order[0] == leader
}

// assertProven brings this machine's whole boot path into agreement
// with the store: NVRAM holds an entry for each slot with the proven
// slot at the head of BootOrder (bootentries.go), and the proven slot
// alone answers the firmware's default boot path (slotloader.go).
//
// Both halves are needed, and they answer different failures. The
// entries are what an ordinary boot reads. The default path is what a
// firmware falls back on when it has no entries left to read.
//
// The two assertions run separately on purpose. A slot that cannot
// carry a loader must not stop the entries from healing, and a variable
// store that refuses a write must not stop the loader from landing.
//
// The first half asserts more than the standing order: it also aims
// the one-shot BootNext at the proven slot, for the firmware that
// holds BootNext through a reset but not BootOrder (assertBootNext in
// bootentries.go).
func (a efiActuator) assertProven(slot string) {
	healBootEntries(a.dir, a.machineName, slot)
	a.healSlotLoader(slot)
}

// healSlotLoader keeps the fallback loader on the proven slot and
// nowhere else. A firmware at its defaults takes the first answer it
// finds, and the other slot holds an older release or nothing at all.
//
// The order of these two steps is the whole guarantee. The proven slot
// takes the loader first, and only a slot that took it lets the other
// slot lose its own. A machine is never left with neither.
func (a efiActuator) healSlotLoader(slot string) {
	mount := slotMountPath(slot)
	if mount == "" || a.machineName == "" {
		// A slot that is not mounted cannot take a loader, and a boot
		// with no name has nothing correct to write in the loader's
		// entry. Both cases leave the loader that is already there.
		return
	}
	if err := writeSlotLoader(mount, slot, a.machineName); err != nil {
		fmt.Fprintf(os.Stderr, "liken: system: %v; this machine has no fallback for a firmware that loses its boot entries\n", err)
		return
	}
	if other := slotMountPath(otherSlot(slot)); other != "" {
		removeSlotLoader(other)
	}
}
