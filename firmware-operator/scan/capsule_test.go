package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"unicode/utf16"
)

// The names of the variables and files that fwupd 2.1.1 wrote for a
// capsule in NVRAM mode under OVMF (liken's milestone 33, the firmware
// under QEMU): an entry for its updater, BootNext, its status variable,
// the updater, and the capsule.
var (
	updaterEntry = "Boot0007-" + efiGlobalGUID
	bootNextFile = "BootNext-" + efiGlobalGUID
	bootOrder    = "BootOrder-" + efiGlobalGUID
	osIndication = "OsIndications-" + efiGlobalGUID
	statusVar    = "fwupd-9c13b7f1-d618-5d68-be61-6b17881014a7-0-" + fwupdVendorGUID
	updaterFile  = "EFI/ubuntu/fwupdx64.efi"
	capsuleFile  = "EFI/ubuntu/fw/fwupd-9c13b7f1-d618-5d68-be61-6b17881014a7.cap"
)

// loadOption builds an EFI_LOAD_OPTION that boots one file, as
// efivarfs stores it: the attributes of the variable, then the option.
func loadOption(description, file string) []byte {
	utf16z := func(s string) []byte {
		var b []byte
		for _, u := range append(utf16.Encode([]rune(s)), 0) {
			b = binary.LittleEndian.AppendUint16(b, u)
		}
		return b
	}
	path := utf16z(file)
	var list []byte
	list = append(list, 0x04, 0x04)
	list = binary.LittleEndian.AppendUint16(list, uint16(4+len(path)))
	list = append(list, path...)
	list = append(list, 0x7f, 0xff, 0x04, 0x00)
	option := []byte{0x07, 0, 0, 0, 0x01, 0, 0, 0}
	option = binary.LittleEndian.AppendUint16(option, uint16(len(list)))
	option = append(option, utf16z(description)...)
	return append(option, list...)
}

func u16(values ...uint16) []byte {
	b := []byte{0x07, 0, 0, 0}
	for _, v := range values {
		b = binary.LittleEndian.AppendUint16(b, v)
	}
	return b
}

// capsuleRig is a rehearsalRig for the System Firmware device, whose
// machine has two liken slot entries in BootOrder and the next boot's
// ESP with its fallback loader. Its install writes what fwupd wrote
// for a capsule in NVRAM mode, unless a test changes the effect.
type capsuleRig struct {
	*rehearsalRig
	esp string
}

func newCapsuleRig(t *testing.T) *capsuleRig {
	t.Helper()
	r := &capsuleRig{rehearsalRig: newRehearsalRig(t)}
	r.deviceID = capsuleID
	writeFile(t, filepath.Join(r.hostDir, "efi", "efivars", "Boot0002-"+efiGlobalGUID), loadOption("liken slot A", `\vmlinuz`))
	writeFile(t, filepath.Join(r.hostDir, "efi", "efivars", bootOrder), u16(2, 3))
	writeFile(t, filepath.Join(r.hostDir, "efi", "efivars", bootNextFile), u16(2))
	r.esp = t.TempDir()
	writeFile(t, filepath.Join(r.esp, "EFI", "BOOT", "BOOTX64.EFI"), []byte("liken's fallback loader"))
	writeFile(t, filepath.Join(r.esp, "vmlinuz"), []byte("a kernel the copy leaves out"))
	r.espSource = r.esp
	r.tool.effects = map[string]func(){"install": func() { r.nvramInstall(t) }}
	return r
}

func (r *capsuleRig) variable(t *testing.T, name string, data []byte) {
	t.Helper()
	writeFile(t, filepath.Join(r.copyDir, "efi", "efivars", name), data)
}

func (r *capsuleRig) file(t *testing.T, name string, data []byte) {
	t.Helper()
	writeFile(t, filepath.Join(r.espDir, filepath.FromSlash(name)), data)
}

func (r *capsuleRig) nvramInstall(t *testing.T) {
	r.variable(t, updaterEntry, loadOption("Linux Firmware Updater", `\EFI\ubuntu\fwupdx64.efi`))
	r.variable(t, bootNextFile, u16(7))
	r.variable(t, statusVar, []byte{0x07, 0, 0, 0, 7, 0, 0, 0})
	r.file(t, updaterFile, []byte("the updater"))
	r.file(t, capsuleFile, []byte("the capsule"))
}

func TestARehearsalPassesACapsule(t *testing.T) {
	r := newCapsuleRig(t)

	got := r.rehearse(context.Background())

	if got.Result != resultPassed {
		t.Fatalf("got %+v, want Passed", got)
	}
	var names []string
	for _, w := range got.Writes {
		names = append(names, w.Kind+" "+w.Name)
	}
	want := []string{"file " + capsuleFile, "file " + updaterFile, "variable " + updaterEntry, "variable " + bootNextFile, "variable " + statusVar}
	if !slices.Equal(names, want) {
		t.Errorf("the writes are %q, want %q", names, want)
	}
	if got.ESPFree != 65536 || got.Bytes != int64(len("the updater")+len("the capsule")) {
		t.Errorf("got %d bytes on an ESP with %d free, want the two files' bytes and the ESP's free space", got.Bytes, got.ESPFree)
	}
	if !strings.HasPrefix(got.Message, "the install stages a capsule with 3 variables and 2 files") {
		t.Errorf("the message is %q", got.Message)
	}
}

// The copy of the ESP holds its \EFI directory alone, which is where
// fwupd looks and writes, so a rehearsal does not copy a release.
func TestACapsuleRehearsalCopiesTheESPsEFIDirectory(t *testing.T) {
	r := newCapsuleRig(t)

	r.rehearse(context.Background())

	if _, err := os.Stat(filepath.Join(r.espDir, "EFI", "BOOT", "BOOTX64.EFI")); err != nil {
		t.Errorf("the copy lacks the ESP's EFI directory: %v", err)
	}
	if _, err := os.Stat(filepath.Join(r.espDir, "vmlinuz")); !os.IsNotExist(err) {
		t.Errorf("the copy holds the release's kernel: %v", err)
	}
}

func TestARehearsalPassesACapsuleOnDisk(t *testing.T) {
	r := newCapsuleRig(t)
	r.tool.effects["install"] = func() {
		r.variable(t, osIndication, []byte{0x07, 0, 0, 0, 0x04, 0, 0, 0, 0, 0, 0, 0})
		r.file(t, "EFI/UpdateCapsule/fwupd-9c13b7f1.cap", []byte("the capsule"))
	}

	got := r.rehearse(context.Background())

	if got.Result != resultPassed {
		t.Errorf("got %+v, want Passed", got)
	}
}

func TestARehearsalRefusesACapsuleThatWritesMore(t *testing.T) {
	cases := []struct {
		name   string
		writes func(t *testing.T, r *capsuleRig)
		want   string
	}{
		{"a second boot entry", func(t *testing.T, r *capsuleRig) {
			r.nvramInstall(t)
			r.variable(t, "Boot0008-"+efiGlobalGUID, loadOption("Linux Firmware Updater", `\EFI\ubuntu\fwupdx64.efi`))
		}, "the install created 2 boot entries, and a capsule needs one"},
		{"an entry that boots another program", func(t *testing.T, r *capsuleRig) {
			r.nvramInstall(t)
			r.variable(t, updaterEntry, loadOption("Other", `\EFI\other\grubx64.efi`))
		}, `the install created ` + updaterEntry + `, which boots \EFI\other\grubx64.efi, not fwupd's updater`},
		{"BootNext aimed elsewhere", func(t *testing.T, r *capsuleRig) {
			r.nvramInstall(t)
			r.variable(t, bootNextFile, u16(3))
		}, "the install set BootNext to Boot0003, not to the updater's entry"},
		{"a reordered BootOrder", func(t *testing.T, r *capsuleRig) {
			r.nvramInstall(t)
			r.variable(t, bootOrder, u16(7, 2, 3))
		}, "the install changed BootOrder by more than adding the updater's entry at its end"},
		{"a changed liken entry", func(t *testing.T, r *capsuleRig) {
			r.nvramInstall(t)
			r.variable(t, "Boot0002-"+efiGlobalGUID, loadOption("liken slot A", `\other`))
		}, "the install changed Boot0002-" + efiGlobalGUID + ", which a capsule does not write"},
		{"a removed variable", func(t *testing.T, r *capsuleRig) {
			r.nvramInstall(t)
			os.Remove(filepath.Join(r.copyDir, "efi", "efivars", "Boot0002-"+efiGlobalGUID))
		}, "the install removed Boot0002-" + efiGlobalGUID},
		{"another bit in OsIndications", func(t *testing.T, r *capsuleRig) {
			r.variable(t, osIndication, []byte{0x07, 0, 0, 0, 0x05, 0, 0, 0, 0, 0, 0, 0})
			r.file(t, "EFI/UpdateCapsule/fwupd-9c13b7f1.cap", []byte("the capsule"))
		}, "the install set OsIndications to 0x5, which asks for more than capsule-on-disk"},
		{"the fallback loader", func(t *testing.T, r *capsuleRig) {
			r.nvramInstall(t)
			r.file(t, "EFI/BOOT/BOOTX64.EFI", []byte("another loader"))
		}, "the install wrote EFI/BOOT/BOOTX64.EFI on the ESP, where a capsule writes nothing"},
		{"a shim", func(t *testing.T, r *capsuleRig) {
			r.nvramInstall(t)
			r.file(t, "EFI/ubuntu/shimx64.efi", []byte("a shim"))
		}, "the install wrote EFI/ubuntu/shimx64.efi on the ESP, where a capsule writes nothing"},
		{"nothing", func(*testing.T, *capsuleRig) {}, "the install wrote nothing"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := newCapsuleRig(t)
			r.tool.effects["install"] = func() { c.writes(t, r) }

			got := r.rehearse(context.Background())

			if got.Result != resultRefused || got.Message != c.want {
				t.Errorf("got %+v, want Refused: %s", got, c.want)
			}
		})
	}
}

// A machine whose store names no proven slot has no ESP for the next
// boot, so the operator mounts none, and no capsule can be staged.
func TestACapsuleRehearsalRefusesAMachineWithNoESPForTheNextBoot(t *testing.T) {
	r := newCapsuleRig(t)
	r.espSource = ""

	got := r.rehearse(context.Background())

	if got.Result != resultRefused || r.installed() {
		t.Errorf("got %+v after an install: %v, want Refused before the install", got, r.installed())
	}
}

func TestACapsuleRehearsalRefusesAnESPWithNoRoom(t *testing.T) {
	r := newCapsuleRig(t)
	r.free = func(path string) (int64, error) {
		if path == r.esp {
			return 10, nil
		}
		return 65536, nil
	}

	got := r.rehearse(context.Background())

	if got.Result != resultRefused || got.Message != "the capsule's files need 22 bytes on the ESP, and it has 10 free" {
		t.Errorf("got %+v, want Refused for the ESP's free space", got)
	}
}

func TestLoadOptionPathReadsTheFile(t *testing.T) {
	got, ok := loadOptionPath(loadOption("Linux Firmware Updater", `\EFI\ubuntu\fwupdx64.efi`)[4:])

	if !ok || got != `\EFI\ubuntu\fwupdx64.efi` {
		t.Errorf("got %q, %v", got, ok)
	}
	if _, ok := loadOptionPath(bytes.Repeat([]byte{0xff}, 3)); ok {
		t.Error("a truncated option has no path")
	}
}
