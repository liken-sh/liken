package image

// Reading newc archives: the inverse of cpio.go's writer, for the
// few places the build tools need to look inside an archive they
// didn't just write. The stick builder reads a deployment layer to
// learn which machines it carries — the layer is what actually
// boots, so it, and not some manifests directory that might have
// drifted, is the authority on what the install menu should offer.
//
// The reader is as strict as the writer is simple: a truncated or
// malformed archive is an error, never a partial result, because
// every caller is about to make decisions from what it read.

import (
	"fmt"
	"path"
	"slices"
	"strconv"
	"strings"
)

// A cpioEntry is one file or directory read back from an archive.
type cpioEntry struct {
	name string
	mode uint32
	uid  uint32
	gid  uint32
	data []byte
}

// readCPIO parses one newc archive into its entries, stopping at the
// trailer, and returns whatever bytes follow it — a composed image is
// several archives concatenated, so "the rest" is often the next one.
// The data slices alias raw; callers that keep them keep the archive
// alive, which every current caller wants anyway.
func readCPIO(raw []byte) ([]cpioEntry, []byte, error) {
	var entries []cpioEntry
	off := 0
	pad4 := func(n int) int { return (n + 3) &^ 3 }
	for {
		if off+110 > len(raw) {
			return nil, nil, fmt.Errorf("archive ends mid-header at byte %d", off)
		}
		if string(raw[off:off+6]) != "070701" {
			return nil, nil, fmt.Errorf("no newc magic at byte %d", off)
		}
		field := func(i int) (uint32, error) {
			start := off + 6 + i*8
			v, err := strconv.ParseUint(string(raw[start:start+8]), 16, 32)
			if err != nil {
				return 0, fmt.Errorf("header field %d at byte %d: %w", i, off, err)
			}
			return uint32(v), nil
		}
		mode, err := field(1)
		if err != nil {
			return nil, nil, err
		}
		uid, err := field(2)
		if err != nil {
			return nil, nil, err
		}
		gid, err := field(3)
		if err != nil {
			return nil, nil, err
		}
		filesize, err := field(6)
		if err != nil {
			return nil, nil, err
		}
		namesize, err := field(11)
		if err != nil {
			return nil, nil, err
		}
		if namesize == 0 || off+110+int(namesize) > len(raw) {
			return nil, nil, fmt.Errorf("archive ends mid-name at byte %d", off)
		}
		name := string(raw[off+110 : off+110+int(namesize)-1])
		dataStart := pad4(off + 110 + int(namesize))
		if dataStart+int(filesize) > len(raw) {
			return nil, nil, fmt.Errorf("archive ends mid-file in %q", name)
		}
		data := raw[dataStart : dataStart+int(filesize)]
		off = pad4(dataStart + int(filesize))
		if name == "TRAILER!!!" {
			return entries, raw[off:], nil
		}
		entries = append(entries, cpioEntry{name: name, mode: mode, uid: uid, gid: gid, data: data})
	}
}

// machineNames lists the machines a deployment layer carries, from
// its etc/liken/machines/*.yaml entries: the same files init selects
// among by the liken.machine= parameter, so the names here are
// exactly the names an install can be asked for. A layer carrying no
// machines is refused — install media with nothing to offer is a
// mistake worth catching while it is still a build.
func machineNames(layer []byte) ([]string, error) {
	entries, _, err := readCPIO(layer)
	if err != nil {
		return nil, fmt.Errorf("reading the deployment layer: %w", err)
	}
	var names []string
	for _, e := range entries {
		dir, file := path.Split(e.name)
		if dir == "etc/liken/machines/" && strings.HasSuffix(file, ".yaml") {
			names = append(names, strings.TrimSuffix(file, ".yaml"))
		}
	}
	if len(names) == 0 {
		return nil, fmt.Errorf("the deployment layer carries no machine manifests; there would be nobody to install")
	}
	slices.Sort(names)
	return names, nil
}
