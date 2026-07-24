package machine

// This file defines the observed half of the Machine API. Kubernetes
// convention keeps the two halves separate: the spec is what a user
// requested, and the status is what the controllers observed. The
// status must be reconstructible. An operator must be able to erase
// the status and rebuild it only by observing the machine. The types
// in this file do not store anything between passes. Each pass
// re-derives every value from the current observation.

import (
	"time"

	"github.com/liken-sh/liken/api"
)

type MachineStatus struct {
	// Phase summarizes the machine's state in one word. Each pass
	// computes it from the conditions below. No component stores
	// Phase between passes, so it can never go out of date compared
	// to the conditions. Conditions serve programs, such as kubectl
	// wait and other controllers. Phase serves the human who reads a
	// fleet listing. See the api package's Phase constants for the
	// vocabulary.
	Phase api.Phase `json:"phase,omitempty"`

	// ObservedGeneration is the metadata.generation of the spec that
	// this status judged, stamped by the operator on every pass. The
	// conditions each carry the same stamp, but a client that only
	// asks "has the operator seen my edit yet" should not have to
	// parse conditions to learn it. Kubernetes controllers publish
	// this field at the top of status for exactly that question.
	// Init leaves this field empty in the facts, because init runs
	// before the cluster exists and a generation is the API server's
	// number to hand out.
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// Version reports what this machine runs. It does not include the
	// k3s and kubelet versions, because Kubernetes already reports
	// them on the built-in Node object. Version covers the layer
	// below the Node object instead.
	Version VersionStatus `json:"version,omitzero"`

	// Role is what this machine is in its cluster: a leader that runs
	// a control plane, or a follower that runs workloads. Boot derives
	// Role from the Cluster manifest's leaders list. No one declares
	// Role here directly.
	Role api.Role `json:"role,omitempty"`

	// Network is the outcome of the boot's networking: DHCP leases and
	// static assignments alike. It makes queryable the same facts that
	// init prints to the console.
	Network NetworkStatus `json:"network,omitzero"`

	// Time reports the state of this machine's clock. It makes
	// queryable the same facts that the time loop prints to the
	// console. Unlike most of status, Time changes for the whole life
	// of the machine, because the clock is disciplined continuously,
	// not configured once at boot.
	Time TimeStatus `json:"time,omitzero"`

	// Hardware is what the machine found when it started running.
	Hardware HardwareStatus `json:"hardware,omitzero"`

	// Firmware is the machine's standing firmware state: the mode it
	// boots in, and the boot menu from its non-volatile store. On
	// UEFI machines, the variables are decoded into words. BIOS is
	// the name for any machine without firmware variables to consult,
	// such as a legacy server or a direct-kernel boot under a
	// hypervisor. Firmware sits beside Hardware because it describes
	// the machine, not the boot: BootOrder is a standing preference,
	// and BootNext is about the next boot. Only BootCurrent is a fact
	// about this boot, and it stays here so the firmware fields read
	// together. Contrast this with Boot, the per-boot record below.
	Firmware FirmwareStatus `json:"firmware,omitzero"`

	// Storage reports where each storage role is actually backed this
	// boot, whether declared or not. The spec says what was
	// requested, and hardware.blockDevices says what is attached.
	// Storage connects the two.
	Storage StorageStatus `json:"storage,omitzero"`

	// Sysctls echoes the observed value of every parameter named in
	// spec.sysctls, read back from /proc/sys. This puts the spec and
	// reality side by side in one kubectl get.
	Sysctls map[string]string `json:"sysctls,omitempty"`

	// Modules reports the outcome of every module named in
	// spec.modules. It makes queryable the same verdicts that init
	// prints to the console. Only the declared extras appear here.
	// The fixed list of modules the OS loads by itself belongs to the
	// image, not to the spec.
	Modules []ModuleStatus `json:"modules,omitempty"`

	// Features reports this machine's standing on every feature that
	// the cluster document enables, in the Cluster's spec.features.
	// It makes queryable the same verdicts that init prints at boot.
	// The Cluster declares features for the whole fleet, but this
	// field is the per-machine answer, because honoring a feature
	// depends on what the booted image carries. Machines can run
	// different releases in the middle of a rollout.
	Features []FeatureStatus `json:"features,omitempty"`

	// Registries reports what this machine rendered into k3s's
	// registries.yaml: which registries are mirrored, which have
	// credentials, and whether the embedded registry is on. It
	// reports the hosts only, never the credential material. This
	// gives console parity, like Features above.
	Registries RegistriesStatus `json:"registries,omitzero"`

	// Boot is what this boot ran under: which documents, and the
	// storage as actuated. Each boot re-derives it, and it is the
	// record the operator compares against the spec. Lifetime is what
	// separates Boot from Firmware. If you power-cut the machine and
	// boot it unchanged, everything in Boot is freshly re-derived (a
	// staged manifest applies, or a rejection lands), while Firmware
	// reports the standing state that carried across the reboot.
	Boot BootStatus `json:"boot,omitzero"`

	// LastCrash is the newest kernel crash this machine still holds
	// records for: a panic or an oops that pstore carried across the
	// reboot. It is not necessarily the previous boot's crash. A
	// machine can boot cleanly for years while an old crash stays on
	// record, and the timestamp is what says how old the news is.
	// Every boot re-derives this field from the preserved records on
	// machineState, so an erased status rebuilds it, and it clears
	// only when the records themselves leave the retention window.
	LastCrash *CrashStatus `json:"lastCrash,omitempty"`

	// Conditions follow the standard Kubernetes pattern: a set of
	// typed, timestamped observations, such as "Ready" and
	// "SysctlsApplied", that controllers maintain and that humans and
	// tooling read.
	Conditions []api.Condition `json:"conditions,omitempty"`
}

// VersionStatus is the complete inventory of what this machine
// runs: liken's own version, and every outside component the OS
// carries. Two kinds of fact live here, sourced in different ways.
// For the kernel and the netfilter userspace, the running machine
// itself reports a version (`uname` and `iptables -V`), so those
// fields are observed, in the running software's own vocabulary. The
// rest — boot artifacts, bundled images, data files — cannot report
// anything about themselves. VersionStatus reports those from the
// components record that the image build wrote alongside the bytes
// it staged (/usr/share/liken/components.yaml). This is the same
// record of pins that the release document publishes, so the two can
// never disagree. This block lists k3s even though the Node object
// already reports a kubelet version, because this block answers a
// different question: not "what is running the pods" but "what did
// this OS image carry".
type VersionStatus struct {
	Liken  string `json:"liken,omitempty"`
	Kernel string `json:"kernel,omitempty"`

	// Xtables is the netfilter userspace version as it reports
	// itself, for example "v1.8.11 (legacy)" from `iptables -V`. It
	// is observed, not echoed from a build pin.
	Xtables string `json:"xtables,omitempty"`

	K3s           string `json:"k3s,omitempty"`
	Trust         string `json:"trust,omitempty"`
	E2fsprogs     string `json:"e2fsprogs,omitempty"`
	OpenISCSI     string `json:"openIscsi,omitempty"`
	NFSUtils      string `json:"nfsUtils,omitempty"`
	SystemdBoot   string `json:"systemdBoot,omitempty"`
	Grub          string `json:"grub,omitempty"`
	Hwdata        string `json:"hwdata,omitempty"`
	LinuxFirmware string `json:"linuxFirmware,omitempty"`

	// Microcode is the pin: which early cpio the release carries.
	// MicrocodeRevision is observed from the running CPUs. The two
	// agreeing is the proof that the early cpio applied, and only
	// real hardware can give it; on a virtual machine the revision is
	// the hypervisor's.
	Microcode         string `json:"microcode,omitempty"`
	MicrocodeRevision string `json:"microcodeRevision,omitempty"`
}

// NetworkStatus reports how the boot attached this machine to the
// network. The top-level fields summarize the primary interface: the
// cluster-facing interface when the Cluster's nodeCIDR identifies it,
// or otherwise the first interface that came up. Interfaces carries
// the full detail for each interface, for a machine with more than
// one.
type NetworkStatus struct {
	Interface    string     `json:"interface,omitempty"`
	MAC          string     `json:"mac,omitempty"`
	Addresses    []string   `json:"addresses,omitempty"`
	Gateway      string     `json:"gateway,omitempty"`
	Nameservers  []string   `json:"nameservers,omitempty"`
	LeaseExpires *time.Time `json:"leaseExpires,omitempty"`

	Interfaces []InterfaceStatus `json:"interfaces,omitempty"`
}

// AddressMethod is how an interface got its address: from a DHCP
// lease, or from a static assignment in the Machine spec.
type AddressMethod string

const (
	MethodDHCP   AddressMethod = "DHCP"
	MethodStatic AddressMethod = "Static"
)

// InterfaceStatus is one interface, as the boot configured it.
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
// Unsynchronized both mean the clock does not follow any source, but
// for different reasons. A free-running machine never received any
// sources and runs on its hardware clock by design. An unsynchronized
// machine has sources, but currently cannot reach them. The
// distinction matters when you decide whether a fleet listing shows a
// configuration choice or an outage.
type TimeState string

const (
	TimeSynchronized   TimeState = "Synchronized"
	TimeFreeRunning    TimeState = "FreeRunning"
	TimeUnsynchronized TimeState = "Unsynchronized"
)

// TimeStatus reports the state of the machine's clock. A fleet with
// no upstreams free-runs and agrees with itself, but still does not
// report Synchronized. Machines that agree with each other make a
// different claim than machines that agree with the rest of the
// world, and certificate validation cares about that difference.
type TimeStatus struct {
	// State reports whether the machine currently disciplines the
	// clock against a source that is itself synchronized. When it
	// does not, State also reports whether that is by design
	// (FreeRunning) or by outage (Unsynchronized).
	State TimeState `json:"state,omitempty"`

	// Source is who this machine follows: an upstream's name on a
	// leader, or one of the cluster's leaders on a follower.
	Source string `json:"source,omitempty"`

	// Stratum is the machine's distance from a reference clock, in
	// NTP's own vocabulary: a source at stratum n makes this machine
	// stratum n+1. A leader that free-runs by design reports the
	// local-clock convention (10). A value of 16 means unsynchronized,
	// the value NTP reserves for a clock that no machine should use
	// as a source.
	Stratum int `json:"stratum,omitempty"`

	// Offset is the clock error measured at the last exchange, as a
	// human-readable duration such as "1.28ms". It is positive when
	// this machine was behind its source.
	Offset string `json:"offset,omitempty"`

	// LastSync is when the clock last agreed with its source.
	LastSync *time.Time `json:"lastSync,omitempty"`
}

type HardwareStatus struct {
	CPUs        int    `json:"cpus,omitempty"`
	MemoryBytes uint64 `json:"memoryBytes,omitempty"`

	// BlockDevices is the machine's storage inventory: every real disk
	// the kernel found, whether or not the spec says anything about
	// it. An attached but undeclared disk shows up here. This is how
	// an operator notices one.
	BlockDevices []BlockDevice `json:"blockDevices,omitempty"`

	// Unclaimed lists every device the kernel enumerated but that
	// nothing drives: hardware that waits on a module that
	// spec.modules does not declare. It is the gap, never the full
	// count. A machine whose hardware is fully driven reports nothing
	// here, the same way healthy conditions read True and a healthy
	// fleet listing looks uneventful. The full inventory of working
	// devices is deliberately not status material. Workloads reach it
	// through /sys, and claimable devices belong to ResourceSlices.
	Unclaimed []UnclaimedDevice `json:"unclaimed,omitempty"`
}

// UnclaimedDevice is one enumerated but undriven device, reported
// with everything an operator needs to fix it: the device named in
// words, and the candidate modules whose alias patterns match its
// fingerprint. Only a device that some loadable module could drive
// appears here at all. A device the kernel build has no module for,
// such as a host bridge or a platform stub, is not actionable, and
// reporting it would hide the fixable gaps among noise.
type UnclaimedDevice struct {
	// Modalias is the kernel's fingerprint for this device, the same
	// string it announces in uevents and matches driver patterns
	// against. It identifies the device precisely, in cases where the
	// words above it do not.
	Modalias string `json:"modalias"`

	// Bus is where the device lives: pci or usb.
	Bus string `json:"bus"`

	// Name is the device in words: a USB device's own manufacturer
	// and product strings, or a PCI device's names from the pci.ids
	// database. It falls back to numeric vendor:device IDs when no
	// better name exists.
	Name string `json:"name,omitempty"`

	// Class is the device's coarse kind, such as mass-storage,
	// display, or network, decoded from the bus's class code.
	Class string `json:"class,omitempty"`

	// Candidates are the loadable modules whose alias patterns match
	// this device, in the kernel build's preference order. More than
	// one candidate is normal; USB storage matches both uas and
	// usb_storage. The choice belongs to whoever edits spec.modules.
	Candidates []string `json:"candidates,omitempty"`

	// Message says what would fix the gap, like every message in this
	// status: declare a candidate when the image carries one, or get
	// an image that carries one when none is present.
	Message string `json:"message,omitempty"`
}

// BlockDevice is one disk, as the machine observed it directly from
// sysfs. Name is the kernel's name for this boot, such as vda or
// nvme0n1, assigned in driver probe order. It addresses the device
// within this boot, but it does not identify the device across
// boots. Model and serial come from the device itself.
type BlockDevice struct {
	Name      string `json:"name"`
	SizeBytes uint64 `json:"sizeBytes,omitempty"`
	Model     string `json:"model,omitempty"`
	Serial    string `json:"serial,omitempty"`
}

// Backing is where a storage role's data actually lives. There are
// exactly two options: a partition claimed for the role, or the
// machine's RAM root, which is the default and needs no setup.
type Backing string

const (
	BackingPartition Backing = "Partition"
	BackingMemory    Backing = "Memory"
)

// FirmwareMode is which kind of firmware booted the machine. Like
// every closed vocabulary in this API, it is a named string type, so
// the compiler can catch a value used in the wrong place.
type FirmwareMode string

const (
	FirmwareUEFI FirmwareMode = "UEFI"
	FirmwareBIOS FirmwareMode = "BIOS"
)

// FirmwareStatus reports the firmware's boot configuration, read from
// its variable store. Each entry field renders as the variable's own
// name plus the entry's decoded description, such as "Boot0001
// (liken slot A)", because a fleet listing should read in words.
type FirmwareStatus struct {
	Mode FirmwareMode `json:"mode,omitempty"`

	// BootCurrent is the entry the firmware reports it used this
	// boot. It is empty when the firmware never picked one, as in a
	// direct-kernel boot, or when the machine is not UEFI at all.
	BootCurrent string `json:"bootCurrent,omitempty"`

	// BootNext, when present, is a one-shot override armed for the
	// next boot. The firmware consumes it at power-on. Seeing a value
	// here means a proving boot is queued but has not yet happened.
	BootNext string `json:"bootNext,omitempty"`

	// BootOrder is the firmware's standing preference list, with the
	// first choice listed first.
	BootOrder []string `json:"bootOrder,omitempty"`
}

// StorageStatus lists every role liken knows, whether declared or
// not. An absent role must be visible in one kubectl get, not merely
// implied. The fields mirror the spec's keys exactly, so spec and
// status line up name for name.
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
// deliberately reports no capacity. All memory-backed roles share the
// one RAM root, and a per-role figure would count that root several
// times over.
type StorageRoleStatus struct {
	Backing       Backing `json:"backing"`
	Device        string  `json:"device,omitempty"`    // the partition's node this boot: vda1
	Partition     string  `json:"partition,omitempty"` // its on-disk name: liken:clusterState
	CapacityBytes uint64  `json:"capacityBytes,omitempty"`
}

// Role addresses one role's status by its spec name. It returns nil
// for a name outside the vocabulary.
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

// AllRolesInMemory marks every role as backed by the RAM root. This
// is the accurate starting point. Reconciliation upgrades each role,
// one at a time, as it places the role on a partition.
func AllRolesInMemory() StorageStatus {
	s := StorageStatus{}
	for _, name := range StorageRoleNames {
		s.Role(name).Backing = BackingMemory
	}
	return s
}

// ModuleState is one declared module's outcome. Like every state word
// in this API, it is a closed vocabulary. Two of the four values are
// healthy. Loaded means the kernel took the module, or already had
// it. Builtin means the kernel compiles the name in, so there was
// nothing to load and nothing wrong. Missing means the booted image
// never shipped the module. This happens when someone edits a spec
// after its image was built; the fix is a new image, not a retry.
// Failed means the module shipped but the kernel refused it, which
// usually points to a problem in the hardware.
type ModuleState string

const (
	ModuleLoaded  ModuleState = "Loaded"
	ModuleBuiltin ModuleState = "Builtin"
	ModuleMissing ModuleState = "Missing"
	ModuleFailed  ModuleState = "Failed"
)

// ModuleStatus is one declared module's outcome this boot. Message
// carries the detail for the unhealthy states, phrased to name the
// fix. A status that names the repair is more useful than one that
// only names the problem.
type ModuleStatus struct {
	Name    string      `json:"name"`
	State   ModuleState `json:"state"`
	Message string      `json:"message,omitempty"`
}

// FeatureState is one enabled feature's standing on one machine, a
// closed vocabulary like ModuleState's. Active means this boot could
// honor everything the feature asks of this machine. Missing means
// the booted image predates the feature. The cluster document
// declares the feature, but the image carries no payload for it, so
// the fix is a release that does. Failed means the image carries the
// payload, but actuating it went wrong, for example a module the
// kernel refused or a boot hook that returned an error; the message
// explains what happened.
type FeatureState string

const (
	FeatureActive  FeatureState = "Active"
	FeatureMissing FeatureState = "Missing"
	FeatureFailed  FeatureState = "Failed"
)

// FeatureStatus is one enabled feature's outcome on this machine, on
// this boot. Like ModuleStatus, Message carries detail for the
// unhealthy states, phrased to name the fix.
type FeatureStatus struct {
	Name    string       `json:"name"`
	State   FeatureState `json:"state"`
	Message string       `json:"message,omitempty"`
}

// ManifestSource is which copy of a document a boot ran under, in
// preference order: a staged manifest awaiting its proving boot, the
// proven last-known-good copy, or the image's seed, for the first
// boot only (see staging.go).
type ManifestSource string

const (
	ManifestSourceStaged ManifestSource = "Staged"
	ManifestSourceProven ManifestSource = "Proven"
	ManifestSourceSeed   ManifestSource = "Seed"
)

// BootStatus records the configuration this boot actually used: which
// copy of each document, identified by the hashes of their exact
// bytes, and the storage spec it actuated. This is the half of drift
// detection that only init can supply. The operator compares it
// against the cluster's copies.
type BootStatus struct {
	// Time is the moment the machine booted, derived by init from the
	// kernel's uptime counter. It is a timestamp rather than a
	// duration, because a timestamp never goes out of date in the
	// cluster. `kubectl get machines` renders it as a live elapsed
	// time in the Uptime column. It belongs to the boot record because
	// it shares the record's lifetime: a reboot moves it, an in-place
	// k3s restart does not.
	//
	// This status deliberately has no heartbeat. The machine's
	// liveness signal is a Lease in the liken-system namespace, not a
	// status field, because a heartbeat must renew forever, and every
	// status write rewrites this whole object and wakes every
	// watcher. This is the same reason kube-node-lease exists (see the
	// kubernetes package). The cluster operator reads the leases and
	// marks a silent machine Lost.
	Time *time.Time `json:"time,omitempty"`

	// The Machine manifest this boot ran under.
	ManifestSource ManifestSource `json:"manifestSource,omitempty"`
	ManifestHash   string         `json:"manifestHash,omitempty"`
	Storage        StorageSpec    `json:"storage,omitzero"`

	// Modules is the module list the winning manifest declared,
	// recorded as actuated regardless of each load's outcome. It is
	// the drift reference, like Storage above. Outcomes are a health
	// signal and live in status.modules instead; a module the image
	// lacked still counts as actuated here, because rebooting again
	// with the same image would not change anything.
	Modules []string `json:"modules,omitempty"`

	// Slot is the system slot this boot came from, "A" or "B", read
	// from the liken.slot= parameter that the installer baked into
	// each boot entry's command line. It is empty when the boot did
	// not come from a slot at all, as in a direct-kernel boot or
	// install media. This is how a machine knows which side of the
	// blue-green pair it runs from: releases download to the other
	// slot.
	Slot string `json:"slot,omitempty"`

	// The Cluster manifest this boot ran under: the same lifecycle,
	// recorded separately, because the two documents stage and prove
	// independently. A machine can be current on one document while
	// drifted on the other.
	ClusterManifestSource ManifestSource `json:"clusterManifestSource,omitempty"`
	ClusterManifestHash   string         `json:"clusterManifestHash,omitempty"`

	// The registry-credentials document this boot, or the latest k3s
	// restart, rendered into registries.yaml: the same lifecycle
	// again, in its own store. The source is only ever Staged or
	// Proven, because the operator is this document's sole author, so
	// no image carries a seed. Both fields are empty on a machine
	// that has never had credentials, which is an ordinary state, not
	// a gap.
	CredentialsSource ManifestSource `json:"credentialsSource,omitempty"`
	CredentialsHash   string         `json:"credentialsHash,omitempty"`

	// The imported-images record this boot ran under (imports.go):
	// Staged while the container store serves unpacks that no
	// operator has yet proven, or Proven on the ordinary boot whose
	// tarballs all match the record. It is empty when the lifecycle
	// is not running, either because there is no durable machineState
	// to remember a trial, or because the container store is
	// ephemeral and a reboot resets it anyway. ImportsDiscarded
	// records that this boot found a trial still standing from a boot
	// that died unproven, and threw the container store away rather
	// than trust it.
	ImportsSource    ManifestSource `json:"importsSource,omitempty"`
	ImportsHash      string         `json:"importsHash,omitempty"`
	ImportsDiscarded bool           `json:"importsDiscarded,omitempty"`

	// Restarts counts the in-place k3s restarts this boot has
	// performed to apply restart-class changes (cluster/changes.go).
	// It lives in the boot record because it shares the boot's
	// lifetime: a reboot re-makes the record, and the count returns
	// to zero. That reset is itself a signal. A change that arrived
	// by restart increments this count without moving the boot
	// record's time, and a change that arrived by reboot does the
	// opposite.
	Restarts int `json:"restarts,omitempty"`

	// Rejection is the standing quarantine record for the Machine
	// manifest. Every boot republishes it until a promotion clears
	// it, so a rejected spec stays visible in the cluster no matter
	// how many times the machine power-cycles. ClusterRejection is
	// the same record for the Cluster document. SystemRejection is
	// for a system release whose proving boot fell back.
	// CredentialsRejection is for a credentials document that would
	// not parse.
	Rejection            *Rejection `json:"rejection,omitempty"`
	ClusterRejection     *Rejection `json:"clusterRejection,omitempty"`
	SystemRejection      *Rejection `json:"systemRejection,omitempty"`
	CredentialsRejection *Rejection `json:"credentialsRejection,omitempty"`
}

// CrashReason is the kernel's own word for why it dumped its log:
// the kmsg-dump reason, taken from the first line of each pstore
// record. Panic and Oops are the two words the vendored kernel's
// configuration dumps. The type stays an open string rather than a
// closed vocabulary, because the kernel owns these words and a
// future kernel may say something new. A status write must not fail
// over an unexpected word.
type CrashReason string

const (
	CrashPanic CrashReason = "Panic"
	CrashOops  CrashReason = "Oops"
)

// CrashStatus summarizes one kernel crash from the records that
// pstore preserved across the reboot. It is a stub, not the
// evidence: the kernel log tail around a crash runs to kilobytes,
// and status is read on every list and watch, so the full text
// stays on disk and this block carries just enough to say what
// happened and where to read the rest.
type CrashStatus struct {
	// Time is the machine's own clock at the moment of the crash. A
	// crash usually beats the boot's first clock sync, so this is
	// RTC time, reported as recorded.
	Time *time.Time `json:"time,omitempty"`

	// Reason is the kernel's word for the crash: Panic or Oops.
	Reason CrashReason `json:"reason,omitempty"`

	// Message is the kernel's own first description of the failure:
	// the "Kernel panic - not syncing:" line, or the oops's BUG
	// line. It is capped and cleaned of control bytes before it
	// leaves the machine.
	Message string `json:"message,omitempty"`

	// Records is where the full kernel log tail lives on the
	// machine: a directory under machineState's crash store, or
	// /sys/fs/pstore itself on a machine whose machineState fell
	// back to memory and whose records therefore stay in the
	// firmware's own store.
	Records string `json:"records,omitempty"`
}

// RebootApprovedCondition is the rollout conductor's grant of a
// reboot turn. It is the one condition type on a Machine's status
// that two different programs use: the cluster operator writes and
// removes it, and the machine's own operator carries it along
// unchanged and acts on it. It belongs here because it is shared
// vocabulary, in the same way that PodScheduled is a condition the
// scheduler writes onto Pods that the kubelet owns.
const RebootApprovedCondition = "RebootApproved"
