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

// slotAPartition is slot A's partition as sysfs reports it for the
// ESP's mount. Start and size are in 512-byte sectors.
var slotAPartition = partition{number: 3, start: 135168, size: 2097152}

// fwupd in a pod writes a hard drive node of zeros. The repair copies
// the node of the liken entry for the same partition, including the
// partition's GUID.
func TestRepairCopiesTheESPsPartitionNodeIntoTheUpdatersEntry(t *testing.T) {
	vars := t.TempDir()
	slotA := loadOptionOn("liken slot A", hdNode(3, 135168, 2097152, 0xaa), `\vmlinuz`)
	slotB := loadOptionOn("liken slot B", hdNode(4, 2232320, 2097152, 0xbb), `\vmlinuz`)
	updater := loadOptionOn("Linux Firmware Updater", hdNode(0, 0, 0, 0), `\EFI\ubuntu\fwupdx64.efi`)
	for name, data := range map[string][]byte{"Boot0002": slotA, "Boot0003": slotB, "Boot0008": updater} {
		writeFile(t, filepath.Join(vars, name+"-"+efiGlobalGUID), data)
	}

	if err := repairUpdaterEntry(vars, slotAPartition, plainVariableWriter); err != nil {
		t.Fatalf("repairing the updater's entry: %v", err)
	}

	got, _ := os.ReadFile(filepath.Join(vars, "Boot0008-"+efiGlobalGUID))
	want := loadOptionOn("Linux Firmware Updater", hdNode(3, 135168, 2097152, 0xaa), `\EFI\ubuntu\fwupdx64.efi`)
	if !bytes.Equal(got, want) {
		t.Errorf("the updater's entry is % x\nwant % x", got, want)
	}
}

// An updater entry that already has a partition node needs no repair,
// and a store with no entry for the ESP's partition has no node to
// copy.
func TestRepairLeavesTheUpdatersEntryUnchangedWhenItCannotRepairIt(t *testing.T) {
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
				t.Fatalf("repairing the updater's entry: %v", err)
			}

			if got, _ := os.ReadFile(filepath.Join(vars, "Boot0008-"+efiGlobalGUID)); !bytes.Equal(got, c.updater) {
				t.Error("the updater's entry changed, want it unchanged")
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
		t.Errorf("espPartition = %+v, %v, want partition 3 at 135168 with size 2097152", got, err)
	}
}

// statusVariable builds fwupd's status variable as efivarfs stores it:
// the 4 bytes of attributes, the 52-byte update info header, then the
// device path of the capsule's file.
func statusVariable(hd []byte, file string) []byte {
	option := loadOptionOn("", hd, file)
	_, list, _ := devicePath(option[4:])
	v := []byte{0x07, 0, 0, 0}
	v = append(v, bytes.Repeat([]byte{0x11}, 52)...)
	return append(v, list...)
}

// fwupd's updater finds the capsule through the device path in fwupd's
// status variable, and fwupd in a pod writes a hard drive node of zeros
// there too.
func TestRepairCopiesTheESPsPartitionNodeIntoTheStatusVariable(t *testing.T) {
	vars := t.TempDir()
	writeFile(t, filepath.Join(vars, "Boot0002-"+efiGlobalGUID), loadOptionOn("liken slot A", hdNode(3, 135168, 2097152, 0xaa), `\vmlinuz`))
	status := "fwupd-b0792605-c842-408f-adf2-edfb07638e83-0-" + fwupdVendorGUID
	writeFile(t, filepath.Join(vars, status), statusVariable(hdNode(0, 0, 0, 0), `\EFI\ubuntu\fw\fwupd-b0792605.cap`))

	if err := repairUpdaterEntry(vars, slotAPartition, plainVariableWriter); err != nil {
		t.Fatalf("repairing the updater's entry: %v", err)
	}

	got, _ := os.ReadFile(filepath.Join(vars, status))
	if want := statusVariable(hdNode(3, 135168, 2097152, 0xaa), `\EFI\ubuntu\fw\fwupd-b0792605.cap`); !bytes.Equal(got, want) {
		t.Errorf("the status variable is % x\nwant % x", got, want)
	}
}

func TestESPPartitionFails(t *testing.T) {
	sys := t.TempDir()
	writeFile(t, filepath.Join(sys, "dev", "block", "252:36", "partition"), []byte("three\n"))
	for name, value := range map[string]string{"partition": "4294967297\n", "start": "135168\n", "size": "2097152\n"} {
		writeFile(t, filepath.Join(sys, "dev", "block", "252:37", name), []byte(value))
	}
	cases := []struct {
		name      string
		mountinfo string
		want      string
	}{
		{"no mount", "29 1 252:33 / / rw - ext4 /dev/vdc1 rw\n", "/run/esp-next is not a mount point"},
		{"no sysfs entry", "612 590 252:35 / /run/esp-next rw - vfat /dev/vdc3 rw\n", "the partition mounted at /run/esp-next"},
		{"a number that does not parse", "612 590 252:36 / /run/esp-next rw - vfat /dev/vdc4 rw\n", "the partition mounted at /run/esp-next"},
		{"a number past a uint32", "612 590 252:37 / /run/esp-next rw - vfat /dev/vdc5 rw\n", "the largest number of a GPT entry"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := espPartition(c.mountinfo, sys, "/run/esp-next")

			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Errorf("the error is %v, want one that contains %q", err, c.want)
			}
		})
	}
}

func TestHardDriveNodeInRejectsAMalformedDevicePath(t *testing.T) {
	for _, list := range [][]byte{{0x04, 0x01, 0x02, 0x00}, {0x04, 0x01, 0xff, 0x00}} {
		if _, _, ok := hardDriveNodeIn(list); ok {
			t.Errorf("hardDriveNodeIn accepted the malformed device path % x", list)
		}
	}
}

// The rehearsal reads the ESP's partition through partitionOf, and a
// failure to read it fails the rehearsal.
func TestARehearsalRepairsTheUpdatersEntry(t *testing.T) {
	r := newCapsuleRig(t)
	writeFile(t, filepath.Join(r.hostDir, "efi", "efivars", "Boot0002-"+efiGlobalGUID), loadOptionOn("liken slot A", hdNode(3, 135168, 2097152, 0xaa), `\vmlinuz`))
	r.tool.effects["install"] = func() {
		r.variable(t, updaterEntry, loadOptionOn("Linux Firmware Updater", hdNode(0, 0, 0, 0), `\EFI\ubuntu\fwupdx64.efi`))
		r.variable(t, bootNextFile, u16(7))
	}
	r.partitionOf = func(string) (partition, error) { return slotAPartition, nil }

	got := r.rehearse(context.Background())

	entry, _ := os.ReadFile(filepath.Join(r.copyDir, "efi", "efivars", updaterEntry))
	if want := loadOptionOn("Linux Firmware Updater", hdNode(3, 135168, 2097152, 0xaa), `\EFI\ubuntu\fwupdx64.efi`); got.Result != resultPassed || !bytes.Equal(entry, want) {
		t.Errorf("the rehearsal is %+v with the entry % x, want Passed with the partition node repaired", got, entry)
	}

	r = newCapsuleRig(t)
	r.partitionOf = func(string) (partition, error) { return partition{}, errors.New("/run/esp-next is not a mount point") }
	if got := r.rehearse(context.Background()); got.Result != resultFailed || got.Message != "/run/esp-next is not a mount point" {
		t.Errorf("the rehearsal is %+v, want Failed with the message that the ESP is not a mount point", got)
	}
}

// efivarfs accepts a variable only as one whole write, and only while
// the immutable flag is clear, so replace clears the flag first and
// sets it again afterward.
func TestReplaceWritesTheWholeVariable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "Boot0008-"+efiGlobalGUID)
	writeFile(t, path, []byte{7, 0, 0, 0, 1})
	efivars := &fakeStore{flags: immutableFlag}

	if err := efivars.store().replace(path, []byte{7, 0, 0, 0, 2, 3}); err != nil {
		t.Fatalf("replacing the variable: %v", err)
	}

	if got, _ := os.ReadFile(path); !bytes.Equal(got, []byte{7, 0, 0, 0, 2, 3}) {
		t.Errorf("the variable is % x, want 07 00 00 00 02 03", got)
	}
	if want := []string{"flags", "setFlags 0x0", "setFlags 0x10"}; !slices.Equal(efivars.calls, want) {
		t.Errorf("the calls are %q, want %q", efivars.calls, want)
	}
	efivars.fail = map[string]error{"flags": errors.New("no inode")}
	if err := efivars.store().replace(path, nil); err == nil {
		t.Error("replace returned no error when the flags cannot be read, want one")
	}
}
