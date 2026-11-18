package main

// Tests for assertProven on the UEFI actuator. proving_test.go and
// handover_test.go cover the other methods.

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/liken-sh/liken/liken/machine"
)

// fakeSlotMounts points both slot roles at temporary directories that
// hold a release's boot files.
func fakeSlotMounts(t *testing.T) (slotA, slotB string) {
	t.Helper()
	slotA, slotB = slotWithMenu(t), slotWithMenu(t)
	for role, dir := range map[machine.StorageRoleName]string{
		machine.SystemARole: slotA,
		machine.SystemBRole: slotB,
	} {
		old := roleMounts[role]
		rm := old
		rm.path = dir
		roleMounts[role] = rm
		t.Cleanup(func() { roleMounts[role] = old })
	}
	return slotA, slotB
}

// A firmware reset to its defaults has no entries and no BootOrder.
func TestEFIActuatorAssertProvenRestoresTheWholeBootPath(t *testing.T) {
	fakeCmdline(t, "console=ttyS0 rdinit=/liken liken.machine=node-1 liken.slot=A\n")
	installedDisk(t)
	slotA, slotB := fakeSlotMounts(t)
	dir := fakeEFIVars(t, map[string][]byte{})
	act := efiActuator{dir: dir, machineName: "node-1"}

	act.assertProven("A")

	if _, ok := findSlotEntry(dir, "A"); !ok {
		t.Error("NVRAM has no entry for the proven slot A after assertProven, want one")
	}
	if _, err := os.Stat(defaultLoaderPath(slotA)); err != nil {
		t.Errorf("the firmware's default loader path is missing, want the proven slot's loader there: %v", err)
	}
	if _, err := os.Stat(defaultLoaderPath(slotB)); !os.IsNotExist(err) {
		t.Error("slot B has a loader at the default path, want only the proven slot to have one")
	}
}

// A promotion moves the fallback loader to the new proven slot. The old
// slot keeps its NVRAM entry, so BootOrder can still fall back to it.
func TestEFIActuatorAssertProvenMovesTheFallback(t *testing.T) {
	fakeCmdline(t, "console=ttyS0 rdinit=/liken liken.machine=node-1 liken.slot=B\n")
	installedDisk(t)
	slotA, slotB := fakeSlotMounts(t)
	dir := fakeEFIVars(t, map[string][]byte{})
	act := efiActuator{dir: dir, machineName: "node-1"}
	act.assertProven("A")

	act.assertProven("B")

	if _, err := os.Stat(defaultLoaderPath(slotB)); err != nil {
		t.Errorf("slot B has no loader at the default path, want one after its promotion: %v", err)
	}
	if _, err := os.Stat(defaultLoaderPath(slotA)); !os.IsNotExist(err) {
		t.Error("slot A still has a loader at the default path, want it removed after slot B is proven")
	}
	if _, ok := findSlotEntry(dir, "A"); !ok {
		t.Error("slot A has no boot entry, want it kept, because BootOrder still falls back to it")
	}
}

func bootNextVar(t *testing.T, dir string) []byte {
	t.Helper()
	payload, err := readEFIVar(dir, "BootNext")
	if err != nil {
		return nil
	}
	return payload
}

// Some firmware accepts a BootOrder write and reads it back, but
// restores its old order after a reset. BootNext survives a reset on
// the same firmware, so the assertion also sets BootNext to the proven
// slot.
func TestEFIActuatorAssertProvenArmsBootNextAtTheProvenSlot(t *testing.T) {
	fakeCmdline(t, "console=ttyS0 rdinit=/liken liken.machine=node-1 liken.slot=A\n")
	installedDisk(t)
	fakeSlotMounts(t)
	dir := fakeEFIVars(t, map[string][]byte{})
	act := efiActuator{dir: dir, machineName: "node-1"}

	act.assertProven("A")

	entryA, ok := findSlotEntry(dir, "A")
	if !ok {
		t.Fatal("NVRAM has no entry for the proven slot A after assertProven, want one")
	}
	if got := bootNextVar(t, dir); !bytes.Equal(got, bootNextPayload(entryA)) {
		t.Errorf("BootNext is % x, want the proven slot's entry", got)
	}
}

// A BootNext that was set for a release that is no longer staged must
// not boot on a later reboot. The assertion overwrites it with the
// proven slot, as the GRUB actuator clears a leftover try_slot.
func TestEFIActuatorAssertProvenOverwritesAStaleBootNext(t *testing.T) {
	fakeCmdline(t, "console=ttyS0 rdinit=/liken liken.machine=node-1 liken.slot=A\n")
	installedDisk(t)
	fakeSlotMounts(t)
	dir := fakeEFIVars(t, map[string][]byte{})
	act := efiActuator{dir: dir, machineName: "node-1"}
	act.assertProven("A")
	entryB, ok := findSlotEntry(dir, "B")
	if !ok {
		t.Fatal("slot B has no boot entry, want an entry for each slot")
	}
	if err := writeEFIVar(dir, "BootNext", bootNextPayload(entryB)); err != nil {
		t.Fatalf("writing BootNext: %v", err)
	}

	act.assertProven("A")

	entryA, _ := findSlotEntry(dir, "A")
	if got := bootNextVar(t, dir); !bytes.Equal(got, bootNextPayload(entryA)) {
		t.Errorf("BootNext is % x, want the proven slot's entry in place of the earlier one-shot", got)
	}
}

// The second call is the assertion before a reboot. It finds the
// BootNext that the boot-time assertion wrote, and does not write NVRAM
// again.
func TestEFIActuatorAssertProvenLeavesACorrectBootNextAlone(t *testing.T) {
	fakeCmdline(t, "console=ttyS0 rdinit=/liken liken.machine=node-1 liken.slot=A\n")
	installedDisk(t)
	fakeSlotMounts(t)
	dir := fakeEFIVars(t, map[string][]byte{})
	act := efiActuator{dir: dir, machineName: "node-1"}
	act.assertProven("A")
	entryA, _ := findSlotEntry(dir, "A")
	path := filepath.Join(dir, "BootNext-"+efiGlobalVariable)
	before, err := os.Stat(path)
	if err != nil {
		t.Fatalf("reading the BootNext file: %v", err)
	}

	act.assertProven("A")

	after, err := os.Stat(path)
	if err != nil {
		t.Fatalf("reading the BootNext file again: %v", err)
	}
	if !after.ModTime().Equal(before.ModTime()) {
		t.Error("BootNext was written again, want no write when it already names the proven slot")
	}
	if got := bootNextVar(t, dir); !bytes.Equal(got, bootNextPayload(entryA)) {
		t.Errorf("BootNext is % x, want the proven slot's entry", got)
	}
}

// Slot B has no boot menu, so writeSlotLoader fails on it.
func TestEFIActuatorKeepsTheOldFallbackLoaderWhenTheNewSlotIsUnwritable(t *testing.T) {
	fakeCmdline(t, "console=ttyS0 liken.machine=node-1\n")
	installedDisk(t)
	slotA, slotB := fakeSlotMounts(t)
	dir := fakeEFIVars(t, map[string][]byte{})
	act := efiActuator{dir: dir, machineName: "node-1"}
	act.assertProven("A")
	if err := os.Remove(filepath.Join(slotB, bootMenuName)); err != nil {
		t.Fatalf("removing the boot menu of slot B: %v", err)
	}

	act.assertProven("B")

	if _, err := os.Stat(defaultLoaderPath(slotA)); err != nil {
		t.Errorf("slot A has no loader at the default path: %v, want it kept, because slot B's loader could not be written", err)
	}
}
