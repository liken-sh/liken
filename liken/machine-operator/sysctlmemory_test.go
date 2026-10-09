package main

// The kernel stores some values in another form than the one written,
// and two names can write one kernel variable. These tests play such a
// kernel through applySysctl, over a fake /proc/sys.

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// kernelThat replaces applySysctl for one test with a writer that stores
// what store answers for each write, and writes each of also too.
func kernelThat(t *testing.T, store func(name, value string) string, also map[string]string) {
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
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(value+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// A value the kernel stores in another form is written once. The next
// pass finds it as the last write left it, writes nothing, and so a
// backstop pass reports no repair. A pass with no memory writes it
// every time.
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
			kernelThat(t, func(_, value string) string { return strings.ReplaceAll(value, "0x10", "16") }, nil)
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

// When a spec's parameter writes the same kernel variable as a default,
// such as net.ipv4.conf.all.forwarding and net.ipv4.ip_forward, both
// names report the value the kernel holds after both writes. The check
// of the sysctls then finds nothing drifted, and the next pass writes
// nothing, so the two writes do not repeat on every check.
func TestTwoNamesForOneSwitchSettleAfterOnePass(t *testing.T) {
	dir := fakeSysctls(t, map[string]string{"net.ipv4.ip_forward": "0", "net.ipv4.conf.all.forwarding": "1"})
	kernelThat(t, func(_, value string) string { return value }, map[string]string{"net.ipv4.conf.all.forwarding": "net.ipv4.ip_forward"})
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

// A write-only parameter refuses every read, so it is written once for
// each value, not on every pass.
func TestAWriteOnlyParameterIsWrittenOncePerValue(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads a file of mode 0200")
	}
	dir := fakeSysctls(t, map[string]string{"vm.drop_caches": "0"})
	if err := os.Chmod(sysctlFilePath(dir, "vm.drop_caches"), 0o200); err != nil {
		t.Fatal(err)
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

// A spec's name in the slash spelling overrides the default in the dot
// spelling, because both name one file.
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

// A parameter whose file did not exist, such as one under an interface
// that has not appeared, makes the check run a pass once the file
// exists.
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
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(value+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}
