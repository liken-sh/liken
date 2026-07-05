package machine

// The observed half of the Machine API. Kubernetes convention draws a
// hard line here: spec is what someone asked for, status is what the
// controllers observed, and status must be *reconstructible* — an
// operator should be able to wipe it and rebuild it purely by looking
// at the machine. Nothing in these types is remembered; everything is
// re-derived, which is what makes it trustworthy.

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

	// Sysctls echoes the *observed* value of every parameter named in
	// spec.sysctls, read back from /proc/sys — so spec and reality sit
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
	// (`iptables -V`, e.g. "v1.8.11 (legacy)") — observed, not echoed
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
}

// Condition mirrors the shape Kubernetes uses everywhere (Pods, Nodes,
// Deployments all carry these). Status is "True", "False", or
// "Unknown" — a string, not a bool, precisely because of that third
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
