package main

// Tests for the UEFI dialect of the boot actuator. Its three
// lifecycle actions are covered through proving_test.go and the
// installer's tests. What is here is the healing: a whole boot path
// coming back from nothing.

import (
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
