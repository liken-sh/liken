package machine

// Resource limits are the kernel's per-process ceilings: how many
// files one process may hold open, how many processes one user may
// run. The kernel gives PID 1 a fixed pair of numbers at boot and
// every process inherits its parent's pair across fork and exec.
// There is no /proc file to write and no way to change a process
// that is already running. Whoever starts a process is the only one
// who can decide its limits, and only before it starts.
//
// That inheritance rule is why this matters on liken. A stock
// distribution sets these per service, in each unit's Limit*
// directives, and systemd applies them when it forks the service.
// liken runs no systemd, so nothing applies them, and k3s, containerd,
// the shims, and every container run under the kernel's own defaults
// of 1024 soft and 4096 hard. rlimitdefaults.go is the table that
// answers this, and init applies it to itself before it starts
// anything, because inheritance is the only transport available.
//
// The value syntax is systemd's, so an operator writes into
// spec.rlimits what a unit file writes in LimitNOFILE. A bare number
// sets both halves, the word "infinity" means no limit, and a
// "soft:hard" pair sets the halves separately. Reusing that grammar
// costs nothing and means the value in a Machine manifest can be
// compared to the unit it replaces without translation.
//
// A resource is named by its ulimit short name, again systemd's
// vocabulary: "nofile" is RLIMIT_NOFILE, "nproc" is RLIMIT_NPROC.
// Only the names in rlimitResources exist. An unknown name is an
// error rather than a silent no-op, for the same reason ApplySysctl
// refuses to create a parameter file: a typo that applies nothing
// must be visible.

import (
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"
)

// RlimitInfinity is the word an operator writes, and the word the
// status reports back, for a limit the kernel does not enforce. The
// kernel's own value is the largest unsigned 64-bit integer, which is
// not a number anybody should have to read or type.
const RlimitInfinity = "infinity"

// rlimitResources maps liken's spelling of a resource to the kernel's
// constant. The list is deliberately short. Every entry here is a
// limit that liken has a reason to set, or that a deployment has a
// reason to override, and adding a name is how that reason gets
// recorded. "core" appears here although OSRlimits does not set it,
// because a deployment that wants core dumps must be able to ask for
// them; rlimitdefaults.go explains why liken does not ask by default.
var rlimitResources = map[string]int{
	"nofile":  unix.RLIMIT_NOFILE,
	"nproc":   unix.RLIMIT_NPROC,
	"core":    unix.RLIMIT_CORE,
	"memlock": unix.RLIMIT_MEMLOCK,
	"stack":   unix.RLIMIT_STACK,
	"fsize":   unix.RLIMIT_FSIZE,
	"nice":    unix.RLIMIT_NICE,
}

// RlimitResourceNames lists every resource liken accepts, in sorted
// order. Error messages and the CRD's own description read from this
// list, so the accepted set is stated in one place.
func RlimitResourceNames() []string {
	names := make([]string, 0, len(rlimitResources))
	for name := range rlimitResources {
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}

// ParseRlimit turns one spec value into the pair of numbers
// setrlimit(2) takes. The three accepted forms are systemd's:
//
//	"1048576"          both halves
//	"infinity"         both halves, unlimited
//	"1024:1048576"     soft, then hard
//
// A soft limit above the hard limit is rejected here rather than at
// the syscall, because the syscall's EINVAL says nothing about which
// half was wrong.
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
			"soft limit %s is above hard limit %s; a process may never exceed its hard limit", soft, hard)
	}
	return unix.Rlimit{Cur: softN, Max: hardN}, nil
}

// parseRlimitHalf reads one half of a value. An empty half is
// rejected, so "1024:" and ":4096" are errors rather than a half
// silently taken as zero. A limit of zero is a real setting, and it
// must be spelled out.
func parseRlimitHalf(half string) (uint64, error) {
	if half == RlimitInfinity {
		return unix.RLIM_INFINITY, nil
	}
	if half == "" {
		return 0, fmt.Errorf("empty limit; write a number, %q, or \"soft:hard\"", RlimitInfinity)
	}
	n, err := strconv.ParseUint(half, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("limit %q is not a number or %q", half, RlimitInfinity)
	}
	// The largest unsigned 64-bit value is what the kernel uses for
	// RLIM_INFINITY. A spec that spells that number out gets the word
	// back from FormatRlimit, so refusing it here would make a value
	// that the status reports impossible to declare.
	return n, nil
}

// FormatRlimit renders a pair of numbers back into the spec's own
// syntax. The status reports what the kernel holds, and it reports it
// in the grammar the spec is written in, so a person comparing the
// two is comparing like with like. Equal halves collapse to one
// number, the same way a person would write them.
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

// RlimitResource translates a resource name to the kernel's constant.
func RlimitResource(name string) (int, error) {
	resource, ok := rlimitResources[name]
	if !ok {
		return 0, fmt.Errorf("unknown resource %q; liken accepts %s",
			name, strings.Join(RlimitResourceNames(), ", "))
	}
	return resource, nil
}

// ValidateRlimits checks that every entry names a resource this
// machine has and carries a value that parses. It catches the errors a
// person can fix in the manifest, before a reboot spends itself
// finding them out.
//
// The API server enforces the same rules on any spec applied through
// it, from the CRD's own schema. This check exists for the specs that
// reach a machine another way: init also reads manifests that a person
// wrote by hand and carried in on a stick, and no API server ever saw
// those. It is the same reasoning NetworkSpec.Validate states.
//
// Init does not call this. A limit it cannot apply is skipped with a
// message, because OSRlimits ships with the release and one bad entry
// must never cost a fleet its boot. The operator calls it, where the
// cost of a wrong answer is a condition rather than an outage.
func ValidateRlimits(rlimits map[string]string) error {
	for _, name := range slices.Sorted(maps.Keys(rlimits)) {
		if _, err := RlimitResource(name); err != nil {
			return fmt.Errorf("rlimit %s: %w", name, err)
		}
		if _, err := ParseRlimit(rlimits[name]); err != nil {
			return fmt.Errorf("rlimit %s = %s: %w", name, rlimits[name], err)
		}
	}
	return nil
}

// ApplyRlimit sets one resource limit on the calling process, and
// therefore on every process it starts afterward.
//
// The call goes through golang.org/x/sys/unix rather than the raw
// syscall for a reason that is easy to miss. Go's runtime raises its
// own soft RLIMIT_NOFILE to the hard limit at startup, and remembers
// the original so that os/exec can restore it in each child, sparing
// old programs that use select(2) and its fixed-size descriptor set.
// A child that inherits the restored value inherits the limit liken
// was trying to replace. unix.Setrlimit delegates to syscall.Setrlimit,
// which discards that remembered value, so children inherit what this
// function set. A direct syscall would not, and the machine would look
// correct in /proc/1/limits while every process below it kept the old
// ceiling.
func ApplyRlimit(name, value string) error {
	resource, err := RlimitResource(name)
	if err != nil {
		return fmt.Errorf("rlimit %s: %w", name, err)
	}
	lim, err := ParseRlimit(value)
	if err != nil {
		return fmt.Errorf("rlimit %s = %s: %w", name, value, err)
	}
	if err := unix.Setrlimit(resource, &lim); err != nil {
		return fmt.Errorf("rlimit %s = %s: %w", name, value, err)
	}
	return nil
}

// ReadRlimit reads one resource limit back from the calling process.
// Init builds its report from these reads rather than from what it
// wrote, for the same reason ReadSysctl exists: the report must say
// what the kernel holds, not what somebody asked for. A limit the
// kernel clamped or refused shows its real value here.
func ReadRlimit(name string) (string, error) {
	resource, err := RlimitResource(name)
	if err != nil {
		return "", fmt.Errorf("rlimit %s: %w", name, err)
	}
	var lim unix.Rlimit
	if err := unix.Getrlimit(resource, &lim); err != nil {
		return "", fmt.Errorf("rlimit %s: %w", name, err)
	}
	return FormatRlimit(lim), nil
}
