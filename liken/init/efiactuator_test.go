package main

// Tests for the UEFI dialect of the boot actuator. Its three
// lifecycle actions are covered through proving_test.go and the
// installer's tests. What is here is the healing: a whole boot path
// coming back from nothing.

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/liken-sh/liken/machine"
)

// fakeSlotMounts points both slot roles at temporary directories that
// carry a release's boot files, the way storage reconciliation would
// have mounted them.
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

// A firmware at its defaults holds no entries and no order. One boot
// through the fallback loader restores the whole boot path.
func TestEFIActuatorAssertProvenHealsEverything(t *testing.T) {
	fakeCmdline(t, "console=ttyS0 rdinit=/liken liken.machine=node-1 liken.slot=A\n")
	installedDisk(t)
	slotA, slotB := fakeSlotMounts(t)
	dir := fakeEFIVars(t, map[string][]byte{})
	act := efiActuator{dir: dir, machineName: "node-1"}

	act.assertProven("A")

	if _, ok := findSlotEntry(dir, "A"); !ok {
		t.Error("the proven slot's entry is back in NVRAM")
	}
	if _, err := os.Stat(defaultLoaderPath(slotA)); err != nil {
		t.Errorf("the proven slot answers the firmware's default path: %v", err)
	}
	if _, err := os.Stat(defaultLoaderPath(slotB)); !os.IsNotExist(err) {
		t.Error("only the proven slot answers it")
	}
}

// A promotion moves the fallback with the proven slot. The old slot
// keeps its entry in NVRAM, so BootOrder can still fall back to it.
func TestEFIActuatorAssertProvenMovesTheFallback(t *testing.T) {
	fakeCmdline(t, "console=ttyS0 rdinit=/liken liken.machine=node-1 liken.slot=B\n")
	installedDisk(t)
	slotA, slotB := fakeSlotMounts(t)
	dir := fakeEFIVars(t, map[string][]byte{})
	act := efiActuator{dir: dir, machineName: "node-1"}
	act.assertProven("A")

	act.assertProven("B")

	if _, err := os.Stat(defaultLoaderPath(slotB)); err != nil {
		t.Errorf("the newly proven slot answers the default path: %v", err)
	}
	if _, err := os.Stat(defaultLoaderPath(slotA)); !os.IsNotExist(err) {
		t.Error("the slot that is no longer proven stops answering it")
	}
	if _, ok := findSlotEntry(dir, "A"); !ok {
		t.Error("slot A keeps its entry, because BootOrder still falls back to it")
	}
}

// bootNextVar reads the raw BootNext payload, or nothing when the
// variable is absent.
func bootNextVar(t *testing.T, dir string) []byte {
	t.Helper()
	payload, err := readEFIVar(dir, "BootNext")
	if err != nil {
		return nil
	}
	return payload
}

// Some firmware accepts a BootOrder write, reads it back correctly, and
// still comes back from a reset with its old order. BootNext survives
// on the same firmware, so the assertion arms it at the proven slot too.
func TestEFIActuatorAssertProvenArmsBootNextAtTheProvenSlot(t *testing.T) {
	fakeCmdline(t, "console=ttyS0 rdinit=/liken liken.machine=node-1 liken.slot=A\n")
	installedDisk(t)
	fakeSlotMounts(t)
	dir := fakeEFIVars(t, map[string][]byte{})
	act := efiActuator{dir: dir, machineName: "node-1"}

	act.assertProven("A")

	entryA, ok := findSlotEntry(dir, "A")
	if !ok {
		t.Fatal("the proven slot's entry is back in NVRAM")
	}
	if got := bootNextVar(t, dir); !bytes.Equal(got, bootNextPayload(entryA)) {
		t.Errorf("BootNext must aim at the proven slot's entry: % x", got)
	}
}

// A one-shot armed for a release that is no longer staged must not run
// on a later reboot. The assertion overwrites it with the proven slot,
// the same way the GRUB dialect clears a leftover try_slot.
func TestEFIActuatorAssertProvenOverwritesAStaleBootNext(t *testing.T) {
	fakeCmdline(t, "console=ttyS0 rdinit=/liken liken.machine=node-1 liken.slot=A\n")
	installedDisk(t)
	fakeSlotMounts(t)
	dir := fakeEFIVars(t, map[string][]byte{})
	act := efiActuator{dir: dir, machineName: "node-1"}
	act.assertProven("A")
	entryB, ok := findSlotEntry(dir, "B")
	if !ok {
		t.Fatal("both slots carry an entry")
	}
	if err := writeEFIVar(dir, "BootNext", bootNextPayload(entryB)); err != nil {
		t.Fatal(err)
	}

	act.assertProven("A")

	entryA, _ := findSlotEntry(dir, "A")
	if got := bootNextVar(t, dir); !bytes.Equal(got, bootNextPayload(entryA)) {
		t.Errorf("the stale one-shot must give way to the proven slot: % x", got)
	}
}

// This is the assertion on the way down, before a reboot. The boot-time
// assertion wrote BootNext, nothing has consumed it since, so the
// reboot path finds it correct and spends no NVRAM write on it. (The
// boot-time assertion itself always writes: the firmware consumed the
// variable at power-on.)
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
		t.Fatal(err)
	}

	act.assertProven("A")

	after, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if !after.ModTime().Equal(before.ModTime()) {
		t.Error("BootNext already aimed at the proven slot, so no write was needed")
	}
	if got := bootNextVar(t, dir); !bytes.Equal(got, bootNextPayload(entryA)) {
		t.Errorf("BootNext still aims at the proven slot: % x", got)
	}
}

// A slot that cannot carry a loader must not cost the machine the
// loader it already has. Never neither.
func TestEFIActuatorKeepsTheOldFallbackWhenTheNewSlotCannotCarryOne(t *testing.T) {
	fakeCmdline(t, "console=ttyS0 liken.machine=node-1\n")
	installedDisk(t)
	slotA, slotB := fakeSlotMounts(t)
	dir := fakeEFIVars(t, map[string][]byte{})
	act := efiActuator{dir: dir, machineName: "node-1"}
	act.assertProven("A")
	if err := os.Remove(filepath.Join(slotB, bootMenuName)); err != nil {
		t.Fatal(err)
	}

	act.assertProven("B")

	if _, err := os.Stat(defaultLoaderPath(slotA)); err != nil {
		t.Error("slot B could not take over, so slot A's loader stays")
	}
}
