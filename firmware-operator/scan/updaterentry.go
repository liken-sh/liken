package main

// fwupd's install of a capsule in NVRAM mode writes a boot entry for
// its updater, whose device path names the ESP's partition with a hard
// drive node: its number, its start and size, and its GUID. fwupd reads
// those from udisks over D-Bus, and a pod has neither, so in a pod it
// writes a node of zeros, and the firmware cannot find the updater. The
// firmware then boots the next entry of BootOrder, and the capsule does
// not run.
//
// The updater then finds the capsule by the device path in fwupd's
// status variable, which has the same node of zeros.
//
// liken's init keeps an entry for each slot, whose hard drive node
// names the slot's partition in full. The ESP that fwupd wrote to is
// the proven slot's, so repairUpdaterEntry copies the node of liken's
// entry for the ESP's partition into the updater's entry and into the
// status variable. The rehearsal
// makes the same repair in its copy, so its record holds the same
// bytes that the apply writes.

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// partition is a partition's place on its disk, in 512-byte sectors, as
// sysfs gives it and a hard drive node holds it.
type partition struct {
	number uint32
	start  uint64
	size   uint64
}

// updateInfoHeaderSize is the length of the header of fwupd's status
// variable, FWUPD_UPDATE_INFO, before the capsule's device path: its
// version, the device's GUID, the capsule's flags, the hardware
// instance, the time of the attempt, and the status.
const updateInfoHeaderSize = 4 + 16 + 4 + 8 + 16 + 4

// hardDriveNodeSize is the length of a hard drive media node: the
// header, the number, the start, the size, the signature, and the two
// bytes of its format and signature type.
const hardDriveNodeSize = 42

// variableWriter replaces one variable file with data, which starts
// with the four bytes of attributes.
type variableWriter func(path string, data []byte) error

// plainVariableWriter writes a variable of a rehearsal's copy, which is
// a plain file.
func plainVariableWriter(path string, data []byte) error { return os.WriteFile(path, data, 0o644) }

// repairUpdaterEntry gives each entry that boots fwupd's updater, and
// each of fwupd's status variables, whose hard drive node names
// partition 0, the node of the entry that boots from esp. It changes
// nothing when no entry names esp's partition.
func repairUpdaterEntry(vars string, esp partition, write variableWriter) error {
	entries, err := readVariables(vars)
	if err != nil {
		return err
	}
	var source []byte
	for name, data := range entries {
		if !bootEntryVariable.MatchString(name) || len(data) < 4 {
			continue
		}
		if at, p, ok := hardDriveNode(data[4:]); ok && p == esp && p.number != 0 {
			source = data[4+at : 4+at+hardDriveNodeSize]
			break
		}
	}
	if source == nil {
		return nil
	}
	for name, data := range entries {
		var at int
		var p partition
		var ok bool
		switch {
		case bootEntryVariable.MatchString(name) && len(data) >= 4:
			file, named := loadOptionPath(data[4:])
			if !named || !isUpdater(file) {
				continue
			}
			at, p, ok = hardDriveNode(data[4:])
			at += 4
		case strings.HasPrefix(name, "fwupd-") && strings.HasSuffix(name, "-"+fwupdVendorGUID) && len(data) > 4+updateInfoHeaderSize:
			at, p, ok = hardDriveNodeIn(data[4+updateInfoHeaderSize:])
			at += 4 + updateInfoHeaderSize
		default:
			continue
		}
		if !ok || p.number != 0 {
			continue
		}
		fixed := bytes.Clone(data)
		copy(fixed[at:], source)
		if err := write(filepath.Join(vars, name), fixed); err != nil {
			return fmt.Errorf("repairing the partition of %s: %w", name, err)
		}
	}
	return nil
}

// hardDriveNode answers the offset in option of the first hard drive
// node of its device path, and the partition it names.
func hardDriveNode(option []byte) (int, partition, bool) {
	start, list, ok := devicePath(option)
	if !ok {
		return 0, partition{}, false
	}
	at, p, ok := hardDriveNodeIn(list)
	return start + at, p, ok
}

// hardDriveNodeIn answers the offset in a device path of its first hard
// drive node, and the partition it names.
func hardDriveNodeIn(list []byte) (int, partition, bool) {
	for at := 0; at+4 <= len(list); {
		nodeType, subType, length := list[at], list[at+1], int(binary.LittleEndian.Uint16(list[at+2:]))
		if length < 4 || at+length > len(list) {
			return 0, partition{}, false
		}
		if nodeType == 0x04 && subType == 0x01 && length == hardDriveNodeSize {
			n := list[at:]
			return at, partition{
				number: binary.LittleEndian.Uint32(n[4:]),
				start:  binary.LittleEndian.Uint64(n[8:]),
				size:   binary.LittleEndian.Uint64(n[16:]),
			}, true
		}
		at += length
	}
	return 0, partition{}, false
}

// devicePath answers the offset of an EFI_LOAD_OPTION's device path, and
// the path: the attributes, the path's length, and the description,
// which ends with a UTF-16 zero, come first.
func devicePath(option []byte) (int, []byte, bool) {
	if len(option) < 6 {
		return 0, nil, false
	}
	listLength := int(binary.LittleEndian.Uint16(option[4:]))
	i := 6
	for ; i+1 < len(option); i += 2 {
		if option[i] == 0 && option[i+1] == 0 {
			i += 2
			break
		}
	}
	if i+listLength > len(option) {
		return 0, nil, false
	}
	return i, option[i : i+listLength], true
}

// espPartition answers the partition mounted at mount: mountinfo names
// its block device's numbers, and sysfs under sys gives the partition.
func espPartition(mountinfo, sys, mount string) (partition, error) {
	for _, line := range strings.Split(mountinfo, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 5 || fields[4] != mount {
			continue
		}
		dir := filepath.Join(sys, "dev", "block", fields[2])
		var values [3]uint64
		for i, name := range []string{"partition", "start", "size"} {
			data, err := os.ReadFile(filepath.Join(dir, name))
			if err != nil {
				return partition{}, fmt.Errorf("reading the partition of %s: %w", mount, err)
			}
			if values[i], err = strconv.ParseUint(strings.TrimSpace(string(data)), 10, 64); err != nil {
				return partition{}, fmt.Errorf("reading the partition of %s: %w", mount, err)
			}
		}
		return partition{number: uint32(values[0]), start: values[1], size: values[2]}, nil
	}
	return partition{}, fmt.Errorf("%s is not a mount point", mount)
}

// mountedPartition answers the partition mounted at mount in this
// process's mount namespace.
func mountedPartition(mount string) (partition, error) {
	mountinfo, err := os.ReadFile("/proc/self/mountinfo")
	if err != nil {
		return partition{}, err
	}
	return espPartition(string(mountinfo), "/sys", mount)
}
