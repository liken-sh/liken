package machine

// The observed half of the Machine API. Kubernetes convention draws a
// hard line here: spec is what someone asked for, status is what the
// controllers observed, and status must be *reconstructible*: an
// operator should be able to wipe it and rebuild it purely by looking
// at the machine. Nothing in these types is remembered between passes;
// everything is re-derived from current observation.

import "time"

type MachineStatus struct {
	// Phase is the machine's whole story in one word, computed from
	// the conditions below on every pass — never remembered, so it can
	// never go stale relative to them. Conditions are for programs
	// (kubectl wait, controllers); the phase is for the human scanning
	// a fleet listing. See the Phase constants for the vocabulary.
	Phase Phase `json:"phase,omitempty"`

	// Version reports what this machine is running. The k3s and kubelet
	// versions aren't here because Kubernetes already reports them on
	// the built-in Node object; this covers the layer below it.
	Version VersionStatus `json:"version,omitzero"`

	// Role is what this machine is in its cluster: a leader (it runs
	// a control plane) or a follower (it runs workloads). Derived at
	// boot from the Cluster manifest's leaders list, never declared
	// here.
	Role Role `json:"role,omitempty"`

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

	// Firmware is the machine's standing firmware state: the mode it
	// boots in and the boot menu from its non-volatile store — UEFI
	// with its variables decoded into words, or BIOS, shorthand for
	// any world without firmware variables to consult (a legacy
	// server, a direct-kernel boot under a hypervisor). It sits
	// beside Hardware because it describes the machine, not the
	// boot: BootOrder is a standing preference and BootNext is about
	// the *next* boot; only bootCurrent is this boot's fact, kept
	// here with its family. Contrast Boot, the per-boot record.
	Firmware FirmwareStatus `json:"firmware,omitzero"`

	// Storage reports where every storage role is actually backed this
	// boot, declared or not. The spec says what was asked for and
	// hardware.blockDevices says what's attached; this connects the
	// two.
	Storage StorageStatus `json:"storage,omitzero"`

	// Sysctls echoes the *observed* value of every parameter named in
	// spec.sysctls, read back from /proc/sys, so spec and reality sit
	// side by side in one kubectl get.
	Sysctls map[string]string `json:"sysctls,omitempty"`

	// Boot is what this boot ran under: which documents, and the
	// storage as actuated — the perishable record, re-made every
	// boot, that the operator diffs the spec against. Its lifetime
	// is what separates it from Firmware: power-cut the machine and
	// boot it unchanged, and everything here is freshly re-derived
	// (a staged manifest applies, a rejection lands), while Firmware
	// reports the standing state that rode through.
	Boot BootStatus `json:"boot,omitzero"`

	// BootedAt is the moment the machine booted, derived by init from
	// the kernel's uptime counter. A timestamp rather than a duration
	// on purpose: it never goes stale in the cluster, and `kubectl get
	// machines` renders it as a live elapsed time (the Uptime column).
	//
	// Deliberately absent: a heartbeat. The machine's liveness signal
	// is a Lease in the liken-machine-lease namespace, not a status
	// field, because a heartbeat must renew forever and a status
	// write rewrites this whole object and wakes every watcher — the
	// reason kube-node-lease exists (see operator/lease.go). The
	// leaders watch the leases and mark a silent machine Lost.
	BootedAt *time.Time `json:"bootedAt,omitempty"`

	// Conditions follow the standard Kubernetes idiom: a set of typed,
	// timestamped observations ("Ready", "SysctlsApplied") that
	// controllers maintain and humans and tooling read.
	Conditions []Condition `json:"conditions,omitempty"`
}

// Phase is a machine's whole story in one word. Named string types
// are how Go models a closed vocabulary: the constants below are the
// only values, the compiler catches a Phase handed where a Role
// belongs, and the wire format is unchanged (a named string marshals
// exactly like a bare one). Kubernetes' own API types use the same
// idiom (v1.PodPhase, metav1.ConditionStatus).
type Phase string

// The phases a machine can report, most severe first. Each is a
// summary of the conditions, not a fact of its own; the operator
// derives the phase from the conditions on every pass, and the table
// that does so (operator/phase.go) is the authority on which
// condition puts a machine in which phase. Lost is the exception to
// "derived from own conditions": a machine cannot report its own
// death, so a leader writes Lost on its behalf when its heartbeat
// goes silent.
const (
	PhaseUnknown       Phase = "Unknown"       // the facts are unreadable; the operator can't tell anything
	PhaseBooting       Phase = "Booting"       // init hasn't finished publishing this boot's record yet
	PhaseLost          Phase = "Lost"          // the heartbeat went silent; a leader wrote this, not the machine
	PhaseBlocked       Phase = "Blocked"       // drift exists but can't be staged; it needs a different edit, not time
	PhaseUpdating      Phase = "Updating"      // a reboot is in flight to apply a staged change
	PhaseUpdatePending Phase = "UpdatePending" // a change is staged, waiting on a Manual reboot
	PhaseDegraded      Phase = "Degraded"      // something is wrong that isn't one of the specific states above
	PhaseReady         Phase = "Ready"         // every condition is True
)

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

// AddressMethod is how an interface got its address: a DHCP lease,
// or a static assignment from the Machine spec.
type AddressMethod string

const (
	MethodDHCP   AddressMethod = "DHCP"
	MethodStatic AddressMethod = "Static"
)

// InterfaceStatus is one interface as the boot configured it.
type InterfaceStatus struct {
	Name         string        `json:"name"`
	MAC          string        `json:"mac,omitempty"`
	Address      string        `json:"address,omitempty"`
	Method       AddressMethod `json:"method,omitempty"`
	Gateway      string        `json:"gateway,omitempty"`
	Nameservers  []string      `json:"nameservers,omitempty"`
	LeaseExpires *time.Time    `json:"leaseExpires,omitempty"`
}

// TimeState is a machine clock's condition. FreeRunning and
// Unsynchronized both mean "not following anyone" but tell different
// stories: a free-running machine was never given sources and runs on
// its hardware clock by design, while an unsynchronized one has
// sources it currently can't reach. The distinction matters when
// you're deciding whether a fleet listing shows a configuration
// choice or an outage.
type TimeState string

const (
	TimeSynchronized   TimeState = "Synchronized"
	TimeFreeRunning    TimeState = "FreeRunning"
	TimeUnsynchronized TimeState = "Unsynchronized"
)

// TimeStatus is the machine's account of its own clock. State is
// deliberately honest: a fleet with no upstreams free-runs, agrees
// with itself, and still doesn't claim Synchronized, because agreeing
// with the world is a different claim than agreeing with each other
// and certificate validation cares about the difference.
type TimeStatus struct {
	// State reports whether the clock is currently being disciplined
	// against a source that is itself synchronized, and when it isn't,
	// whether that's by design (FreeRunning) or by outage
	// (Unsynchronized).
	State TimeState `json:"state,omitempty"`

	// Source is who this machine follows: an upstream's name on a
	// leader, one of the cluster's leaders on a follower.
	Source string `json:"source,omitempty"`

	// Stratum is the machine's distance from a reference clock in
	// NTP's own vocabulary: a source at stratum n makes this machine
	// stratum n+1. A leader free-running on purpose reports the
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

// Backing is where a storage role's data actually lives. There are
// exactly two: a partition claimed for the role, or the machine's RAM
// root, which is the default and requires no setup.
type Backing string

const (
	BackingPartition Backing = "Partition"
	BackingMemory    Backing = "Memory"
)

// FirmwareMode is which kind of firmware booted the machine. Named
// string types over bare strings, as everywhere in this API: the
// vocabulary is closed and the compiler can hold it.
type FirmwareMode string

const (
	FirmwareUEFI FirmwareMode = "UEFI"
	FirmwareBIOS FirmwareMode = "BIOS"
)

// FirmwareStatus is the firmware's boot story, read from its
// variable store. Each entry field renders as the variable's own
// name plus the entry's decoded description ("Boot0001 (liken slot
// A)"), because a fleet listing should read in words.
type FirmwareStatus struct {
	Mode FirmwareMode `json:"mode,omitempty"`

	// BootCurrent is the entry the firmware reports it used this
	// boot; empty when the firmware never picked one (direct-kernel
	// boots) or the machine isn't UEFI at all.
	BootCurrent string `json:"bootCurrent,omitempty"`

	// BootNext, when present, is a one-shot override armed for the
	// next boot: the firmware consumes it at power-on. Seeing it here
	// means a proving boot is queued but hasn't happened yet.
	BootNext string `json:"bootNext,omitempty"`

	// BootOrder is the firmware's standing preference list, first
	// choice first.
	BootOrder []string `json:"bootOrder,omitempty"`
}

// StorageStatus enumerates every role liken knows, whether declared or
// not: absence should be visible in one kubectl get, not implied. The
// fields mirror the spec's keys exactly, so spec and status line up
// name for name.
type StorageStatus struct {
	SystemA          StorageRoleStatus `json:"systemA"`
	SystemB          StorageRoleStatus `json:"systemB"`
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
	Backing       Backing `json:"backing"`
	Device        string  `json:"device,omitempty"`    // the partition's node this boot: vda1
	Partition     string  `json:"partition,omitempty"` // its on-disk name: liken:clusterState
	CapacityBytes uint64  `json:"capacityBytes,omitempty"`
}

// Role addresses one role's status by its spec name; nil for names
// outside the vocabulary.
func (s *StorageStatus) Role(name StorageRoleName) *StorageRoleStatus {
	switch name {
	case SystemARole:
		return &s.SystemA
	case SystemBRole:
		return &s.SystemB
	case MachineStateRole:
		return &s.MachineState
	case MachineEphemeralRole:
		return &s.MachineEphemeral
	case ClusterStateRole:
		return &s.ClusterState
	case PodStorageRole:
		return &s.PodStorage
	case PodEphemeralRole:
		return &s.PodEphemeral
	}
	return nil
}

// AllRolesInMemory marks every role as backed by the RAM root: the
// accurate starting point, upgraded role by role as reconciliation
// places each on a partition.
func AllRolesInMemory() StorageStatus {
	return StorageStatus{
		SystemA:          StorageRoleStatus{Backing: BackingMemory},
		SystemB:          StorageRoleStatus{Backing: BackingMemory},
		MachineState:     StorageRoleStatus{Backing: BackingMemory},
		MachineEphemeral: StorageRoleStatus{Backing: BackingMemory},
		ClusterState:     StorageRoleStatus{Backing: BackingMemory},
		PodStorage:       StorageRoleStatus{Backing: BackingMemory},
		PodEphemeral:     StorageRoleStatus{Backing: BackingMemory},
	}
}

// ManifestSource is which copy of a document a boot ran under, in
// preference order: a staged manifest awaiting its proving boot, the
// proven last-known-good, or the image's seed (first boot only; see
// staging.go).
type ManifestSource string

const (
	ManifestSourceStaged ManifestSource = "Staged"
	ManifestSourceProven ManifestSource = "Proven"
	ManifestSourceSeed   ManifestSource = "Seed"
)

// BootStatus is the boot's account of its own configuration: which
// documents won, identified by the hashes of their exact bytes, and
// the storage spec it actuated. This is the half of drift detection
// only init can supply; the operator compares it against the
// cluster's copies.
type BootStatus struct {
	// The Machine manifest this boot ran under.
	ManifestSource ManifestSource `json:"manifestSource,omitempty"`
	ManifestHash   string         `json:"manifestHash,omitempty"`
	Storage        StorageSpec    `json:"storage,omitzero"`

	// Slot is the system slot this boot came from — "A" or "B", read
	// from the liken.slot= parameter the installer baked into each
	// boot entry's command line; empty when the boot didn't come from
	// a slot at all (direct-kernel boots, install media). This is how
	// a machine knows which half of blue-green it stands on: releases
	// download to the other slot.
	Slot string `json:"slot,omitempty"`

	// The Cluster manifest this boot ran under: the same lifecycle,
	// recorded separately, because the two documents stage and prove
	// independently and a machine can be current on one while drifted
	// on the other.
	ClusterManifestSource ManifestSource `json:"clusterManifestSource,omitempty"`
	ClusterManifestHash   string         `json:"clusterManifestHash,omitempty"`

	// Rejection is the standing quarantine record for the Machine
	// manifest, republished every boot until a promotion clears it,
	// so a rejected spec stays visible in the cluster no matter how
	// many times the machine power-cycles. ClusterRejection is the
	// same record for the Cluster document.
	Rejection        *Rejection `json:"rejection,omitempty"`
	ClusterRejection *Rejection `json:"clusterRejection,omitempty"`
}

// ConditionStatus is a condition's verdict: a string, not a bool,
// precisely because of the third state — a controller that can't
// currently tell must be able to say so.
type ConditionStatus string

const (
	ConditionTrue    ConditionStatus = "True"
	ConditionFalse   ConditionStatus = "False"
	ConditionUnknown ConditionStatus = "Unknown"
)

// Condition mirrors metav1.Condition, the shape Kubernetes uses
// everywhere (Pods, Nodes, Deployments all carry these).
// ObservedGeneration records which metadata.generation the condition
// judged: generation counts spec edits, so a consumer can tell
// "Ready, for the spec as it stands" from "Ready, but for a spec two
// edits ago" — a distinction that matters in liken, where edits wait
// for a reboot to take effect. (The convergence conditions make the
// stronger, content-hashed version of this claim; the generation is
// for tooling that speaks the convention.)
type Condition struct {
	Type               string          `json:"type"`
	Status             ConditionStatus `json:"status"`
	ObservedGeneration int64           `json:"observedGeneration,omitempty"`
	Reason             string          `json:"reason,omitempty"`
	Message            string          `json:"message,omitempty"`
	LastTransitionTime time.Time       `json:"lastTransitionTime"`
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

// FindCondition reports the condition of the named type, nil when the
// list carries none.
func FindCondition(conditions []Condition, conditionType string) *Condition {
	for i := range conditions {
		if conditions[i].Type == conditionType {
			return &conditions[i]
		}
	}
	return nil
}

// RemoveCondition drops the condition of the named type. Most
// conditions are observations and live forever, flipping between True
// and False; removal exists for the ones that are grants — present
// while extended, gone when revoked — so their absence can carry
// meaning without a False state that other machinery would read as
// trouble.
func RemoveCondition(conditions []Condition, conditionType string) []Condition {
	for i := range conditions {
		if conditions[i].Type == conditionType {
			return append(conditions[:i], conditions[i+1:]...)
		}
	}
	return conditions
}
