// Package hardware is how liken observes the machine's devices: the
// sysfs walk that finds them, the modalias matching that names the
// drivers they want, and the databases that turn hex IDs into words.
//
// The kernel does nearly all of device management by itself — it
// enumerates hardware, binds resident drivers, creates the /dev
// nodes — but it never loads a module. When a device appears and no
// resident driver claims it, the kernel announces an orphan (a
// uevent carrying a MODALIAS fingerprint) and waits for userspace.
// Elsewhere udev answers by loading whatever matches; liken's answer
// is deliberately different: drivers are declared (spec.modules),
// and an undriven device becomes a *report* naming the module that
// would drive it, never a silently-loaded driver. This package is
// the observing half of that posture. It changes nothing on the
// machine; it only reads.
package hardware

// The modalias system, which this file implements the reading half
// of, is the kernel's own driver-matching database turned inside
// out. Every device the kernel enumerates gets a MODALIAS string, a
// one-line fingerprint of its identity in a bus-specific format:
//
//	usb:v46F4p0001d0100dc00dsc00dp00ic08isc06ip50in00
//	pci:v00001AF4d00001050sv00001AF4sd00001100bc03sc80i00
//
// and every module declares, in its .modinfo section, glob patterns
// for the fingerprints it can drive. At build time depmod extracts
// those patterns into modules.alias, one "alias <pattern> <module>"
// line each. Matching a device's fingerprint against that table is
// how anyone — udev there, this package here — answers "which module
// drives this device?".

import (
	"fmt"
	"os"
	"slices"
	"strings"
)

// AliasTable is a loaded modules.alias: every driver-matching
// pattern the kernel build produced, in file order. Lookups scan
// linearly; the table is a few tens of thousands of lines and a
// machine consults it only when a device is sitting undriven, so an
// index would be complexity without a customer.
type AliasTable struct {
	entries []aliasEntry
}

type aliasEntry struct {
	pattern string
	module  string
}

// LoadAliasTable reads a modules.alias file. A missing table is an
// error rather than an empty table: without it every unclaimed
// device would report "no driver known", which is a worse lie than
// a loud failure.
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
// modalias, deduplicated, in table order. More than one answer is
// normal, not an error: a USB mass-storage device matches both uas
// and usb_storage, and udev's response is to load them all and let
// the drivers negotiate. liken reports all of them instead, because
// the choice belongs to the person writing spec.modules.
func (t *AliasTable) Candidates(modalias string) []string {
	var modules []string
	for _, e := range t.entries {
		if matchModalias(e.pattern, modalias) && !slices.Contains(modules, e.module) {
			modules = append(modules, e.module)
		}
	}
	return modules
}

// matchModalias implements the glob dialect modules.alias uses: '*'
// matches any run of characters, '?' matches exactly one, everything
// else is literal. This is fnmatch without character classes —
// modinfo emits only these two metacharacters — and the whole
// pattern must consume the whole value, which is what makes
// "usb:v0403" not a match for "usb:v0403p6001".
func matchModalias(pattern, value string) bool {
	// Greedy '*' with backtracking, the classic two-pointer glob
	// walk: remember the position of the last star and where it had
	// matched to, and on a mismatch, retry from the star with one
	// more character consumed.
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
