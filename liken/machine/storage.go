package machine

// The storage part of the Machine spec.
//
// A manifest declares storage by role, such as "this disk holds the
// cluster state", and never by mount path, so the manifest does not
// depend on k3s's directory layout under /var/lib/rancher. Each role is
// its own field, not an entry in a list, because a machine has each
// role at most once and a struct cannot hold a duplicate.
//
// init reads a role's device path only on the boot that claims a blank
// disk. The path can be a kernel name such as /dev/vda, or a name under
// /dev/disk/by-id/ or /dev/disk/by-path/. A kernel name can point to a
// different disk on the next boot, because the kernel assigns names in
// the order that drivers probe. A by-id name follows the disk, and a
// by-path name follows the port. When init claims the disk, it writes
// the role's name into the GPT partition name, and every later boot
// finds the partition by that name, so the device path does not have
// to stay valid. A /dev/disk/by-uuid/ name is refused, because it
// names a filesystem and a blank disk has none.
//
// The /dev/disk link trees do not exist yet when init claims a disk,
// because the program that creates them starts after storage is
// mounted. So init computes each attached disk's by-id and by-path names from
// sysfs, the same way that program does.

import (
	"fmt"
	"strconv"
	"strings"
)

// PartitionPrefix starts the GPT partition name of every partition
// that liken creates, so a partition table shows which partitions are
// liken's and the role of each.
const PartitionPrefix = "liken:"

// StorageRoleName is the name of a storage role. The same name is the
// spec's field name, the GPT partition name after PartitionPrefix, and
// the key in the status.
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

// StorageRoleNames lists the roles in the order that liken lays out
// partitions when roles share a disk. The partitions that a boot reads
// first come first. The MBR's boot code loads GRUB's core image from
// biosBoot, and GRUB then reads its config from bootHome.
// The system slots are EFI system partitions, which by convention come
// first on a disk. machineState comes before the data roles because a
// boot reads it before it has read any spec. Later boots find each
// role by its partition name, not by its position.
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

// StorageSpec declares which disk holds each storage role.
type StorageSpec struct {
	// Declaring BIOSBoot and BootHome makes the machine boot through GRUB
	// in BIOS mode instead of through UEFI firmware. No other field
	// selects the boot mode. BIOSBoot is a raw partition of about 1Mi
	// that holds GRUB's core image. BootHome is a FAT32 partition of
	// about 64Mi that holds GRUB's config and its environment block. A
	// BIOS machine has no EFI boot variables, so the environment block
	// records the slot to try next and the default slot, the jobs that
	// BootNext and BootOrder do on UEFI. A UEFI machine omits both
	// fields.
	BIOSBoot *StorageRole `json:"biosBoot,omitempty"`
	BootHome *StorageRole `json:"bootHome,omitempty"`

	// SystemA and SystemB are the two boot slots. Each slot holds one
	// liken version: the kernel and the initramfs. The machine runs from
	// one slot and writes an upgrade to the other. The slots are FAT32
	// EFI system partitions, because FAT is the filesystem that the UEFI
	// specification requires firmware to read. A machine without slots
	// boots only from outside media, such as a USB stick or QEMU's
	// -kernel flag, and cannot take an upgrade.
	SystemA *StorageRole `json:"systemA,omitempty"`
	SystemB *StorageRole `json:"systemB,omitempty"`

	// MachineState holds the machine's own durable data, such as the
	// staged and proven copies of its manifests. An edited spec is
	// staged here and applies at the next boot, and it stays in place
	// when an upgrade replaces the image.
	MachineState *StorageRole `json:"machineState,omitempty"`

	// MachineEphemeral is mounted at /tmp, where the container runtime
	// stages exec sessions. When the root filesystem is in RAM, a disk
	// for /tmp frees that memory for pods.
	MachineEphemeral *StorageRole `json:"machineEphemeral,omitempty"`

	// ClusterState holds k3s's state: the etcd or sqlite database, TLS
	// material, and containerd's images, so a reboot rejoins the same
	// cluster.
	ClusterState *StorageRole `json:"clusterState,omitempty"`

	// PodStorage is the durable storage that pods claim through
	// PersistentVolumeClaims. k3s's local-path provisioner serves
	// volumes from this role's filesystem.
	PodStorage *StorageRole `json:"podStorage,omitempty"`

	// PodEphemeral is the kubelet's working space: emptyDir volumes and
	// the scratch space that ephemeral-storage requests and limits count.
	// Pod logs are here too, bind-mounted onto /var/log/pods, because a
	// log grows for as long as its pod runs and would fill the small
	// tmpfs that takes the root filesystem's writes.
	PodEphemeral *StorageRole `json:"podEphemeral,omitempty"`
}

// StorageRole places one role on a disk. A role that the spec omits is
// not an error, and its directory stays on the RAM root.
type StorageRole struct {
	// Device is the disk that holds this role, as a kernel name or a
	// /dev/disk/by-id/ or /dev/disk/by-path/ name. init reads it only
	// when it claims a blank disk.
	Device string `json:"device"`

	// Size is the exact size of this role's partition, as a binary
	// quantity ("2Gi"). When it is omitted, the role takes the rest of
	// its disk, and only one role per disk may omit it.
	Size string `json:"size,omitempty"`
}

// DeclaredRole is a role that the spec declares, with its name.
type DeclaredRole struct {
	Name StorageRoleName
	StorageRole
}

// PartitionName returns the GPT partition name that init writes when
// it claims the role's disk and finds the partition by on later boots.
func (r DeclaredRole) PartitionName() string {
	return PartitionPrefix + string(r.Name)
}

// systemSlotsDir holds the slot mountpoints, system/a and system/b.
// init mounts the slots there, and the operator writes downloaded
// releases there through a hostPath mount, so both read this constant.
const systemSlotsDir = "/var/lib/liken/system"

// SystemSlotDir returns the mountpoint of slot "A" or "B". People see
// the uppercase letter, in boot entries, conditions, and the
// liken.slot= kernel parameter, and the directory uses the lowercase
// letter.
func SystemSlotDir(slot string) string {
	return systemSlotsDir + "/" + strings.ToLower(slot)
}

// ProvenESPLink is a symlink to the mountpoint of the proven slot,
// whose ESP a UEFI machine's next boot reads. init points it at that
// slot at each boot and each promotion. A pod that stages a firmware
// update for the next boot, such as firmware-operator's, mounts this
// path as a hostPath volume, and the bind mount resolves the link when
// the pod starts. A machine with no proven slot has no link.
const ProvenESPLink = "/run/liken/proven-esp"

// InactiveSlot returns the slot that the machine is not running from,
// where a downloaded release is written. It returns "" when the machine
// did not boot from a slot, because that machine has no slot to write
// a release to.
func InactiveSlot(running string) string {
	switch running {
	case "A":
		return "B"
	case "B":
		return "A"
	}
	return ""
}

// Role returns the declaration of the named role. It returns nil for
// a name that is not a role and for a role that the spec omits.
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

// Roles returns the declared roles in the order of StorageRoleNames.
func (s StorageSpec) Roles() []DeclaredRole {
	var roles []DeclaredRole
	for _, name := range StorageRoleNames {
		if role := s.Role(name); role != nil {
			roles = append(roles, DeclaredRole{name, *role})
		}
	}
	return roles
}

// Validate checks the spec on its own, without the disks, so a person
// can fix a mistake in the manifest before init writes to any disk.
func (s StorageSpec) Validate() error {
	remainders := map[string]StorageRoleName{}
	for _, role := range s.Roles() {
		if role.Device == "" {
			return fmt.Errorf("storage role %s has no device", role.Name)
		}
		if suffix, ok := strings.CutPrefix(role.Device, "/dev/disk/"); ok {
			tree, name, _ := strings.Cut(suffix, "/")
			if tree == "by-uuid" {
				return fmt.Errorf(
					"storage role %s: device %s is a by-uuid name, but a role claims a whole blank disk, and a blank disk has no filesystem to have a UUID. Use a by-id or by-path name",
					role.Name, role.Device)
			}
			if tree != "by-id" && tree != "by-path" {
				return fmt.Errorf(
					"storage role %s: device %s is in /dev/disk/%s, and a device must be a /dev name or be in /dev/disk/by-id or /dev/disk/by-path",
					role.Name, role.Device, tree)
			}
			if name == "" {
				return fmt.Errorf(
					"storage role %s: device %s is the /dev/disk/%s directory, not a disk in it",
					role.Name, role.Device, tree)
			}
		}
		if role.Size == "" {
			if other, ok := remainders[role.Device]; ok {
				return fmt.Errorf(
					"storage roles %s and %s both omit size on %s, and only one role per disk may omit it",
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

// ParseSize converts a binary quantity ("2Gi", "512Mi") or a plain
// count of bytes into bytes. It refuses decimal suffixes such as "2G",
// because "2G" is about 7% smaller than "2Gi" and the partition math
// uses binary units.
func ParseSize(s string) (uint64, error) {
	// The loop takes the first suffix that matches, so a suffix that
	// ends with another suffix in this list must come before it.
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
		return 0, fmt.Errorf("size %q is not a count of bytes or a number with the suffix Ki, Mi, Gi, or Ti", s)
	}
	// A zero-byte role is always a mistake in the manifest, and this
	// error is clearer than the one the partition math would give.
	if n == 0 {
		return 0, fmt.Errorf("size %q is zero bytes, and a storage role needs at least 1 byte", s)
	}
	return n * unit, nil
}

// SizeText converts a byte count into the largest binary unit that
// divides it exactly, or plain bytes when none does. ParseSize reads
// back every nonzero result, so a person can paste a size that liken
// prints into a spec.
func SizeText(bytes uint64) string {
	switch {
	case bytes%(1<<40) == 0:
		return fmt.Sprintf("%dTi", bytes>>40)
	case bytes%(1<<30) == 0:
		return fmt.Sprintf("%dGi", bytes>>30)
	case bytes%(1<<20) == 0:
		return fmt.Sprintf("%dMi", bytes>>20)
	case bytes%(1<<10) == 0:
		return fmt.Sprintf("%dKi", bytes>>10)
	}
	return fmt.Sprintf("%d", bytes)
}
