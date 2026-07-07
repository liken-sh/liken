package main

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/chrisguidry/liken/machine"
)

// fakeEFIVars builds a directory standing in for efivarfs, each
// variable a file of 4 attribute bytes plus payload — exactly the
// shape the kernel presents.
func fakeEFIVars(t *testing.T, vars map[string][]byte) string {
	t.Helper()
	dir := t.TempDir()
	for name, payload := range vars {
		file := name + "-" + efiGlobalVariable
		content := append([]byte{0x07, 0, 0, 0}, payload...)
		if err := os.WriteFile(filepath.Join(dir, file), content, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func u16le(values ...uint16) []byte {
	b := make([]byte, len(values)*2)
	for i, v := range values {
		binary.LittleEndian.PutUint16(b[i*2:], v)
	}
	return b
}

func TestFirmwareFactsDecodesTheBootStory(t *testing.T) {
	slotA := encodeLoadOption(loadOption{attributes: loadOptionActive, description: "liken slot A"})
	slotB := encodeLoadOption(loadOption{attributes: loadOptionActive, description: "liken slot B"})
	dir := fakeEFIVars(t, map[string][]byte{
		"BootCurrent": u16le(0),
		"BootOrder":   u16le(0, 1),
		"Boot0000":    slotA,
		"Boot0001":    slotB,
	})

	fw := firmwareFacts(dir)
	if fw.Mode != machine.FirmwareUEFI {
		t.Errorf("mode: got %q", fw.Mode)
	}
	if fw.BootCurrent != "Boot0000 (liken slot A)" {
		t.Errorf("bootCurrent: got %q", fw.BootCurrent)
	}
	if fw.BootNext != "" {
		t.Errorf("bootNext should be absent: got %q", fw.BootNext)
	}
	want := []string{"Boot0000 (liken slot A)", "Boot0001 (liken slot B)"}
	if !slices.Equal(fw.BootOrder, want) {
		t.Errorf("bootOrder: got %v, want %v", fw.BootOrder, want)
	}
}

func TestFirmwareFactsReportsAnArmedBootNext(t *testing.T) {
	dir := fakeEFIVars(t, map[string][]byte{
		"BootNext": u16le(1),
		"Boot0001": encodeLoadOption(loadOption{description: "liken slot B"}),
	})
	if got := firmwareFacts(dir).BootNext; got != "Boot0001 (liken slot B)" {
		t.Errorf("bootNext: got %q", got)
	}
}

func TestFirmwareFactsNamesUndecodableEntriesHonestly(t *testing.T) {
	// An entry in the order whose variable is missing or mangled
	// still shows its ID: an honest listing beats a hidden one.
	dir := fakeEFIVars(t, map[string][]byte{
		"BootOrder": u16le(0x2001, 0x2002),
		"Boot2002":  {0xFF}, // too short to even carry attributes
	})
	want := []string{"Boot2001", "Boot2002"}
	if got := firmwareFacts(dir).BootOrder; !slices.Equal(got, want) {
		t.Errorf("bootOrder: got %v, want %v", got, want)
	}
}

func TestFirmwareFactsOnABIOSMachine(t *testing.T) {
	// No variable store at all: the mode says BIOS and every other
	// field stays empty.
	fw := firmwareFacts(filepath.Join(t.TempDir(), "nonexistent"))
	if fw.Mode != machine.FirmwareBIOS {
		t.Errorf("mode: got %q", fw.Mode)
	}
	if fw.BootCurrent != "" || fw.BootNext != "" || fw.BootOrder != nil {
		t.Errorf("a BIOS machine has no boot variables: %+v", fw)
	}
}

func TestReadEFIVarStripsAttributes(t *testing.T) {
	dir := fakeEFIVars(t, map[string][]byte{"BootCurrent": {0xAA, 0xBB}})
	b, err := readEFIVar(dir, "BootCurrent")
	if err != nil {
		t.Fatal(err)
	}
	if len(b) != 2 || b[0] != 0xAA || b[1] != 0xBB {
		t.Errorf("payload: got % X", b)
	}
}
