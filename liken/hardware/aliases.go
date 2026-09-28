// Package hardware is the part of liken that observes the machine's
// devices. It holds the sysfs walk that finds the devices, the
// modalias matching that names their drivers, and the databases
// that turn hex IDs into words.
//
// The kernel does nearly all device management by itself. It
// enumerates hardware, binds resident drivers, and creates the
// /dev nodes. But the kernel never loads a module. When a device
// appears and no resident driver claims it, the kernel sends a
// uevent that carries a MODALIAS fingerprint, and the device has no
// driver until a userspace program responds to that uevent. On most
// systems, udev responds by loading whatever module matches. liken's
// design is different: drivers are declared in spec.modules, and an
// undriven device produces a report that names the module that would
// drive it, instead of liken loading a driver without a declaration.
// This package does the observing half of that design. It changes
// nothing on the machine; it only reads.
package hardware

// This file implements the read side of the modalias system. The
// modalias system is the kernel's own driver-matching database, but
// organized for lookup by device fingerprint instead of by driver.
// Every device the kernel enumerates gets a MODALIAS string, a
// one-line fingerprint of its identity in a bus-specific format:
//
//	usb:v46F4p0001d0100dc00dsc00dp00ic08isc06ip50in00
//	pci:v00001AF4d00001050sv00001AF4sd00001100bc03sc80i00
//
// Every module declares, in its .modinfo section, glob patterns for
// the fingerprints it can drive. At build time, depmod extracts those
// patterns into modules.alias, with one "alias <pattern> <module>"
// line for each pattern. Matching a device's fingerprint against that
// table is how udev, or this package, finds the module that drives
// the device.

import (
	"fmt"
	"os"
	"slices"
	"strings"
)

// AliasTable is a loaded modules.alias file. It holds every
// driver-matching pattern that the kernel build produced, in file
// order. A lookup scans the table from the start. The table has only
// a few tens of thousands of lines, and a machine checks it only
// when a device has no driver. For this size and use, an index would
// add complexity without a benefit.
type AliasTable struct {
	entries []aliasEntry
}

type aliasEntry struct {
	pattern string
	module  string
}

// LoadAliasTable reads a modules.alias file. A missing table
// produces an error, not an empty table. Without the table, every
// unclaimed device would incorrectly report "no driver known". An
// error is more accurate than that incorrect report.
func LoadAliasTable(path string) (*AliasTable, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading the module alias table: %w", err)
	}
	table := &AliasTable{}
	for line := range strings.SplitSeq(string(raw), "\n") {
		pattern, module, found := strings.Cut(strings.TrimPrefix(line, "alias "), " ")
		if !found || strings.HasPrefix(line, "#") {
			continue
		}
		table.entries = append(table.entries, aliasEntry{pattern: pattern, module: module})
	}
	return table, nil
}

// Candidates returns every module whose alias patterns match this
// modalias. It removes duplicates and keeps table order. More than
// one match is normal, not an error. For example, a USB mass-storage
// device matches both uas and usb_storage. udev's response to this
// is to load both modules, after which the modules themselves
// determine which one binds to the device. liken reports all
// matching modules instead of loading any of them, because the
// choice belongs to the person who writes spec.modules.
func (t *AliasTable) Candidates(modalias string) []string {
	var modules []string
	for _, e := range t.entries {
		if matchModalias(e.pattern, modalias) && !slices.Contains(modules, e.module) {
			modules = append(modules, e.module)
		}
	}
	return modules
}

// matchModalias implements the glob dialect that modules.alias uses.
// In this dialect, '*' matches any run of characters, '?' matches
// exactly one character, and every other character is literal. This
// dialect is fnmatch without character classes, because modinfo
// emits only these two metacharacters. The whole pattern must match
// the whole value. For this reason, "usb:v0403" does not match
// "usb:v0403p6001".
func matchModalias(pattern, value string) bool {
	// This function matches '*' greedily and backtracks on a
	// mismatch: a two-pointer glob walk. It stores the position of
	// the last star and the value position where the star last
	// matched. On a mismatch, it retries from the star, one more
	// character into the value.
	p, v := 0, 0
	star, mark := -1, 0
	for v < len(value) {
		switch {
		case p < len(pattern) && (pattern[p] == value[v] || pattern[p] == '?'):
			p++
			v++
		case p < len(pattern) && pattern[p] == '*':
			star, mark = p, v
			p++
		case star >= 0:
			mark++
			p, v = star+1, mark
		default:
			return false
		}
	}
	for p < len(pattern) && pattern[p] == '*' {
		p++
	}
	return p == len(pattern)
}
