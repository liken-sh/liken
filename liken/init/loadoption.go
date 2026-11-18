package main

// This file encodes and decodes UEFI boot entries.
//
// A UEFI machine keeps its boot menu in firmware variables named
// Boot0000, Boot0001, and so on, each holding one EFI_LOAD_OPTION.
// BootOrder lists the entries in order of preference. BootNext names
// one entry for the next boot only, and the firmware deletes it after
// that boot, which is what lets a failed boot of a new slot fall back
// to the other slot. BootCurrent is read-only and names the entry that
// this boot used.
//
// An EFI_LOAD_OPTION is a packed binary record with UTF-16LE strings.
// The location of the executable is a device path: a list of
// variable-length nodes from the hardware to the file. liken writes
// two nodes, a hard-drive node for the GPT partition and a file-path
// node for the executable inside it. The bytes after the device path
// are the optional data, which a kernel booted by its EFI stub reads
// as its command line. The parser also reads entries that other
// software wrote, and skips each node of a type it does not decode.

import (
	"encoding/binary"
	"errors"
	"fmt"
	"unicode/utf16"
)

// loadOptionActive marks an entry that the boot manager may boot on
// its own. An entry without it is listed but never chosen
// automatically.
const loadOptionActive = 0x00000001

// A loadOption is one decoded Boot#### variable. The decode drops the
// device-path nodes that it does not decode, so re-encoding a foreign
// entry does not reproduce it.
type loadOption struct {
	attributes  uint32
	description string

	// hardDrive and filePath are nil and empty when the entry has no
	// such node, as with a PXE entry.
	hardDrive *hardDriveNode
	filePath  string

	optionalData []byte
}

// A hardDriveNode identifies a partition by its number, its extent on
// the disk, and its unique GPT GUID. The firmware finds the partition
// by the GUID, so the entry still works when the disks change order.
type hardDriveNode struct {
	partitionNumber uint32
	firstLBA        uint64
	sectors         uint64
	partitionGUID   [16]byte
}

// Device-path node types, from the UEFI specification.
const (
	dpTypeMedia        = 0x04
	dpSubtypeHardDrive = 0x01
	dpSubtypeFilePath  = 0x04
	dpTypeEnd          = 0x7F
)

// parseLoadOption decodes the payload of one Boot#### variable. The
// caller has already removed the 4-byte efivarfs attribute word.
func parseLoadOption(b []byte) (loadOption, error) {
	if len(b) < 6 {
		return loadOption{}, fmt.Errorf("the load option is %d bytes, and the smallest is 6", len(b))
	}
	o := loadOption{attributes: binary.LittleEndian.Uint32(b[0:4])}
	pathListLen := int(binary.LittleEndian.Uint16(b[4:6]))

	description, rest, err := decodeUTF16Z(b[6:])
	if err != nil {
		return loadOption{}, fmt.Errorf("decoding the description: %w", err)
	}
	o.description = description

	if len(rest) < pathListLen {
		return loadOption{}, fmt.Errorf("the device path list is %d bytes and the load option has %d left after the description", pathListLen, len(rest))
	}
	o.optionalData = rest[pathListLen:]

	// Each node is a type byte, a subtype byte, a little-endian uint16
	// length that includes this 4-byte header, and the node's data.
	paths := rest[:pathListLen]
	for len(paths) > 0 {
		if len(paths) < 4 {
			return loadOption{}, fmt.Errorf("a device path node needs a 4-byte header and %d bytes are left", len(paths))
		}
		nodeType, subType := paths[0], paths[1]
		nodeLen := int(binary.LittleEndian.Uint16(paths[2:4]))
		if nodeLen < 4 || nodeLen > len(paths) {
			return loadOption{}, fmt.Errorf("a device path node is %d bytes, want between 4 and the %d left in the list", nodeLen, len(paths))
		}
		data := paths[4:nodeLen]
		switch {
		case nodeType == dpTypeEnd:
			paths = nil
			continue
		case nodeType == dpTypeMedia && subType == dpSubtypeHardDrive:
			if len(data) < 38 {
				return loadOption{}, fmt.Errorf("the hard-drive node is %d bytes, want at least 38", len(data))
			}
			hd := &hardDriveNode{
				partitionNumber: binary.LittleEndian.Uint32(data[0:4]),
				firstLBA:        binary.LittleEndian.Uint64(data[4:12]),
				sectors:         binary.LittleEndian.Uint64(data[12:20]),
			}
			copy(hd.partitionGUID[:], data[20:36])
			o.hardDrive = hd
		case nodeType == dpTypeMedia && subType == dpSubtypeFilePath:
			path, _, err := decodeUTF16Z(data)
			if err != nil {
				return loadOption{}, fmt.Errorf("decoding the file path: %w", err)
			}
			o.filePath = path
		}
		paths = paths[nodeLen:]
	}
	return o, nil
}

// encodeLoadOption is the inverse of parseLoadOption. It returns the
// bytes of a Boot#### variable without the efivarfs attribute word,
// which the write adds.
func encodeLoadOption(o loadOption) []byte {
	var paths []byte
	if o.hardDrive != nil {
		// 42 bytes: the 4-byte header and 38 bytes of data. The
		// length field includes the header.
		node := make([]byte, 42)
		node[0], node[1] = dpTypeMedia, dpSubtypeHardDrive
		binary.LittleEndian.PutUint16(node[2:4], 42)
		binary.LittleEndian.PutUint32(node[4:8], o.hardDrive.partitionNumber)
		binary.LittleEndian.PutUint64(node[8:16], o.hardDrive.firstLBA)
		binary.LittleEndian.PutUint64(node[16:24], o.hardDrive.sectors)
		copy(node[24:40], o.hardDrive.partitionGUID[:])
		node[40] = 0x02 // the partition table is GPT
		node[41] = 0x02 // so the signature above is a GUID
		paths = append(paths, node...)
	}
	if o.filePath != "" {
		encoded := encodeUTF16Z(o.filePath)
		node := make([]byte, 4, 4+len(encoded))
		node[0], node[1] = dpTypeMedia, dpSubtypeFilePath
		binary.LittleEndian.PutUint16(node[2:4], uint16(4+len(encoded)))
		paths = append(paths, append(node, encoded...)...)
	}
	paths = append(paths, dpTypeEnd, 0xFF, 0x04, 0x00)

	b := make([]byte, 6, 6+len(paths))
	binary.LittleEndian.PutUint32(b[0:4], o.attributes)
	binary.LittleEndian.PutUint16(b[4:6], uint16(len(paths)))
	b = append(b, encodeUTF16Z(o.description)...)
	b = append(b, paths...)
	return append(b, o.optionalData...)
}

// bootEntryID returns the name of an entry's variable: Boot and four
// uppercase hex digits, as in Boot0001 and Boot2001.
func bootEntryID(n uint16) string {
	return fmt.Sprintf("Boot%04X", n)
}

// decodeUTF16Z reads a NUL-terminated UTF-16LE string and returns the
// bytes after it.
func decodeUTF16Z(b []byte) (string, []byte, error) {
	var units []uint16
	for i := 0; ; i += 2 {
		if i+2 > len(b) {
			return "", nil, errors.New("the UTF-16 string has no NUL terminator")
		}
		u := binary.LittleEndian.Uint16(b[i : i+2])
		if u == 0 {
			return string(utf16.Decode(units)), b[i+2:], nil
		}
		units = append(units, u)
	}
}

func encodeUTF16Z(s string) []byte {
	units := utf16.Encode([]rune(s))
	b := make([]byte, (len(units)+1)*2)
	for i, u := range units {
		binary.LittleEndian.PutUint16(b[i*2:], u)
	}
	return b
}
