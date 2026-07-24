package cluster

// The runtime discipline the cluster imposes on the k3s process.
//
// liken runs k3s under a Go runtime environment that init chooses at
// boot. This section is the operator's control over that environment.
// It shapes only the environment init hands the k3s process. containerd
// and the shims k3s starts inherit it, because k3s is their parent. No
// other process reads it: not init, not the operators, not the
// workloads, which get their environment from their own pod specs.
//
// The section is an opt-in. An unset section imposes nothing, so k3s
// runs on Go's own runtime defaults: no memory ceiling, and a heap that
// grows to twice its live data before the collector runs. That trade is
// right on a machine with memory to spare. It is worth tuning on the
// small machines liken targets, where k3s is the dominant resident
// process, and every uncollected megabyte takes memory from the
// workloads.
//
// The section nests under a k3s key so the process it governs is
// named in the path. spec.runtime.k3s.goMemoryLimit reads as "the
// runtime memory limit of the k3s process", which is exactly what it
// is.

import (
	"fmt"
	"strconv"
	"strings"
)

// ClusterRuntimeSpec is the runtime discipline section of a
// ClusterSpec. It holds one subsection per process liken supervises
// directly. Today that is k3s alone.
type ClusterRuntimeSpec struct {
	// K3s is the Go runtime environment for the k3s process init
	// launches, inherited by containerd and the shims.
	K3s K3sRuntimeSpec `json:"k3s,omitzero"`
}

// K3sRuntimeSpec is the Go runtime environment for the k3s process.
// Both fields are read only when k3s starts, so an edit converges by
// restarting k3s in place, the same tier as a features edit. An unset
// field imposes nothing, so k3s keeps Go's own default for it.
type K3sRuntimeSpec struct {
	// GoMemoryLimit is the soft ceiling on everything the k3s
	// runtime manages: heap, stacks, and its own metadata (Go's
	// GOMEMLIMIT). It accepts three forms. "off" removes the ceiling.
	// A percent such as "25%" is that share of this machine's memory,
	// so one setting scales across a fleet of different sizes. An
	// absolute quantity such as "448Mi" is a fixed ceiling on every
	// machine. Left unset, k3s runs with no ceiling, the same as "off".
	GoMemoryLimit string `json:"goMemoryLimit,omitempty"`

	// GoGC is the collector's everyday pace, as a percent of heap
	// growth between collections (Go's GOGC). Left unset, init sets no
	// GOGC, so k3s keeps Go's own pace of one hundred percent. It is a
	// pointer so an explicit value is told apart from unset, and the
	// file doors refuse a value below 1.
	GoGC *int `json:"goGC,omitempty"`
}

// GoGCPercent resolves the collector pace. It reports the set value and
// true, or zero and false when the cluster names none, so a caller can
// tell an explicit pace from Go's own default.
func (k K3sRuntimeSpec) GoGCPercent() (int, bool) {
	if k.GoGC == nil {
		return 0, false
	}
	return *k.GoGC, true
}

// GoMemoryLimitBytes resolves the memory ceiling against this machine's
// memory. It returns the ceiling in bytes, whether the ceiling is off,
// and any error in the setting. An unset limit is no ceiling, the same
// as "off", so k3s runs on Go's own default. "off" returns off true and
// no ceiling.
func (k K3sRuntimeSpec) GoMemoryLimitBytes(memoryBytes uint64) (uint64, bool, error) {
	s := strings.TrimSpace(k.GoMemoryLimit)
	switch {
	case s == "" || s == "off":
		return 0, true, nil
	case strings.HasSuffix(s, "%"):
		pct, err := parseMemoryPercent(s)
		if err != nil {
			return 0, false, err
		}
		return memoryBytes * pct / 100, false, nil
	default:
		bytes, err := parseBinaryQuantity(s)
		return bytes, false, err
	}
}

// parseMemoryPercent reads a whole-number percent in the range
// (0, 100]. Zero would ask for no memory at all, and a value above
// 100 would ask for more than the machine has.
func parseMemoryPercent(s string) (uint64, error) {
	n, err := strconv.ParseUint(strings.TrimSuffix(s, "%"), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("goMemoryLimit %q: a percent must be a whole number, like %q", s, "25%")
	}
	if n < 1 || n > 100 {
		return 0, fmt.Errorf("goMemoryLimit %q: a percent must be between 1%% and 100%%", s)
	}
	return n, nil
}

// parseBinaryQuantity reads an absolute ceiling: a plain byte count,
// or a Ki/Mi/Gi/Ti quantity. It accepts only the power-of-two
// suffixes, the same units the storage math uses, because mixing "2G"
// (decimal) with "2Gi" (binary) would invite a silent seven-percent
// mistake. A zero ceiling is always an error, not a quiet "off".
func parseBinaryQuantity(s string) (uint64, error) {
	units := []struct {
		suffix string
		factor uint64
	}{
		{"Ki", 1 << 10},
		{"Mi", 1 << 20},
		{"Gi", 1 << 30},
		{"Ti", 1 << 40},
	}
	digits := s
	var unit uint64 = 1
	for _, u := range units {
		if rest, ok := strings.CutSuffix(s, u.suffix); ok {
			digits, unit = rest, u.factor
			break
		}
	}
	n, err := strconv.ParseUint(digits, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("goMemoryLimit %q: expected \"off\", a percent like %q, or a quantity like %q", s, "25%", "448Mi")
	}
	if n == 0 {
		return 0, fmt.Errorf("goMemoryLimit %q: a memory ceiling can't be zero; write \"off\" to remove the ceiling", s)
	}
	// The multiply must not wrap. An absurd count times a large unit
	// would otherwise wrap around to a small ceiling and starve k3s
	// silently, which is the worst possible reading of a typo.
	if n > ^uint64(0)/unit {
		return 0, fmt.Errorf("goMemoryLimit %q: the quantity is too large to be a memory size", s)
	}
	return n * unit, nil
}

// Validate holds the runtime section to its shape, so every file door
// refuses garbage the same way the CRD refuses it at admission. It
// checks the memory limit's grammar against a reference memory size,
// because the grammar errors do not depend on the machine, and refuses
// a collector pace below 1.
func (r ClusterRuntimeSpec) Validate() error {
	return r.K3s.Validate()
}

// Validate checks one k3s runtime section.
func (k K3sRuntimeSpec) Validate() error {
	if _, _, err := k.GoMemoryLimitBytes(1 << 30); err != nil {
		return err
	}
	if k.GoGC != nil && *k.GoGC < 1 {
		return fmt.Errorf("goGC %d: GOGC must be at least 1; a smaller value makes the collector run continuously and starve the process of CPU", *k.GoGC)
	}
	return nil
}
