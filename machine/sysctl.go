package machine

// Sysctls: the kernel's runtime tuning knobs, exposed as one file per
// parameter under /proc/sys. There is no syscall for these: writing
// "1" to /proc/sys/vm/overcommit_memory *is* the interface, which is
// why applying them needs no privileges beyond a writable /proc/sys
// (PID 1 has it at boot; the operator gets it by running privileged).
//
// Other components manage sysctls too: kubelet sets a few parameters
// itself (vm.overcommit_memory, kernel.panic, ...) and per-pod sysctls
// exist for the namespaced net.* family. spec.sysctls is for the
// machine-level parameters neither of those covers.

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// sysctlPath translates a parameter name to its file. Dots become path
// separators, unless the name already contains slashes, which is the
// kernel's own escape hatch (documented in sysctl(8)) for path segments
// with literal dots in them, like an interface named eth0.100.
//
// The containment check matters because sysctl names arrive from the
// Machine spec, which is user input. A crafted name like "../../etc/passwd"
// must fail here rather than become a write outside /proc/sys.
func sysctlPath(dir, name string) (string, error) {
	rel := name
	if !strings.Contains(name, "/") {
		rel = strings.ReplaceAll(name, ".", "/")
	}
	// filepath.Join cleans its result, which would quietly *repair* a
	// malicious name (folding "a/../../b" or rooting an absolute path
	// inside dir), so reject anything absolute or upward-pointing
	// before joining, rather than inspecting after.
	if filepath.IsAbs(rel) || rel != filepath.Clean(rel) || strings.HasPrefix(rel, "..") {
		return "", fmt.Errorf("sysctl name %q escapes %s", name, dir)
	}
	return filepath.Join(dir, rel), nil
}

// ApplySysctl sets one parameter. The open deliberately does not
// create: a parameter file the kernel didn't put there is a parameter
// this kernel doesn't have, and inventing the file would silently
// swallow the typo.
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

// ReadSysctl reads a parameter's current value, trimmed of the newline
// the kernel appends. This is how the operator builds status.sysctls:
// not by remembering what it wrote, but by asking the kernel what
// holds.
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
