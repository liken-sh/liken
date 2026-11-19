package machine

// Default values for parameters that a kernel module registers when it
// loads. A parameter in OSSysctls exists on every liken machine, so a
// failed write is a fault. A parameter here has no file until its
// module loads, so init and the operator skip it without an error
// while the file is missing. The operator's sysctl check
// (machine-operator/backstop.go) finds the file when it appears and
// starts a reconcile pass that applies it.
//
// An entry is never applied when the Machine spec sets the same
// parameter. Some of these parameters refuse a second write of a
// different value, so the spec's value must be the only write.

import (
	"errors"
	"io/fs"
	"maps"
	"os"
	"slices"
	"strings"
)

// ModuleSysctls maps each module parameter to liken's default value.
var ModuleSysctls = map[string]string{
	// vsock is the socket family between a host and its virtual
	// machines, and a guest's address (its CID) is unique within a
	// network namespace. With local, each pod's namespace gets its own
	// addresses, and its sockets reach only guests in that namespace.
	// With global, every namespace shares the host's addresses, so two
	// pods that each start a guest at CID 3 collide, and any pod can
	// open a socket to any pod's guest. A new namespace takes its mode
	// from its parent's child_ns_mode, so the value applies to pods
	// created after the write. The host's namespace, and so every
	// hostNetwork pod, stays global, and the kernel does not let a
	// global socket reach a local namespace.
	//
	// vhost_vsock loads the vsock module, which registers the
	// parameter. The kernel refuses a second write of a different
	// value with EBUSY. machine-operator offers /dev/vhost-vsock to a
	// claim only while the value is local.
	"net.vsock.child_ns_mode": "local",
}

// DefaultSysctls returns the settings that liken applies before the
// spec's: every entry of OSSysctls, and each entry of ModuleSysctls
// whose file exists in dir and whose parameter the spec does not set.
// absent lists the ModuleSysctls entries that the spec does not set
// and whose file does not exist.
func DefaultSysctls(dir string, spec map[string]string) (defaults map[string]string, absent []string) {
	named := map[string]bool{}
	for name := range spec {
		named[sysctlFileName(name)] = true
	}
	defaults = maps.Clone(OSSysctls)
	for _, name := range slices.Sorted(maps.Keys(ModuleSysctls)) {
		if named[sysctlFileName(name)] {
			continue
		}
		path, err := sysctlPath(dir, name)
		if err != nil {
			continue
		}
		if _, err := os.Stat(path); errors.Is(err, fs.ErrNotExist) {
			absent = append(absent, name)
			continue
		}
		defaults[name] = ModuleSysctls[name]
	}
	return defaults, absent
}

// sysctlFileName returns a parameter's path relative to /proc/sys, so
// that the two spellings of one name, net.vsock.child_ns_mode and
// net/vsock/child_ns_mode, compare equal.
func sysctlFileName(name string) string {
	if strings.Contains(name, "/") {
		return name
	}
	return strings.ReplaceAll(name, ".", "/")
}
