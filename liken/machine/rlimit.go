package machine

// A child process inherits its parent's resource limits across fork
// and exec, and no process can change the limits of another running
// process. On a distribution with systemd, each unit sets its limits
// with directives such as LimitNOFILE. liken has no systemd, so without
// help k3s, containerd, and every container would run with the
// kernel's default nofile limit of 1024 soft and 4096 hard. So init
// sets the limits on itself before it starts anything, and every
// process on the machine inherits them.
//
// spec.rlimits uses systemd's syntax, so a value reads like the same
// setting in a unit file, and the ulimit short names, such as "nofile"
// for RLIMIT_NOFILE. A name that is not in rlimitResources is an
// error, because a typo that applied nothing would be hard to find.

import (
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"
)

// RlimitInfinity is the spec and status value for no limit. The
// kernel's value for it is RLIM_INFINITY, the largest uint64.
const RlimitInfinity = "infinity"

// rlimitResources lists the limits that liken sets or that a
// deployment has a reason to override. The CRD's validation rule for
// spec.rlimits lists the same names. "core" is here although
// OSRlimits does not set it, so a deployment can turn on core dumps.
var rlimitResources = map[string]int{
	"nofile":  unix.RLIMIT_NOFILE,
	"nproc":   unix.RLIMIT_NPROC,
	"core":    unix.RLIMIT_CORE,
	"memlock": unix.RLIMIT_MEMLOCK,
	"stack":   unix.RLIMIT_STACK,
	"fsize":   unix.RLIMIT_FSIZE,
	"nice":    unix.RLIMIT_NICE,
}

// RlimitResourceNames returns the accepted resource names, sorted.
func RlimitResourceNames() []string {
	names := make([]string, 0, len(rlimitResources))
	for name := range rlimitResources {
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}

// ParseRlimit converts one spec value into the soft and hard limits
// that setrlimit(2) takes. It accepts these forms:
//
//	"1048576"          the same soft and hard limit
//	"infinity"         no soft or hard limit
//	"1024:1048576"     soft, then hard
//
// ParseRlimit refuses a soft limit above the hard limit itself,
// because setrlimit returns a bare EINVAL that names neither value.
// The CRD's pattern does not check this.
func ParseRlimit(value string) (unix.Rlimit, error) {
	soft, hard, found := strings.Cut(value, ":")
	if !found {
		hard = soft
	}
	softN, err := parseRlimitHalf(soft)
	if err != nil {
		return unix.Rlimit{}, err
	}
	hardN, err := parseRlimitHalf(hard)
	if err != nil {
		return unix.Rlimit{}, err
	}
	if softN > hardN {
		return unix.Rlimit{}, fmt.Errorf(
			"the soft limit %s is above the hard limit %s, and a process cannot exceed its hard limit", soft, hard)
	}
	return unix.Rlimit{Cur: softN, Max: hardN}, nil
}

// parseRlimitHalf parses the soft or the hard limit of a value. An
// empty string is an error, so "1024:" is not read as a hard limit of
// zero.
func parseRlimitHalf(half string) (uint64, error) {
	if half == RlimitInfinity {
		return unix.RLIM_INFINITY, nil
	}
	if half == "" {
		return 0, fmt.Errorf("a limit is empty, and must be a number, %q, or soft:hard", RlimitInfinity)
	}
	n, err := strconv.ParseUint(half, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("the limit %q is neither a number nor %q", half, RlimitInfinity)
	}
	return n, nil
}

// FormatRlimit writes a limit in the spec's syntax, so a status value
// can be compared with a spec value as a string.
func FormatRlimit(lim unix.Rlimit) string {
	soft := formatRlimitHalf(lim.Cur)
	hard := formatRlimitHalf(lim.Max)
	if soft == hard {
		return soft
	}
	return soft + ":" + hard
}

func formatRlimitHalf(n uint64) string {
	if n == unix.RLIM_INFINITY {
		return RlimitInfinity
	}
	return strconv.FormatUint(n, 10)
}

// RlimitResource returns the kernel's constant for a resource name.
func RlimitResource(name string) (int, error) {
	resource, ok := rlimitResources[name]
	if !ok {
		return 0, fmt.Errorf("unknown resource %q, want one of %s",
			name, strings.Join(RlimitResourceNames(), ", "))
	}
	return resource, nil
}

// ValidateRlimits returns an error for the first entry, in name order,
// whose resource is unknown or whose value does not parse.
//
// machine-operator calls it before it stages a spec, and refuses a bad
// spec with a SpecConverged condition. Without this check the error
// would show only on the console after a reboot, because init skips a
// limit that it cannot apply so that one bad entry in OSRlimits cannot
// stop a whole fleet from booting.
func ValidateRlimits(rlimits map[string]string) error {
	for _, name := range slices.Sorted(maps.Keys(rlimits)) {
		if _, err := RlimitResource(name); err != nil {
			return fmt.Errorf("spec.rlimits: %w", err)
		}
		if _, err := ParseRlimit(rlimits[name]); err != nil {
			return fmt.Errorf("spec.rlimits.%s: %w", name, err)
		}
	}
	return nil
}

// ApplyRlimit sets one resource limit on the calling process.
//
// It must call unix.Setrlimit and not the raw syscall. At startup the
// Go runtime raises its soft RLIMIT_NOFILE to the hard limit and saves
// the original, and os/exec restores the saved limit in each child.
// unix.Setrlimit calls syscall.Setrlimit, which clears the saved
// limit. With the raw syscall, init would have the new limit and
// every child would start with the old one.
func ApplyRlimit(name, value string) error {
	resource, err := RlimitResource(name)
	if err != nil {
		return fmt.Errorf("rlimit %s: %w", name, err)
	}
	lim, err := ParseRlimit(value)
	if err != nil {
		return fmt.Errorf("rlimit %s: %w", name, err)
	}
	if err := unix.Setrlimit(resource, &lim); err != nil {
		return fmt.Errorf("setting rlimit %s to %s: %w", name, value, err)
	}
	return nil
}

// ReadRlimit returns one resource limit of the calling process, in the
// spec's syntax. Init reports these reads and not the values it wrote,
// so status.rlimits shows a limit that the kernel refused at its real
// value.
func ReadRlimit(name string) (string, error) {
	resource, err := RlimitResource(name)
	if err != nil {
		return "", fmt.Errorf("rlimit %s: %w", name, err)
	}
	var lim unix.Rlimit
	if err := unix.Getrlimit(resource, &lim); err != nil {
		return "", fmt.Errorf("reading rlimit %s: %w", name, err)
	}
	return FormatRlimit(lim), nil
}
