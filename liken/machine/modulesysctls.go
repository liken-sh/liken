package machine

// The kernel settings for parameters that a module registers when it
// loads.
//
// A parameter in OSSysctls exists on every liken machine, so a write
// that fails is a fault. A parameter in this table exists only while
// its module is loaded, and a machine that never declares the module
// has no file to write. Both init and the operator apply each entry
// where its file exists and skip it, with no error, where it does not.
// The operator's check of the sysctls (backstop.go in the operator)
// applies it when the module loads later and the file appears.
//
// An entry here is also never written over a spec that names the same
// parameter. Some of these parameters take one write and refuse a
// second value, so the spec's value must be the only write.

import (
	"errors"
	"io/fs"
	"maps"
	"os"
	"slices"
	"strings"
)

// ModuleSysctls holds the settings for parameters that an optional
// module registers.
var ModuleSysctls = map[string]string{
	// Whether each pod gets vsock addresses of its own. vsock is the
	// socket family that a host and its virtual machines talk over,
	// and a guest's address, its CID, is unique in its network
	// namespace. In global mode, a namespace shares the host's
	// addresses, so two pods that each start a guest at CID 3 collide,
	// and any pod can open a socket to any pod's guest. In local mode,
	// a namespace has addresses of its own, and its sockets reach only
	// the guests and sockets in the same namespace. A new namespace
	// takes its mode from its parent's child_ns_mode, so this value
	// gives every pod created after it a namespace of its own. The
	// host's own namespace, and every pod with hostNetwork, stays
	// global, and the kernel stops a global socket from reaching a
	// local namespace.
	//
	// The vsock module registers the parameter, and vhost_vsock loads
	// it. The kernel takes the first write and refuses a different
	// value after it with EBUSY, so a spec that names the parameter
	// sets it alone. machine-operator offers /dev/vhost-vsock to a
	// claim only while this reads local.
	"net.vsock.child_ns_mode": "local",
}

// DefaultSysctls answers the settings that liken applies on this
// machine before the spec's: every entry of OSSysctls, and each entry
// of ModuleSysctls whose parameter exists in dir and that the spec
// does not name. absent names each entry of ModuleSysctls that the
// spec does not name and whose file does not exist.
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

// sysctlFileName answers the path of a parameter under /proc/sys, so
// the two spellings of one name, net.vsock.child_ns_mode and
// net/vsock/child_ns_mode, compare equal.
func sysctlFileName(name string) string {
	if strings.Contains(name, "/") {
		return name
	}
	return strings.ReplaceAll(name, ".", "/")
}
