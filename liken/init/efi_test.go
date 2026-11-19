package main

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/liken-sh/liken/liken/machine"
)

// fakeEFIVars returns a directory laid out as efivarfs lays out its
// files.
func fakeEFIVars(t *testing.T, vars map[string][]byte) string {
	t.Helper()
	dir := t.TempDir()
	for name, payload := range vars {
		file := name + "-" + efiGlobalVariable
		content := append([]byte{0x07, 0, 0, 0}, payload...)
		if err := os.WriteFile(filepath.Join(dir, file), content, 0o644); err != nil {
			t.Fatalf("writing the variable file: %v", err)
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

func TestFirmwareFactsShowsBootEntriesByDescription(t *testing.T) {
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
		t.Errorf("mode is %q, want UEFI", fw.Mode)
	}
	if fw.BootCurrent != "Boot0000 (liken slot A)" {
		t.Errorf("BootCurrent is %q, want %q", fw.BootCurrent, "Boot0000 (liken slot A)")
	}
	if fw.BootNext != "" {
		t.Errorf("BootNext is %q, want it empty because no BootNext variable exists", fw.BootNext)
	}
	want := []string{"Boot0000 (liken slot A)", "Boot0001 (liken slot B)"}
	if !slices.Equal(fw.BootOrder, want) {
		t.Errorf("BootOrder is %v, want %v", fw.BootOrder, want)
	}
}

func TestFirmwareFactsShowsBootNextByDescription(t *testing.T) {
	dir := fakeEFIVars(t, map[string][]byte{
		"BootNext": u16le(1),
		"Boot0001": encodeLoadOption(loadOption{description: "liken slot B"}),
	})
	if got := firmwareFacts(dir).BootNext; got != "Boot0001 (liken slot B)" {
		t.Errorf("BootNext is %q, want %q", got, "Boot0001 (liken slot B)")
	}
}

func TestFirmwareFactsNotesBootNextThatRepeatsTheFirstBootOrderEntry(t *testing.T) {
	cases := map[string]struct {
		next uint16
		want string
	}{
		"BootNext equals the first BootOrder entry": {
			next: 1, want: "Boot0001 (liken slot B, pinned at the proven slot)",
		},
		"BootNext is the other slot": {next: 0, want: "Boot0000 (liken slot A)"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			dir := fakeEFIVars(t, map[string][]byte{
				"BootOrder": u16le(1, 0),
				"BootNext":  u16le(c.next),
				"Boot0000":  encodeLoadOption(loadOption{description: "liken slot A"}),
				"Boot0001":  encodeLoadOption(loadOption{description: "liken slot B"}),
			})
			if got := firmwareFacts(dir).BootNext; got != c.want {
				t.Errorf("BootNext is %q, want %q", got, c.want)
			}
		})
	}
}

func TestFirmwareFactsShowsOnlyTheVariableNameOfAnUndecodableEntry(t *testing.T) {
	// Boot2001 does not exist.
	dir := fakeEFIVars(t, map[string][]byte{
		"BootOrder": u16le(0x2001, 0x2002),
		"Boot2002":  {0xFF}, // too short to hold the attribute bytes
	})
	want := []string{"Boot2001", "Boot2002"}
	if got := firmwareFacts(dir).BootOrder; !slices.Equal(got, want) {
		t.Errorf("BootOrder is %v, want %v", got, want)
	}
}

func TestFirmwareFactsOnABIOSMachine(t *testing.T) {
	fw := firmwareFacts(filepath.Join(t.TempDir(), "nonexistent"))
	if fw.Mode != machine.FirmwareBIOS {
		t.Errorf("mode is %q, want BIOS", fw.Mode)
	}
	if fw.BootCurrent != "" || fw.BootNext != "" || fw.BootOrder != nil {
		t.Errorf("the facts are %+v, want no boot variables on a BIOS machine", fw)
	}
}

func TestReadEFIVarStripsAttributes(t *testing.T) {
	dir := fakeEFIVars(t, map[string][]byte{"BootCurrent": {0xAA, 0xBB}})
	b, err := readEFIVar(dir, "BootCurrent")
	if err != nil {
		t.Fatalf("reading BootCurrent: %v", err)
	}
	if len(b) != 2 || b[0] != 0xAA || b[1] != 0xBB {
		t.Errorf("payload is % X, want AA BB", b)
	}
}

func TestWriteEFIVarRoundTrips(t *testing.T) {
	dir := t.TempDir()
	if err := writeEFIVar(dir, "BootNext", u16le(3)); err != nil {
		t.Fatalf("writing BootNext: %v", err)
	}
	b, err := readEFIVar(dir, "BootNext")
	if err != nil {
		t.Fatalf("reading BootNext: %v", err)
	}
	if len(b) != 2 || binary.LittleEndian.Uint16(b) != 3 {
		t.Errorf("payload is % X, want 03 00", b)
	}
	if err := writeEFIVar(dir, "BootNext", u16le(7)); err != nil {
		t.Fatalf("writing BootNext again: %v", err)
	}
	if b, _ = readEFIVar(dir, "BootNext"); binary.LittleEndian.Uint16(b) != 7 {
		t.Errorf("payload after the second write is % X, want 07 00", b)
	}
}

func TestSetBootEntryReusesTheNumberOfAnEntryWithTheSameDescription(t *testing.T) {
	dir := fakeEFIVars(t, map[string][]byte{
		"Boot0000": encodeLoadOption(loadOption{description: "BootManagerMenuApp"}),
		"Boot0001": encodeLoadOption(loadOption{description: "liken slot A"}),
	})
	n, err := setBootEntry(dir, loadOption{description: "liken slot A", filePath: `\vmlinuz`})
	if err != nil {
		t.Fatalf("setting the entry for slot A: %v", err)
	}
	if n != 1 {
		t.Errorf("slot A was written to Boot%04X, want Boot0001", n)
	}
	n, err = setBootEntry(dir, loadOption{description: "liken slot B"})
	if err != nil {
		t.Fatalf("setting the entry for slot B: %v", err)
	}
	if n != 2 {
		t.Errorf("slot B was written to Boot%04X, want Boot0002", n)
	}
	if got := listBootEntries(dir)[0].description; got != "BootManagerMenuApp" {
		t.Errorf("the description of Boot0000 is %q, want it unchanged", got)
	}
}

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
	got := capturedStdout(t, reportFirmware)
	want := "liken: firmware: UEFI\n" +
		"liken: firmware: booted via Boot0002 (liken slot A)\n" +
		"liken: firmware: BootNext is Boot0002 (liken slot A, pinned at the proven slot)\n" +
		"liken: firmware: boot order: Boot0002 (liken slot A)\n"
	if got != want {
		t.Errorf("reportFirmware printed\n%s\nwant\n%s", got, want)
	}
}

func TestReportFirmwareBIOS(t *testing.T) {
	old := efiVarsDir
	efiVarsDir = filepath.Join(t.TempDir(), "no-efivars")
	t.Cleanup(func() { efiVarsDir = old })
	// The BIOS facts read liken.slot from the command line, so the test
	// gives one with no slot in place of the workstation's.
	fakeCmdline(t, "console=ttyS0\n")
	if got := capturedStdout(t, reportFirmware); !strings.HasPrefix(got, "liken: firmware: BIOS, because /sys/firmware/efi does not exist") {
		t.Errorf("reportFirmware printed %q, want the line that names BIOS and its reason", got)
	}
}

func TestReadEFIVarRejectsATruncatedVariable(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "BootNext-"+efiGlobalVariable), []byte{1, 2}, 0o644); err != nil {
		t.Fatalf("writing the truncated BootNext file: %v", err)
	}
	if _, err := readEFIVar(dir, "BootNext"); err == nil {
		t.Error("readEFIVar returned no error, want one for a variable shorter than its attribute word")
	}
}

func TestListBootEntriesSkipsEntriesThatDoNotDecode(t *testing.T) {
	dir := fakeEFIVars(t, map[string][]byte{
		"Boot0007": encodeLoadOption(loadOption{attributes: loadOptionActive, description: "liken slot A"}),
		"Boot0008": {0x01}, // too short for a load option header
	})
	if err := os.WriteFile(filepath.Join(dir, "SecureBoot-"+efiGlobalVariable), []byte{7, 0, 0, 0, 1}, 0o644); err != nil {
		t.Fatalf("writing the SecureBoot file: %v", err)
	}
	entries := listBootEntries(dir)
	if len(entries) != 1 || entries[7].description != "liken slot A" {
		t.Errorf("entries are %+v, want only Boot0007", entries)
	}
}

func TestWriteEFIVarReportsARefusedWrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "BootNext-"+efiGlobalVariable)
	if err := os.WriteFile(path, []byte{7, 0, 0, 0, 1, 0}, 0o444); err != nil {
		t.Fatalf("writing the BootNext file: %v", err)
	}
	if err := writeEFIVar(dir, "BootNext", u16le(3)); err == nil {
		t.Error("writeEFIVar returned no error, want one for a variable file that refuses the write")
	}
}

func TestListBootEntriesReturnsNothingWithoutAVariableStore(t *testing.T) {
	if entries := listBootEntries(filepath.Join(t.TempDir(), "no-efivars")); len(entries) != 0 {
		t.Errorf("entries are %+v, want none without a variable store", entries)
	}
}
