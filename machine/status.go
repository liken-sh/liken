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

	// Network is the boot's DHCP outcome: the same facts init prints to
	// the console, made queryable.
	Network NetworkStatus `json:"network,omitzero"`

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

type NetworkStatus struct {
	Interface    string     `json:"interface,omitempty"`
	MAC          string     `json:"mac,omitempty"`
	Addresses    []string   `json:"addresses,omitempty"`
	Gateway      string     `json:"gateway,omitempty"`
	Nameservers  []string   `json:"nameservers,omitempty"`
	LeaseExpires *time.Time `json:"leaseExpires,omitempty"`
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
	ClusterState    StorageRoleStatus `json:"clusterState"`
	SystemEphemeral StorageRoleStatus `json:"systemEphemeral"`
	PodStorage      StorageRoleStatus `json:"podStorage"`
	PodEphemeral    StorageRoleStatus `json:"podEphemeral"`
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
	case "clusterState":
		return &s.ClusterState
	case "systemEphemeral":
		return &s.SystemEphemeral
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
		ClusterState:    StorageRoleStatus{Backing: BackingMemory},
		SystemEphemeral: StorageRoleStatus{Backing: BackingMemory},
		PodStorage:      StorageRoleStatus{Backing: BackingMemory},
		PodEphemeral:    StorageRoleStatus{Backing: BackingMemory},
	}
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
