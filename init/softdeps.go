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

// Reading a module's soft dependencies.
//
// A hard dependency is a symbol one module needs from another, and
// depmod records every one of them in modules.dep, ordered so that
// loading right-to-left satisfies them. A soft dependency is a weaker
// thing: a hint that one module names another to load before it, so
// that a device probes to the right driver. The lab's Realtek NIC is
// the case that taught this. The r8169 driver asks, through a "pre"
// soft dependency, for the realtek PHY library to load ahead of it.
// Without realtek the NIC still binds, but to a generic PHY, and the
// link does not come up the same way.
//
// depmod does not index soft dependencies. modules.dep, modules.alias,
// and the rest say nothing about them. Each module carries its own
// soft dependencies inside its compiled object, in the .modinfo
// section, as strings of the form "softdep=pre: realtek". modprobe
// reads them there, and so does liken. (modprobe also reads softdep
// lines from modprobe.d files, which liken does not ship, so a
// module's own .modinfo is the whole source here.)
//
// This knowledge never reaches the module loader. The loader stays
// explicit: it loads what a manifest declares, in the declared order,
// and a soft dependency the manifest did not name does not load. liken
// has no udev for the same reason. The manifest is the whole truth
// about what a machine runs. Soft dependencies inform the advice a
// person reads before they write the manifest, so a recommendation can
// name realtek and r8169 together, but they never change what the
// loader does with the manifest it is handed.
//
// Only "pre" soft dependencies matter here. liken loads a driver so
// that a device appears, and a "pre" dependency changes which driver
// binds, so it changes that outcome. A "post" dependency loads after
// the module and does not change whether the device appears, so the
// recommendation ignores it.

// busCompanions names the modules that a driver needs beside it, which
// no index in the module tree can name.
//
// A recommendation starts from modules.alias, which maps a device's
// fingerprint to the drivers that claim that fingerprint. Some drivers
// claim nothing there, because they bind over a bus that another
// driver creates: usbhid turns a USB interface into a HID device, and
// hid-generic then binds that HID device. No modalias points at
// hid-generic, and no soft dependency names it, so a report that reads
// only the index recommends a keyboard's transport and stops one
// module short of a working keyboard.
//
// The table is the place to record each of these pairs as hardware
// proves them. It stays short on purpose: an entry here is a claim
// that the index cannot express, not a shortcut around reading it.
var busCompanions = map[string][]string{
	"usbhid": {"hid_generic"},
}

// driverChain is the full ordered list a person declares to make one
// driver work: its soft dependencies, the driver, and any companion
// that binds over the bus the driver creates. The companions come last
// because they bind to what the driver produces.
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

// softdepChain expands one module name into the ordered list of
// modules to declare so the kernel binds the intended driver: the
// module's "pre" soft dependencies first, resolved recursively because
// a soft dependency can carry its own, then the module itself. The
// report boot and the unclaimed-hardware advice both call this to turn
// a single recommended driver into the full list a person writes into
// spec.modules. A tree with no readable index yields the name alone,
// which is the honest answer when nothing can be resolved.
func softdepChain(base, name string) []string {
	deps, err := readModulesDep(filepath.Join(base, "modules.dep"))
	if err != nil {
		return []string{name}
	}
	return expandSoftdeps(name, func(n string) []string {
		return modulePreSoftdeps(base, deps, n)
	})
}

// expandSoftdeps walks the "pre" soft-dependency graph depth-first and
// returns the names in load order, each name once. The seen set is the
// cycle guard. Soft dependencies are only a hint, and nothing stops a
// module from naming one that names it back, so the walk records every
// name it enters and never enters one twice. A name already placed by
// an earlier branch is not placed again.
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

// modulePreSoftdeps returns the "pre" soft dependencies that a single
// module names in its .modinfo, without recursing. It finds the
// module's file through modules.dep, whose first field on each line is
// the module's own path, then reads the strings from that file.
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

// readModinfo returns the raw bytes of a module's .modinfo section.
// The module files are compiled objects, so the soft-dependency
// strings live in an ELF section, not in any index. liken's modules
// are zstd-compressed exactly as Ubuntu shipped them, and the kernel
// normally decompresses them itself during finit_module. Reading the
// section back in userspace has no such help, so this decompresses the
// file first when its name says it carries a zstd frame.
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

// elfSection returns one named section's bytes from an ELF image held
// in memory.
func elfSection(image []byte, name string) ([]byte, error) {
	f, err := elf.NewFile(bytes.NewReader(image))
	if err != nil {
		return nil, err
	}
	defer f.Close()
	section := f.Section(name)
	if section == nil {
		return nil, fmt.Errorf("no %s section", name)
	}
	return section.Data()
}

// zstdDecode decompresses a whole zstd frame held in memory.
func zstdDecode(data []byte) ([]byte, error) {
	reader, err := zstd.NewReader(nil)
	if err != nil {
		return nil, err
	}
	defer reader.Close()
	return reader.DecodeAll(data, nil)
}

// parseSoftdepPre reads the "pre" soft dependencies out of a .modinfo
// section. The section is a run of NUL-separated "key=value" strings.
// A soft dependency is a "softdep" key whose value lists names under
// "pre:" and "post:" markers, for example "pre: realtek post: foo".
// This collects the names under "pre:", in order, across every softdep
// string, and drops the "post:" names for the reason stated above.
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
