package main

// The kernel command line: the one input channel that exists before
// any filesystem does. rdinit= on the command line pointed the
// kernel at this program, and the command line is liken's channel
// for facts a machine needs before it has read a single file. The
// bootloader owns the command line, which is why it can carry
// identity: the bootloader configures the command line per machine,
// even when the image is shared by a fleet.

import (
	"os"
	"slices"
	"strings"
)

// cmdlinePath is a package variable rather than a constant so tests
// can point the parsers at a file of their own making.
var cmdlinePath = "/proc/cmdline"

// cmdlineFields reads the kernel command line as its words, split on
// whitespace. Every parameter lookup starts from this shape. A
// command line that cannot be read yields no words. The file exists
// on any booted kernel, so its absence only ever means a test did not
// fake one, and every lookup then reports "not present".
func cmdlineFields() []string {
	raw, err := os.ReadFile(cmdlinePath)
	if err != nil {
		return nil
	}
	return strings.Fields(string(raw))
}

// bootParamValue returns the value of a name=value parameter on the
// kernel command line ("" when absent). Examples are which machine
// this is (liken.machine=) and which system slot booted it
// (liken.slot=).
func bootParamValue(name string) string {
	for _, field := range cmdlineFields() {
		if value, ok := strings.CutPrefix(field, name+"="); ok {
			return value
		}
	}
	return ""
}

// bootParam reports whether a word appears on the kernel command
// line. This is liken's channel for per-boot behavior that is not
// machine configuration; machine configuration belongs in the
// Machine manifest. Parameter names use the liken.* prefix, to stay
// clear of the kernel's own parameters.
func bootParam(name string) bool {
	return slices.Contains(cmdlineFields(), name)
}
