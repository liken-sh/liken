package main

import (
	"os"
	"path/filepath"
	"testing"
)

// fakeCmdline points the readers at a file with the given content. It
// sets a package variable, so tests that call it must not run in
// parallel.
func fakeCmdline(t *testing.T, content string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "cmdline")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("writing the command line file: %v", err)
	}
	old := cmdlinePath
	cmdlinePath = path
	t.Cleanup(func() { cmdlinePath = old })
}

func TestBootParamValue(t *testing.T) {
	fakeCmdline(t, "console=ttyS0 rdinit=/liken liken.machine=node-3 liken.slot=B\n")
	if got := bootParamValue("liken.machine"); got != "node-3" {
		t.Errorf("bootParamValue(liken.machine) = %q, want node-3", got)
	}
	if got := bootParamValue("liken.slot"); got != "B" {
		t.Errorf("bootParamValue(liken.slot) = %q, want B", got)
	}
	if got := bootParamValue("liken.absent"); got != "" {
		t.Errorf("bootParamValue of an absent parameter = %q, want an empty string", got)
	}
}

// The trailing newline is dropped and the spaces inside the line stay,
// so status shows exactly what the bootloader wrote.
func TestCmdlineRawKeepsTheWholeLine(t *testing.T) {
	const line = "console=ttyS0 rdinit=/liken liken.machine=node-3 liken.slot=B panic=10"
	fakeCmdline(t, line+"\n")
	if got := cmdlineRaw(); got != line {
		t.Errorf("cmdlineRaw() = %q, want %q", got, line)
	}
}

func TestCmdlineRawWithNoCmdline(t *testing.T) {
	old := cmdlinePath
	cmdlinePath = filepath.Join(t.TempDir(), "missing")
	t.Cleanup(func() { cmdlinePath = old })
	if got := cmdlineRaw(); got != "" {
		t.Errorf("cmdlineRaw() = %q with no command line file, want an empty string", got)
	}
}

func TestBootParamValueWithNoCmdline(t *testing.T) {
	old := cmdlinePath
	cmdlinePath = filepath.Join(t.TempDir(), "missing")
	t.Cleanup(func() { cmdlinePath = old })
	if got := bootParamValue("liken.machine"); got != "" {
		t.Errorf("bootParamValue(liken.machine) = %q with no command line file, want an empty string", got)
	}
	if bootParam("liken.install") {
		t.Error("bootParam(liken.install) is true with no command line file, want false")
	}
}

func TestBootParam(t *testing.T) {
	fakeCmdline(t, "console=ttyS0 liken.install panic=10\n")
	if !bootParam("liken.install") {
		t.Error("bootParam(liken.install) is false, want true because the command line has the word")
	}
	if bootParam("liken.oneshot") {
		t.Error("bootParam(liken.oneshot) is true, want false because the command line does not have the word")
	}
	if bootParam("liken.inst") {
		t.Error("bootParam(liken.inst) is true, want false because it is only a prefix of liken.install")
	}
}
