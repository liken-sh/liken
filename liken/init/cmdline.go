package main

// Every machine in a fleet boots the same image, so init reads the
// facts that differ per machine, such as its name and the slot that
// booted, from the kernel command line that the bootloader writes. The
// command line is readable before any storage is mounted.

import (
	"os"
	"slices"
	"strings"
)

// cmdlinePath is a variable so tests can point the readers at a file
// of their own.
var cmdlinePath = "/proc/cmdline"

// cmdlineFields returns the command line split on whitespace, or nil
// when the file cannot be read, which happens only in a test.
func cmdlineFields() []string {
	raw, err := os.ReadFile(cmdlinePath)
	if err != nil {
		return nil
	}
	return strings.Fields(string(raw))
}

// cmdlineRaw returns the whole command line for machine status, so a
// person sees every parameter the bootloader passed, including the
// ones that `liken` does not read.
func cmdlineRaw() string {
	raw, err := os.ReadFile(cmdlinePath)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(raw))
}

// bootParamValue returns the value of a name=value parameter, such as
// liken.machine= or liken.slot=, or "" when it is absent.
func bootParamValue(name string) string {
	for _, field := range cmdlineFields() {
		if value, ok := strings.CutPrefix(field, name+"="); ok {
			return value
		}
	}
	return ""
}

// bootParam reports whether a flag, such as liken.install, is on the
// command line as a whole word. A flag changes the behavior of one
// boot. Configuration that persists belongs in the `Machine` manifest.
func bootParam(name string) bool {
	return slices.Contains(cmdlineFields(), name)
}
