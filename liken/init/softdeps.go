package main

import (
	"bytes"
	"debug/elf"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/klauspost/compress/zstd"
)

// Soft dependencies, read to recommend the modules that a person lists
// in spec.modules for a device.
//
// A soft dependency is a module that should load before or after
// another, though neither uses the other's symbols. r8169 has a "pre"
// soft dependency on the realtek PHY module. Without realtek, the
// network card binds to the generic PHY driver, and the link does not
// come up correctly.
//
// modules.dep and modules.alias do not list soft dependencies. Each
// module records them in the .modinfo section of its ELF file, as
// strings such as "softdep=pre: realtek". liken ships no modprobe.d,
// so .modinfo is the only source.
//
// The module loader ignores soft dependencies and loads only what the
// manifest lists, in its order, so the manifest describes everything a
// machine loads. Only "pre" soft dependencies are read, because a
// "post" module loads after the device already works.

// busCompanions maps a driver to modules that must load after it and
// that neither modules.alias nor a soft dependency lists. These
// modules bind to a device that the driver creates, so no modalias of
// the hardware matches them. usbhid creates a HID device from a USB
// interface, and hid-generic binds to it. Without hid-generic, a USB
// keyboard does not work.
//
// Add an entry only for a pair that real hardware has shown the
// module index cannot find.
var busCompanions = map[string][]string{
	"usbhid": {"hid_generic"},
}

// driverChain returns the modules to list in spec.modules, in load
// order, for one driver: softdepChain followed by the busCompanions of
// each module in it.
func driverChain(base, name string) []string {
	chain := softdepChain(base, name)
	for _, module := range chain {
		for _, companion := range busCompanions[strings.ReplaceAll(module, "-", "_")] {
			if !slices.Contains(chain, companion) {
				chain = append(chain, companion)
			}
		}
	}
	return chain
}

// softdepChain returns name preceded by its "pre" soft dependencies and
// theirs, in load order. Without a readable modules.dep, it returns
// only name.
func softdepChain(base, name string) []string {
	deps, err := readModulesDep(filepath.Join(base, "modules.dep"))
	if err != nil {
		return []string{name}
	}
	return expandSoftdeps(name, func(n string) []string {
		return modulePreSoftdeps(base, deps, n)
	})
}

// expandSoftdeps returns each name once, dependencies before the
// module that needs them. Two modules can list each other as soft
// dependencies, so the walk skips a name it has already visited.
func expandSoftdeps(name string, pre func(string) []string) []string {
	return appendSoftdeps(name, pre, map[string]bool{}, nil)
}

func appendSoftdeps(name string, pre func(string) []string, seen map[string]bool, out []string) []string {
	key := strings.ReplaceAll(name, "-", "_")
	if seen[key] {
		return out
	}
	seen[key] = true
	for _, p := range pre(name) {
		out = appendSoftdeps(p, pre, seen, out)
	}
	return append(out, name)
}

// modulePreSoftdeps returns the "pre" soft dependencies of one module,
// without recursing, or nil when the module or its file is missing.
// The first field of a module's entry in modules.dep is its file path.
func modulePreSoftdeps(base string, deps map[string][]string, name string) []string {
	entry, ok := deps[strings.ReplaceAll(name, "-", "_")]
	if !ok || len(entry) == 0 {
		return nil
	}
	modinfo, err := readModinfo(filepath.Join(base, entry[0]))
	if err != nil {
		return nil
	}
	return parseSoftdepPre(modinfo)
}

// readModinfo returns the .modinfo section of a module file, and
// decompresses a .ko.zst file first.
func readModinfo(path string) ([]byte, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if strings.HasSuffix(path, ".zst") {
		raw, err = zstdDecode(raw)
		if err != nil {
			return nil, err
		}
	}
	return elfSection(raw, ".modinfo")
}

func elfSection(image []byte, name string) ([]byte, error) {
	f, err := elf.NewFile(bytes.NewReader(image))
	if err != nil {
		return nil, err
	}
	defer f.Close()
	section := f.Section(name)
	if section == nil {
		return nil, fmt.Errorf("the module has no %s section", name)
	}
	return section.Data()
}

func zstdDecode(data []byte) ([]byte, error) {
	reader, err := zstd.NewReader(nil)
	if err != nil {
		return nil, err
	}
	defer reader.Close()
	return reader.DecodeAll(data, nil)
}

// parseSoftdepPre returns the names after "pre:" in each softdep entry
// of a .modinfo section, in order. The section holds NUL-separated
// "key=value" strings, and a softdep value looks like
// "pre: realtek post: foo".
func parseSoftdepPre(modinfo []byte) []string {
	var pre []string
	for entry := range bytes.SplitSeq(modinfo, []byte{0}) {
		key, value, ok := bytes.Cut(entry, []byte{'='})
		if !ok || string(key) != "softdep" {
			continue
		}
		bucket := ""
		for _, token := range strings.Fields(string(value)) {
			switch token {
			case "pre:":
				bucket = "pre"
			case "post:":
				bucket = "post"
			default:
				if bucket == "pre" {
					pre = append(pre, token)
				}
			}
		}
	}
	return pre
}
