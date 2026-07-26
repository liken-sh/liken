package main

// Tests for the firmware boot entries: what a slot's entry says, and
// how a boot puts back an entry that the firmware lost.

import (
	"bytes"
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
