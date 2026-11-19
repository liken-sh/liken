package main

// The kernel can store a value in a different form than the one
// written, and two sysctl names can write the same kernel variable.
// These tests fake such a kernel by replacing applySysctl.

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// fakeKernel replaces applySysctl for one test. Each write stores the
// value that store returns. When also maps the written name to another
// name, the write stores the value under that name too.
func fakeKernel(t *testing.T, store func(name, value string) string, also map[string]string) {
	t.Helper()
	saved := applySysctl
	t.Cleanup(func() { applySysctl = saved })
	applySysctl = func(dir, name, value string) error {
		if err := os.WriteFile(sysctlFilePath(dir, name), []byte(store(name, value)+"\n"), 0o644); err != nil {
			return err
		}
		if other, ok := also[name]; ok {
			return os.WriteFile(sysctlFilePath(dir, other), []byte(store(other, value)+"\n"), 0o644)
		}
		return nil
	}
}

func sysctlFilePath(dir, name string) string {
	return filepath.Join(dir, sysctlFile(name))
}

// fakeSysctls writes each parameter into a fake /proc/sys.
func fakeSysctls(t *testing.T, values map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, value := range values {
		path := sysctlFilePath(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("creating the fake parameter directory: %v", err)
		}
		if err := os.WriteFile(path, []byte(value+"\n"), 0o644); err != nil {
			t.Fatalf("writing the fake parameter %s: %v", name, err)
		}
	}
	return dir
}

// sysctlMemory records the value that the kernel stored after the
// last write, so the next pass writes nothing and a backstop pass
// reports no repair. Without it, each pass writes the value again.
func TestAValueTheKernelStoresInAnotherFormIsWrittenOnce(t *testing.T) {
	cases := []struct {
		name   string
		mem    *sysctlMemory
		writes int
	}{
		{"with the memory", newSysctlMemory(), 0},
		{"with no memory", nil, 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := fakeSysctls(t, map[string]string{"kernel.test_value": "0"})
			fakeKernel(t, func(_, value string) string { return strings.ReplaceAll(value, "0x10", "16") }, nil)
			desired := map[string]string{"kernel.test_value": "0x10"}
			applySysctls(dir, nil, desired, &passOutcome{}, c.mem)

			second := &passOutcome{}
			observed, _, _, _ := applySysctls(dir, nil, desired, second, c.mem)

			if len(second.writes) != c.writes || observed["kernel.test_value"] != "16" {
				t.Errorf("the second pass wrote %q and reports %q, want %d writes and 16", second.writes, observed["kernel.test_value"], c.writes)
			}
		})
	}
}

// A parameter in the spec can write the same kernel variable as a
// default, as net.ipv4.conf.all.forwarding and net.ipv4.ip_forward do.
// After one pass, the check finds no drift and the next pass writes
// nothing, so the two writes do not repeat on every check.
func TestTwoNamesForOneSwitchSettleAfterOnePass(t *testing.T) {
	dir := fakeSysctls(t, map[string]string{"net.ipv4.ip_forward": "0", "net.ipv4.conf.all.forwarding": "1"})
	fakeKernel(t, func(_, value string) string { return value }, map[string]string{"net.ipv4.conf.all.forwarding": "net.ipv4.ip_forward"})
	defaults := map[string]string{"net.ipv4.ip_forward": "1"}
	desired := map[string]string{"net.ipv4.conf.all.forwarding": "0"}
	mem := newSysctlMemory()

	observed, _, _, _ := applySysctls(dir, defaults, desired, &passOutcome{}, mem)
	check := sysctlCheck{applied: observed}
	second := &passOutcome{}
	applySysctls(dir, defaults, desired, second, mem)

	if observed["net.ipv4.ip_forward"] != "0" || check.drifted(dir) || len(second.writes) != 0 {
		t.Errorf("ip_forward reads %q, the check drifted = %v, and the second pass wrote %q; want 0, false, nothing",
			observed["net.ipv4.ip_forward"], check.drifted(dir), second.writes)
	}
}

func TestAWriteOnlyParameterIsWrittenOncePerValue(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads a file of mode 0200")
	}
	dir := fakeSysctls(t, map[string]string{"vm.drop_caches": "0"})
	if err := os.Chmod(sysctlFilePath(dir, "vm.drop_caches"), 0o200); err != nil {
		t.Fatalf("setting the mode of vm.drop_caches: %v", err)
	}
	mem := newSysctlMemory()
	first, second, third := &passOutcome{}, &passOutcome{}, &passOutcome{}

	applySysctls(dir, nil, map[string]string{"vm.drop_caches": "3"}, first, mem)
	applySysctls(dir, nil, map[string]string{"vm.drop_caches": "3"}, second, mem)
	applySysctls(dir, nil, map[string]string{"vm.drop_caches": "1"}, third, mem)

	if len(first.writes) != 1 || len(second.writes) != 0 || len(third.writes) != 1 {
		t.Errorf("the passes wrote %d, %d, and %d times, want 1, 0, and 1", len(first.writes), len(second.writes), len(third.writes))
	}
}

// A spec name with slashes overrides the default with dots, because
// both name the same file.
func TestASlashSpellingOverridesTheDottedDefault(t *testing.T) {
	kept := withoutKeys(map[string]string{"net.ipv4.ip_forward": "1", "vm.swappiness": "10"},
		map[string]string{"net/ipv4/ip_forward": "0"})

	if got := slices.Sorted(func(yield func(string) bool) {
		for name := range kept {
			if !yield(name) {
				return
			}
		}
	}); !slices.Equal(got, []string{"vm.swappiness"}) {
		t.Errorf("the defaults kept %q, want only vm.swappiness", got)
	}
}

// A parameter whose file does not exist yet, such as one under a
// network interface that has not appeared, makes the check report
// drift when the file appears.
func TestTheCheckRunsAPassWhenAMissingParameterAppears(t *testing.T) {
	dir := fakeSysctls(t, nil)
	_, missing, _, _ := applySysctls(dir, nil, map[string]string{"net.ipv4.conf.wg0.forwarding": "1"}, &passOutcome{}, newSysctlMemory())
	check := sysctlCheck{applied: map[string]string{}, missing: missing}
	before := check.drifted(dir)

	fakeSysctlsAt(t, dir, map[string]string{"net.ipv4.conf.wg0.forwarding": "0"})

	if before || !check.drifted(dir) {
		t.Errorf("the check drifted %v before the file existed and %v after, want false and true", before, check.drifted(dir))
	}
}

func fakeSysctlsAt(t *testing.T, dir string, values map[string]string) {
	t.Helper()
	for name, value := range values {
		path := sysctlFilePath(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("creating the fake parameter directory: %v", err)
		}
		if err := os.WriteFile(path, []byte(value+"\n"), 0o644); err != nil {
			t.Fatalf("writing the fake parameter %s: %v", name, err)
		}
	}
}
