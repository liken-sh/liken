package main

// Boot entries: the firmware's menu, one binary record per entry.
//
// A UEFI machine keeps its boot menu in firmware variables named
// Boot0000, Boot0001, and so on, each holding one EFI_LOAD_OPTION:
// the record for one bootable thing. Each record carries a
// human-readable name, the location of the executable, and the
// arguments to hand it. Three companion variables give the list its
// meaning: BootOrder (the durable preference list), BootNext (the
// entry to try on the next boot, once; the firmware erases it after
// use, and that one-shot behavior is what makes the blue-green
// fallback work), and BootCurrent (read-only: the entry actually
// used this boot).
//
// The record is a packed binary format much like the GPT's, with the
// same Microsoft heritage: strings are UTF-16LE, and the location of
// the executable is a "device path", a chain of variable-length
// nodes that narrows from hardware to file. liken's entries need
// exactly two nodes: a hard-drive node that pins one GPT partition
// by its unique GUID (position-independent, like liken's own
// recognition by name), and a file-path node that names the
// executable inside it, backslashes and all. Whatever follows the
// path list is "optional data", which for a Linux EFI-stub kernel is
// simply the kernel command line.
//
// The encoder writes only the entries that liken itself creates. The
// parser must handle more, because real firmware fills these
// variables with vendor nodes of every kind. This code skips unknown
// node types by their declared lengths and otherwise ignores them.

import (
	"encoding/binary"
	"fmt"
	"unicode/utf16"
)

// loadOptionActive marks an entry the boot manager may use on its
// own; without it an entry is listed but never chosen automatically.
const loadOptionActive = 0x00000001

// A loadOption is one Boot#### variable, decoded. Fields that liken
// does not model (vendor device-path nodes, unusual attributes)
// survive a decode only to the extent that each field's comment
// describes.
type loadOption struct {
	attributes  uint32
	description string

	// hardDrive and filePath are the two device-path nodes that liken
	// cares about. They are nil or empty when an entry does not
	// carry them (a PXE entry, a vendor recovery tool).
	hardDrive *hardDriveNode
	filePath  string

	// optionalData is everything after the device paths: for a
	// kernel booted by its EFI stub, the command line.
	optionalData []byte
}

// A hardDriveNode identifies one partition three redundant ways: by
// index, by extent, and by GUID. The GUID is the one that matters.
// It is the partition's unique GUID from the GPT itself, so the
// entry survives disks being reordered, exactly like liken's
// recognition by partition name.
type hardDriveNode struct {
	partitionNumber uint32
	firstLBA        uint64
	sectors         uint64
	partitionGUID   [16]byte
}

// Device-path node types, from the specification's tables.
const (
	dpTypeMedia        = 0x04
	dpSubtypeHardDrive = 0x01
	dpSubtypeFilePath  = 0x04
	dpTypeEnd          = 0x7F
)

// parseLoadOption decodes one Boot#### variable's payload (the
// efivarfs attribute word already stripped).
func parseLoadOption(b []byte) (loadOption, error) {
	if len(b) < 6 {
		return loadOption{}, fmt.Errorf("load option is %d bytes; even an empty one needs 6", len(b))
	}
	o := loadOption{attributes: binary.LittleEndian.Uint32(b[0:4])}
	pathListLen := int(binary.LittleEndian.Uint16(b[4:6]))

	description, rest, err := decodeUTF16Z(b[6:])
	if err != nil {
		return loadOption{}, fmt.Errorf("description: %w", err)
	}
	o.description = description

	if len(rest) < pathListLen {
		return loadOption{}, fmt.Errorf("device path list claims %d bytes but only %d remain", pathListLen, len(rest))
	}
	o.optionalData = rest[pathListLen:]

	// This walks the device-path nodes: type, subtype, a
	// little-endian length that includes the 4-byte header, then
	// that node's data. The walk trusts each node's declared length
	// and nothing else.
	paths := rest[:pathListLen]
	for len(paths) > 0 {
		if len(paths) < 4 {
			return loadOption{}, fmt.Errorf("device path node truncated: %d bytes left", len(paths))
		}
		nodeType, subType := paths[0], paths[1]
		nodeLen := int(binary.LittleEndian.Uint16(paths[2:4]))
		if nodeLen < 4 || nodeLen > len(paths) {
			return loadOption{}, fmt.Errorf("device path node claims %d bytes of %d", nodeLen, len(paths))
		}
		data := paths[4:nodeLen]
		switch {
		case nodeType == dpTypeEnd:
			paths = nil
			continue
		case nodeType == dpTypeMedia && subType == dpSubtypeHardDrive:
			if len(data) < 38 {
				return loadOption{}, fmt.Errorf("hard-drive node is %d bytes, want 38", len(data))
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
				return loadOption{}, fmt.Errorf("file path: %w", err)
			}
			o.filePath = path
		}
		paths = paths[nodeLen:]
	}
	return o, nil
}

// encodeLoadOption is the inverse of parseLoadOption. It produces
// the exact bytes that a Boot#### variable holds (again minus the
// efivarfs attribute word, which belongs to the write, not the
// record).
func encodeLoadOption(o loadOption) []byte {
	var paths []byte
	if o.hardDrive != nil {
		// 42 bytes exactly: the 4-byte header plus 38 of payload. The
		// length field counts the header too, which is the easiest
		// detail to get wrong in this format.
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

// bootEntryID renders an entry number the way its variable is named:
// four uppercase hex digits, so Boot0001 and Boot2001 read exactly as
// the firmware spells them.
func bootEntryID(n uint16) string {
	return fmt.Sprintf("Boot%04X", n)
}

// decodeUTF16Z reads a NUL-terminated UTF-16LE string and returns
// what follows it. UTF-16 because these formats come from Microsoft.
// The terminator carries real information: the description's length
// is recorded nowhere else in the record.
func decodeUTF16Z(b []byte) (string, []byte, error) {
	var units []uint16
	for i := 0; ; i += 2 {
		if i+2 > len(b) {
			return "", nil, fmt.Errorf("UTF-16 string never reached its terminator")
		}
		u := binary.LittleEndian.Uint16(b[i : i+2])
		if u == 0 {
			return string(utf16.Decode(units)), b[i+2:], nil
		}
		units = append(units, u)
	}
}

// encodeUTF16Z writes a string as NUL-terminated UTF-16LE.
func encodeUTF16Z(s string) []byte {
	units := utf16.Encode([]rune(s))
	b := make([]byte, (len(units)+1)*2)
	for i, u := range units {
		binary.LittleEndian.PutUint16(b[i*2:], u)
	}
	return b
}
