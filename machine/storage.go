package machine

// The storage half of the Machine spec.
//
// Storage is declared by purpose, not by mount path. The manifest
// never says "/var/lib/rancher" (that's a k3s implementation detail
// liken owns); it says "this disk holds the cluster's state", and
// liken translates. The roles are fields rather than a list on
// purpose: each is a singleton (a machine has one cluster state, one
// pod-storage pool), so making them schema means a duplicate role
// can't even be expressed, and a new role is visibly an API change.
//
// The device path in each role matters only on the boot that claims
// the disk: kernel device names are assigned in driver probe order,
// which makes them a handle, not an identity. Claiming writes the
// role's name onto the partition itself (its GPT partition name), and
// every boot after finds it by that name, wherever the disk
// enumerates.

import (
	"fmt"
	"strconv"
	"strings"
)

// PartitionPrefix namespaces liken's GPT partition names, so a glance
// at any partition table shows which partitions belong to liken and
// which role each serves.
const PartitionPrefix = "liken:"

// StorageRoleName names one of the storage roles. The vocabulary is
// closed: these five names are the spec's field names, the GPT
// partition names (behind PartitionPrefix), and the status's keys,
// so they are defined once here and everything else ranges over
// StorageRoleNames instead of respelling them.
type StorageRoleName string

const (
	SystemARole          StorageRoleName = "systemA"
	SystemBRole          StorageRoleName = "systemB"
	MachineStateRole     StorageRoleName = "machineState"
	MachineEphemeralRole StorageRoleName = "machineEphemeral"
	ClusterStateRole     StorageRoleName = "clusterState"
	PodStorageRole       StorageRoleName = "podStorage"
	PodEphemeralRole     StorageRoleName = "podEphemeral"
)

// StorageRoleNames is the canonical order: the order partitions are
// laid down when roles share a disk (fixed here rather than by YAML
// map order, which Kubernetes doesn't preserve). The system slots
// come first, because the firmware is the earliest reader of any
// partition liken owns and an EFI system partition conventionally
// leads its disk; machineState comes next, ahead of all the data
// roles, so the partition a future boot must find before it has read
// any spec sits at the front of them. (Recognition is by partition
// name, never by position — the order is layout convention, not a
// discovery mechanism.)
var StorageRoleNames = []StorageRoleName{
	SystemARole,
	SystemBRole,
	MachineStateRole,
	MachineEphemeralRole,
	ClusterStateRole,
	PodStorageRole,
	PodEphemeralRole,
}

type StorageSpec struct {
	// SystemA and SystemB are the operating system's own boot slots:
	// each holds one complete liken version (the kernel and the
	// initramfs that is the rest of the OS), and the machine runs
	// from one while upgrades are written to the other — blue-green,
	// at the scale of the whole operating system. They are EFI system
	// partitions carrying FAT32, because the firmware itself is their
	// first reader and FAT is the only filesystem it promises to
	// understand. A machine without them boots from external media
	// forever (the lab's -kernel flag, a USB stick); a machine with
	// them can be installed once and upgraded declaratively.
	SystemA *StorageRole `json:"systemA,omitempty"`
	SystemB *StorageRole `json:"systemB,omitempty"`

	// MachineState is the machine's own durable data: the manifests
	// that configure it (the staged and proven copies that let a spec
	// edit survive a reboot and apply at the next one). Small, but it
	// changes what the machine *is*: with it, configuration outlives
	// the image that first delivered it.
	MachineState *StorageRole `json:"machineState,omitempty"`

	// MachineEphemeral is the operating system's own scratch space:
	// /tmp. Small but necessary: the container runtime stages exec
	// sessions there, and on a machine whose root is RAM, moving it
	// to disk frees that memory for pods.
	MachineEphemeral *StorageRole `json:"machineEphemeral,omitempty"`

	// ClusterState is k3s's state: the etcd or sqlite database, TLS
	// material, and containerd's images. Persisting it is what lets a
	// reboot resume the same cluster instead of starting a new one.
	// (Named for the cluster, not the machine, on purpose: this data
	// belongs to the future kind: Cluster story.)
	ClusterState *StorageRole `json:"clusterState,omitempty"`

	// PodStorage is durable storage pods claim by name: the
	// PersistentVolumeClaim pool, served by k3s's local-path
	// provisioner from this role's filesystem.
	PodStorage *StorageRole `json:"podStorage,omitempty"`

	// PodEphemeral is kubelet's working space: emptyDir volumes and
	// per-pod scratch, the pool that pods meter with
	// ephemeral-storage requests and limits.
	PodEphemeral *StorageRole `json:"podEphemeral,omitempty"`
}

// StorageRole places one role onto hardware. A role missing from the
// spec isn't an error; that role's directory simply stays on the
// machine's RAM root.
type StorageRole struct {
	// Device is the disk this role lives on, as a device path
	// (/dev/vda). Consulted only when claiming a blank disk.
	Device string `json:"device"`

	// Size is how much of the device this role takes, as a binary
	// quantity ("2Gi"): an exact allocation, not a request. Omitted,
	// the role takes the rest of its disk; only one role per disk may
	// do that.
	Size string `json:"size,omitempty"`
}

// A DeclaredRole is one role present in the spec, paired with its
// name: the form the rest of liken works with, since the name becomes
// the partition's on-disk identity.
type DeclaredRole struct {
	Name StorageRoleName
	StorageRole
}

// PartitionName is the role's on-disk identity: the GPT partition
// name written when the role's disk is claimed, and matched on every
// boot after.
func (r DeclaredRole) PartitionName() string {
	return PartitionPrefix + string(r.Name)
}

// Role addresses one role's declaration by name; nil for names
// outside the vocabulary and for roles the spec leaves out.
func (s *StorageSpec) Role(name StorageRoleName) *StorageRole {
	switch name {
	case SystemARole:
		return s.SystemA
	case SystemBRole:
		return s.SystemB
	case MachineStateRole:
		return s.MachineState
	case MachineEphemeralRole:
		return s.MachineEphemeral
	case ClusterStateRole:
		return s.ClusterState
	case PodStorageRole:
		return s.PodStorage
	case PodEphemeralRole:
		return s.PodEphemeral
	}
	return nil
}

// Roles returns the declared roles in StorageRoleNames' canonical
// order.
func (s StorageSpec) Roles() []DeclaredRole {
	var roles []DeclaredRole
	for _, name := range StorageRoleNames {
		if role := s.Role(name); role != nil {
			roles = append(roles, DeclaredRole{name, *role})
		}
	}
	return roles
}

// Validate checks the spec's internal consistency: the errors a
// person can fix in the manifest, caught before any disk is touched.
func (s StorageSpec) Validate() error {
	remainders := map[string]StorageRoleName{}
	for _, role := range s.Roles() {
		if role.Device == "" {
			return fmt.Errorf("storage role %s: no device", role.Name)
		}
		if role.Size == "" {
			if other, ok := remainders[role.Device]; ok {
				return fmt.Errorf(
					"storage roles %s and %s both want the rest of %s; only one role per disk may omit its size",
					other, role.Name, role.Device)
			}
			remainders[role.Device] = role.Name
			continue
		}
		if _, err := ParseSize(role.Size); err != nil {
			return fmt.Errorf("storage role %s: %w", role.Name, err)
		}
	}
	return nil
}

// StorageCondition summarizes storage as one standard Kubernetes
// condition, comparing what the spec declared against where each role
// is actually backed. True means every declared role sits on its
// partition. False should be unreachable on a running machine, since
// init powers off rather than boot with a declared role unsatisfied.
// But a condition has to be able to express every state it names, and
// a future, softer failure mode may need it.
func StorageCondition(spec StorageSpec, status StorageStatus) Condition {
	var placed, inMemory []string
	for _, role := range spec.Roles() {
		rs := status.Role(role.Name)
		if rs != nil && rs.Backing == BackingPartition {
			placed = append(placed, fmt.Sprintf("%s on %s", role.Name, rs.Device))
		} else {
			inMemory = append(inMemory, string(role.Name))
		}
	}
	switch {
	case len(inMemory) > 0:
		return Condition{
			Type: "StorageReady", Status: "False", Reason: "RolesInMemory",
			Message: fmt.Sprintf("declared roles backed by memory: %s", strings.Join(inMemory, ", ")),
		}
	case len(placed) > 0:
		return Condition{
			Type: "StorageReady", Status: "True", Reason: "AllRolesPlaced",
			Message: strings.Join(placed, ", "),
		}
	default:
		return Condition{
			Type: "StorageReady", Status: "True", Reason: "NothingDeclared",
			Message: "no storage declared; all roles backed by memory",
		}
	}
}

// ParseSize reads a binary quantity ("2Gi", "512Mi", or a plain
// count of bytes) into bytes. Only the power-of-two suffixes are
// accepted: disks are carved in the same units their partition math
// uses, and accepting "2G" (decimal) alongside "2Gi" (binary) invites
// off-by-7% surprises.
func ParseSize(s string) (uint64, error) {
	digits := s
	var unit uint64 = 1
	for suffix, size := range map[string]uint64{
		"Ki": 1 << 10,
		"Mi": 1 << 20,
		"Gi": 1 << 30,
		"Ti": 1 << 40,
	} {
		if rest, ok := strings.CutSuffix(s, suffix); ok {
			digits, unit = rest, size
			break
		}
	}
	n, err := strconv.ParseUint(digits, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("size %q: expected bytes or a Ki/Mi/Gi/Ti quantity", s)
	}
	// Zero is rejected here rather than left for the partition math to
	// trip over: a zero-sector partition would have an end before its
	// start, and a zero-byte role is always a manifest mistake.
	if n == 0 {
		return 0, fmt.Errorf("size %q: a storage role can't be zero bytes", s)
	}
	return n * unit, nil
}
