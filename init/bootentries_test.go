package main

// Tests for the firmware boot entries: what a slot's entry says, and
// how a boot puts back an entry that the firmware lost.

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
		t.Errorf("every console= argument is copied, nothing else: %v", got)
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
		t.Fatalf("the entry decodes back out of the store")
	}
	if option.description != "liken slot A" {
		t.Errorf("description: got %q", option.description)
	}
	if option.filePath != `\vmlinuz` {
		t.Errorf("file path: got %q", option.filePath)
	}
	wantArgs := `console=ttyS0 rdinit=/liken initrd=\microcode.cpio initrd=\boot.cpio initrd=\deployment.cpio liken.machine=node-1 liken.slot=A panic=10`
	if !bytes.Equal(option.optionalData, encodeUTF16Z(wantArgs)) {
		t.Errorf("the baked command line is assembled from scratch: % x", option.optionalData)
	}
	if option.hardDrive == nil || option.hardDrive.partitionNumber != 1 ||
		option.hardDrive.sectors != 2048 {
		t.Errorf("the entry pins the partition: %+v", option.hardDrive)
	}
}

// The install is the first boot's only protection on a firmware that
// drops a BootOrder write. Slot A leads the order and the one-shot is
// pinned to the same entry.
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
		t.Fatal("the install writes slot A's entry")
	}
	if order := readBootOrder(dir); len(order) == 0 || order[0] != entryA {
		t.Errorf("slot A leads the order: %v", order)
	}
	next, err := readEFIVar(dir, "BootNext")
	if err != nil || bootNextEntry(next) != int32(entryA) {
		t.Errorf("the one-shot is pinned to slot A's entry: % x, %v", next, err)
	}
}

// A firmware may store a variable wider than the two bytes BootNext
// needs. Comparing the raw bytes would rewrite NVRAM on every boot and
// warn about a firmware that is holding the value correctly.
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
		t.Error("the firmware already names this entry, however wide it stores it")
	}
}

// A firmware whose variables reset to defaults holds no entries at
// all. The boot that the fallback loader started puts both of them
// back.
func TestHealBootEntriesWritesBothSlotsFromNothing(t *testing.T) {
	fakeCmdline(t, "console=ttyS0 rdinit=/liken liken.machine=node-1 liken.slot=A\n")
	installedDisk(t)
	dir := fakeEFIVars(t, map[string][]byte{})

	healBootEntries(dir, "node-1", "A")

	entryA, ok := findSlotEntry(dir, "A")
	if !ok {
		t.Fatal("slot A's entry is back")
	}
	entryB, ok := findSlotEntry(dir, "B")
	if !ok {
		t.Fatal("slot B's entry is back, so the fallback is real")
	}
	order := readBootOrder(dir)
	if len(order) != 2 || order[0] != entryA || order[1] != entryB {
		t.Errorf("both slots lead the order, the proven slot first: %v", order)
	}
	option := listBootEntries(dir)[entryA]
	if option.hardDrive == nil || option.hardDrive.partitionNumber != 1 {
		t.Errorf("the entry pins the slot it found on this disk: %+v", option.hardDrive)
	}
}

func TestHealBootEntriesLeadsWithTheProvenSlot(t *testing.T) {
	fakeCmdline(t, "console=ttyS0 rdinit=/liken liken.machine=node-1 liken.slot=B\n")
	installedDisk(t)
	dir := fakeEFIVars(t, map[string][]byte{})

	healBootEntries(dir, "node-1", "B")

	entryB, _ := findSlotEntry(dir, "B")
	if order := readBootOrder(dir); len(order) == 0 || order[0] != entryB {
		t.Errorf("slot B is proven, so slot B leads: %v", order)
	}
}

// A firmware's own entries stay reachable. They are just never
// preferred.
func TestHealBootEntriesKeepsTheFirmwaresOwnEntries(t *testing.T) {
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

	healBootEntries(dir, "node-1", "A")

	order := readBootOrder(dir)
	if len(order) != 3 || order[2] != 0 {
		t.Errorf("the firmware's shell keeps its place behind both slots: %v", order)
	}
}

// NVRAM accepts a limited number of writes, and this runs on every
// boot and before every reboot. An entry that already agrees costs
// none of them.
func TestHealBootEntriesWritesNothingWhenNothingDrifted(t *testing.T) {
	fakeCmdline(t, "console=ttyS0 liken.machine=node-1\n")
	installedDisk(t)
	dir := fakeEFIVars(t, map[string][]byte{})
	healBootEntries(dir, "node-1", "A")
	entryA, _ := findSlotEntry(dir, "A")
	path := filepath.Join(dir, bootEntryID(entryA)+"-"+efiGlobalVariable)
	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}

	healBootEntries(dir, "node-1", "A")

	after, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if !after.ModTime().Equal(before.ModTime()) {
		t.Error("the entry already agreed, so no variable needed writing")
	}
}

// An entry that survived but no longer describes this machine is
// rewritten under the number the firmware already gave it.
func TestHealBootEntriesRewritesADriftedEntry(t *testing.T) {
	fakeCmdline(t, "console=ttyS0 liken.machine=node-1\n")
	installedDisk(t)
	dir := fakeEFIVars(t, map[string][]byte{})
	healBootEntries(dir, "node-2", "A")
	stale, _ := findSlotEntry(dir, "A")

	healBootEntries(dir, "node-1", "A")

	current, ok := findSlotEntry(dir, "A")
	if !ok || current != stale {
		t.Fatalf("the entry keeps its number: was %d, now %d", stale, current)
	}
	option := listBootEntries(dir)[current]
	want := encodeUTF16Z(strings.Join(slotKernelArgs("node-1", "A"), " "))
	if !bytes.Equal(option.optionalData, want) {
		t.Errorf("the command line names this machine: % x", option.optionalData)
	}
}

// A machine whose slots cannot be read gets no guesses. This is the
// same rule the installer follows.
func TestHealBootEntriesRefusesAMachineWithNoSlots(t *testing.T) {
	fakeCmdline(t, "console=ttyS0 liken.machine=node-1\n")
	fakeMachine(t)
	dir := fakeEFIVars(t, map[string][]byte{})

	healBootEntries(dir, "node-1", "A")

	if _, ok := findSlotEntry(dir, "A"); ok {
		t.Error("no partition carries a slot, so there is nothing correct to write")
	}
}

func TestHealBootEntriesRefusesAnAnonymousMachine(t *testing.T) {
	fakeCmdline(t, "console=ttyS0\n")
	installedDisk(t)
	dir := fakeEFIVars(t, map[string][]byte{})

	healBootEntries(dir, "", "A")

	if _, ok := findSlotEntry(dir, "A"); ok {
		t.Error("an entry carries identity, so an anonymous entry would be wrong on every later boot")
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
			t.Errorf("preferred %q: got %v, want %v", preferred, got, want)
		}
	}
}

// Some firmware clones a boot option it did not write, so two entries
// can answer to one slot. Every reader must pick the same one, because
// fallbackLeads compares its number against the head of BootOrder and
// armProvingBoot refuses a trial when they disagree.
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
		// the decoder reports through hardDrive.
		hardDrive: &hardDriveNode{partitionNumber: 2, firstLBA: 4096, sectors: 2048},
	})
	dir := fakeEFIVars(t, map[string][]byte{"Boot0003": ours, "Boot0004": clone})

	// Map iteration is randomized, so a wrong implementation passes
	// sometimes. Ask enough times that it cannot.
	for range 20 {
		number, ok := findSlotEntry(dir, "B")
		if !ok || number != 3 {
			t.Fatalf("the lowest matching entry wins every time: got %d, %v", number, ok)
		}
	}
}
