package main

// Tests for the firmware boot entries: what a slot's entry contains,
// and how a boot writes back an entry that the firmware lost.

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestConsoleArgs(t *testing.T) {
	fakeCmdline(t, "console=ttyS0 console=tty0 rdinit=/liken liken.machine=node-1\n")
	got := consoleArgs()
	want := []string{"console=ttyS0", "console=tty0"}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("consoleArgs is %v, want %v", got, want)
	}
}

func TestWriteSlotBootEntry(t *testing.T) {
	fakeCmdline(t, "console=ttyS0 rdinit=/liken\n")
	dir := fakeEFIVars(t, map[string][]byte{})
	part := &slotPartition{number: 1, firstLBA: 2048, lastLBA: 4095}

	number, err := writeSlotBootEntry(dir, "A", part, "node-1")
	if err != nil {
		t.Fatal(err)
	}
	option, ok := listBootEntries(dir)[number]
	if !ok {
		t.Fatalf("listBootEntries has no entry %d after writeSlotBootEntry, want it", number)
	}
	if option.description != "liken slot A" {
		t.Errorf("description is %q, want %q", option.description, "liken slot A")
	}
	if option.filePath != `\vmlinuz` {
		t.Errorf("file path is %q, want %q", option.filePath, `\vmlinuz`)
	}
	wantArgs := `console=ttyS0 rdinit=/liken initrd=\microcode.cpio initrd=\boot.cpio initrd=\deployment.cpio liken.machine=node-1 liken.slot=A panic=10`
	if !bytes.Equal(option.optionalData, encodeUTF16Z(wantArgs)) {
		t.Errorf("optional data is % x, want the UTF-16 of %q", option.optionalData, wantArgs)
	}
	if option.hardDrive == nil || option.hardDrive.partitionNumber != 1 ||
		option.hardDrive.sectors != 2048 {
		t.Errorf("hard drive node is %+v, want partition 1 with 2048 sectors", option.hardDrive)
	}
}

// On a firmware that drops a BootOrder write, BootNext is the only
// thing that protects the first boot. Slot A leads BootOrder, and
// BootNext points at the same entry.
func TestRegisterSlotEntriesPinTheFirstBoot(t *testing.T) {
	fakeCmdline(t, "console=ttyS0 rdinit=/liken\n")
	dir := fakeFirmwareVars(t, map[string][]byte{})
	slotA := &slotPartition{number: 1, firstLBA: 2048, lastLBA: 4095}
	slotB := &slotPartition{number: 2, firstLBA: 4096, lastLBA: 6143}

	if err := registerSlotEntries(slotA, slotB, "node-1"); err != nil {
		t.Fatal(err)
	}

	entryA, ok := findSlotEntry(dir, "A")
	if !ok {
		t.Fatal("registerSlotEntries wrote no entry for slot A, want one")
	}
	if order := readBootOrder(dir); len(order) == 0 || order[0] != entryA {
		t.Errorf("BootOrder is %v, want slot A's entry %d first", order, entryA)
	}
	next, err := readEFIVar(dir, "BootNext")
	if err != nil || bootNextEntry(next) != int32(entryA) {
		t.Errorf("BootNext is % x (error %v), want slot A's entry %d", next, err, entryA)
	}
}

func TestAssertBootNextAcceptsAWiderVariable(t *testing.T) {
	dir := fakeEFIVars(t, map[string][]byte{"BootNext": {0x03, 0x00, 0x00, 0x00}})
	path := filepath.Join(dir, "BootNext-"+efiGlobalVariable)
	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}

	assertBootNext(dir, 0x0003, "B")

	after, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if !after.ModTime().Equal(before.ModTime()) {
		t.Error("assertBootNext rewrote a BootNext that already names the entry, want no write")
	}
}

// A firmware that resets its variables to defaults has no entries. The
// boot that the fallback loader started writes both entries again.
func TestRepairBootEntriesWritesBothSlotsFromNothing(t *testing.T) {
	fakeCmdline(t, "console=ttyS0 rdinit=/liken liken.machine=node-1 liken.slot=A\n")
	installedDisk(t)
	dir := fakeEFIVars(t, map[string][]byte{})

	repairBootEntries(dir, "node-1", "A", true)

	entryA, ok := findSlotEntry(dir, "A")
	if !ok {
		t.Fatal("repairBootEntries wrote no entry for slot A, want one")
	}
	entryB, ok := findSlotEntry(dir, "B")
	if !ok {
		t.Fatal("repairBootEntries wrote no entry for slot B, want one")
	}
	order := readBootOrder(dir)
	if len(order) != 2 || order[0] != entryA || order[1] != entryB {
		t.Errorf("BootOrder is %v, want [%d %d]", order, entryA, entryB)
	}
	option := listBootEntries(dir)[entryA]
	if option.hardDrive == nil || option.hardDrive.partitionNumber != 1 {
		t.Errorf("hard drive node is %+v, want partition 1", option.hardDrive)
	}
}

func TestRepairBootEntriesLeadsWithTheProvenSlot(t *testing.T) {
	fakeCmdline(t, "console=ttyS0 rdinit=/liken liken.machine=node-1 liken.slot=B\n")
	installedDisk(t)
	dir := fakeEFIVars(t, map[string][]byte{})

	repairBootEntries(dir, "node-1", "B", true)

	entryB, _ := findSlotEntry(dir, "B")
	if order := readBootOrder(dir); len(order) == 0 || order[0] != entryB {
		t.Errorf("BootOrder is %v, want slot B's entry %d first", order, entryB)
	}
}

func TestRepairBootEntriesKeepsTheFirmwaresOwnEntries(t *testing.T) {
	fakeCmdline(t, "console=ttyS0 liken.machine=node-1\n")
	installedDisk(t)
	foreign := encodeLoadOption(loadOption{
		attributes:  loadOptionActive,
		description: "UEFI Shell",
		filePath:    `\EFI\shell.efi`,
	})
	dir := fakeEFIVars(t, map[string][]byte{
		"Boot0000":  foreign,
		"BootOrder": {0x00, 0x00},
	})

	repairBootEntries(dir, "node-1", "A", true)

	order := readBootOrder(dir)
	if len(order) != 3 || order[2] != 0 {
		t.Errorf("BootOrder is %v, want both slot entries first and 0 last", order)
	}
}

func TestRepairBootEntriesWritesNothingWhenNothingDrifted(t *testing.T) {
	fakeCmdline(t, "console=ttyS0 liken.machine=node-1\n")
	installedDisk(t)
	dir := fakeEFIVars(t, map[string][]byte{})
	repairBootEntries(dir, "node-1", "A", true)
	entryA, _ := findSlotEntry(dir, "A")
	path := filepath.Join(dir, bootEntryID(entryA)+"-"+efiGlobalVariable)
	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}

	repairBootEntries(dir, "node-1", "A", true)

	after, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if !after.ModTime().Equal(before.ModTime()) {
		t.Error("repairBootEntries rewrote an entry that already matched, want no write")
	}
}

func TestRepairBootEntriesRewritesADriftedEntry(t *testing.T) {
	fakeCmdline(t, "console=ttyS0 liken.machine=node-1\n")
	installedDisk(t)
	dir := fakeEFIVars(t, map[string][]byte{})
	repairBootEntries(dir, "node-2", "A", true)
	stale, _ := findSlotEntry(dir, "A")

	repairBootEntries(dir, "node-1", "A", true)

	current, ok := findSlotEntry(dir, "A")
	if !ok || current != stale {
		t.Fatalf("slot A's entry is %d (found %v), want %d", current, ok, stale)
	}
	option := listBootEntries(dir)[current]
	want := encodeUTF16Z(strings.Join(slotKernelArgs("node-1", "A"), " "))
	if !bytes.Equal(option.optionalData, want) {
		t.Errorf("optional data is % x, want % x", option.optionalData, want)
	}
}

// A disk with no slot partitions gives no GPT data to render an entry
// from, so no entry is written.
func TestRepairBootEntriesRefusesAMachineWithNoSlots(t *testing.T) {
	fakeCmdline(t, "console=ttyS0 liken.machine=node-1\n")
	fakeMachine(t)
	dir := fakeEFIVars(t, map[string][]byte{})

	repairBootEntries(dir, "node-1", "A", true)

	if _, ok := findSlotEntry(dir, "A"); ok {
		t.Error("repairBootEntries wrote an entry for a disk with no slot partitions, want none")
	}
}

func TestRepairBootEntriesRefusesAnAnonymousMachine(t *testing.T) {
	fakeCmdline(t, "console=ttyS0\n")
	installedDisk(t)
	dir := fakeEFIVars(t, map[string][]byte{})

	repairBootEntries(dir, "", "A", true)

	if _, ok := findSlotEntry(dir, "A"); ok {
		t.Error("repairBootEntries wrote an entry with no machine name, want none")
	}
}

func TestSlotOrder(t *testing.T) {
	cases := map[string][]string{
		"A": {"A", "B"},
		"B": {"B", "A"},
		"":  {"A", "B"},
	}
	for preferred, want := range cases {
		got := slotOrder(preferred)
		if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
			t.Errorf("slotOrder(%q) is %v, want %v", preferred, got, want)
		}
	}
}

// findSlotEntry explains why a firmware's copy of an entry must lose
// to the original.
func TestFindSlotEntryTakesTheLowestOfTwoEntriesForOneSlot(t *testing.T) {
	fakeCmdline(t, "console=ttyS0 liken.machine=node-1\n")
	ours := encodeLoadOption(loadOption{
		attributes:  loadOptionActive,
		description: "liken slot B",
		filePath:    `\vmlinuz`,
	})
	clone := encodeLoadOption(loadOption{
		attributes:  loadOptionActive,
		description: "liken slot B",
		filePath:    `\vmlinuz`,
		// The firmware's copy differs only in the device path, which
		// the decoder returns in hardDrive.
		hardDrive: &hardDriveNode{partitionNumber: 2, firstLBA: 4096, sectors: 2048},
	})
	dir := fakeEFIVars(t, map[string][]byte{"Boot0003": ours, "Boot0004": clone})

	// listBootEntries returns a map, and Go randomizes map iteration,
	// so an implementation that takes the first match passes some
	// runs. Repeating the lookup makes that unlikely.
	for range 20 {
		number, ok := findSlotEntry(dir, "B")
		if !ok || number != 3 {
			t.Fatalf("findSlotEntry is %d (found %v), want 3", number, ok)
		}
	}
}
