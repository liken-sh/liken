package machine

import (
	"os"
	"path/filepath"
	"testing"
)

// fakeProcSys builds a miniature /proc/sys in a temp directory with one
// pre-existing parameter file, the way the kernel presents real ones.
func fakeProcSys(t *testing.T, relpath, value string) string {
	t.Helper()
	dir := t.TempDir()
	full := filepath.Join(dir, relpath)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(value), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestSysctlPathTranslation(t *testing.T) {
	tests := []struct {
		name string
		want string
	}{
		{"vm.overcommit_memory", "vm/overcommit_memory"},
		{"net.ipv4.ip_forward", "net/ipv4/ip_forward"},
		// The slash form is the kernel's own escape hatch for interface
		// names that contain dots: with slashes present, dots are literal.
		{"net/ipv4/conf/eth0.100/rp_filter", "net/ipv4/conf/eth0.100/rp_filter"},
	}
	for _, tt := range tests {
		got, err := sysctlPath("/proc/sys", tt.name)
		if err != nil {
			t.Errorf("%s: %v", tt.name, err)
			continue
		}
		if got != filepath.Join("/proc/sys", tt.want) {
			t.Errorf("%s: got %q", tt.name, got)
		}
	}
}

func TestSysctlPathRejectsEscapes(t *testing.T) {
	tests := []string{
		"../shadow",
		"vm/../../etc/passwd",
		"/etc/passwd",
	}
	for _, name := range tests {
		if _, err := sysctlPath("/proc/sys", name); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
}

func TestApplySysctlWritesValue(t *testing.T) {
	dir := fakeProcSys(t, "vm/overcommit_memory", "0\n")
	if err := ApplySysctl(dir, "vm.overcommit_memory", "1"); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "vm/overcommit_memory"))
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != "1\n" {
		t.Errorf("got %q", raw)
	}
}

func TestApplySysctlRejectsUnknownParameter(t *testing.T) {
	dir := fakeProcSys(t, "vm/overcommit_memory", "0\n")
	if err := ApplySysctl(dir, "vm.no_such_knob", "1"); err == nil {
		t.Fatal("expected an error for a parameter the kernel doesn't have")
	}
}

func TestReadSysctlTrimsValue(t *testing.T) {
	dir := fakeProcSys(t, "vm/overcommit_memory", "1\n")
	got, err := ReadSysctl(dir, "vm.overcommit_memory")
	if err != nil {
		t.Fatal(err)
	}
	if got != "1" {
		t.Errorf("got %q", got)
	}
}

func TestApplySysctlRefusesToInventParameters(t *testing.T) {
	// The open deliberately does not create: a parameter file the
	// kernel didn't put there is a parameter this kernel doesn't
	// have, and inventing it would silently hide the typo.
	err := ApplySysctl(t.TempDir(), "vm.nonsense", "1")
	if err == nil {
		t.Error("an unknown parameter must be an error someone sees")
	}
}

func TestReadSysctlReportsUnknownParameters(t *testing.T) {
	if _, err := ReadSysctl(t.TempDir(), "vm.nonsense"); err == nil {
		t.Error("an unknown parameter must be an error someone sees")
	}
}

func TestApplySysctlRejectsEscapingNames(t *testing.T) {
	dir := fakeProcSys(t, "vm/overcommit_memory", "0\n")
	if err := ApplySysctl(dir, "../escape", "1"); err == nil {
		t.Error("a name that escapes the sysctl tree must be an error")
	}
}

func TestReadSysctlRejectsEscapingNames(t *testing.T) {
	dir := fakeProcSys(t, "vm/overcommit_memory", "0\n")
	if _, err := ReadSysctl(dir, "../escape"); err == nil {
		t.Error("a name that escapes the sysctl tree must be an error")
	}
}
