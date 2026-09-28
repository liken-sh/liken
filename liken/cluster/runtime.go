package cluster

// The runtime discipline the cluster imposes on the k3s process, on
// the components inside it, and on the container runtime beside it.
//
// liken supervises one long-lived process: k3s. This section is the
// operator's control over how that process runs, and it has one
// subsection per thing that reads a setting. The k3s subsection is the
// discipline of the process itself. The kubelet subsection is the
// configuration of the kubelet, which is a component compiled into
// that same process rather than a program of its own. The containerd
// subsection is the configuration of containerd, which is a separate
// program that k3s starts. The split follows the reader, so a path
// names the thing that acts on the value:
// spec.runtime.k3s.goMemoryLimit reads as "the runtime memory limit of
// the k3s process", spec.runtime.kubelet.imageGC.maximumAge reads as
// "the age ceiling of the kubelet's image collector", and
// spec.runtime.containerd.logLevel reads as "the log level of
// containerd".
//
// Every setting here is an opt-in. An unset field imposes nothing, so
// the reader keeps its own default: Go's runtime defaults for the k3s
// subsection, the kubelet's own image collection policy for the
// kubelet subsection, and containerd's own level for the containerd
// subsection. This matters for an upgrade. A cluster that names
// nothing renders the same bytes it rendered before, so no machine
// restarts k3s to gain a section it never asked for.
//
// Every setting here is also read only when the k3s process starts, so
// an edit converges by restarting k3s in place, the same tier as a
// features edit. changes.go classifies the whole section that way.
// containerd is included in that rule, because k3s starts containerd
// and stops it again, so a k3s restart is also a containerd restart.

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"
)

// ClusterRuntimeSpec is the runtime discipline section of a
// ClusterSpec. It holds one subsection per thing that reads a
// setting: the k3s process, the kubelet inside it, and containerd
// beside it.
type ClusterRuntimeSpec struct {
	// K3s is the runtime discipline of the k3s process init launches:
	// the Go environment it runs under, and how much it prints.
	K3s K3sRuntimeSpec `json:"k3s,omitzero"`

	// Kubelet is the configuration of the kubelet component that runs
	// inside the k3s process, on every machine of the cluster.
	Kubelet KubeletRuntimeSpec `json:"kubelet,omitzero"`

	// Containerd is the configuration of containerd, the container
	// runtime that k3s starts on every machine of the cluster.
	Containerd ContainerdRuntimeSpec `json:"containerd,omitzero"`
}

// K3sRuntimeSpec is the runtime discipline of the k3s process itself:
// the Go environment it runs under, and how much it prints. Every
// field is read only when k3s starts, so an edit converges by
// restarting k3s in place, the same tier as a features edit. An unset
// field imposes nothing, so k3s keeps its own default for it.
//
// The two Go values shape only the environment init hands the k3s
// process, where k3s keeps Go's own defaults: no memory ceiling, and a
// heap that grows to twice its live data before the collector runs.
// That trade is right on a machine with memory to spare. It is worth
// tuning on the small machines liken targets, where k3s is the
// dominant resident process, and every uncollected megabyte takes
// memory from the workloads. containerd and the shims k3s starts
// inherit that environment, because k3s is their parent. No other
// process reads it: not init, not the operators, not the workloads,
// which get their environment from their own pod specs.
//
// Debug is not part of that environment. It is a k3s setting of its
// own, and it reaches only the components compiled into the k3s
// process. containerd has a level of its own, in the containerd
// subsection.
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

	// Debug raises the k3s process to debug logging, the same thing
	// k3s's own --debug flag does. It reaches every Kubernetes
	// component compiled into the process: the API server, the
	// scheduler, the controllers, and the kubelet. Left unset, k3s
	// logs at info. Turn it on to read a decision that the info lines
	// do not explain, and turn it off again, because debug multiplies
	// the volume of a stream that a small machine has to store and
	// ship off itself.
	Debug bool `json:"debug,omitempty"`
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

// KubeletRuntimeSpec is the configuration of the kubelet that runs
// inside the k3s process. The kubelet reads its configuration once,
// when the process starts, so an edit converges by restarting k3s in
// place.
type KubeletRuntimeSpec struct {
	// ImageGC is the policy for the kubelet's image collector, which
	// prunes containerd's image store.
	ImageGC ImageGCSpec `json:"imageGC,omitzero"`
}

// ImageGCSpec is the cluster's image collection policy. containerd's
// image store grows with every image a node pulls, and nothing in the
// store is removed while a container uses it. The kubelet is the only
// thing that prunes it, and it prunes on two triggers: the disk
// thresholds, and the age of an unused image.
//
// The thresholds are percentages of the filesystem that holds the
// store, which on liken is clusterState. The kubelet measures that
// filesystem every five minutes. When usage passes the high threshold,
// it removes unused images, oldest first, until usage falls under the
// low threshold. A percentage rather than a byte count is what lets
// one setting serve a fleet of different disk sizes.
//
// The ages are the other trigger, and they work on one image at a
// time. An image unused for longer than MaximumAge is removed even
// when the disk is nearly empty, which keeps a store from carrying
// years of tags no workload names any more. An image unused for less
// than MinimumAge is kept even when the disk is full, which stops a
// node from re-pulling an image it just stopped using.
//
// The thresholds are pointers so that an explicit value is told apart
// from unset. An unset field leaves the kubelet's own default in
// place, and a section that names no field at all renders no
// configuration file, so the kubelet keeps its whole default policy.
type ImageGCSpec struct {
	// HighThresholdPercent is the disk usage that starts collection,
	// as a percent of the filesystem that holds containerd's image
	// store. The kubelet's own default is 85.
	HighThresholdPercent *int `json:"highThresholdPercent,omitempty"`

	// LowThresholdPercent is the disk usage that stops collection,
	// as a percent of the same filesystem. It must be below
	// HighThresholdPercent. The kubelet's own default is 80.
	LowThresholdPercent *int `json:"lowThresholdPercent,omitempty"`

	// MaximumAge is how long an unused image may stay in the store
	// before the kubelet removes it, regardless of disk usage, as a Go
	// duration such as "168h". The kubelet does no age check by
	// default.
	MaximumAge string `json:"maximumAge,omitempty"`

	// MinimumAge is how long an unused image is kept before the
	// kubelet may remove it, as a Go duration such as "5m". The
	// kubelet's own default is two minutes.
	MinimumAge string `json:"minimumAge,omitempty"`
}

// The kubelet's own image collection defaults, which hold for every
// field the cluster leaves unset. They are named here because the
// validation needs them: a document that sets one threshold and not
// the other is still a pair, and the pair has to make sense against
// the value the kubelet will supply for the missing half.
const (
	defaultImageGCHighThresholdPercent = 85
	defaultImageGCLowThresholdPercent  = 80
	defaultImageMinimumGCAge           = 2 * time.Minute
)

// Validate holds the image collection policy to what the kubelet
// accepts when it starts. The kubelet refuses a threshold outside 0 to
// 100, a low threshold at or above the high one, and a maximum age at
// or below the minimum. It exits rather than starting without them, so
// a machine that boots a rejected configuration runs no control plane
// and serves no shell to repair it. This door is therefore at least as
// strict as the kubelet's own.
func (g ImageGCSpec) Validate() error {
	// A field the document leaves unset still takes part in the
	// comparisons below, because the kubelet supplies its own default
	// for the missing half and then compares the pair. A lone
	// highThresholdPercent of 70 sits under the default low of 80, and
	// the kubelet refuses that pair. Naming where each value came from
	// is what makes the error tell an operator which field to edit.
	const fromDefault = " (the kubelet's default)"
	high, highNote := defaultImageGCHighThresholdPercent, fromDefault
	if g.HighThresholdPercent != nil {
		high, highNote = *g.HighThresholdPercent, ""
		if err := validateThresholdPercent("highThresholdPercent", high); err != nil {
			return err
		}
	}
	low, lowNote := defaultImageGCLowThresholdPercent, fromDefault
	if g.LowThresholdPercent != nil {
		low, lowNote = *g.LowThresholdPercent, ""
		if err := validateThresholdPercent("lowThresholdPercent", low); err != nil {
			return err
		}
	}
	if low >= high {
		return fmt.Errorf("lowThresholdPercent %d%s must be below highThresholdPercent %d%s, so collection has a range to work in", low, lowNote, high, highNote)
	}

	minimum, minimumNote := defaultImageMinimumGCAge, fromDefault
	if g.MinimumAge != "" {
		parsed, err := parseImageAge("minimumAge", g.MinimumAge)
		if err != nil {
			return err
		}
		minimum, minimumNote = parsed, ""
	}
	if g.MaximumAge != "" {
		maximum, err := parseImageAge("maximumAge", g.MaximumAge)
		if err != nil {
			return err
		}
		if maximum <= minimum {
			return fmt.Errorf("maximumAge %q must be greater than minimumAge %s%s, so an unused image has a span in which it is kept", g.MaximumAge, minimum, minimumNote)
		}
	}
	return nil
}

// validateThresholdPercent bounds one threshold to 1 through 100. Zero
// would ask the kubelet to collect against a disk that is never empty
// enough, and a value above 100 names a fullness a filesystem cannot
// reach, so collection would never start.
func validateThresholdPercent(field string, value int) error {
	if value < 1 || value > 100 {
		return fmt.Errorf("%s %d: a threshold is a percent of the filesystem that holds the image store, so it must be between 1 and 100", field, value)
	}
	return nil
}

// parseImageAge reads one age as a Go duration, the same grammar the
// kubelet's own configuration file uses. A zero or negative age is
// refused here rather than passed on, because the kubelet reads zero
// as "no age check" and would then run a policy the document did not
// ask for.
func parseImageAge(field, value string) (time.Duration, error) {
	age, err := time.ParseDuration(value)
	if err != nil {
		return 0, fmt.Errorf("%s %q: expected a Go duration, like %q or %q", field, value, "30m", "168h")
	}
	if age <= 0 {
		return 0, fmt.Errorf("%s %q: an age must be greater than zero", field, value)
	}
	return age, nil
}

// Validate checks the kubelet section.
func (k KubeletRuntimeSpec) Validate() error {
	if err := k.ImageGC.Validate(); err != nil {
		return fmt.Errorf("imageGC: %w", err)
	}
	return nil
}

// ContainerdRuntimeSpec is the configuration of containerd, the
// container runtime that runs beside the k3s process. containerd is a
// program of its own that k3s starts, not a component compiled into
// k3s, so it keeps its own configuration file and its own log level.
// Nothing in the k3s subsection reaches it.
//
// containerd is the loudest writer on a liken machine. At info it
// prints a line for each step of every pod's life, on every node, for
// as long as the node runs. That detail is worth having while a
// cluster is new and worth turning down once it is not, and a machine
// serves no shell in which to turn it down by hand.
type ContainerdRuntimeSpec struct {
	// LogLevel is how much containerd prints. Left unset, containerd
	// keeps its own default of info. Set "warn" to keep the failures
	// and drop the pod lifecycle lines.
	LogLevel string `json:"logLevel,omitempty"`
}

// containerdLogLevels are the levels a cluster may name, from loudest
// to quietest. containerd itself takes three more (trace above debug,
// and fatal and panic below error), which liken does not offer. trace
// is a volume no machine should write to the disk it also runs on, and
// fatal and panic drop the error lines written before a crash, the
// lines that explain it.
var containerdLogLevels = []string{"debug", "info", "warn", "error"}

// Validate holds the containerd section to the levels above. containerd
// exits when it reads a level it does not know, and k3s does not start
// without containerd, so a machine that boots a rejected level runs no
// workloads and serves no shell to repair it.
func (c ContainerdRuntimeSpec) Validate() error {
	if c.LogLevel == "" || slices.Contains(containerdLogLevels, c.LogLevel) {
		return nil
	}
	return fmt.Errorf("logLevel %q: expected one of %s", c.LogLevel, strings.Join(containerdLogLevels, ", "))
}

// Validate holds the runtime section to its shape, so every file door
// refuses garbage the same way the CRD refuses it at admission. Each
// subsection names itself in the error, so a message reads as the path
// of the field that is wrong.
func (r ClusterRuntimeSpec) Validate() error {
	if err := r.K3s.Validate(); err != nil {
		return fmt.Errorf("k3s: %w", err)
	}
	if err := r.Kubelet.Validate(); err != nil {
		return fmt.Errorf("kubelet: %w", err)
	}
	if err := r.Containerd.Validate(); err != nil {
		return fmt.Errorf("containerd: %w", err)
	}
	return nil
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
