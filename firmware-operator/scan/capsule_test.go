package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
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
// fwupdtool install with a device ID plans the release for that device
// twice, and fwupd refuses the second install of a capsule, so the
// rehearsal names only the cabinet, as the apply does.
func TestACapsuleRehearsalInstallsThePinnedCabinetOnce(t *testing.T) {
	r := newCapsuleRig(t)

	r.rehearse(context.Background())

	want := []string{"install", "--assume-yes", "--no-reboot-check", filepath.Join(r.workDir, "update.cab")}
	if got := r.tool.calls[len(r.tool.calls)-1]; !slices.Equal(got, want) {
		t.Errorf("the last call is %q, want %q", got, want)
	}
}

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

// capsuleApplyRig is an applier on a capsuleRig's machine, which expects
// the writes that an identical machine's rehearsal recorded. Its
// installer plays fwupd on the machine itself: the real variable store
// and the ESP of the next boot. The signal arrives at once.
type capsuleApplyRig struct {
	applier
	rig      *capsuleRig
	installs int
	// install plays what fwupd writes on the machine.
	install func(t *testing.T)
	fail    error
	efivars *fakeStore
}

func newCapsuleApplyRig(t *testing.T) *capsuleApplyRig {
	t.Helper()
	recorded := newCapsuleRig(t).rehearse(context.Background())
	r := newCapsuleRig(t)
	a := &capsuleApplyRig{rig: r}
	a.install = func(t *testing.T) { a.nvramInstall(t) }
	a.efivars = &fakeStore{}
	a.applier = applier{rehearser: r.rehearser, appendSHA256: recorded.SHA256, store: a.efivars.store(),
		marker: filepath.Join(t.TempDir(), "prepared"),
		signal: func(context.Context) error { return nil },
		installer: func(_ context.Context, args ...string) ([]byte, []byte, error) {
			a.installs++
			a.install(t)
			if a.fail != nil {
				return nil, []byte("fwupdtool: failed to write\n"), a.fail
			}
			return nil, nil, nil
		}}
	return a
}

func (a *capsuleApplyRig) variable(t *testing.T, name string, data []byte) {
	writeFile(t, filepath.Join(a.rig.hostDir, "efi", "efivars", name), data)
}

func (a *capsuleApplyRig) file(t *testing.T, name string, data []byte) {
	writeFile(t, filepath.Join(a.rig.esp, filepath.FromSlash(name)), data)
}

func (a *capsuleApplyRig) nvramInstall(t *testing.T) {
	a.variable(t, updaterEntry, loadOption("Linux Firmware Updater", `\EFI\ubuntu\fwupdx64.efi`))
	a.variable(t, bootNextFile, u16(7))
	a.variable(t, statusVar, []byte{0x07, 0, 0, 0, 7, 0, 0, 0})
	a.file(t, updaterFile, []byte("the updater"))
	a.file(t, capsuleFile, []byte("the capsule"))
}

func (a *capsuleApplyRig) run() ApplyResult {
	var progress bytes.Buffer
	return a.apply(context.Background(), &progress)
}

func TestAnApplyStagesTheRehearsedCapsule(t *testing.T) {
	a := newCapsuleApplyRig(t)

	got := a.run()

	if got.Result != resultWritten || got.Message != "staged the capsule with 3 variables and 2 files, as rehearsed; BootNext names Boot0007" {
		t.Errorf("got %+v, want Written", got)
	}
	if held, _ := os.ReadFile(filepath.Join(a.rig.esp, capsuleFile)); string(held) != "the capsule" {
		t.Errorf("the ESP holds %q, want the capsule", held)
	}
}

// fwupd writes the machine itself, so the apply checks what it wrote
// against the rehearsal afterward. Writes that differ, or a failure
// after a write, leave a staged capsule nobody vouched for, so the
// apply answers Uncertain: the turn reboots with liken's own BootNext,
// and the capsule does not run.
func TestAnApplyThatDivergesFromTheRehearsal(t *testing.T) {
	cases := []struct {
		name    string
		install func(a *capsuleApplyRig, t *testing.T)
		fail    error
		result  string
		message string
	}{
		{"another capsule", func(a *capsuleApplyRig, t *testing.T) {
			a.nvramInstall(t)
			a.file(t, capsuleFile, []byte("another capsule"))
		}, nil, resultUncertain, "fwupd's writes differ from the rehearsal's"},
		{"a shim", func(a *capsuleApplyRig, t *testing.T) {
			a.nvramInstall(t)
			a.file(t, "EFI/ubuntu/shimx64.efi", []byte("a shim"))
		}, nil, resultUncertain, "the install wrote EFI/ubuntu/shimx64.efi on the ESP, where a capsule writes nothing"},
		{"a failure after a write", func(a *capsuleApplyRig, t *testing.T) {
			a.variable(t, updaterEntry, loadOption("Linux Firmware Updater", `\EFI\ubuntu\fwupdx64.efi`))
		}, errExit2, resultUncertain, "fwupdtool install: fwupdtool: failed to write"},
		{"a failure that wrote nothing", func(*capsuleApplyRig, *testing.T) {}, errExit2, resultFailed,
			"fwupdtool install: fwupdtool: failed to write, and it wrote nothing"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			a := newCapsuleApplyRig(t)
			a.install = func(t *testing.T) { c.install(a, t) }
			a.fail = c.fail

			got := a.run()

			if got.Result != c.result || !strings.Contains(got.Message, c.message) {
				t.Errorf("got %+v, want %s with %q", got, c.result, c.message)
			}
		})
	}
}

func TestACapsulesApplyInstallsOnlyAfterTheSignal(t *testing.T) {
	a := newCapsuleApplyRig(t)
	a.signal = func(context.Context) error {
		if a.installs != 0 {
			t.Error("fwupd installed on the machine before the signal")
		}
		return errors.New("the operator withdrew the turn")
	}

	got := a.run()

	if got.Result != resultFailed || a.installs != 0 {
		t.Errorf("got %+v after %d installs, want Failed with none", got, a.installs)
	}
}

// fwupd's capsule install reads the files directly under efi/, such as
// fw_platform_size, which says whether the firmware runs 64-bit code,
// so the copy holds them beside the variables, the ESRT, and the DMI
// tables.
func TestTheCopyHoldsTheEFIDirectorysOwnFiles(t *testing.T) {
	host, copy := t.TempDir(), filepath.Join(t.TempDir(), "fw")
	writeFile(t, filepath.Join(host, "efi", "efivars", "BootOrder-"+efiGlobalGUID), []byte{7, 0, 0, 0})
	writeFile(t, filepath.Join(host, "efi", "fw_platform_size"), []byte("64\n"))

	if err := copyFirmware(host, copy); err != nil {
		t.Fatal(err)
	}

	if held, err := os.ReadFile(filepath.Join(copy, "efi", "fw_platform_size")); err != nil || string(held) != "64\n" {
		t.Errorf("the copy's fw_platform_size reads %q, %v", held, err)
	}
}

// fwupd keeps its updater's entry after an update, and a later install
// writes the same entry again and names it in BootNext.
func TestARehearsalPassesACapsuleOverAnEarlierUpdatersEntry(t *testing.T) {
	r := newCapsuleRig(t)
	writeFile(t, filepath.Join(r.hostDir, "efi", "efivars", "Boot0008-"+efiGlobalGUID), loadOption("Linux Firmware Updater", `\EFI\ubuntu\fwupdx64.efi`))
	r.tool.effects["install"] = func() {
		r.variable(t, "Boot0008-"+efiGlobalGUID, loadOption("Linux Firmware Updater (again)", `\EFI\ubuntu\fwupdx64.efi`))
		r.variable(t, bootNextFile, u16(8))
		r.file(t, capsuleFile, []byte("the capsule"))
	}

	got := r.rehearse(context.Background())

	if got.Result != resultPassed {
		t.Errorf("got %+v, want Passed", got)
	}
}

// fwupd's status variable from an earlier update carries efivarfs's
// immutable flag, and fwupd cannot open it to write it again, so the
// apply clears the flag on fwupd's own variables before the install.
func TestAnApplyLetsFwupdWriteItsEarlierStatusVariable(t *testing.T) {
	a := newCapsuleApplyRig(t)
	a.variable(t, statusVar, []byte{0x07, 0, 0, 0, 3, 0, 0, 0})
	a.efivars.held = immutableFlag
	var flagsAtInstall []string
	install := a.install
	a.install = func(t *testing.T) { flagsAtInstall = slices.Clone(a.efivars.calls); install(t) }

	a.run()

	if !slices.Equal(flagsAtInstall, []string{"flags", "setFlags 0x0"}) {
		t.Errorf("before the install the store saw %q, want the flag of fwupd's variable cleared", flagsAtInstall)
	}
}

func TestAnApplyThatCannotReleaseFwupdsVariableWritesNothing(t *testing.T) {
	for _, fail := range []map[string]error{{"flags": errors.New("no inode")}, {"setFlags": errors.New("not permitted")}} {
		a := newCapsuleApplyRig(t)
		a.variable(t, statusVar, []byte{0x07, 0, 0, 0, 3, 0, 0, 0})
		a.efivars.held, a.efivars.fail = immutableFlag, fail

		got := a.run()

		if got.Result != resultFailed || a.installs != 0 {
			t.Errorf("with %v got %+v after %d installs, want Failed before the install", fail, got, a.installs)
		}
	}
}
