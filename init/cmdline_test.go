package main

// Tests for the kernel command-line parsers. The real file is
// /proc/cmdline; these point cmdlinePath at files of their own
// making, so every shape (present, absent, unreadable) is pinned.

import (
	"os"
	"path/filepath"
	"testing"
)

// fakeCmdline points the parsers at a command line of the test's
// choosing, restoring the real path when the test ends. Like the
// fake sysfs, this swaps a package variable, so tests in this
// package must not run in parallel.
func fakeCmdline(t *testing.T, content string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "cmdline")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	old := cmdlinePath
	cmdlinePath = path
	t.Cleanup(func() { cmdlinePath = old })
}

func TestBootParamValue(t *testing.T) {
	fakeCmdline(t, "console=ttyS0 rdinit=/liken liken.machine=node-3 liken.slot=B\n")
	if got := bootParamValue("liken.machine"); got != "node-3" {
		t.Errorf("liken.machine: got %q", got)
	}
	if got := bootParamValue("liken.slot"); got != "B" {
		t.Errorf("liken.slot: got %q", got)
	}
	if got := bootParamValue("liken.absent"); got != "" {
		t.Errorf("an absent parameter reads empty: got %q", got)
	}
}

func TestBootParamValueWithNoCmdline(t *testing.T) {
	old := cmdlinePath
	cmdlinePath = filepath.Join(t.TempDir(), "missing")
	t.Cleanup(func() { cmdlinePath = old })
	if got := bootParamValue("liken.machine"); got != "" {
		t.Errorf("an unreadable command line reads empty: got %q", got)
	}
	if bootParam("liken.install") {
		t.Error("an unreadable command line carries no flags")
	}
}

func TestBootParam(t *testing.T) {
	fakeCmdline(t, "console=ttyS0 liken.install panic=10\n")
	if !bootParam("liken.install") {
		t.Error("liken.install is on the command line")
	}
	if bootParam("liken.oneshot") {
		t.Error("liken.oneshot is not on the command line")
	}
	// A flag is a whole word, never a prefix of one.
	if bootParam("liken.inst") {
		t.Error("a prefix of a word is not the word")
	}
}
