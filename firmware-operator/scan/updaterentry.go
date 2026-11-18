package main

// When fwupd installs a capsule in NVRAM mode, it writes a boot entry
// for its updater. The entry's device path names the ESP's partition
// with a hard drive node: the partition number, start, size, and GUID.
// fwupd reads those from udisks over D-Bus, and a pod has no udisks, so
// in a pod fwupd writes a node of zeros. The firmware cannot find the
// updater through that entry, so it boots the next entry in BootOrder
// and the capsule does not run. The updater also locates the capsule
// through the device path in fwupd's status variable, and that path has
// the same node of zeros.
//
// Each liken slot has a boot entry whose hard drive node describes the
// slot's partition completely. fwupd writes to the ESP of the next
// boot, so repairUpdaterEntry copies the node of the boot entry for
// that partition into the updater's entry and into the status variable.
// The rehearsal makes the same repair in its copy of the variables, so
// the bytes it records are the bytes the apply writes.

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// partition is a partition's number and its start and size in 512-byte
// sectors, as sysfs reports them and a hard drive node stores them.
type partition struct {
	number uint32
	start  uint64
	size   uint64
}

// updateInfoHeaderSize is the length of the fixed header of fwupd's
// status variable (FWUPD_UPDATE_INFO), before the capsule's device path:
// the version, the device's GUID, the capsule's flags, the hardware
// instance, the time of the attempt, and the status.
const updateInfoHeaderSize = 4 + 16 + 4 + 8 + 16 + 4

// hardDriveNodeSize is the length of a hard drive media device path
// node: the 4-byte node header, the partition number, start, and size,
// the 16-byte signature, and one byte each for the partition format and
// the signature type.
const hardDriveNodeSize = 42

// variableWriter replaces the variable file at path with data. The
// first four bytes of data are the variable's attributes.
type variableWriter func(path string, data []byte) error

// plainVariableWriter writes a variable in a rehearsal's copy, where
// each variable is an ordinary file.
func plainVariableWriter(path string, data []byte) error { return os.WriteFile(path, data, 0o644) }

// repairUpdaterEntry replaces the hard drive node of zeros in each boot
// entry for fwupd's updater, and in each of fwupd's status variables,
// with the node of a boot entry for esp's partition. It changes
// nothing when no boot entry has that partition.
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
			return fmt.Errorf("writing the repaired partition node to %s: %w", name, err)
		}
	}
	return nil
}

// hardDriveNode returns the offset in option of the first hard drive
// node in its device path, and the partition that node describes. It
// returns false when the path has no such node.
func hardDriveNode(option []byte) (int, partition, bool) {
	start, list, ok := devicePath(option)
	if !ok {
		return 0, partition{}, false
	}
	at, p, ok := hardDriveNodeIn(list)
	return start + at, p, ok
}

// hardDriveNodeIn returns the offset in a device path of its first hard
// drive node, and the partition that node describes. It returns false
// when the path has no such node or is malformed.
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

// devicePath returns the device path of an EFI_LOAD_OPTION and its
// offset in option. The path follows the 4-byte attributes, the 2-byte
// path length, and the description, which ends with a UTF-16 zero.
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

// espPartition returns the partition mounted at mount. It finds the
// mount's block device number in mountinfo, then reads the partition's
// number, start, and size from the sysfs tree at sys.
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
				return partition{}, fmt.Errorf("reading the %s of the partition mounted at %s: %w", name, mount, err)
			}
			if values[i], err = strconv.ParseUint(strings.TrimSpace(string(data)), 10, 64); err != nil {
				return partition{}, fmt.Errorf("parsing the %s of the partition mounted at %s: %w", name, mount, err)
			}
		}
		number := values[0]
		if number > math.MaxUint32 {
			return partition{}, fmt.Errorf("the partition number %d of %s is above %d, the largest number of a GPT entry", number, mount, uint32(math.MaxUint32))
		}
		return partition{number: uint32(number), start: values[1], size: values[2]}, nil
	}
	return partition{}, fmt.Errorf("%s is not a mount point", mount)
}

// mountedPartition returns the partition mounted at mount in this
// process's mount namespace.
func mountedPartition(mount string) (partition, error) {
	mountinfo, err := os.ReadFile("/proc/self/mountinfo")
	if err != nil {
		return partition{}, err
	}
	return espPartition(string(mountinfo), "/sys", mount)
}
