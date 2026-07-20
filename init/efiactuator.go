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
	if err := writeEFIVar(a.dir, "BootNext", []byte{byte(entry), byte(entry >> 8)}); err != nil {
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

// assertProven puts the slot's entry at the head of BootOrder and
// keeps everything else in its relative order. This method corrects
// a lost or corrupted BootOrder, for example from a dead NVRAM
// battery or someone editing the setup menu, the next time it runs.
func (a efiActuator) assertProven(slot string) {
	leader, ok := findSlotEntry(a.dir, slot)
	if !ok {
		fmt.Fprintf(os.Stderr, "liken: system: the store says slot %s is proven, but no boot entry answers to it; leaving BootOrder alone\n", slot)
		return
	}

	order := readBootOrder(a.dir)
	if len(order) > 0 && order[0] == leader {
		return // the firmware already agrees
	}

	repaired := []uint16{leader}
	for _, n := range order {
		if n != leader {
			repaired = append(repaired, n)
		}
	}
	if err := writeBootOrder(a.dir, repaired); err != nil {
		fmt.Fprintf(os.Stderr, "liken: system: repairing BootOrder: %v\n", err)
		return
	}
	// Trust the readback, not the write. Some firmware accepts a
	// write and then fails to hold it, and every later report would
	// be wrong if the code took the write result at face value.
	if readback := readBootOrder(a.dir); len(readback) == 0 || readback[0] != leader {
		fmt.Fprintf(os.Stderr, "liken: system: BootOrder was written but reads back unchanged; the firmware is not holding it\n")
		return
	}
	fmt.Printf("liken: system: BootOrder now leads with %s (slot %s is proven)\n",
		bootEntryID(leader), slot)
}

// findSlotEntry locates a slot's firmware entry the same way
// everything in liken finds things: by the name written on it at
// install time.
func findSlotEntry(efiDir, slot string) (uint16, bool) {
	for number, option := range listBootEntries(efiDir) {
		if option.description == "liken slot "+slot {
			return number, true
		}
	}
	return 0, false
}
