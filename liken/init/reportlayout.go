package main

// This file chooses which storage roles to propose for a machine and
// how large to make them, from the disks the report measured. The
// installer lays out each disk with exact partition sizes (claim.go)
// and refuses a manifest whose roles do not fit, so a proposal must
// never name a size its disk cannot hold. The planner reads no sysfs
// and writes no disk, so tests pass it hand-made disks and check the
// result with the installer's own partition math.

import (
	"fmt"

	"github.com/liken-sh/liken/liken/machine"
)

// Fixed sizes for the boot and system roles. A system slot holds one
// whole OS image, and the GRUB roles are sized by what GRUB and the
// firmware write into them. dataRoles explains how the data roles
// shrink on a small disk.
//
// Every size is a whole number of mebibytes, which is the alignment
// the installer gives each partition.
const (
	biosBootBytes         = 1 << 20
	bootHomeBytes         = 64 << 20
	systemSlotBytes       = 1 << 30
	machineStateBytes     = 64 << 20
	machineEphemeralBytes = 512 << 20

	// clusterStateBytes is the size proposed for clusterState when the
	// disk can hold it. The role mounts /var/lib/rancher, which holds
	// k3s's database, its TLS material, and containerd's image store.
	// The image store holds liken's bundled images, k3s's packaged
	// ones (coredns, traefik, metrics-server, the local-path
	// provisioner), and every workload image, and an upgrade keeps the
	// old and the new set at once. 6Gi is the clusterState of liken's
	// own public node.
	clusterStateBytes = 6 << 30

	// clusterStateFloorBytes is the smallest clusterState the layout
	// proposes. It holds the images that liken and k3s ship, with little
	// room for more. On a disk that cannot give clusterState this much,
	// the layout proposes no clusterState, and a note says why.
	clusterStateFloorBytes = 2 << 30

	// On a disk with room to spare, clusterState takes
	// 1/clusterStateShare of the disk, up to clusterStateCeilingBytes.
	// The size cannot be corrected later: podStorage follows
	// clusterState on the disk, and a partition can only grow into
	// free space behind it. So extra space is spent here and not on
	// scratch space. Above the ceiling, a person who needs more
	// should choose the size.
	clusterStateShare        = 8
	clusterStateCeilingBytes = 64 << 30

	// podEphemeralCeilingBytes limits the scratch space that
	// podEphemeral takes on a large disk. podEphemeral is the last
	// role, so it takes the rest of the disk, which on a large disk
	// would leave almost nothing for podStorage. Space above this size
	// goes to podStorage. The role also holds the pod logs
	// (podlogs.go).
	podEphemeralCeilingBytes = 64 << 30

	// podStorageBytes is the size proposed for podStorage, the
	// local-path provisioner's pool for the volumes that pods claim.
	// A person sizes it for their own workloads, so the layout shrinks
	// it first on a small disk.
	podStorageBytes = 4 << 30

	// dataRoleFloor is the smallest podStorage or podEphemeral the
	// layout proposes. Below it the layout leaves the role out, and
	// the role's directory stays on the machine's RAM root.
	dataRoleFloor = 256 << 20

	// dataShareUnit is the multiple that a scaled data role rounds
	// down to, so the manifest shows a round number and not an exact
	// remainder of the disk.
	dataShareUnit = 64 << 20

	// gptOverhead is the space the partition table takes. GPT keeps
	// the primary header and entries at the start of the disk and a
	// mirror in the last 33 sectors, and the installer starts the
	// first partition on a 1MiB boundary. 1MiB at each end covers both.
	gptOverhead = 2 << 20
)

// plannedRole is one role placed on one disk. An empty Size means the
// role takes the rest of the disk, which the spec allows for one role
// per disk. Comment is the YAML comment that the proposal writes beside
// the role.
type plannedRole struct {
	Name    machine.StorageRoleName
	Device  string
	Size    string
	Comment string
}

// storageLayout is the plan for a machine: the roles in the order the
// installer lays them down, and notes that explain the sizes to a
// person. A layout with no roles means that no disk in the report can
// hold a liken install, and the notes say what disk would.
type storageLayout struct {
	Roles []plannedRole
	Notes []string
}

// planStorageLayout fits the roles onto the disks. It puts the
// machine's own roles on one disk and the cluster's data on another
// when a second disk has more room, so the cluster's state survives a
// reinstall that replaces the system disk.
func planStorageLayout(measured []reportDisk, uefi bool) storageLayout {
	candidates := placeableDisks(measured)
	if len(candidates) == 0 {
		return storageLayout{Notes: []string{
			"This report found no disk that can hold a storage role."}}
	}

	system, ok := pickSystemDisk(candidates, uefi)
	if !ok {
		grubRoles := ""
		if !uefi {
			grubRoles = fmt.Sprintf(", %s for GRUB's core image, and %s for GRUB's config",
				machine.SizeText(biosBootBytes), machine.SizeText(bootHomeBytes))
		}
		return storageLayout{Notes: []string{fmt.Sprintf(
			"No disk here can hold liken's own roles. They need %s on one disk: two %s system slots, %s of machine state, %s of /tmp%s. The largest disk this report saw offers %s. Attach a larger disk, then run this report again.",
			gib(systemRoleBytes(uefi)), machine.SizeText(systemSlotBytes), machine.SizeText(machineStateBytes),
			machine.SizeText(machineEphemeralBytes), grubRoles, gib(largestDisk(candidates).SizeBytes))}}
	}

	layout := storageLayout{Roles: systemRoles(system.deviceName(), uefi)}
	data, available := pickDataDisk(candidates, system, uefi)
	if data.Path == system.Path {
		layout.Notes = append(layout.Notes, fmt.Sprintf(
			"Every role is on %s. No other disk here gives the cluster's data more room than this disk has left over. A reinstall replaces the system slots and the data roles together.", system.Path))
	} else {
		layout.Notes = append(layout.Notes, fmt.Sprintf(
			"The durable roles are on %s, so the cluster's state and its volumes survive an install onto a replaced system disk. A wipe and reinstall is the other case: it erases every disk this manifest declares, including this one.", data.Path))
	}

	roles, notes := dataRoles(data.deviceName(), available)
	layout.Roles = append(layout.Roles, roles...)
	layout.Notes = append(layout.Notes, notes...)
	return layout
}

// placeableDisks returns the disks that may hold a role, best first.
// A disk that might be the install stick is left out, because the
// person takes the stick away and the role would go with it. A disk
// that appeared only after this boot loaded a driver comes last,
// because an install claims its disks before it loads the modules of a
// manifest, so the install does not find that disk.
func placeableDisks(measured []reportDisk) []reportDisk {
	var reachable, behind []reportDisk
	for _, d := range measured {
		switch {
		case d.MaybeStick:
			continue
		case len(d.BehindModules) > 0:
			behind = append(behind, d)
		default:
			reachable = append(reachable, d)
		}
	}
	return append(reachable, behind...)
}

func usableBytes(d reportDisk) uint64 {
	if d.SizeBytes <= gptOverhead {
		return 0
	}
	return d.SizeBytes - gptOverhead
}

// systemRoleBytes is the space that the machine's own roles need: two
// system slots, machine state, /tmp, and the partition table. A BIOS
// machine also needs the two GRUB roles that UEFI firmware makes
// unnecessary.
func systemRoleBytes(uefi bool) uint64 {
	total := uint64(2*systemSlotBytes + machineStateBytes + machineEphemeralBytes + gptOverhead)
	if !uefi {
		total += biosBootBytes + bootHomeBytes
	}
	return total
}

// pickSystemDisk returns the first candidate that can hold the
// machine's own roles. It takes the first and not the largest, so that
// a larger second disk stays free for pickDataDisk to give to the
// cluster's data.
func pickSystemDisk(candidates []reportDisk, uefi bool) (reportDisk, bool) {
	for _, d := range candidates {
		if usableBytes(d)+gptOverhead >= systemRoleBytes(uefi) {
			return d, true
		}
	}
	return reportDisk{}, false
}

// pickDataDisk returns the disk for the cluster's data and the space
// available on it. It chooses another disk only when that disk has
// more space than the system disk has left over, so that a small
// second disk, such as a 64Mi flash card, does not take the data from
// a large system disk.
func pickDataDisk(candidates []reportDisk, system reportDisk, uefi bool) (reportDisk, uint64) {
	leftover := usableBytes(system) - (systemRoleBytes(uefi) - gptOverhead)
	best, bestAvailable := system, leftover
	for _, d := range candidates {
		if d.Path == system.Path {
			continue
		}
		if available := usableBytes(d); available > bestAvailable {
			best, bestAvailable = d, available
		}
	}
	return best, bestAvailable
}

// systemRoles places the machine's own roles at their fixed sizes.
func systemRoles(device string, uefi bool) []plannedRole {
	var roles []plannedRole
	if !uefi {
		roles = append(roles,
			plannedRole{machine.BIOSBootRole, device, machine.SizeText(biosBootBytes), "# GRUB core image, on a small raw partition"},
			plannedRole{machine.BootHomeRole, device, machine.SizeText(bootHomeBytes), "# GRUB config and environment block"})
	}
	return append(roles,
		plannedRole{machine.SystemARole, device, machine.SizeText(systemSlotBytes), "# one of the two OS slots"},
		plannedRole{machine.SystemBRole, device, machine.SizeText(systemSlotBytes), ""},
		plannedRole{machine.MachineStateRole, device, machine.SizeText(machineStateBytes), "# staged and proven manifests"},
		plannedRole{machine.MachineEphemeralRole, device, machine.SizeText(machineEphemeralBytes), "# the OS's /tmp"})
}

// roleNote is the note that every proposal with the data roles
// includes. It tells a person which sizes they choose and which sizes
// cannot be changed after the install.
const roleNote = "clusterState holds k3s's database, its TLS material, and containerd's image store, so this node's images decide its size. Raise it if this node runs many images or large ones. This size is permanent: podStorage follows clusterState on the disk, so clusterState cannot grow later. podStorage is the local-path provisioner's pool, which is yours to size for the volumes your workloads claim."

// dataRoles fits the cluster's roles into the available space. When
// space is short, podStorage shrinks and is then left out, podEphemeral
// shrinks toward dataRoleFloor, and clusterState shrinks last and never
// below clusterStateFloorBytes. That order follows what a shortage
// costs. A shortage of podStorage or podEphemeral fails only the
// workload that needs the space. A node short of clusterState cannot
// unpack the images it runs, and fails when someone deploys a
// workload, long after the install that sized it. A role that does not
// fit is left out, and each size that differs from the conventional
// sizes gets a note that says what it costs.
func dataRoles(device string, available uint64) ([]plannedRole, []string) {
	cluster := plannedRole{machine.ClusterStateRole, device, "", "# k3s's database, TLS material, and containerd's images"}
	pods := plannedRole{machine.PodStorageRole, device, "", "# size to your workloads' volumes"}
	ephemeral := plannedRole{machine.PodEphemeralRole, device, "", "# takes the rest of this disk"}

	// On a large disk the conventional sizes would leave almost all of
	// it to podEphemeral, which takes the rest. A 477Gi disk would get
	// 6Gi of images, 4Gi of volumes, and 466Gi of /var/lib/kubelet. So
	// clusterState and podStorage take the space instead.
	if scaled := scaledClusterState(available); scaled > clusterStateBytes {
		left := available - scaled
		scratch := min(uint64(podEphemeralCeilingBytes), left/2)
		cluster.Size = machine.SizeText(scaled)
		pods.Size = machine.SizeText((left - scratch) / (1 << 30) * (1 << 30))
		return []plannedRole{cluster, pods, ephemeral}, []string{roleNote, fmt.Sprintf(
			"%s has room beyond the conventional sizes, so clusterState takes %s and podStorage takes %s. A larger clusterState is worth more here than a larger scratch space, because clusterState cannot grow after the install and podEphemeral takes whatever is left.",
			device, cluster.Size, pods.Size)}
	}
	if available >= clusterStateBytes+podStorageBytes+dataRoleFloor {
		cluster.Size, pods.Size = machine.SizeText(clusterStateBytes), machine.SizeText(podStorageBytes)
		return []plannedRole{cluster, pods, ephemeral}, []string{roleNote}
	}
	if share := spareAfter(available, clusterStateBytes+dataRoleFloor); share >= dataRoleFloor {
		cluster.Size, pods.Size = machine.SizeText(clusterStateBytes), machine.SizeText(share)
		return []plannedRole{cluster, pods, ephemeral}, []string{roleNote, fmt.Sprintf(
			"%s cannot hold the conventional %s of podStorage beside clusterState, so podStorage takes %s. clusterState keeps its %s, because a node that cannot unpack an image cannot run the pod that requires it.",
			device, machine.SizeText(podStorageBytes), machine.SizeText(share), machine.SizeText(clusterStateBytes))}
	}
	if available >= clusterStateBytes+dataRoleFloor {
		cluster.Size = machine.SizeText(clusterStateBytes)
		return []plannedRole{cluster, ephemeral}, []string{roleNote, fmt.Sprintf(
			"%s has room for clusterState and kubelet's scratch space only, so podStorage is left out. A pod that claims a volume gets one from the machine's RAM root, and loses it at the next reboot. clusterState keeps its %s, because the images this node runs are stored there.",
			device, machine.SizeText(clusterStateBytes))}
	}
	if share := spareAfter(available, dataRoleFloor); share >= clusterStateFloorBytes {
		cluster.Size = machine.SizeText(share)
		return []plannedRole{cluster, ephemeral}, []string{roleNote, fmt.Sprintf(
			"%s cannot hold a %s clusterState, so podStorage is left out and clusterState takes %s. That is enough for the images that liken and k3s ship, with little room for more. A pull of a large image can fail with no space left, and an upgrade that keeps the old images and the new ones at the same time can run out of space. Attach a larger disk before this machine runs many images.",
			device, machine.SizeText(clusterStateBytes), machine.SizeText(share))}
	}
	if available >= dataRoleFloor {
		return []plannedRole{ephemeral}, []string{fmt.Sprintf(
			"%s offers %s to the cluster's roles, and a liken node's image store needs %s at the least, so this proposal declares no clusterState and no podStorage. A smaller clusterState installs with no error, and then an image pull fails when it is full. kubelet's scratch space takes this disk. The cluster's state and its volumes stay on the machine's RAM root, so the node imports every image again after every reboot. Attach a larger disk for this machine's durable roles.",
			device, gib(available), gib(clusterStateFloorBytes))}
	}
	return nil, []string{fmt.Sprintf(
		"%s has no room for any data role. The cluster's state, its volumes, and kubelet's scratch space all stay on the machine's RAM root. Attach a larger disk before this machine runs real workloads.", device)}
}

// scaledClusterState returns the clusterState size for a disk with the
// given available space: 1/clusterStateShare of it, rounded down to a
// whole gibibyte, up to clusterStateCeilingBytes.
func scaledClusterState(available uint64) uint64 {
	scaled := available / clusterStateShare / (1 << 30) * (1 << 30)
	return min(scaled, uint64(clusterStateCeilingBytes))
}

// spareAfter returns what is left of available after reserved, rounded
// down to a multiple of dataShareUnit. It returns zero when reserved is
// larger, because the unsigned subtraction would wrap.
func spareAfter(available, reserved uint64) uint64 {
	if available <= reserved {
		return 0
	}
	return (available - reserved) / dataShareUnit * dataShareUnit
}

func largestDisk(disks []reportDisk) reportDisk {
	largest := reportDisk{}
	for _, d := range disks {
		if d.SizeBytes > largest.SizeBytes {
			largest = d
		}
	}
	return largest
}
