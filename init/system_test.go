package main

// Tests for the boot-time sysctl application: failures are reported
// and skipped, never fatal, and the two passes leave the machine
// holding the values a deployment asked for. The QEMU harness tests
// this against a real kernel. What these tests confirm is the part
// that has nothing to do with the kernel: that a bad parameter name
// cannot stop a boot, that every declared default is applyable, and
// that the second pass wins.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/liken-sh/liken/machine"
)

// fakeSysctlDir builds a directory shaped like /proc/sys, with an
// existing file for every named parameter, and points applySysctls at
// it. The files have to exist already: ApplySysctl opens without
// creating, so that a name the kernel does not have fails rather than
// turning into a new file nobody reads.
func fakeSysctlDir(t *testing.T, names ...string) string {
	t.Helper()
	dir := t.TempDir()
	for _, name := range names {
		full := filepath.Join(dir, strings.ReplaceAll(name, ".", "/"))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte("0\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	old := sysctlDir
	sysctlDir = dir
	t.Cleanup(func() { sysctlDir = old })
	return dir
}

func readParameter(t *testing.T, dir, name string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(dir, strings.ReplaceAll(name, ".", "/")))
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(raw))
}

func TestApplySysctlsSkipsFailuresAndContinues(t *testing.T) {
	applySysctls(map[string]string{
		"liken.test.no.such.parameter": "1", // fails to open; init reports and skips it
		"../escape/attempt":            "1", // the traversal guard refuses this path
	})
}

// Every parameter liken declares has to be one this code can actually
// write. A name with a typo in it, or a value the table's author never
// tried, would otherwise reach a booting machine before anyone noticed.
func TestApplySysctlsAppliesEveryDeclaredDefault(t *testing.T) {
	names := make([]string, 0, len(machine.OSSysctls))
	for name := range machine.OSSysctls {
		names = append(names, name)
	}
	dir := fakeSysctlDir(t, names...)

	applySysctls(machine.OSSysctls)

	for name, want := range machine.OSSysctls {
		if got := readParameter(t, dir, name); got != want {
			t.Errorf("%s: got %q, want %q", name, got, want)
		}
	}
}

// The two passes in the boot, in the order the boot runs them. A name
// in both sets holds the spec's value, because the spec is written
// second and the last write is the one that holds.
func TestSpecSysctlsOverrideTheDeclaredDefaults(t *testing.T) {
	const shared = "vm.max_map_count"
	const untouched = "kernel.pid_max"
	dir := fakeSysctlDir(t, shared, untouched)

	applySysctls(map[string]string{
		shared:    machine.OSSysctls[shared],
		untouched: machine.OSSysctls[untouched],
	})
	applySysctls(map[string]string{shared: "524288"})

	if got := readParameter(t, dir, shared); got != "524288" {
		t.Errorf("%s: got %q, want the spec's value", shared, got)
	}
	if got := readParameter(t, dir, untouched); got != machine.OSSysctls[untouched] {
		t.Errorf("%s: got %q, want the default to stand", untouched, got)
	}
}
