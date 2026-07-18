package machine

// The observed half of the Machine API. Kubernetes convention draws a
// hard line here: spec is what someone asked for, status is what the
// controllers observed, and status must be *reconstructible*: an
// operator should be able to wipe it and rebuild it purely by looking
// at the machine. Nothing in these types is remembered between passes;
// everything is re-derived from current observation.

import (
	"time"

	"github.com/liken-sh/liken/api"
)

type MachineStatus struct {
	// Phase summarizes the machine's state in one word, computed from
	// the conditions below on every pass. It is never remembered, so
	// it can never go stale relative to them. Conditions are for programs
	// (kubectl wait, controllers); the phase is for the human scanning
	// a fleet listing. See the api package's Phase constants for the vocabulary.
	Phase api.Phase `json:"phase,omitempty"`

	// Version reports what this machine is running. The k3s and kubelet
	// versions aren't here because Kubernetes already reports them on
	// the built-in Node object; this covers the layer below it.
	Version VersionStatus `json:"version,omitzero"`

	// Role is what this machine is in its cluster: a leader (it runs
	// a control plane) or a follower (it runs workloads). Derived at
	// boot from the Cluster manifest's leaders list, never declared
	// here.
	Role api.Role `json:"role,omitempty"`

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
	Conditions []api.Condition `json:"conditions,omitempty"`
}

// VersionStatus is the complete inventory of what this machine
// runs: liken's own version and every outside component the OS
// carries. Two kinds of fact live here, sourced differently. The
// kernel and the netfilter userspace can answer for themselves on
// the running machine (uname and `iptables -V`), so those are
// observed, in the running software's own vocabulary. The rest —
// boot artifacts, bundled images, data files — cannot be asked
// anything, so they are reported from the components record the
// image build wrote alongside the bytes it staged
// (/usr/share/liken/components.yaml), the same pins the release
// document publishes, so the two can never disagree. k3s is listed
// even though the Node object reports a kubelet version, because
// this block answers a different question: not "what is running the
// pods" but "what did this OS image carry".
type VersionStatus struct {
	Liken  string `json:"liken,omitempty"`
	Kernel string `json:"kernel,omitempty"`

	// Xtables is the netfilter userspace as it reports itself
	// (`iptables -V`, e.g. "v1.8.11 (legacy)"): observed, not echoed
	// from a build pin.
	Xtables string `json:"xtables,omitempty"`

	K3s         string `json:"k3s,omitempty"`
	Trust       string `json:"trust,omitempty"`
	E2fsprogs   string `json:"e2fsprogs,omitempty"`
	OpenISCSI   string `json:"openIscsi,omitempty"`
	NFSUtils    string `json:"nfsUtils,omitempty"`
	SystemdBoot string `json:"systemdBoot,omitempty"`
	Grub        string `json:"grub,omitempty"`
	Hwdata      string `json:"hwdata,omitempty"`
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

	// Unclaimed is every device the kernel enumerated but nothing
	// drives: hardware waiting on a module that spec.modules doesn't
	// declare. It is the gap, never the census — a machine whose
	// hardware is fully driven reports nothing here, the way healthy
	// conditions read True and a healthy fleet listing is boring. The
	// full inventory of working devices is deliberately not status
	// material; workloads reach it through /sys, and claimable
	// devices belong to ResourceSlices.
	Unclaimed []UnclaimedDevice `json:"unclaimed,omitempty"`
}

// UnclaimedDevice is one enumerated-but-undriven device, reported
// with everything an operator needs to fix it: the device named in
// words, and the candidate modules whose alias patterns match its
// fingerprint. Only devices some loadable module could drive appear
// at all — a device the kernel build has no module for (a host
// bridge, a platform stub) is not actionable, and reporting it would
// bury the fixable gaps in noise.
type UnclaimedDevice struct {
	// Modalias is the kernel's fingerprint for this device, the same
	// string it announces in uevents and matches driver patterns
	// against. It identifies the device precisely when the words
	// above it don't.
	Modalias string `json:"modalias"`

	// Bus is where the device lives: pci or usb.
	Bus string `json:"bus"`

	// Name is the device in words — a USB device's own manufacturer
	// and product strings, a PCI device's names from the pci.ids
	// database — falling back to numeric vendor:device IDs when no
	// better name exists.
	Name string `json:"name,omitempty"`

	// Class is the device's coarse kind (mass-storage, display,
	// network), decoded from the bus's class code.
	Class string `json:"class,omitempty"`

	// Candidates are the loadable modules whose alias patterns match
	// this device, in the kernel build's preference order. More than
	// one is normal (USB storage matches uas and usb_storage); the
	// choice belongs to whoever edits spec.modules.
	Candidates []string `json:"candidates,omitempty"`

	// Message says what would fix it, like every message in this
	// status: declare a candidate when the image carries one, or get
	// an image that carries it when none is aboard.
	Message string `json:"message,omitempty"`
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
	BIOSBoot         StorageRoleStatus `json:"biosBoot"`
	BootHome         StorageRoleStatus `json:"bootHome"`
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
	case BIOSBootRole:
		return &s.BIOSBoot
	case BootHomeRole:
		return &s.BootHome
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
	s := StorageStatus{}
	for _, name := range StorageRoleNames {
		s.Role(name).Backing = BackingMemory
	}
	return s
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

	// The imported-images record this boot ran under (imports.go):
	// Staged while the container store is serving unpacks no operator
	// has yet proven, Proven on the ordinary boot whose tarballs all
	// match the record. Empty when the lifecycle isn't running (no
	// durable machineState to remember a trial, or an ephemeral
	// container store that a reboot resets anyway). ImportsDiscarded
	// records that this boot found a trial still standing from a boot
	// that died unproven, and threw the container store away rather
	// than trust it.
	ImportsSource    ManifestSource `json:"importsSource,omitempty"`
	ImportsHash      string         `json:"importsHash,omitempty"`
	ImportsDiscarded bool           `json:"importsDiscarded,omitempty"`

	// Restarts counts the in-place k3s restarts this boot has
	// performed to apply restart-class changes (cluster/changes.go).
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

// RebootApprovedCondition is the rollout conductor's grant of a
// reboot turn, and the one condition type on a Machine's status that
// two different programs speak: the cluster operator writes and
// removes it, and the machine's own operator carries it along
// verbatim and acts on it. It lives here because it is shared
// vocabulary, exactly the way PodScheduled is a condition the
// scheduler writes onto Pods the kubelet owns.
const RebootApprovedCondition = "RebootApproved"
