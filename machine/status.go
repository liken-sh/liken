package machine

// The observed half of the Machine API. Kubernetes convention draws a
// hard line here: spec is what someone asked for, status is what the
// controllers observed, and status must be *reconstructible*: an
// operator should be able to wipe it and rebuild it purely by looking
// at the machine. Nothing in these types is remembered between passes;
// everything is re-derived from current observation.

import (
	"slices"
	"time"
)

type MachineStatus struct {
	// Phase summarizes the machine's state in one word, computed from
	// the conditions below on every pass. It is never remembered, so
	// it can never go stale relative to them. Conditions are for programs
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
	// boots in and the boot menu from its non-volatile store. On UEFI
	// machines the variables are decoded into words; BIOS is
	// shorthand for any machine without firmware variables to consult
	// (a legacy server, a direct-kernel boot under a hypervisor).
	// Firmware sits beside Hardware because it describes the machine,
	// not the boot: BootOrder is a standing preference and BootNext
	// is about the next boot. Only bootCurrent is a fact about this
	// boot, and it stays here so the firmware fields read together.
	// Contrast Boot, the per-boot record.
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

	// Modules reports the outcome of every module named in
	// spec.modules: the same verdicts init prints to the console, made
	// queryable. Only the declared extras appear here; the fixed list
	// the OS loads for itself is the image's business, not the spec's.
	Modules []ModuleStatus `json:"modules,omitempty"`

	// Features reports this machine's standing on every feature the
	// cluster document enables (the Cluster's spec.features): the
	// same verdicts init prints at boot, made queryable. The Cluster
	// declares features for the whole fleet; this field is the
	// per-machine answer, because honoring a feature depends on what
	// the booted image carries, and machines can be running
	// different releases mid-rollout.
	Features []FeatureStatus `json:"features,omitempty"`

	// Registries reports what this machine rendered into k3s's
	// registries.yaml: which registries are mirrored, which have
	// credentials, whether the embedded registry is on — the hosts,
	// never the material. Console parity, like Features above.
	Registries RegistriesStatus `json:"registries,omitzero"`

	// Boot is what this boot ran under: which documents, and the
	// storage as actuated. It is re-derived on every boot, and it is
	// the record the operator diffs the spec against. Lifetime is
	// what separates it from Firmware: power-cut the machine and boot
	// it unchanged, and everything here is freshly re-derived (a
	// staged manifest applies, a rejection lands), while Firmware
	// reports the standing state that carried across the reboot.
	Boot BootStatus `json:"boot,omitzero"`

	// BootedAt is the moment the machine booted, derived by init from
	// the kernel's uptime counter. It is a timestamp rather than a
	// duration because a timestamp never goes stale in the cluster,
	// and `kubectl get machines` renders it as a live elapsed time
	// (the Uptime column).
	//
	// There is deliberately no heartbeat here. The machine's liveness
	// signal is a Lease in the liken-system namespace, not a
	// status field, because a heartbeat must renew forever, and every
	// status write rewrites this whole object and wakes every
	// watcher. That is the same reason kube-node-lease exists (see
	// the kubernetes package). The cluster operator reads the leases
	// and marks a silent machine Lost.
	BootedAt *time.Time `json:"bootedAt,omitempty"`

	// Conditions follow the standard Kubernetes idiom: a set of typed,
	// timestamped observations ("Ready", "SysctlsApplied") that
	// controllers maintain and humans and tooling read.
	Conditions []Condition `json:"conditions,omitempty"`
}

// Phase summarizes a machine's state in one word. Named string types
// are how Go models a closed vocabulary: the constants below are the
// only values, the compiler catches a Phase handed where a Role
// belongs, and the wire format is unchanged (a named string marshals
// exactly like a bare one). Kubernetes' own API types use the same
// idiom (v1.PodPhase, metav1.ConditionStatus).
type Phase string

// The phases a machine can report, most severe first. Each is a
// summary of the conditions, not a fact of its own; the operator
// derives the phase from the conditions on every pass, and the table
// that does so (machine-operator/phase.go) is the authority on which
// condition puts a machine in which phase. Lost is the exception to
// "derived from own conditions": a machine cannot report its own
// death, so the cluster operator writes Lost on its behalf when its
// heartbeat goes silent.
const (
	PhaseUnknown       Phase = "Unknown"       // the facts are unreadable; the operator can't tell anything
	PhaseBooting       Phase = "Booting"       // init hasn't finished publishing this boot's record yet
	PhaseLost          Phase = "Lost"          // the heartbeat went silent; the cluster operator wrote this, not the machine
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
// Unsynchronized both mean the clock is not following any source, but
// for different reasons: a free-running machine was never given
// sources and runs on its hardware clock by design, while an
// unsynchronized one has sources it currently can't reach. The
// distinction matters when you're deciding whether a fleet listing
// shows a configuration choice or an outage.
type TimeState string

const (
	TimeSynchronized   TimeState = "Synchronized"
	TimeFreeRunning    TimeState = "FreeRunning"
	TimeUnsynchronized TimeState = "Unsynchronized"
)

// TimeStatus reports the state of the machine's clock. A fleet with
// no upstreams free-runs and agrees with itself, but still doesn't
// report Synchronized: machines agreeing with each other is a
// different claim than agreeing with the rest of the world, and
// certificate validation cares about the difference.
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
	// stratum n+1. A leader that free-runs by design reports the
	// local-clock convention (10); 16 means unsynchronized, the value
	// NTP reserves for a clock that should not be used as a source.
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
// assigned in driver probe order, so it addresses the device within
// this boot but does not identify it across boots. Model and serial
// come from the device itself.
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

// FirmwareMode is which kind of firmware booted the machine. It is a
// named string type, like every closed vocabulary in this API, so the
// compiler can catch a value used in the wrong place.
type FirmwareMode string

const (
	FirmwareUEFI FirmwareMode = "UEFI"
	FirmwareBIOS FirmwareMode = "BIOS"
)

// FirmwareStatus reports the firmware's boot configuration, read
// from its variable store. Each entry field renders as the variable's
// own name plus the entry's decoded description ("Boot0001 (liken
// slot A)"), because a fleet listing should read in words.
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
// deliberately reports no capacity: all memory-backed roles share the
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

// ModuleState is one declared module's outcome, a closed vocabulary
// like every state word in this API. Two of the four are healthy:
// Loaded means the kernel took the module (or already had it), and
// Builtin means the name is compiled into the kernel, so there was
// nothing to load and nothing wrong. Missing means the booted image
// never shipped the module, which happens when a spec is edited after
// its image was built: the fix is a new image, not a retry. Failed
// means the module shipped but the kernel refused it, which is
// usually the hardware's story to tell.
type ModuleState string

const (
	ModuleLoaded  ModuleState = "Loaded"
	ModuleBuiltin ModuleState = "Builtin"
	ModuleMissing ModuleState = "Missing"
	ModuleFailed  ModuleState = "Failed"
)

// ModuleStatus is one declared module's outcome this boot. Message
// carries the detail for the unhealthy states, phrased to name the
// fix, because a status that says what would repair it beats one that
// only says what's wrong.
type ModuleStatus struct {
	Name    string      `json:"name"`
	State   ModuleState `json:"state"`
	Message string      `json:"message,omitempty"`
}

// FeatureState is one enabled feature's standing on one machine, a
// closed vocabulary like ModuleState's. Active means this boot could
// honor everything the feature asks of this machine. Missing means
// the booted image predates the feature: the cluster document
// declares it, but the image carries no payload for it, and the fix
// is a release that does. Failed means the image carries the payload
// and actuating it went wrong (a module the kernel refused, a boot
// hook that errored); the message tells that story.
type FeatureState string

const (
	FeatureActive  FeatureState = "Active"
	FeatureMissing FeatureState = "Missing"
	FeatureFailed  FeatureState = "Failed"
)

// FeatureStatus is one enabled feature's outcome on this machine this
// boot. Like ModuleStatus, Message carries detail for the unhealthy
// states, phrased to name the fix.
type FeatureStatus struct {
	Name    string       `json:"name"`
	State   FeatureState `json:"state"`
	Message string       `json:"message,omitempty"`
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

// BootStatus records the configuration this boot actually used:
// which copy of each document, identified by the hashes of their
// exact bytes, and the storage spec it actuated. This is the half of
// drift detection only init can supply; the operator compares it
// against the cluster's copies.
type BootStatus struct {
	// The Machine manifest this boot ran under.
	ManifestSource ManifestSource `json:"manifestSource,omitempty"`
	ManifestHash   string         `json:"manifestHash,omitempty"`
	Storage        StorageSpec    `json:"storage,omitzero"`

	// Modules is the module list the winning manifest declared,
	// recorded as actuated whatever each load's outcome was: the
	// drift reference, like Storage above. Outcomes are a health
	// signal and live in status.modules; a module the image lacked
	// still counts as actuated here, because rebooting again with the
	// same image would not change anything.
	Modules []string `json:"modules,omitempty"`

	// Slot is the system slot this boot came from, "A" or "B", read
	// from the liken.slot= parameter the installer baked into each
	// boot entry's command line; empty when the boot didn't come from
	// a slot at all (direct-kernel boots, install media). This is how
	// a machine knows which side of the blue-green pair it is running
	// from: releases download to the other slot.
	Slot string `json:"slot,omitempty"`

	// The Cluster manifest this boot ran under: the same lifecycle,
	// recorded separately, because the two documents stage and prove
	// independently and a machine can be current on one while drifted
	// on the other.
	ClusterManifestSource ManifestSource `json:"clusterManifestSource,omitempty"`
	ClusterManifestHash   string         `json:"clusterManifestHash,omitempty"`

	// The registry-credentials document this boot (or the latest k3s
	// restart) rendered into registries.yaml: the same lifecycle
	// again, in its own store. The source is only ever Staged or
	// Proven — the operator is this document's sole author, so no
	// image carries a seed. Both fields are empty on a machine that
	// has never had credentials, which is an ordinary state, not a
	// gap.
	CredentialsSource ManifestSource `json:"credentialsSource,omitempty"`
	CredentialsHash   string         `json:"credentialsHash,omitempty"`

	// Restarts counts the in-place k3s restarts this boot has
	// performed to apply restart-class changes (machine/changes.go).
	// It lives in the boot record because it shares the boot's
	// lifetime: a reboot re-makes the record and the count returns to
	// zero, which is itself the signal — a change that arrived by
	// restart increments this without moving bootedAt, and a change
	// that arrived by reboot does the opposite.
	Restarts int `json:"restarts,omitempty"`

	// Rejection is the standing quarantine record for the Machine
	// manifest, republished every boot until a promotion clears it,
	// so a rejected spec stays visible in the cluster no matter how
	// many times the machine power-cycles. ClusterRejection is the
	// same record for the Cluster document, SystemRejection for a
	// system release whose proving boot fell back, and
	// CredentialsRejection for a credentials document that would not
	// parse.
	Rejection            *Rejection `json:"rejection,omitempty"`
	ClusterRejection     *Rejection `json:"clusterRejection,omitempty"`
	SystemRejection      *Rejection `json:"systemRejection,omitempty"`
	CredentialsRejection *Rejection `json:"credentialsRejection,omitempty"`
}

// ConditionStatus is a condition's verdict. It is a string rather
// than a bool because there is a third state: a controller that can't
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
// edits ago". That distinction matters in liken, where edits wait
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

// RebootApprovedCondition is the rollout conductor's grant of a
// reboot turn, and the one condition type on a Machine's status that
// two different programs speak: the cluster operator writes and
// removes it, and the machine's own operator carries it along
// verbatim and acts on it. It lives here because it is shared
// vocabulary, exactly the way PodScheduled is a condition the
// scheduler writes onto Pods the kubelet owns.
const RebootApprovedCondition = "RebootApproved"

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
// conditions are observations: they stay in the list forever and flip
// between True and False. Removal exists for the conditions that are
// grants, which are present while extended and gone when revoked.
// Their absence carries the meaning, so there is no False state for
// other machinery to misread as trouble.
func RemoveCondition(conditions []Condition, conditionType string) []Condition {
	for i := range conditions {
		if conditions[i].Type == conditionType {
			return slices.Delete(conditions, i, i+1)
		}
	}
	return conditions
}
