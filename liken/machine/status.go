package machine

// The status of a Machine is what init and the operators observed on
// the machine. No value in status depends on an earlier status: each
// pass derives every value again from the machine, so a status that is
// erased is rebuilt in full.

import (
	"strings"
	"time"

	"github.com/liken-sh/liken/liken/api"
)

type MachineStatus struct {
	// Phase summarizes the conditions in one word for a person who
	// reads `kubectl get machines`. Each pass computes it from the
	// conditions, so the two agree. A program reads the conditions.
	Phase api.Phase `json:"phase,omitempty"`

	// ObservedGeneration is the metadata.generation of the spec that
	// the operator computed this status from, so a client can tell
	// whether the operator has read its edit without parsing the
	// conditions. Init leaves it empty, because the API server assigns
	// generations and init runs before the cluster exists.
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// Version reports the version of liken and of each component that
	// the OS image contains.
	Version VersionStatus `json:"version,omitzero"`

	// Role is a leader, which runs a control plane, or a follower,
	// which runs workloads. Init derives it at boot from the leaders
	// list of the Cluster manifest. No field of the Machine spec sets
	// it.
	Role api.Role `json:"role,omitempty"`

	// Network is the result of the network setup at boot, by DHCP or
	// by static assignment.
	Network NetworkStatus `json:"network,omitzero"`

	// Time reports the state of this machine's clock. Init updates it
	// for as long as the machine runs, because it adjusts the clock
	// continuously and not only at boot.
	Time TimeStatus `json:"time,omitzero"`

	// Hardware is what the machine found when it started.
	Hardware HardwareStatus `json:"hardware,omitzero"`

	// Firmware is the firmware mode that the machine booted in, and the
	// boot entries from the firmware's variable store. The mode is BIOS
	// on any machine with no firmware variables to read, such as a
	// legacy server or a direct-kernel boot under a hypervisor.
	//
	// Firmware is separate from Boot because the firmware variables
	// persist across reboots. BootCurrent is the only field here that
	// describes this boot.
	Firmware FirmwareStatus `json:"firmware,omitzero"`

	// Storage reports where each storage role is on this boot, whether
	// the spec declares the role or not. It connects the roles of the
	// spec with the disks of hardware.blockDevices.
	Storage StorageStatus `json:"storage,omitzero"`

	// Sysctls has the value of each parameter of machine.OSSysctls and
	// of spec.sysctls, read back from /proc/sys. A parameter that liken
	// could not write is absent, so the map lists the parameters in
	// effect, not every one that was requested.
	Sysctls map[string]string `json:"sysctls,omitempty"`

	// HostEntries has the entries of /etc/hosts, as the observed
	// counterpart of spec.network.hostEntries. Init writes the file at
	// boot, and the machine operator writes it on every pass where it
	// differs from the spec, so an edit takes effect without a reboot.
	// For the same reason, BootStatus records no copy of it.
	HostEntries []HostEntry `json:"hostEntries,omitempty"`

	// Rlimits has the resource limits of machine.OSRlimits and of
	// spec.rlimits that init applied, read back from the kernel. Init
	// is PID 1, so every process on the machine inherits them,
	// including k3s, containerd, and the containers. `/proc/1/limits`
	// shows the same limits.
	//
	// Each value uses the syntax of spec.rlimits, so the two compare
	// directly: a number when the soft and hard limits are equal,
	// "infinity" for no limit, and "soft:hard" when they differ. A
	// limit that liken could not set is absent.
	Rlimits map[string]string `json:"rlimits,omitempty"`

	// Modules reports the result for each module in spec.modules. It
	// does not list the modules that every liken image loads, because
	// the image and not the spec chooses those.
	Modules []ModuleStatus `json:"modules,omitempty"`

	// Serio reports each attachment that init made, or tried to make,
	// for spec.serio, and the device nodes that each attached driver
	// created. It changes while the machine runs: an adapter that is
	// unplugged becomes Missing, and it becomes Attached again when it
	// is plugged back in.
	Serio []SerioStatus `json:"serio,omitempty"`

	// Features reports the result on this machine for each feature
	// that spec.features of the Cluster enables. The result is per
	// machine because it depends on what the booted image contains,
	// and machines can run different releases during a rollout.
	Features []FeatureStatus `json:"features,omitempty"`

	// Registries reports what this machine wrote into the
	// registries.yaml of k3s: which registries are mirrored, which have
	// credentials, and whether the embedded registry is on. It lists
	// the hosts and never the credentials.
	Registries RegistriesStatus `json:"registries,omitzero"`

	// Runtime reports the runtime settings that init applied to k3s,
	// the kubelet, and containerd on this boot.
	Runtime RuntimeStatus `json:"runtime,omitzero"`

	// Boot records what this boot ran under: the copy of each document
	// and the storage as init set it up. The operator compares it with
	// the spec to find drift. Unlike Firmware, every value here is
	// derived again on each boot.
	Boot BootStatus `json:"boot,omitzero"`

	// NextBoot records the copy of each document that the next boot
	// would choose. machine-operator reads the machine's store and
	// writes it on every pass, because the operator stages and promotes
	// documents in the store after init wrote Boot.
	NextBoot NextBootStatus `json:"nextBoot,omitzero"`

	// LastCrash is the newest kernel panic or oops that this machine
	// has records of, from the pstore records that init copied to
	// machineState. It can be much older than the previous boot. Read
	// its time. A newer crash replaces it, and nothing else clears it.
	LastCrash *CrashStatus `json:"lastCrash,omitempty"`

	// LastFailStop is the last boot that this machine refused to run,
	// with the reason that init gave (see failstop.go). It can be much
	// older than the previous boot. Read its time. The next refusal
	// replaces it, and nothing else clears it.
	LastFailStop *FailStop `json:"lastFailStop,omitempty"`

	// Conditions are the standard Kubernetes conditions, such as
	// "Ready" and "SysctlsApplied".
	Conditions []api.Condition `json:"conditions,omitempty"`

	// Pending lists each staged document that waits for a reboot or a
	// k3s restart to apply it, with the hash that a person approves. A
	// program reads the hash here and does not parse it out of a
	// condition message.
	Pending []PendingDisruption `json:"pending,omitempty"`
}

// VersionStatus lists the version of liken and of each outside
// component that the OS image contains.
//
// Kernel, Xtables, and MicrocodeRevision are read from the running
// machine. A boot artifact, a bundled image, or a data file cannot
// report its own version, so those fields come from
// /usr/share/liken/components.yaml, which the image build writes from
// the same pins as the release document.
//
// K3s is here although the Node reports a kubelet version, because
// this field reports what the OS image contains.
type VersionStatus struct {
	Liken  string `json:"liken,omitempty"`
	Kernel string `json:"kernel,omitempty"`

	// Xtables is the version that `iptables -V` reports, for example
	// "v1.8.11 (legacy)".
	Xtables string `json:"xtables,omitempty"`

	K3s       string `json:"k3s,omitempty"`
	Trust     string `json:"trust,omitempty"`
	E2fsprogs string `json:"e2fsprogs,omitempty"`
	OpenISCSI string `json:"openIscsi,omitempty"`
	NFSUtils  string `json:"nfsUtils,omitempty"`

	// WPASupplicant is the hostap release of the supplicant that init
	// runs for a wireless entry in spec.network.
	WPASupplicant string `json:"wpaSupplicant,omitempty"`

	SystemdBoot   string `json:"systemdBoot,omitempty"`
	Grub          string `json:"grub,omitempty"`
	Hwdata        string `json:"hwdata,omitempty"`
	Tzdata        string `json:"tzdata,omitempty"`
	LinuxFirmware string `json:"linuxFirmware,omitempty"`

	// WirelessRegdb is the version of the wireless regulatory database
	// under /lib/firmware, which decides the channels a radio can use.
	// linux-firmware/fetch.sh pins it.
	WirelessRegdb string `json:"wirelessRegdb,omitempty"`

	// Microcode is the pinned version of the early microcode cpio in
	// the release. MicrocodeRevision is the revision that the running
	// CPUs report. When the two agree, the CPUs loaded the early cpio.
	// On a virtual machine the hypervisor reports the revision, so only
	// real hardware shows this.
	Microcode         string `json:"microcode,omitempty"`
	MicrocodeRevision string `json:"microcodeRevision,omitempty"`
}

// NetworkStatus reports how the boot configured the network. The
// top-level fields describe the primary interface: the interface whose
// address is in the nodeCIDR of the Cluster, or the first interface
// that came up when none is. Interfaces has each interface.
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

	// Wireless is the state of the radio, set only for an interface
	// with a wireless entry in the spec. The interface gets an address
	// only after the radio joins, so a failed join leaves the address
	// fields empty.
	Wireless *WirelessStatus `json:"wireless,omitempty"`
}

// WirelessState is the state of a radio. The kernel reports the same
// missing carrier for a wrong passphrase and for an access point that
// is off, so only the supplicant's events tell WrongKey from
// NoCarrier. Waiting does not fix WrongKey.
type WirelessState string

const (
	WirelessAssociating WirelessState = "Associating"
	WirelessConnected   WirelessState = "Connected"
	WirelessWrongKey    WirelessState = "WrongKey"
	WirelessNoCarrier   WirelessState = "NoCarrier"
	// WirelessNotRaised means that the kernel call to bring the
	// interface up did not return before init's deadline, so no
	// supplicant ran. A driver that deadlocks while it powers the radio
	// causes it. The blocked call cannot be stopped, so the state
	// reports a fault of the driver and not of the network, and only a
	// reboot without the radio in the spec clears it.
	WirelessNotRaised WirelessState = "NotRaised"
)

// WirelessStatus is the wireless part of one interface's status: the
// network that the spec names, the result of the join, and the reason
// for a failed join.
type WirelessStatus struct {
	SSID    string        `json:"ssid,omitempty"`
	State   WirelessState `json:"state,omitempty"`
	Message string        `json:"message,omitempty"`

	// These fields describe the access point that the radio is
	// associated with, and are set only while the state is Connected.
	// A network can have many access points, and the BSSID identifies
	// one. Init updates the fields only when the radio associates with
	// another access point or channel, so a radio that stays connected
	// causes no status writes.
	BSSID        string     `json:"bssid,omitempty"`
	Frequency    int        `json:"frequency,omitempty"`
	Band         string     `json:"band,omitempty"`
	AssociatedAt *time.Time `json:"associatedAt,omitempty"`

	// LastDisconnectReason is the IEEE 802.11 reason code of the last
	// disconnection during this boot. Zero means none.
	LastDisconnectReason int `json:"lastDisconnectReason,omitempty"`
}

// TimeState is the state of a machine's clock. Synchronized means the
// machine follows a source that is itself synchronized. FreeRunning
// and Unsynchronized both mean that the clock follows no source. A
// free-running machine has no sources configured and runs on its
// hardware clock. An unsynchronized machine has sources and cannot
// reach them. A fleet with no upstream sources free-runs: its machines
// agree with each other but not necessarily with the rest of the
// world, and certificate validation depends on the second.
type TimeState string

const (
	TimeSynchronized   TimeState = "Synchronized"
	TimeFreeRunning    TimeState = "FreeRunning"
	TimeUnsynchronized TimeState = "Unsynchronized"
)

type TimeStatus struct {
	State TimeState `json:"state,omitempty"`

	// Source is the clock that this machine follows: an upstream
	// server on a leader, or one of the cluster's leaders on a
	// follower.
	Source string `json:"source,omitempty"`

	// Stratum is the NTP stratum: one more than the stratum of the
	// source. A free-running machine reports 10, the NTP convention for
	// a local clock. An unsynchronized machine reports 16, which NTP
	// reserves for a clock that no machine may use as a source.
	Stratum int `json:"stratum,omitempty"`

	// Offset is a bound on the clock's error, such as "±5ms" (see
	// OffsetBound in clock.go), and not the exact offset, because the
	// exact offset changes on every poll. A worse bound is published at
	// once. A better bound is published after three polls in a row
	// inside it (init/clockbound.go). Offset is empty while State is
	// not Synchronized, and after a boot that stepped the clock until
	// the next poll.
	Offset string `json:"offset,omitempty"`
}

type HardwareStatus struct {
	CPUs        int    `json:"cpus,omitempty"`
	MemoryBytes uint64 `json:"memoryBytes,omitempty"`

	// BlockDevices lists every real disk that the kernel found,
	// whether or not the spec names it.
	BlockDevices []BlockDevice `json:"blockDevices,omitempty"`

	// Unclaimed lists each device that has no driver bound and that a
	// loadable module could drive, so a machine with a driver for all
	// its hardware lists nothing. Devices with a driver are not
	// listed: workloads find them through /sys, and ResourceSlices
	// publish the ones that a claim can use.
	Unclaimed []UnclaimedDevice `json:"unclaimed,omitempty"`
}

// UnclaimedDevice is one device that has no driver bound, with the
// modules that could drive it. A device for which the kernel build has
// no module, such as a host bridge or a platform stub, is not listed,
// because no edit to spec.modules can fix it.
type UnclaimedDevice struct {
	// Modalias is the kernel's identifier for this device, the string
	// that the kernel matches the alias patterns of modules against.
	Modalias string `json:"modalias"`

	// Bus is pci or usb.
	Bus string `json:"bus"`

	// Name is a USB device's manufacturer and product strings, or the
	// name of a PCI device from the pci.ids database. When neither is
	// available it is the numeric vendor:device ID.
	Name string `json:"name,omitempty"`

	// Class is the kind of device, such as mass-storage, display, or
	// network, decoded from the bus's class code.
	Class string `json:"class,omitempty"`

	// Candidates lists the loadable modules whose alias patterns match
	// this device, in the order that the kernel build prefers. A device
	// can match more than one, as a USB storage device matches both uas
	// and usb_storage, and the person who edits spec.modules chooses.
	Candidates []string `json:"candidates,omitempty"`

	// Message says how to fix the gap: add a candidate to
	// spec.modules, or move to an image that has one.
	Message string `json:"message,omitempty"`
}

// BlockDevice is one disk, as read from sysfs. Name is the kernel's
// name for this boot, such as vda or nvme0n1, which depends on the
// order that drivers probe, so it can name another disk on the next
// boot.
type BlockDevice struct {
	Name      string `json:"name"`
	SizeBytes uint64 `json:"sizeBytes,omitempty"`
	Model     string `json:"model,omitempty"`
	Serial    string `json:"serial,omitempty"`

	// StableNames lists the names that identify this disk across
	// boots: each by-id name, which comes from the disk's own
	// controller, and then the by-path name, which comes from the port,
	// when the disk has one. spec.storage.<role>.device accepts any of
	// them. Prefer a by-id name, because a by-path name names another
	// disk when the disk moves to another bay. A disk with no by-id name
	// has only a by-path name.
	StableNames []string `json:"stableNames,omitempty"`

	// MaxRequestSize is the largest request that the kernel sends to
	// this disk now, read back from /sys/block/<name>/queue/max_sectors_kb,
	// in the syntax of spec.disks[].maxRequestSize.
	// MaxHardwareRequestSize is the largest that the disk's driver
	// accepts, max_hw_sectors_kb, and no setting can go above it. Both
	// are empty when sysfs shows no queue for the disk.
	MaxRequestSize         string `json:"maxRequestSize,omitempty"`
	MaxHardwareRequestSize string `json:"maxHardwareRequestSize,omitempty"`
}

// Backing is where the data of a storage role is: a partition assigned
// to the role, or the machine's RAM root, which is the default.
type Backing string

const (
	BackingPartition Backing = "Partition"
	BackingMemory    Backing = "Memory"
)

// FirmwareMode is the kind of firmware that booted the machine.
type FirmwareMode string

const (
	FirmwareUEFI FirmwareMode = "UEFI"
	FirmwareBIOS FirmwareMode = "BIOS"
)

// FirmwareStatus reports the boot variables of the firmware. Each entry
// is the variable's name and the entry's description, such as
// "Boot0001 (liken slot A)".
type FirmwareStatus struct {
	Mode FirmwareMode `json:"mode,omitempty"`

	// BootCurrent is the entry that the firmware reports it booted
	// from. It is empty when the firmware did not boot an entry, as in
	// a direct-kernel boot, and when the machine is not UEFI.
	BootCurrent string `json:"bootCurrent,omitempty"`

	// BootNext is the entry that the firmware boots once, at the next
	// power-on, in place of BootOrder. Init sets it for the proving
	// boot of a staged release. The requester of a Disruption with
	// spec.keepBootNext, such as a firmware update, can also set it.
	BootNext string `json:"bootNext,omitempty"`

	// BootOrder is the firmware's list of boot entries, with the first
	// choice first.
	BootOrder []string `json:"bootOrder,omitempty"`

	// BootOrderKept is true when the proven slot was first in
	// BootOrder at power-on, before init wrote anything. Every
	// shutdown puts the proven slot first, so false means that the
	// firmware did not keep the order through the reset, or that a
	// person changed it in the firmware menu. It is absent when the
	// machine has no proven slot. Init leaves BootNext to the requester
	// of a Disruption only when this is true (init/handover.go).
	BootOrderKept *bool `json:"bootOrderKept,omitempty"`
}

// StorageStatus has an entry for every storage role, whether the spec
// declares it or not, so a role that is not declared shows as Memory.
// The field names are the keys of spec.storage.
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

// StorageRoleStatus is where one role is. A role in memory reports no
// capacity, because all of those roles share one RAM root, and a
// figure for each would count it more than once.
type StorageRoleStatus struct {
	Backing       Backing `json:"backing"`
	Device        string  `json:"device,omitempty"`    // the partition's node this boot: vda1
	Partition     string  `json:"partition,omitempty"` // its on-disk name: liken:clusterState
	CapacityBytes uint64  `json:"capacityBytes,omitempty"`

	// LastStopUnclean is true when this boot found the role's
	// filesystem still marked as mounted, so the previous shutdown did
	// not unmount it. Only the FAT32 roles, the system slots and the
	// boot home, report it. The ext4 roles replay their journal and
	// need no warning.
	LastStopUnclean bool `json:"lastStopUnclean,omitempty"`
}

// Role returns the status of the role with the given spec name, or nil
// for a name that is not a role.
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

// AllRolesInMemory returns a status with every role in the RAM root,
// which is the state before init places any role on a partition.
func AllRolesInMemory() StorageStatus {
	s := StorageStatus{}
	for _, name := range StorageRoleNames {
		s.Role(name).Backing = BackingMemory
	}
	return s
}

// ModuleState is the result for one module of spec.modules. Loaded
// means the kernel loaded the module or already had it. Builtin means
// the module is compiled into the kernel.
//
// Missing means this kernel has no module with that name. The image
// contains the kernel's whole module tree, so the usual cause is a
// misspelled name. When the name is correct, the machine needs a
// release whose kernel builds the module. Failed means the kernel has
// the module and refused to load it, and Message has the kernel's
// error and the parameters that the load passed.
type ModuleState string

const (
	ModuleLoaded  ModuleState = "Loaded"
	ModuleBuiltin ModuleState = "Builtin"
	ModuleMissing ModuleState = "Missing"
	ModuleFailed  ModuleState = "Failed"
)

// ModuleStatus is the result for one module of spec.modules, from the
// boot or from the load after a later edit. For an unhealthy state,
// Message says how to fix it.
type ModuleStatus struct {
	Name    string      `json:"name"`
	State   ModuleState `json:"state"`
	Message string      `json:"message,omitempty"`

	// Parameters has the values read back from
	// /sys/module/<name>/parameters/ for the declared parameter names,
	// unchanged. A declared name is absent when the kernel ignored it
	// as unknown, and logged "unknown parameter ... ignored", or when
	// the driver registered it with no sysfs file or as write-only.
	// The values are not compared with the declaration, because the
	// kernel prints a bool as Y or N and an array with its own
	// separators.
	Parameters map[string]string `json:"parameters,omitempty"`

	// AlreadyResident is true when the module was in the kernel before
	// init loaded the modules of spec.modules, because the image's
	// fixed list, a feature, or an earlier module's dependencies loaded
	// it. Such a module never receives its parameters: finit_module
	// returns EEXIST and ignores them.
	AlreadyResident bool `json:"alreadyResident,omitempty"`
}

// FeatureState is the result for one enabled feature on one machine.
// Active means this boot set up everything that the feature needs on
// this machine. Missing means the booted image has no payload for the
// feature, so the machine needs a newer release. Failed means the
// image has the payload and its setup failed, for example because the
// kernel refused a module or a boot hook returned an error.
type FeatureState string

const (
	FeatureActive  FeatureState = "Active"
	FeatureMissing FeatureState = "Missing"
	FeatureFailed  FeatureState = "Failed"
)

type FeatureStatus struct {
	Name    string       `json:"name"`
	State   FeatureState `json:"state"`
	Message string       `json:"message,omitempty"`
}

// RuntimeStatus reports the runtime settings that this machine uses,
// with the same sections as spec.runtime. Each value is the resolved
// one, not the string of the spec, so after an edit across machines
// of different sizes it shows what each one uses.
type RuntimeStatus struct {
	K3s        K3sRuntimeStatus        `json:"k3s,omitzero"`
	Kubelet    KubeletRuntimeStatus    `json:"kubelet,omitzero"`
	Containerd ContainerdRuntimeStatus `json:"containerd,omitzero"`
	Etcd       EtcdRuntimeStatus       `json:"etcd,omitzero"`
}

// K3sRuntimeStatus has the settings that init applied to the k3s
// process on this boot. GoMemoryLimit is the resolved limit in MiB,
// for example "256Mi". It is empty when the cluster set no limit or
// turned it off, and GoGC is empty when the cluster set none. Go's
// defaults apply to an empty field. Debug is true when init wrote the
// debug key of k3s. Otherwise k3s logs at info level.
type K3sRuntimeStatus struct {
	GoMemoryLimit string `json:"goMemoryLimit,omitempty"`
	GoGC          int    `json:"goGC,omitempty"`
	Debug         bool   `json:"debug,omitempty"`
}

// ContainerdRuntimeStatus is the containerd configuration that init
// wrote on this boot. LogLevel is the level of the drop-in, empty when
// the cluster set none, and then containerd logs at info.
type ContainerdRuntimeStatus struct {
	LogLevel string `json:"logLevel,omitempty"`
}

// EtcdRuntimeStatus is the etcd configuration that init wrote on this
// boot. ExposeMetrics is true when init wrote the etcd-expose-metrics
// key of k3s, so the machine's etcd member serves its metrics on port
// 2381 of the node IP. It is false on a follower, and on a leader of a
// cluster that set spec.runtime.etcd.exposeMetrics to false. It is
// also true on a leader that keeps its datastore in sqlite, where no
// etcd runs and nothing listens on the port.
type EtcdRuntimeStatus struct {
	ExposeMetrics bool `json:"exposeMetrics,omitempty"`
}

// KubeletRuntimeStatus is the kubelet configuration that init wrote on
// this boot. It is empty when the cluster set none.
type KubeletRuntimeStatus struct {
	ImageGC ImageGCStatus `json:"imageGC,omitzero"`
}

// ImageGCStatus is the image garbage collection policy that init wrote
// into the kubelet's configuration file. A field is set only when the
// cluster set it. Otherwise the kubelet's default applies: 85 percent
// for the high threshold, 80 percent for the low threshold, two
// minutes for the minimum age, and no limit on the maximum age.
type ImageGCStatus struct {
	HighThresholdPercent int    `json:"highThresholdPercent,omitempty"`
	LowThresholdPercent  int    `json:"lowThresholdPercent,omitempty"`
	MaximumAge           string `json:"maximumAge,omitempty"`
	MinimumAge           string `json:"minimumAge,omitempty"`
}

// ManifestSource is the copy of a document that a boot ran under, in
// order of preference: a staged copy on its proving boot, the proven
// copy from the last good boot, and the image's seed copy, which only
// the first boot uses (see staging.go).
type ManifestSource string

const (
	ManifestSourceStaged ManifestSource = "Staged"
	ManifestSourceProven ManifestSource = "Proven"
	ManifestSourceSeed   ManifestSource = "Seed"
)

// BootStatus records what this boot ran under: the copy of each
// document, identified by the hash of its bytes, and the spec fields
// that init applied. The operator compares it with the spec to find
// drift. Each field records the request as init applied it, whatever
// the result. The results are in the other fields of status.
type BootStatus struct {
	// Time is when the machine booted, which init derives from the
	// kernel's uptime counter. It is a timestamp and not a duration,
	// because a duration would be out of date as soon as it was
	// written. `kubectl get machines` shows it as elapsed time in the
	// Uptime column. A reboot changes it, and a k3s restart does not.
	//
	// Status has no heartbeat, because every status write sends the
	// whole object to every watcher. A machine renews a Lease in the
	// liken-system namespace instead, as a Node renews its Lease in
	// kube-node-lease, and the cluster operator marks a machine whose
	// Lease is not renewed Lost.
	Time *time.Time `json:"time,omitempty"`

	// The Machine manifest that this boot ran under.
	ManifestSource ManifestSource `json:"manifestSource,omitempty"`
	ManifestHash   string         `json:"manifestHash,omitempty"`
	Storage        StorageSpec    `json:"storage,omitzero"`

	// Network is the network spec of the manifest that this boot used.
	// It is a pointer because nil and empty mean different things. An
	// empty spec declares no interface and uses the default network
	// setup. A nil record means the boot reported nothing, and the
	// operator does not compare it, because a comparison with nothing
	// would report drift on every machine and reboot the whole fleet.
	Network *NetworkSpec `json:"network,omitempty"`

	// Modules is the module list of the manifest that this boot used. A
	// module that the image did not have still counts as applied,
	// because another reboot with the same image changes nothing.
	//
	// The list is in the order in which this machine loaded the
	// modules, because the order decides which driver binds a device.
	// A live load appends the modules it loaded (see init/liveload.go).
	// The operator compares this order with spec.modules and stages a
	// reorder for the next boot.
	Modules []string `json:"modules,omitempty"`

	// Rlimits is the resource limit map of the manifest that this boot
	// used. It does not include machine.OSRlimits, because those ship
	// with the release and change only with a new system image. If it
	// included them, a release that changed them would report drift on
	// every machine.
	Rlimits map[string]string `json:"rlimits,omitempty"`

	// ModuleParameters is the spec.moduleParameters map that this boot
	// passed to the module loads. The values read back are in
	// status.modules[].parameters.
	ModuleParameters map[string]string `json:"moduleParameters,omitempty"`

	// Serio is the spec.serio list that this boot used. A live load
	// adds the entries that it applied.
	Serio []SerioAttachment `json:"serio,omitempty"`

	// Slot is the system slot that this boot came from, "A" or "B",
	// read from the liken.slot= kernel parameter. It is empty for a
	// boot that did not come from a slot, such as a direct-kernel boot
	// or a boot from install media. A release downloads to the other
	// slot.
	Slot string `json:"slot,omitempty"`

	// CommandLine is the complete kernel command line of this boot. No
	// spec field sets it, and the boot entries that liken writes set
	// only the console, the machine, and the slot. When a machine
	// behaves differently from the others, this field shows what the
	// firmware passed to the kernel without a serial console.
	CommandLine string `json:"commandLine,omitempty"`

	// The Cluster manifest that this boot ran under. It is recorded
	// apart from the Machine manifest because the two are staged and
	// proven independently, so a machine can match one and have
	// drifted from the other.
	ClusterManifestSource ManifestSource `json:"clusterManifestSource,omitempty"`
	ClusterManifestHash   string         `json:"clusterManifestHash,omitempty"`

	// The registry credentials document that this boot, or the latest
	// k3s restart, wrote into registries.yaml. The source is only
	// Staged or Proven, because the operator writes this document and
	// no image has a seed copy. Both fields are empty on a machine that
	// never had credentials.
	CredentialsSource ManifestSource `json:"credentialsSource,omitempty"`
	CredentialsHash   string         `json:"credentialsHash,omitempty"`

	// The record of the image tarballs that this boot gave to
	// containerd (see imports.go). It is Proven when the tarballs match
	// the record that an earlier boot proved, and Staged when they are
	// new and the operator has not yet proven that containerd serves
	// them. It is empty when machineState or clusterState is not on
	// disk, because then there is no place to keep the record or the
	// container store resets at every boot. ImportsDiscarded is true
	// when this boot found a staged record that no boot proved, and
	// deleted the container store in place of trusting it.
	ImportsSource    ManifestSource `json:"importsSource,omitempty"`
	ImportsHash      string         `json:"importsHash,omitempty"`
	ImportsDiscarded bool           `json:"importsDiscarded,omitempty"`

	// Restarts is the number of k3s restarts that this boot made to
	// apply a Cluster change that does not need a reboot (see
	// cluster.RestartApplies). A reboot sets it back to zero, so a
	// change by restart raises this count and keeps Time, and a change
	// by reboot changes Time.
	Restarts int `json:"restarts,omitempty"`

	// Rejection records that a staged Machine manifest was refused.
	// Every boot publishes it again until a manifest is promoted, so
	// the refusal stays in status across reboots. ClusterRejection is
	// the same record for the Cluster manifest. SystemRejection is for
	// a system release whose proving boot fell back.
	// CredentialsRejection is for a credentials document that did not
	// parse.
	Rejection            *Rejection `json:"rejection,omitempty"`
	ClusterRejection     *Rejection `json:"clusterRejection,omitempty"`
	SystemRejection      *Rejection `json:"systemRejection,omitempty"`
	CredentialsRejection *Rejection `json:"credentialsRejection,omitempty"`
}

// NextBootStatus records the copy of the Cluster document that the
// next boot would choose, by the rule that init applies at boot
// (chooseCluster in init/cluster.go): a staged copy that parses and has
// not had its trial, then a proven copy that parses, then the image's
// seed. The source and the hash have the same values as
// Boot.ClusterManifestSource and Boot.ClusterManifestHash, so a person
// can compare what this machine runs with what its next boot runs. The
// ClusterProven condition reports the comparison.
type NextBootStatus struct {
	ClusterManifestSource ManifestSource `json:"clusterManifestSource,omitempty"`
	ClusterManifestHash   string         `json:"clusterManifestHash,omitempty"`
}

// CrashReason is the kmsg-dump reason on the first line of a pstore
// record. liken's kernel configuration dumps on Panic and Oops. The
// type is an open string, because a later kernel can add a reason, and
// an unknown one must not make a status write fail.
type CrashReason string

const (
	CrashPanic CrashReason = "Panic"
	CrashOops  CrashReason = "Oops"
)

// CrashStatus summarizes one kernel crash. The kernel log of a crash is
// kilobytes long, and every list and watch of Machines sends status,
// so the log stays on the machine and Records gives its path.
type CrashStatus struct {
	// Time is when pstore recorded the dump, by the machine's own clock
	// at that moment, which may not yet have been synchronized.
	Time *time.Time `json:"time,omitempty"`

	Reason CrashReason `json:"reason,omitempty"`

	// Message is the first line in which the kernel described the
	// failure: the "Kernel panic - not syncing:" line, or the BUG line
	// of an oops. Init cuts it to a maximum length and removes control
	// bytes.
	Message string `json:"message,omitempty"`

	// Records is the path of the full kernel log on the machine: a
	// directory in the crash store of machineState, or /sys/fs/pstore
	// when machineState is in memory and the records stay in the
	// firmware's store.
	Records string `json:"records,omitempty"`
}

// RebootApprovedCondition is the condition that cluster-operator sets
// on a Machine when it grants the machine a reboot turn, and removes
// when the turn ends. machine-operator acts on the machine's entry in
// the Cluster's status.rebootTurns and not on this condition, and
// keeps the condition unchanged when it writes status.
// cluster-operator reads it back to count a silent machine as
// rebooting and not Lost.
const RebootApprovedCondition = "RebootApproved"

// ApprovalGrants reports whether an approval matches the hash of a
// request: the spec.approvedHash of a Disruption against its
// spec.hash.
func ApprovalGrants(approval, hash string) bool {
	return matchesHash(approval, hash)
}

// RebootRequestNames reports whether a person's reboot request, the
// spec.hash of a Disruption, matches bootID, the BootID of the running
// boot. No request matches an empty bootID.
func RebootRequestNames(request, bootID string) bool {
	return bootID != "" && matchesHash(request, bootID)
}

// matchesHash reports whether value, which a person wrote, is hash or
// a prefix of it. Condition messages and listings show the first 12
// characters of a hash, and a person pastes what they see. A shorter
// prefix never matches, so a bad paste does not match by accident.
func matchesHash(value, hash string) bool {
	return len(value) >= 12 && strings.HasPrefix(hash, value)
}

// BootID identifies the running boot: the sha256 of the boot time,
// which no two boots share. A person's reboot request names this ID,
// so the request applies to one boot only. It is a hash and not the
// timestamp so that a person sees and shortens it like every other
// hash that liken shows. It returns "" when the boot published no time.
func BootID(boot BootStatus) string {
	if boot.Time == nil || boot.Time.IsZero() {
		return ""
	}
	return ManifestHash([]byte(boot.Time.UTC().Format(time.RFC3339Nano)))
}

// A DisruptionKind is what applies a staged document: a reboot of the
// machine, or a restart of k3s.
type DisruptionKind string

const (
	DisruptionReboot  DisruptionKind = "Reboot"
	DisruptionRestart DisruptionKind = "Restart"
)

// A PendingDisruption is one staged document that waits for a reboot or
// a k3s restart. A reboot that a person requested is also an entry,
// with no document: it waits for the same policy, turn, and drain.
type PendingDisruption struct {
	// Condition is the type of the condition that reported this entry:
	// SpecConverged, ClusterConverged, VersionConverged,
	// CredentialsConverged, or RebootRequestHonored.
	Condition string `json:"condition"`

	// Kind is what applies the staged document. A reboot applies every
	// staged document. A restart applies only the documents that k3s
	// reads when it starts.
	Kind DisruptionKind `json:"kind"`

	// Hash is the hash of the staged document. The spec.hash of the
	// Disruption that machine-operator creates covers the hash of each
	// entry, so an approval of that Disruption approves exactly these
	// bytes. For a requested reboot, Hash is the BootID of the running
	// boot, which no longer matches after the machine reboots.
	Hash string `json:"hash"`

	// Summary is one line that says what the document changes.
	Summary string `json:"summary"`
}
