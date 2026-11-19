package machine

// The kernel exposes each sysctl as a file under /proc/sys, and a
// write to the file sets it. init writes them at boot, and the
// operator can write them because it runs privileged.
//
// kubelet sets some parameters itself, such as vm.overcommit_memory
// and kernel.panic, and a pod can set namespaced net.* parameters.
// spec.sysctls is for the machine-wide parameters that neither sets.

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// sysctlPath returns the file for a parameter name under dir. Dots in
// the name become path separators, unless the name already contains a
// slash. sysctl(8) accepts the slash form for a path segment that has
// a dot, such as an interface named eth0.100.
//
// Names come from the Machine spec, so a name such as
// "../../etc/passwd" must return an error and not become a write
// outside /proc/sys.
func sysctlPath(dir, name string) (string, error) {
	rel := name
	if !strings.Contains(name, "/") {
		rel = strings.ReplaceAll(name, ".", "/")
	}
	// The check runs before filepath.Join, because Join cleans its
	// result and would hide a bad name.
	if filepath.IsAbs(rel) || rel != filepath.Clean(rel) || strings.HasPrefix(rel, "..") {
		return "", fmt.Errorf("sysctl name %q escapes %s: %w", name, dir, fs.ErrInvalid)
	}
	return filepath.Join(dir, rel), nil
}

// ApplySysctl writes one parameter. It does not create the file, so a
// misspelled name returns an error.
func ApplySysctl(dir, name, value string) error {
	path, err := sysctlPath(dir, name)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_TRUNC, 0)
	if err != nil {
		return fmt.Errorf("opening sysctl %s for writing: %w", name, err)
	}
	defer f.Close()
	if _, err := fmt.Fprintln(f, value); err != nil {
		return fmt.Errorf("writing sysctl %s = %q: %w", name, value, err)
	}
	return nil
}

// ReadSysctl returns a parameter's current value without its trailing
// newline. The operator builds status.sysctls from these reads, so
// the status shows what the kernel has and not what the operator
// wrote.
func ReadSysctl(dir, name string) (string, error) {
	path, err := sysctlPath(dir, name)
	if err != nil {
		return "", err
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("reading sysctl %s: %w", name, err)
	}
	return strings.TrimSpace(string(raw)), nil
}
