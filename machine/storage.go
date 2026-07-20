package machine

// This file holds the storage half of the Machine spec.
//
// Storage is declared by purpose, not by mount path. The manifest
// never says "/var/lib/rancher" (a k3s implementation detail that
// liken owns). Instead it says "this disk holds the cluster's
// state", and liken translates that purpose into the actual path.
// The roles are fields rather than a list, on purpose. Each role is
// a singleton: a machine has one cluster state, and one pod-storage
// pool. Making the roles fields in the schema means a duplicate role
// cannot even be expressed. A new role is visibly a change to the
// API.
//
// The device path in each role matters only on the boot that claims
// the disk. The kernel assigns device names in driver probe order,
// so a name addresses a disk within one boot, but the name does not
// identify the disk across boots. Claiming a disk writes the role's
// name onto the partition itself, as its GPT partition name. Every
// boot after that finds the partition by that name, wherever the
// disk enumerates.

import (
	"fmt"
	"strconv"
	"strings"
)

// PartitionPrefix namespaces liken's GPT partition names. Anyone who
// looks at a partition table can see which partitions belong to
// liken, and which role each partition serves.
const PartitionPrefix = "liken:"

// StorageRoleName names one of the storage roles. The vocabulary is
// closed: these nine names are the spec's field names, the GPT
// partition names (behind PartitionPrefix), and the status's keys.
// The names are defined once here, and everything else ranges over
// StorageRoleNames instead of spelling them out again.
type StorageRoleName string

const (
	BIOSBootRole         StorageRoleName = "biosBoot"
	BootHomeRole         StorageRoleName = "bootHome"
	SystemARole          StorageRoleName = "systemA"
	SystemBRole          StorageRoleName = "systemB"
	MachineStateRole     StorageRoleName = "machineState"
	MachineEphemeralRole StorageRoleName = "machineEphemeral"
	ClusterStateRole     StorageRoleName = "clusterState"
	PodStorageRole       StorageRoleName = "podStorage"
	PodEphemeralRole     StorageRoleName = "podEphemeral"
)

// StorageRoleNames is the canonical order. It is the order that
// liken lays partitions down when roles share a disk. The order is
// fixed here, rather than by YAML map order, because Kubernetes does
// not preserve YAML map order.
//
// The boot roles lead the list, with the earliest reader first. BIOS
// firmware executes the MBR before anything else exists, so the
// partition that its boot code jumps into comes first. GRUB's own
// config home comes next. The system slots come after that: an EFI
// system partition conventionally leads its disk, and here it leads
// everything that the firmware does not read. machineState comes
// next, ahead of all the data roles. It holds the partition that a
// future boot must find, before that boot has read any spec.
//
// liken recognizes a role by its partition name, never by its
// position in this order. The order is a layout convention, not a
// way to discover roles.
var StorageRoleNames = []StorageRoleName{
	BIOSBootRole,
	BootHomeRole,
	SystemARole,
	SystemBRole,
	MachineStateRole,
	MachineEphemeralRole,
	ClusterStateRole,
	PodStorageRole,
	PodEphemeralRole,
}

type StorageSpec struct {
	// BIOSBoot and BootHome are how a machine declares that it boots
	// through GRUB rather than UEFI firmware. There is no separate
	// firmware field. Declaring the partitions that GRUB needs is
	// itself the declaration. A BIOS machine has no boot variables to
	// hold the blue-green bookkeeping, so liken supplies the pieces
	// that the firmware would otherwise provide. BIOSBoot is a tiny
	// raw partition (about 1Mi, with no filesystem) that holds GRUB's
	// core image, the code that the MBR's 440 boot bytes jump into.
	// BootHome is a small FAT32 partition (about 64Mi) that holds
	// GRUB's config and its environment block, the file that holds
	// the same role as BootNext and BootOrder. Machines that boot
	// UEFI leave both fields out.
	BIOSBoot *StorageRole `json:"biosBoot,omitempty"`
	BootHome *StorageRole `json:"bootHome,omitempty"`

	// SystemA and SystemB are the operating system's own boot slots.
	// Each slot holds one complete liken version: the kernel and the
	// initramfs that makes up the rest of the OS. The machine runs
	// from one slot while it writes upgrades to the other slot. This
	// is a blue-green deployment at the scale of the whole operating
	// system. The slots are EFI system partitions that carry FAT32,
	// because the firmware itself is their first reader, and FAT is
	// the only filesystem that the firmware promises to understand. A
	// machine without these slots boots from external media forever,
	// for example the lab's -kernel flag or a USB stick. A machine
	// with these slots can be installed once, and upgraded
	// declaratively after that.
	SystemA *StorageRole `json:"systemA,omitempty"`
	SystemB *StorageRole `json:"systemB,omitempty"`

	// MachineState is the machine's own durable data: the manifests
	// that configure the machine. It holds the staged and proven
	// copies that let a spec edit survive a reboot, and apply at the
	// next reboot. The data is small, but it changes the machine in a
	// fundamental way. With this data, the configuration outlives the
	// image that first delivered it.
	MachineState *StorageRole `json:"machineState,omitempty"`

	// MachineEphemeral is the operating system's own scratch space:
	// /tmp. It is small, but necessary, because the container runtime
	// stages exec sessions there. On a machine whose root filesystem
	// is RAM, moving /tmp to disk frees that memory for pods.
	MachineEphemeral *StorageRole `json:"machineEphemeral,omitempty"`

	// ClusterState is k3s's state: the etcd or sqlite database, TLS
	// material, and containerd's images. Persisting this state is
	// what lets a reboot resume the same cluster, instead of starting
	// a new one. This role is named for the cluster, not the machine,
	// because this data belongs to the cluster rather than to any one
	// machine.
	ClusterState *StorageRole `json:"clusterState,omitempty"`

	// PodStorage is durable storage that pods claim by name: the
	// PersistentVolumeClaim pool. k3s's local-path provisioner serves
	// this pool from this role's filesystem.
	PodStorage *StorageRole `json:"podStorage,omitempty"`

	// PodEphemeral is kubelet's working space: emptyDir volumes and
	// per-pod scratch space. It is the pool that pods measure with
	// ephemeral-storage requests and limits.
	PodEphemeral *StorageRole `json:"podEphemeral,omitempty"`
}

// StorageRole places one role onto hardware. A role that is missing
// from the spec is not an error. That role's directory simply stays
// on the machine's RAM root.
type StorageRole struct {
	// Device is the disk that this role lives on, as a device path
	// (/dev/vda). The code consults this field only when it claims a
	// blank disk.
	Device string `json:"device"`

	// Size is how much of the device this role takes, as a binary
	// quantity ("2Gi"). This is an exact allocation, not a request.
	// If Size is omitted, the role takes the rest of its disk. Only
	// one role per disk may omit Size.
	Size string `json:"size,omitempty"`
}

// A DeclaredRole is one role that is present in the spec, paired
// with its name. This is the form that the rest of liken works with,
// because the name becomes the partition's on-disk identity.
type DeclaredRole struct {
	Name StorageRoleName
	StorageRole
}

// PartitionName is the role's on-disk identity: the GPT partition
// name. liken writes this name when it claims the role's disk, and
// matches it on every boot after that.
func (r DeclaredRole) PartitionName() string {
	return PartitionPrefix + string(r.Name)
}

// systemSlotsDir is where the system slots' filesystems are mounted:
// slot A at system/a, and slot B at system/b. Init mounts the slots
// there, through its roleMounts table. The operator writes
// downloaded releases there too, through a hostPath mount. So this
// path is defined once, in the package that both programs share.
const systemSlotsDir = "/var/lib/liken/system"

// SystemSlotDir is one slot's mountpoint. Slots are named "A" and
// "B" everywhere a person sees them: boot entries, conditions, and
// the liken.slot= parameter. The directory names use the same
// letters, in lowercase.
func SystemSlotDir(slot string) string {
	return systemSlotsDir + "/" + strings.ToLower(slot)
}

// InactiveSlot is the slot that a machine is not running from. A
// downloaded release lands on this slot; that placement is the point
// of the blue-green arrangement. InactiveSlot returns "" for a
// machine whose boot did not come from a slot at all. Such a machine
// has no inactive slot, and it should never download a release that
// it could not boot.
func InactiveSlot(running string) string {
	switch running {
	case "A":
		return "B"
	case "B":
		return "A"
	}
	return ""
}

// Role addresses one role's declaration by name. It returns nil for
// names outside the vocabulary, and for roles that the spec leaves
// out.
func (s *StorageSpec) Role(name StorageRoleName) *StorageRole {
	switch name {
	case BIOSBootRole:
		return s.BIOSBoot
	case BootHomeRole:
		return s.BootHome
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

// Validate checks the spec's internal consistency. It catches the
// errors that a person can fix in the manifest, before the code
// touches any disk.
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

// ParseSize reads a binary quantity ("2Gi", "512Mi", or a plain
// count of bytes) into bytes. ParseSize accepts only the
// power-of-two suffixes. liken divides disks into partitions using
// the same units that its partition math uses. Accepting "2G"
// (decimal) alongside "2Gi" (binary) would invite subtle mistakes,
// because the two units differ by about 7%.
func ParseSize(s string) (uint64, error) {
	// The units form an ordered list rather than a map. The search
	// stops at the first suffix that matches, so the order of the
	// candidates must stay fixed, and a map's iteration order does
	// not stay fixed. No suffix here is a suffix of another suffix,
	// so any fixed order works. If someone adds a suffix that
	// overlaps another (for example "KiB"), that suffix must come
	// before the shorter suffix that it ends with.
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
		return 0, fmt.Errorf("size %q: expected bytes or a Ki/Mi/Gi/Ti quantity", s)
	}
	// ParseSize rejects zero here, instead of leaving it for the
	// partition math to fail on. A zero-sector partition would have
	// an end before its start, and a zero-byte role is always a
	// mistake in the manifest.
	if n == 0 {
		return 0, fmt.Errorf("size %q: a storage role can't be zero bytes", s)
	}
	return n * unit, nil
}
