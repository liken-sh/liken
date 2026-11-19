package machine

import (
	"os"
	"path/filepath"
	"testing"
)

// fakeProcSys returns a temporary /proc/sys with one parameter file.
func fakeProcSys(t *testing.T, relpath, value string) string {
	t.Helper()
	dir := t.TempDir()
	full := filepath.Join(dir, relpath)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("creating the parameter directory: %v", err)
	}
	if err := os.WriteFile(full, []byte(value), 0o644); err != nil {
		t.Fatalf("writing the parameter file: %v", err)
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
		// A name with slashes keeps its dots, so it can name an
		// interface such as eth0.100.
		{"net/ipv4/conf/eth0.100/rp_filter", "net/ipv4/conf/eth0.100/rp_filter"},
	}
	for _, tt := range tests {
		got, err := sysctlPath("/proc/sys", tt.name)
		if err != nil {
			t.Errorf("sysctlPath(%q) returned the error %v, want a path", tt.name, err)
			continue
		}
		if got != filepath.Join("/proc/sys", tt.want) {
			t.Errorf("sysctlPath(%q) = %q, want %q", tt.name, got, filepath.Join("/proc/sys", tt.want))
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
			t.Errorf("sysctlPath(%q) returned no error, want one", name)
		}
	}
}

func TestApplySysctlWritesValue(t *testing.T) {
	dir := fakeProcSys(t, "vm/overcommit_memory", "0\n")
	if err := ApplySysctl(dir, "vm.overcommit_memory", "1"); err != nil {
		t.Fatalf("applying vm.overcommit_memory: %v", err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "vm/overcommit_memory"))
	if err != nil {
		t.Fatalf("reading the parameter file: %v", err)
	}
	if string(raw) != "1\n" {
		t.Errorf("the parameter file holds %q, want %q", raw, "1\n")
	}
}

func TestApplySysctlRejectsUnknownParameter(t *testing.T) {
	dir := fakeProcSys(t, "vm/overcommit_memory", "0\n")
	if err := ApplySysctl(dir, "vm.no_such_knob", "1"); err == nil {
		t.Fatal("applying a parameter that the kernel does not have returned no error, want one")
	}
}

func TestReadSysctlTrimsValue(t *testing.T) {
	dir := fakeProcSys(t, "vm/overcommit_memory", "1\n")
	got, err := ReadSysctl(dir, "vm.overcommit_memory")
	if err != nil {
		t.Fatalf("reading vm.overcommit_memory: %v", err)
	}
	if got != "1" {
		t.Errorf("ReadSysctl returned %q, want %q", got, "1")
	}
}

func TestApplySysctlDoesNotCreateUnknownParameters(t *testing.T) {
	err := ApplySysctl(t.TempDir(), "vm.nonsense", "1")
	if err == nil {
		t.Error("applying an unknown parameter returned no error, want one")
	}
}

func TestReadSysctlFailsOnUnknownParameters(t *testing.T) {
	if _, err := ReadSysctl(t.TempDir(), "vm.nonsense"); err == nil {
		t.Error("reading an unknown parameter returned no error, want one")
	}
}

func TestApplySysctlRejectsEscapingNames(t *testing.T) {
	dir := fakeProcSys(t, "vm/overcommit_memory", "0\n")
	if err := ApplySysctl(dir, "../escape", "1"); err == nil {
		t.Error("applying a name that escapes the sysctl tree returned no error, want one")
	}
}

func TestReadSysctlRejectsEscapingNames(t *testing.T) {
	dir := fakeProcSys(t, "vm/overcommit_memory", "0\n")
	if _, err := ReadSysctl(dir, "../escape"); err == nil {
		t.Error("reading a name that escapes the sysctl tree returned no error, want one")
	}
}
