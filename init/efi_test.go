package main

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/liken-sh/liken/machine"
)

// fakeEFIVars builds a directory standing in for efivarfs, each
// variable a file of 4 attribute bytes plus payload, exactly the
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
	// still shows its ID, so nothing in the order is hidden.
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

func TestWriteEFIVarRoundTrips(t *testing.T) {
	dir := t.TempDir()
	if err := writeEFIVar(dir, "BootNext", u16le(3)); err != nil {
		t.Fatal(err)
	}
	b, err := readEFIVar(dir, "BootNext")
	if err != nil {
		t.Fatal(err)
	}
	if len(b) != 2 || binary.LittleEndian.Uint16(b) != 3 {
		t.Errorf("payload: got % X", b)
	}
	// Overwriting must replace, not append: efivarfs semantics.
	if err := writeEFIVar(dir, "BootNext", u16le(7)); err != nil {
		t.Fatal(err)
	}
	if b, _ = readEFIVar(dir, "BootNext"); binary.LittleEndian.Uint16(b) != 7 {
		t.Errorf("overwrite: got % X", b)
	}
}

func TestSetBootEntryFindsItsOwnByDescription(t *testing.T) {
	dir := fakeEFIVars(t, map[string][]byte{
		"Boot0000": encodeLoadOption(loadOption{description: "BootManagerMenuApp"}),
		"Boot0001": encodeLoadOption(loadOption{description: "liken slot A"}),
	})
	// Rewriting slot A lands on its existing number, not a new one.
	n, err := setBootEntry(dir, loadOption{description: "liken slot A", filePath: `\vmlinuz`})
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("slot A should overwrite Boot0001, landed on Boot%04X", n)
	}
	// A new description takes the lowest free number, skipping the
	// firmware's entries rather than clobbering them.
	n, err = setBootEntry(dir, loadOption{description: "liken slot B"})
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Errorf("slot B should land on Boot0002, landed on Boot%04X", n)
	}
	if got := listBootEntries(dir)[0].description; got != "BootManagerMenuApp" {
		t.Errorf("the firmware's entry should be untouched: %q", got)
	}
}

// fakeFirmwareVars points the package's efivars path at a fake store,
// restoring the real one afterward; reportFirmware and the facts read
// through the variable.
func fakeFirmwareVars(t *testing.T, vars map[string][]byte) string {
	t.Helper()
	dir := fakeEFIVars(t, vars)
	old := efiVarsDir
	efiVarsDir = dir
	t.Cleanup(func() { efiVarsDir = old })
	return dir
}

func TestReportFirmwareUEFI(t *testing.T) {
	fakeFirmwareVars(t, map[string][]byte{
		"Boot0002":    encodeLoadOption(loadOption{attributes: loadOptionActive, description: "liken slot A"}),
		"BootCurrent": u16le(0x0002),
		"BootNext":    u16le(0x0002),
		"BootOrder":   u16le(0x0002),
	})
	// The report prints; what the test pins is that the same facts
	// decode for status, console parity's other half.
	reportFirmware()
	fw := firmwareFacts(efiVarsDir)
	if fw.Mode != machine.FirmwareUEFI {
		t.Errorf("a variable store means UEFI: %s", fw.Mode)
	}
	if fw.BootCurrent != "Boot0002 (liken slot A)" {
		t.Errorf("entries decode to their names: %q", fw.BootCurrent)
	}
	if fw.BootNext != "Boot0002 (liken slot A)" {
		t.Errorf("BootNext decodes too: %q", fw.BootNext)
	}
}

func TestReportFirmwareBIOS(t *testing.T) {
	old := efiVarsDir
	efiVarsDir = filepath.Join(t.TempDir(), "no-efivars")
	t.Cleanup(func() { efiVarsDir = old })
	reportFirmware()
	if fw := firmwareFacts(efiVarsDir); fw.Mode != machine.FirmwareBIOS {
		t.Errorf("no store reads as BIOS: %s", fw.Mode)
	}
}

func TestReadEFIVarRejectsATruncatedVariable(t *testing.T) {
	dir := t.TempDir()
	// Two bytes can't even hold the attribute word the kernel always
	// prepends; such a file is mangled, not empty.
	if err := os.WriteFile(filepath.Join(dir, "BootNext-"+efiGlobalVariable), []byte{1, 2}, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := readEFIVar(dir, "BootNext"); err == nil {
		t.Error("a variable shorter than its attributes is an error")
	}
}

func TestListBootEntriesSkipsWhatItCannotDecode(t *testing.T) {
	dir := fakeEFIVars(t, map[string][]byte{
		"Boot0007": encodeLoadOption(loadOption{attributes: loadOptionActive, description: "liken slot A"}),
		"Boot0008": {0x01}, // truncated: not even a whole load option header
	})
	// Something else's variable, not a Boot#### entry at all.
	if err := os.WriteFile(filepath.Join(dir, "SecureBoot-"+efiGlobalVariable), []byte{7, 0, 0, 0, 1}, 0o644); err != nil {
		t.Fatal(err)
	}
	entries := listBootEntries(dir)
	if len(entries) != 1 || entries[7].description != "liken slot A" {
		t.Errorf("only decodable Boot entries are listed: %+v", entries)
	}
}

func TestWriteEFIVarReportsARefusedWrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "BootNext-"+efiGlobalVariable)
	if err := os.WriteFile(path, []byte{7, 0, 0, 0, 1, 0}, 0o444); err != nil {
		t.Fatal(err)
	}
	if err := writeEFIVar(dir, "BootNext", u16le(3)); err == nil {
		t.Error("a variable that refuses its write is an error the caller must hear")
	}
}

func TestListBootEntriesWithNoStoreListsNothing(t *testing.T) {
	if entries := listBootEntries(filepath.Join(t.TempDir(), "no-efivars")); len(entries) != 0 {
		t.Errorf("no store, no entries: %+v", entries)
	}
}
