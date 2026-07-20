package machine

// Sysctls are the kernel's runtime tuning parameters. The kernel
// exposes each sysctl as one file under /proc/sys. No syscall exists
// for sysctls. Writing "1" to /proc/sys/vm/overcommit_memory is the
// interface itself. So applying a sysctl needs no privileges beyond
// a writable /proc/sys. PID 1 has that access at boot. The operator
// gets that access by running as a privileged process.
//
// Other components manage sysctls too. kubelet sets a few parameters
// itself (vm.overcommit_memory, kernel.panic, and others). Per-pod
// sysctls exist for the namespaced net.* family. spec.sysctls covers
// the machine-level parameters that neither of those two paths
// covers.

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// sysctlPath translates a parameter name to its file path. Dots
// become path separators, unless the name already contains slashes.
// A name with slashes uses the kernel's own escape hatch, documented
// in sysctl(8), for path segments with literal dots in them, like an
// interface named eth0.100.
//
// The containment check matters because sysctl names arrive from the
// Machine spec, and the Machine spec is user input. A crafted name
// like "../../etc/passwd" must fail here. It must not become a write
// outside /proc/sys.
func sysctlPath(dir, name string) (string, error) {
	rel := name
	if !strings.Contains(name, "/") {
		rel = strings.ReplaceAll(name, ".", "/")
	}
	// filepath.Join cleans its result. This cleaning would quietly
	// repair a malicious name, by folding "a/../../b" or by rooting an
	// absolute path inside dir. So this function rejects anything
	// absolute or upward-pointing before the join, instead of
	// inspecting the result after.
	if filepath.IsAbs(rel) || rel != filepath.Clean(rel) || strings.HasPrefix(rel, "..") {
		return "", fmt.Errorf("sysctl name %q escapes %s", name, dir)
	}
	return filepath.Join(dir, rel), nil
}

// ApplySysctl sets one parameter. The open call does not create the
// file, by design. A parameter file that the kernel did not put
// there names a parameter this kernel does not have. Creating the
// file would hide a typo silently.
func ApplySysctl(dir, name, value string) error {
	path, err := sysctlPath(dir, name)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_TRUNC, 0)
	if err != nil {
		return fmt.Errorf("sysctl %s: %w", name, err)
	}
	defer f.Close()
	if _, err := fmt.Fprintln(f, value); err != nil {
		return fmt.Errorf("sysctl %s = %s: %w", name, value, err)
	}
	return nil
}

// ReadSysctl reads a parameter's current value, trimmed of the
// newline that the kernel appends. The operator uses ReadSysctl to
// build status.sysctls. The operator reads the kernel's current
// values instead of keeping a record of what it wrote.
func ReadSysctl(dir, name string) (string, error) {
	path, err := sysctlPath(dir, name)
	if err != nil {
		return "", err
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("sysctl %s: %w", name, err)
	}
	return strings.TrimSpace(string(raw)), nil
}
