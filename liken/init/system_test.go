package main

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/liken-sh/liken/liken/machine"
)

// fakeSysctlDir builds a directory laid out like /proc/sys, with a
// file for each named parameter, and points applySysctls at it.
// ApplySysctl does not create a file, so an unknown parameter fails.
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
		"liken.test.no.such.parameter": "1", // the file does not exist
		"../escape/attempt":            "1", // the path leaves the sysctl directory
	})
}

// A typo in a parameter name or an unwritable value in OSSysctls would
// otherwise first show up on a booting machine.
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
		t.Errorf("%s: got %q, want the default", untouched, got)
	}
}

func TestOpenTmpMakesAStickyDirectoryForEveryUser(t *testing.T) {
	cases := []struct {
		name  string
		setup func(t *testing.T, dir string)
	}{
		{"a directory that does not exist", func(t *testing.T, dir string) {}},
		{"a root that arrived with mode 0755", func(t *testing.T, dir string) {
			if err := os.Mkdir(dir, 0o755); err != nil {
				t.Fatal(err)
			}
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "tmp")
			c.setup(t, dir)

			openTmp(dir)

			if got, want := modeOf(t, dir), fs.ModeSticky|0o777; got != want {
				t.Errorf("mode = %v, want %v", got, want)
			}
		})
	}
}
