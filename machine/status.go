package machine

// The observed half of the Machine API. Kubernetes convention draws a
// hard line here: spec is what someone asked for, status is what the
// controllers observed, and status must be *reconstructible*: an
// operator should be able to wipe it and rebuild it purely by looking
// at the machine. Nothing in these types is remembered between passes;
// everything is re-derived from current observation.

import "time"

type MachineStatus struct {
	// Version reports what this machine is running. The k3s and kubelet
	// versions aren't here because Kubernetes already reports them on
	// the built-in Node object; this covers the layer below it.
	Version VersionStatus `json:"version,omitzero"`

	// Role is what this machine is in its cluster: a server (it runs a
	// control plane) or an agent (it runs workloads). Derived at boot
	// from the Cluster manifest's servers list, never declared here.
	Role string `json:"role,omitempty"`

	// Network is the boot's networking outcome, DHCP leases and static
	// assignments alike: the same facts init prints to the console,
	// made queryable.
	Network NetworkStatus `json:"network,omitzero"`

	// Time reports how this machine's clock is doing: the same facts
	// the time loop prints to the console, made queryable. Unlike most
	// of status it changes for the machine's whole life, because the
	// clock is disciplined continuously, not configured once at boot.
	Time TimeStatus `json:"time,omitzero"`

	// Hardware is what the machine found itself running on.
	Hardware HardwareStatus `json:"hardware,omitzero"`

	// Storage reports where every storage role is actually backed this
	// boot, declared or not. The spec says what was asked for and
	// hardware.blockDevices says what's attached; this connects the
	// two.
	Storage StorageStatus `json:"storage,omitzero"`

	// Sysctls echoes the *observed* value of every parameter named in
	// spec.sysctls, read back from /proc/sys, so spec and reality sit
	// side by side in one kubectl get.
	Sysctls map[string]string `json:"sysctls,omitempty"`

	// Boot reports which manifest this boot actually ran under: the
	// reference point that lets the operator compare the cluster's
	// spec against what the machine actuated, and see any rejection
	// that happened on the way up.
	Boot BootStatus `json:"boot,omitzero"`

	// BootedAt is the moment the machine booted, derived by init from
	// the kernel's uptime counter. A timestamp rather than a duration
	// on purpose: it never goes stale in the cluster, and `kubectl get
	// machines` renders it as a live elapsed time (the Uptime column).
	BootedAt *time.Time `json:"bootedAt,omitempty"`

	// Conditions follow the standard Kubernetes idiom: a set of typed,
	// timestamped observations ("Ready", "SysctlsApplied") that
	// controllers maintain and humans and tooling read.
	Conditions []Condition `json:"conditions,omitempty"`
}

type VersionStatus struct {
	Liken  string `json:"liken,omitempty"`
	Kernel string `json:"kernel,omitempty"`

	// Xtables is the netfilter userspace as it reports itself
	// (`iptables -V`, e.g. "v1.8.11 (legacy)"): observed, not echoed
	// from a build pin, like every other fact here.
	Xtables string `json:"xtables,omitempty"`
}

// NetworkStatus reports how the boot attached this machine to the
// network. The top-level fields summarize the *primary* interface:
// the cluster-facing one when the Cluster's nodeCIDR identifies it,
// otherwise the first that came up. Interfaces carries the full
// per-interface detail when a machine has more than one.
type NetworkStatus struct {
	Interface    string     `json:"interface,omitempty"`
	MAC          string     `json:"mac,omitempty"`
	Addresses    []string   `json:"addresses,omitempty"`
	Gateway      string     `json:"gateway,omitempty"`
	Nameservers  []string   `json:"nameservers,omitempty"`
	LeaseExpires *time.Time `json:"leaseExpires,omitempty"`

	Interfaces []InterfaceStatus `json:"interfaces,omitempty"`
}

// How an interface got its address: a DHCP lease, or a static
// assignment from the Machine spec.
const (
	MethodDHCP   = "DHCP"
	MethodStatic = "Static"
)

// InterfaceStatus is one interface as the boot configured it.
type InterfaceStatus struct {
	Name         string     `json:"name"`
	MAC          string     `json:"mac,omitempty"`
	Address      string     `json:"address,omitempty"`
	Method       string     `json:"method,omitempty"`
	Gateway      string     `json:"gateway,omitempty"`
	Nameservers  []string   `json:"nameservers,omitempty"`
	LeaseExpires *time.Time `json:"leaseExpires,omitempty"`
}

// TimeStatus is the machine's account of its own clock. Synchronized
// is deliberately honest: a fleet with no upstreams free-runs, agrees
// with itself, and still reports false here, because agreeing with
// the world is a different claim than agreeing with each other and
// certificate validation cares about the difference.
type TimeStatus struct {
	// Synchronized reports whether the clock is currently being
	// disciplined against a source that is itself synchronized.
	Synchronized bool `json:"synchronized"`

	// Source is who this machine follows: an upstream's name on a
	// server, the cluster endpoint's host on an agent.
	Source string `json:"source,omitempty"`

	// Stratum is the machine's distance from a reference clock in
	// NTP's own vocabulary: a source at stratum n makes this machine
	// stratum n+1. A server free-running on purpose reports the
	// local-clock convention (10); 16 means unsynchronized with no
	// pretense of serving anyone.
	Stratum int `json:"stratum,omitempty"`

	// Offset is the clock error measured at the last exchange, as a
	// human-readable duration ("1.28ms"): positive when this machine
	// was behind its source.
	Offset string `json:"offset,omitempty"`

	// LastSync is when the clock last agreed with its source.
	LastSync *time.Time `json:"lastSync,omitempty"`
}

type HardwareStatus struct {
	CPUs        int    `json:"cpus,omitempty"`
	MemoryBytes uint64 `json:"memoryBytes,omitempty"`

	// BlockDevices is the machine's storage inventory: every real disk
	// the kernel found, whether or not the spec says anything about
	// it. An attached-but-undeclared disk shows up here, which is how
	// you notice one.
	BlockDevices []BlockDevice `json:"blockDevices,omitempty"`
}

// BlockDevice is one disk as the machine observed it, straight from
// sysfs. Name is the kernel's name for this boot (vda, nvme0n1),
// assigned in driver probe order, so it's a handle, not an identity.
// Model and serial come from the device itself.
type BlockDevice struct {
	Name      string `json:"name"`
	SizeBytes uint64 `json:"sizeBytes,omitempty"`
	Model     string `json:"model,omitempty"`
	Serial    string `json:"serial,omitempty"`
}

// Backings a storage role can have. There are exactly two: a
// partition claimed for the role, or the machine's RAM root, which is
// the default and requires no setup.
const (
	BackingPartition = "Partition"
	BackingMemory    = "Memory"
)

// StorageStatus enumerates every role liken knows, whether declared or
// not: absence should be visible in one kubectl get, not implied. The
// fields mirror the spec's keys exactly, so spec and status line up
// name for name.
type StorageStatus struct {
	MachineState     StorageRoleStatus `json:"machineState"`
	MachineEphemeral StorageRoleStatus `json:"machineEphemeral"`
	ClusterState     StorageRoleStatus `json:"clusterState"`
	PodStorage       StorageRoleStatus `json:"podStorage"`
	PodEphemeral     StorageRoleStatus `json:"podEphemeral"`
}

// StorageRoleStatus is where one role is backed. A memory-backed role
// reports no capacity on purpose: all memory-backed roles share the
// one RAM root, and per-role figures would count it several times
// over.
type StorageRoleStatus struct {
	Backing       string `json:"backing"`
	Device        string `json:"device,omitempty"`    // the partition's node this boot: vda1
	Partition     string `json:"partition,omitempty"` // its on-disk name: liken:clusterState
	CapacityBytes uint64 `json:"capacityBytes,omitempty"`
}

// Role addresses one role's status by its spec name; nil for names
// outside the vocabulary.
func (s *StorageStatus) Role(name string) *StorageRoleStatus {
	switch name {
	case "machineState":
		return &s.MachineState
	case "machineEphemeral":
		return &s.MachineEphemeral
	case "clusterState":
		return &s.ClusterState
	case "podStorage":
		return &s.PodStorage
	case "podEphemeral":
		return &s.PodEphemeral
	}
	return nil
}

// AllRolesInMemory marks every role as backed by the RAM root: the
// accurate starting point, upgraded role by role as reconciliation
// places each on a partition.
func AllRolesInMemory() StorageStatus {
	return StorageStatus{
		MachineState:     StorageRoleStatus{Backing: BackingMemory},
		MachineEphemeral: StorageRoleStatus{Backing: BackingMemory},
		ClusterState:     StorageRoleStatus{Backing: BackingMemory},
		PodStorage:       StorageRoleStatus{Backing: BackingMemory},
		PodEphemeral:     StorageRoleStatus{Backing: BackingMemory},
	}
}

// The manifests a boot can run under, in preference order: a staged
// manifest awaiting its proving boot, the proven last-known-good, or
// the image's seed (first boot only; see staging.go).
const (
	ManifestSourceStaged = "Staged"
	ManifestSourceProven = "Proven"
	ManifestSourceSeed   = "Seed"
)

// BootStatus is the boot's account of its own configuration: which
// manifest won, identified by the hash of its exact bytes, and the
// storage spec it actuated. This is the half of drift detection only
// init can supply; the operator compares it against the cluster's
// spec.
type BootStatus struct {
	ManifestSource string      `json:"manifestSource,omitempty"`
	ManifestHash   string      `json:"manifestHash,omitempty"`
	Storage        StorageSpec `json:"storage,omitzero"`

	// Rejection is the standing quarantine record, republished every
	// boot until a promotion clears it, so a rejected spec stays
	// visible in the cluster no matter how many times the machine
	// power-cycles.
	Rejection *Rejection `json:"rejection,omitempty"`
}

// Condition mirrors the shape Kubernetes uses everywhere (Pods, Nodes,
// Deployments all carry these). Status is "True", "False", or
// "Unknown": a string, not a bool, precisely because of that third
// state: a controller that can't currently tell must be able to say so.
type Condition struct {
	Type               string    `json:"type"`
	Status             string    `json:"status"`
	Reason             string    `json:"reason,omitempty"`
	Message            string    `json:"message,omitempty"`
	LastTransitionTime time.Time `json:"lastTransitionTime"`
}

// SetCondition upserts a condition by type, preserving the Kubernetes
// rule that makes lastTransitionTime meaningful: it moves only when
// Status flips, not on every write. That's what lets `kubectl get`
// answer "how long has this machine been Ready?" instead of "when did
// the operator last say so?".
func SetCondition(conditions []Condition, c Condition, now time.Time) []Condition {
	c.LastTransitionTime = now
	for i, existing := range conditions {
		if existing.Type != c.Type {
			continue
		}
		if existing.Status == c.Status {
			c.LastTransitionTime = existing.LastTransitionTime
		}
		conditions[i] = c
		return conditions
	}
	return append(conditions, c)
}
