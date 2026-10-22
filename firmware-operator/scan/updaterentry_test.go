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

// hdNode builds a hard drive media device path node of a GPT partition.
func hdNode(number uint32, start, size uint64, guid byte) []byte {
	n := []byte{0x04, 0x01, 42, 0}
	n = binary.LittleEndian.AppendUint32(n, number)
	n = binary.LittleEndian.AppendUint64(n, start)
	n = binary.LittleEndian.AppendUint64(n, size)
	n = append(n, bytes.Repeat([]byte{guid}, 16)...)
	return append(n, 0x02, 0x02)
}

// loadOptionOn builds an EFI_LOAD_OPTION, as efivarfs stores it, that
// boots file on the partition that hd names.
func loadOptionOn(description string, hd []byte, file string) []byte {
	utf16z := func(s string) []byte {
		var b []byte
		for _, u := range append(utf16.Encode([]rune(s)), 0) {
			b = binary.LittleEndian.AppendUint16(b, u)
		}
		return b
	}
	path := utf16z(file)
	list := append([]byte{}, hd...)
	list = append(list, 0x04, 0x04)
	list = binary.LittleEndian.AppendUint16(list, uint16(4+len(path)))
	list = append(list, path...)
	list = append(list, 0x7f, 0xff, 0x04, 0x00)
	option := []byte{0x07, 0, 0, 0, 0x01, 0, 0, 0}
	option = binary.LittleEndian.AppendUint16(option, uint16(len(list)))
	option = append(option, utf16z(description)...)
	return append(option, list...)
}

// slotAPartition is slot A's partition as sysfs gives it for the ESP's
// mount: number 3, start 135168, size 2097152, in 512-byte sectors.
var slotAPartition = partition{number: 3, start: 135168, size: 2097152}

// fwupd in a pod finds no partition for the ESP, so it writes an empty
// hard drive node. The updater's entry then takes the node of liken's
// entry for the same partition, which carries the partition's GUID.
func TestTheUpdatersEntryTakesTheESPsPartition(t *testing.T) {
	vars := t.TempDir()
	slotA := loadOptionOn("liken slot A", hdNode(3, 135168, 2097152, 0xaa), `\vmlinuz`)
	slotB := loadOptionOn("liken slot B", hdNode(4, 2232320, 2097152, 0xbb), `\vmlinuz`)
	updater := loadOptionOn("Linux Firmware Updater", hdNode(0, 0, 0, 0), `\EFI\ubuntu\fwupdx64.efi`)
	for name, data := range map[string][]byte{"Boot0002": slotA, "Boot0003": slotB, "Boot0008": updater} {
		writeFile(t, filepath.Join(vars, name+"-"+efiGlobalGUID), data)
	}

	if err := repairUpdaterEntry(vars, slotAPartition, plainVariableWriter); err != nil {
		t.Fatal(err)
	}

	got, _ := os.ReadFile(filepath.Join(vars, "Boot0008-"+efiGlobalGUID))
	want := loadOptionOn("Linux Firmware Updater", hdNode(3, 135168, 2097152, 0xaa), `\EFI\ubuntu\fwupdx64.efi`)
	if !bytes.Equal(got, want) {
		t.Errorf("the updater's entry is % x\nwant % x", got, want)
	}
}

// An entry that names its partition already is fwupd's own, and a
// store with no entry of the ESP's partition gives nothing to copy.
func TestARepairChangesNothingItCannotImprove(t *testing.T) {
	cases := []struct {
		name    string
		updater []byte
		slot    []byte
	}{
		{"an entry with its partition", loadOptionOn("Linux Firmware Updater", hdNode(3, 135168, 2097152, 0xcc), `\EFI\ubuntu\fwupdx64.efi`),
			loadOptionOn("liken slot A", hdNode(3, 135168, 2097152, 0xaa), `\vmlinuz`)},
		{"no entry of the ESP's partition", loadOptionOn("Linux Firmware Updater", hdNode(0, 0, 0, 0), `\EFI\ubuntu\fwupdx64.efi`),
			loadOptionOn("liken slot B", hdNode(4, 2232320, 2097152, 0xbb), `\vmlinuz`)},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			vars := t.TempDir()
			writeFile(t, filepath.Join(vars, "Boot0002-"+efiGlobalGUID), c.slot)
			writeFile(t, filepath.Join(vars, "Boot0008-"+efiGlobalGUID), c.updater)

			if err := repairUpdaterEntry(vars, slotAPartition, plainVariableWriter); err != nil {
				t.Fatal(err)
			}

			if got, _ := os.ReadFile(filepath.Join(vars, "Boot0008-"+efiGlobalGUID)); !bytes.Equal(got, c.updater) {
				t.Error("the updater's entry changed")
			}
		})
	}
}

func TestESPPartitionReadsSysfs(t *testing.T) {
	sys := t.TempDir()
	for name, value := range map[string]string{"partition": "3\n", "start": "135168\n", "size": "2097152\n"} {
		writeFile(t, filepath.Join(sys, "dev", "block", "252:35", name), []byte(value))
	}
	mountinfo := "29 1 252:33 / / rw - ext4 /dev/vdc1 rw\n" +
		"612 590 252:35 / /run/esp-next rw,nosuid - vfat /dev/vdc3 rw\n"

	got, err := espPartition(mountinfo, sys, "/run/esp-next")

	if err != nil || got != (partition{number: 3, start: 135168, size: 2097152}) {
		t.Errorf("got %+v, %v", got, err)
	}
}

// statusVariable builds fwupd's status variable, as efivarfs stores it:
// the 52-byte header of its update info, then the device path of the
// capsule's file.
func statusVariable(hd []byte, file string) []byte {
	option := loadOptionOn("", hd, file)
	_, list, _ := devicePath(option[4:])
	v := []byte{0x07, 0, 0, 0}
	v = append(v, bytes.Repeat([]byte{0x11}, 52)...)
	return append(v, list...)
}

// fwupd's updater finds the capsule by the device path in fwupd's status
// variable, which fwupd in a pod also writes with a hard drive node of
// zeros.
func TestTheStatusVariableTakesTheESPsPartition(t *testing.T) {
	vars := t.TempDir()
	writeFile(t, filepath.Join(vars, "Boot0002-"+efiGlobalGUID), loadOptionOn("liken slot A", hdNode(3, 135168, 2097152, 0xaa), `\vmlinuz`))
	status := "fwupd-b0792605-c842-408f-adf2-edfb07638e83-0-" + fwupdVendorGUID
	writeFile(t, filepath.Join(vars, status), statusVariable(hdNode(0, 0, 0, 0), `\EFI\ubuntu\fw\fwupd-b0792605.cap`))

	if err := repairUpdaterEntry(vars, slotAPartition, plainVariableWriter); err != nil {
		t.Fatal(err)
	}

	got, _ := os.ReadFile(filepath.Join(vars, status))
	if want := statusVariable(hdNode(3, 135168, 2097152, 0xaa), `\EFI\ubuntu\fw\fwupd-b0792605.cap`); !bytes.Equal(got, want) {
		t.Errorf("the status variable is % x\nwant % x", got, want)
	}
}

func TestESPPartitionFails(t *testing.T) {
	sys := t.TempDir()
	writeFile(t, filepath.Join(sys, "dev", "block", "252:36", "partition"), []byte("three\n"))
	cases := []struct {
		name      string
		mountinfo string
		want      string
	}{
		{"no mount", "29 1 252:33 / / rw - ext4 /dev/vdc1 rw\n", "/run/esp-next is not a mount point"},
		{"no sysfs entry", "612 590 252:35 / /run/esp-next rw - vfat /dev/vdc3 rw\n", "reading the partition of /run/esp-next"},
		{"a number that does not parse", "612 590 252:36 / /run/esp-next rw - vfat /dev/vdc4 rw\n", "reading the partition of /run/esp-next"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := espPartition(c.mountinfo, sys, "/run/esp-next")

			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Errorf("got %v, want %q", err, c.want)
			}
		})
	}
}

func TestAMalformedDevicePathNamesNoPartition(t *testing.T) {
	for _, list := range [][]byte{{0x04, 0x01, 0x02, 0x00}, {0x04, 0x01, 0xff, 0x00}} {
		if _, _, ok := hardDriveNodeIn(list); ok {
			t.Errorf("% x names a partition", list)
		}
	}
}

// The rehearsal and the apply read the ESP's partition through
// partitionOf, and a failure to read it fails the rehearsal.
func TestARehearsalRepairsTheUpdatersEntry(t *testing.T) {
	r := newCapsuleRig(t)
	writeFile(t, filepath.Join(r.hostDir, "efi", "efivars", "Boot0002-"+efiGlobalGUID), loadOptionOn("liken slot A", hdNode(3, 135168, 2097152, 0xaa), `\vmlinuz`))
	r.tool.effects["install"] = func() {
		r.variable(t, updaterEntry, loadOptionOn("Linux Firmware Updater", hdNode(0, 0, 0, 0), `\EFI\ubuntu\fwupdx64.efi`))
		r.variable(t, bootNextFile, u16(7))
	}
	r.partitionOf = func(string) (partition, error) { return slotAPartition, nil }

	got := r.rehearse(context.Background())

	held, _ := os.ReadFile(filepath.Join(r.copyDir, "efi", "efivars", updaterEntry))
	if want := loadOptionOn("Linux Firmware Updater", hdNode(3, 135168, 2097152, 0xaa), `\EFI\ubuntu\fwupdx64.efi`); got.Result != resultPassed || !bytes.Equal(held, want) {
		t.Errorf("got %+v with the entry % x, want Passed with the entry repaired", got, held)
	}

	r = newCapsuleRig(t)
	r.partitionOf = func(string) (partition, error) { return partition{}, errors.New("/run/esp-next is not a mount point") }
	if got := r.rehearse(context.Background()); got.Result != resultFailed || got.Message != "/run/esp-next is not a mount point" {
		t.Errorf("got %+v, want Failed for the partition", got)
	}
}

// efivarfs takes a whole variable in one write, with the immutable flag
// cleared for it.
func TestReplaceWritesTheWholeVariable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "Boot0008-"+efiGlobalGUID)
	writeFile(t, path, []byte{7, 0, 0, 0, 1})
	efivars := &fakeStore{held: immutableFlag}

	if err := efivars.store().replace(path, []byte{7, 0, 0, 0, 2, 3}); err != nil {
		t.Fatal(err)
	}

	if held, _ := os.ReadFile(path); !bytes.Equal(held, []byte{7, 0, 0, 0, 2, 3}) {
		t.Errorf("the variable reads % x", held)
	}
	if want := []string{"flags", "setFlags 0x0", "setFlags 0x10"}; !slices.Equal(efivars.calls, want) {
		t.Errorf("the calls are %q, want %q", efivars.calls, want)
	}
	efivars.fail = map[string]error{"flags": errors.New("no inode")}
	if err := efivars.store().replace(path, nil); err == nil {
		t.Error("a store whose flags cannot be read took the write")
	}
}
