package main

// The kernel command line: the one input channel that exists before
// any filesystem does. It's where rdinit= pointed the kernel at this
// program, and it's liken's channel for facts a machine must know
// before it has read a single file. The bootloader owns the command
// line, which is exactly why it can carry identity: it's configured
// per machine even when the image is shared by a fleet.

import (
	"os"
	"slices"
	"strings"
)

// cmdlinePath is a package variable rather than a constant so tests
// can point the parsers at a file of their own making.
var cmdlinePath = "/proc/cmdline"

// bootParamValue returns the value of a name=value parameter on the
// kernel command line ("" when absent), like which machine this is
// (liken.machine=) or which system slot booted it (liken.slot=).
func bootParamValue(name string) string {
	raw, err := os.ReadFile(cmdlinePath)
	if err != nil {
		return ""
	}
	for _, field := range strings.Fields(string(raw)) {
		if value, ok := strings.CutPrefix(field, name+"="); ok {
			return value
		}
	}
	return ""
}

// bootParam reports whether a word appears on the kernel command
// line, liken's channel for per-boot behavior that isn't machine
// configuration (that belongs in the Machine manifest). Parameters
// are namespaced liken.* to stay clear of the kernel's own.
func bootParam(name string) bool {
	raw, err := os.ReadFile(cmdlinePath)
	if err != nil {
		return false
	}
	return slices.Contains(strings.Fields(string(raw)), name)
}
