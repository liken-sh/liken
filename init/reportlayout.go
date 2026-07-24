package main

// Fitting liken's storage roles onto the disks the report measured.
//
// A proposal that names sizes the disks cannot hold is worse than no
// proposal: it reads as a manifest a person can install from, and the
// install refuses it at the first disk it claims. The installer lays
// each disk out with exact allocations (claim.go), so every byte a
// role asks for must exist on the disk that role names. This file is
// the arithmetic that keeps that promise. It takes the disks the
// report measured and returns the roles those disks can carry, at
// sizes those disks can hold.
//
// The planner is pure. It reads no sysfs and touches no disk, so the
// tests drive it with disks made up by hand, and check the result
// against the same partition math the install runs.

import (
	"fmt"

	"github.com/liken-sh/liken/machine"
)

// These are the sizes the layout starts from. The boot and system
// roles are fixed, because their contents are fixed: a slot holds one
// whole OS image, and the GRUB roles hold structures whose sizes the
// firmware and GRUB decide.
//
// The three data roles are not equal, and the difference decides which
// one gives up space on a small disk. clusterState is the operating
// system's own working set: containerd unpacks every image the node
// runs underneath it. podStorage and podEphemeral are the workloads'
// space, and a workload that runs out of it fails one workload.
//
// Every size here is a whole number of mebibytes, which is also the
// alignment the installer gives each partition, so a role never
// occupies more of a disk than its size says.
const (
	biosBootBytes         = 1 << 20
	bootHomeBytes         = 64 << 20
	systemSlotBytes       = 1 << 30
	machineStateBytes     = 64 << 20
	machineEphemeralBytes = 512 << 20

	// clusterStateBytes is what clusterState asks for when a disk can
	// hold it. The role mounts /var/lib/rancher, which holds k3s's
	// database, its TLS material, and containerd's image store, so its
	// size follows what the node runs and not what a person prefers.
	// The images add up faster than the number suggests: liken's own
	// bundled images, k3s's packaged ones (coredns, traefik,
	// metrics-server, the local-path provisioner), and every workload
	// image the operator deploys, and an upgrade holds the old set and
	// the new set at the same time. 6Gi is what liken's own public node
	// runs on, which is the one place a person has had to choose this
	// number against real work.
	clusterStateBytes = 6 << 30

	// clusterStateFloorBytes is the smallest clusterState this layout
	// will name. The lab's hardware-parity guest converges with the
	// whole control-plane pod set in this much, so it is the smallest
	// size with evidence behind it. Under the floor the layout names no
	// clusterState at all, for the reason belowFloorNote gives.
	clusterStateFloorBytes = 2 << 30

	// podStorageBytes is what podStorage asks for. This role is the
	// local-path provisioner's pool: the space pods claim by name. It
	// is the operator's to size against their own workloads, so it is
	// also the first space the layout takes back when a disk is small.
	podStorageBytes = 4 << 30

	// dataRoleFloor is the smallest podStorage or podEphemeral may be
	// before the layout leaves the role out. A role below this size
	// holds too little to be worth the space it takes from the roles
	// beside it, and a role that is absent from the spec is not an
	// error: its directory stays on the machine's RAM root.
	dataRoleFloor = 256 << 20

	// dataShareUnit rounds a scaled data role down to a size a person
	// reads without effort. An exact remainder of a disk is a number
	// nobody wants to see in a manifest.
	dataShareUnit = 64 << 20

	// gptOverhead is what the partition table itself costs. The
	// primary header and its entry array sit in the first sectors, and
	// a mirror of both sits in the last 33; the installer then starts
	// the first partition on the 1MiB boundary that every partitioner
	// aligns to. One mebibyte at each end covers both ends with room
	// to spare.
	gptOverhead = 2 << 20
)

// plannedRole is one role placed on one disk at one size. An empty
// Size means the role takes the rest of its disk, which the spec
// allows for one role per disk. Comment is the reason a person reads
// beside the number.
type plannedRole struct {
	Name    machine.StorageRoleName
	Device  string
	Size    string
	Comment string
}

// storageLayout is the plan for a whole machine: the roles, in the
// canonical order the installer lays them down, and the notes that
// explain what the plan could not do. A layout with no roles is a
// real answer: it means no disk this report saw can carry a liken
// install, and the notes say what would.
type storageLayout struct {
	Roles []plannedRole
	Notes []string
}

// planStorageLayout fits the roles onto the disks. It puts the
// machine's own roles on one disk and the cluster's data on another
// when the machine has one to spare, so the cluster's state survives a
// reinstall that replaces the system disk. It never plans a role onto
// a disk that might be the installation stick, and it prefers a disk
// the install can reach over one that only this boot can see.
func planStorageLayout(measured []reportDisk, uefi bool) storageLayout {
	candidates := placeableDisks(measured)
	if len(candidates) == 0 {
		return storageLayout{Notes: []string{
			"This report saw no disk that can carry a storage role."}}
	}

	system, ok := pickSystemDisk(candidates, uefi)
	if !ok {
		grubRoles := ""
		if !uefi {
			grubRoles = fmt.Sprintf(", %s for GRUB's core image, and %s for GRUB's config",
				sizeText(biosBootBytes), sizeText(bootHomeBytes))
		}
		return storageLayout{Notes: []string{fmt.Sprintf(
			"No disk here can hold liken's own roles. They need %s on one disk: two %s system slots, %s of machine state, %s of /tmp%s. The largest disk this report saw offers %s. Attach a larger disk, then run this report again.",
			gib(systemRoleBytes(uefi)), sizeText(systemSlotBytes), sizeText(machineStateBytes),
			sizeText(machineEphemeralBytes), grubRoles, gib(largestDisk(candidates).SizeBytes))}}
	}

	layout := storageLayout{Roles: systemRoles(system.Path, uefi)}
	data, available := pickDataDisk(candidates, system, uefi)
	if data.Path == system.Path {
		layout.Notes = append(layout.Notes, fmt.Sprintf(
			"Every role lives on %s. No other disk here gives the cluster's data more room than this disk has left over. A reinstall replaces the system slots and the data roles together.", system.Path))
	} else {
		layout.Notes = append(layout.Notes, fmt.Sprintf(
			"The durable roles live on %s, so the cluster's state and its volumes survive a reinstall that replaces the system disk.", data.Path))
	}

	roles, notes := dataRoles(data.Path, available)
	layout.Roles = append(layout.Roles, roles...)
	layout.Notes = append(layout.Notes, notes...)
	return layout
}

// placeableDisks is the disks a role may land on, best first. A disk
// that might be the installation stick is out entirely: the stick
// leaves the machine with the person, and a role on it would vanish
// with them. A disk that appeared only after this boot loaded a driver
// comes last, because an install claims its disks before it loads any
// module a manifest declares, so it never sees that disk at all.
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

// usableBytes is what one disk offers to roles: its size, less what
// the partition table takes at each end.
func usableBytes(d reportDisk) uint64 {
	if d.SizeBytes <= gptOverhead {
		return 0
	}
	return d.SizeBytes - gptOverhead
}

// systemRoleBytes is the space the machine's own roles need: the two
// system slots, the machine's state, its /tmp, and, on a BIOS machine,
// the two roles that UEFI firmware would otherwise supply.
func systemRoleBytes(uefi bool) uint64 {
	total := uint64(2*systemSlotBytes + machineStateBytes + machineEphemeralBytes + gptOverhead)
	if !uefi {
		total += biosBootBytes + bootHomeBytes
	}
	return total
}

// pickSystemDisk chooses the disk that boots the machine: the first
// placeable disk with room for the machine's own roles. Order matters
// more than size here. The disks come in the kernel's enumeration
// order, and the first disk is the one a person points at when they
// say "the system disk".
func pickSystemDisk(candidates []reportDisk, uefi bool) (reportDisk, bool) {
	for _, d := range candidates {
		if usableBytes(d)+gptOverhead >= systemRoleBytes(uefi) {
			return d, true
		}
	}
	return reportDisk{}, false
}

// pickDataDisk chooses where the cluster's data lives, and says how
// much room it has there. A second disk is the better home, because it
// survives a reinstall of the system disk, but only when it holds more
// than the system disk has left over. A 64Mi flash card is a second
// disk and not a home for a cluster's state.
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

// systemRoles places the machine's own roles, at their fixed sizes.
func systemRoles(device string, uefi bool) []plannedRole {
	var roles []plannedRole
	if !uefi {
		roles = append(roles,
			plannedRole{machine.BIOSBootRole, device, sizeText(biosBootBytes), "# GRUB core image; a tiny raw partition"},
			plannedRole{machine.BootHomeRole, device, sizeText(bootHomeBytes), "# GRUB config and environment block"})
	}
	return append(roles,
		plannedRole{machine.SystemARole, device, sizeText(systemSlotBytes), "# one OS slot; the blue-green pair"},
		plannedRole{machine.SystemBRole, device, sizeText(systemSlotBytes), ""},
		plannedRole{machine.MachineStateRole, device, sizeText(machineStateBytes), "# staged and proven manifests"},
		plannedRole{machine.MachineEphemeralRole, device, sizeText(machineEphemeralBytes), "# the OS's /tmp"})
}

// roleNote is the sentence every proposal that plans the durable roles
// carries. It teaches the one distinction a person needs before they
// edit a size: which number is theirs to choose, and which number the
// machine chooses for them.
const roleNote = "clusterState holds k3s's database, its TLS material, and containerd's image store, so this node's images decide its size. Raise it if this node runs many images or large ones. podStorage is the local-path provisioner's pool, which is yours to size for the volumes your workloads claim."

// dataRoles fits the cluster's roles into the space that is left, in
// the order that keeps the node able to run at all.
//
// The order follows from what each role holds. A node with too little
// podStorage refuses one workload's volume claim. A node with too
// little clusterState cannot unpack the images it is told to run, so
// it fails when an operator deploys anything, weeks after the install
// that sized it. So podStorage shrinks and then goes, then podEphemeral
// falls toward its floor, and clusterState gives up space last and
// never below the floor a node is known to converge in. A role that
// does not fit is left out rather
// than shrunk to nothing, and every departure from the conventional
// layout gets a note that says what it costs.
func dataRoles(device string, available uint64) ([]plannedRole, []string) {
	cluster := plannedRole{machine.ClusterStateRole, device, "", "# k3s's database, TLS material, and containerd's images"}
	pods := plannedRole{machine.PodStorageRole, device, "", "# size to your workloads' volumes"}
	ephemeral := plannedRole{machine.PodEphemeralRole, device, "", "# takes the rest of this disk"}

	if available >= clusterStateBytes+podStorageBytes+dataRoleFloor {
		cluster.Size, pods.Size = sizeText(clusterStateBytes), sizeText(podStorageBytes)
		return []plannedRole{cluster, pods, ephemeral}, []string{roleNote}
	}
	if share := spareAfter(available, clusterStateBytes+dataRoleFloor); share >= dataRoleFloor {
		cluster.Size, pods.Size = sizeText(clusterStateBytes), sizeText(share)
		return []plannedRole{cluster, pods, ephemeral}, []string{roleNote, fmt.Sprintf(
			"%s cannot hold the conventional %s of podStorage beside clusterState, so podStorage takes %s. clusterState keeps its %s, because a node that cannot unpack an image cannot run the pod that wants it.",
			device, sizeText(podStorageBytes), sizeText(share), sizeText(clusterStateBytes))}
	}
	if available >= clusterStateBytes+dataRoleFloor {
		cluster.Size = sizeText(clusterStateBytes)
		return []plannedRole{cluster, ephemeral}, []string{roleNote, fmt.Sprintf(
			"%s has room for clusterState and kubelet's scratch space only, so podStorage is left out. A pod that claims a volume gets one from the machine's RAM root, and loses it at the next reboot. clusterState keeps its %s, because the images this node runs live there.",
			device, sizeText(clusterStateBytes))}
	}
	if share := spareAfter(available, dataRoleFloor); share >= clusterStateFloorBytes {
		cluster.Size = sizeText(share)
		return []plannedRole{cluster, ephemeral}, []string{roleNote, fmt.Sprintf(
			"%s cannot hold a %s clusterState, so podStorage is left out and clusterState takes %s. That is enough for the images liken and k3s bring, and little more: a pull of a large image can fail with no space left, and an upgrade that holds the old images and the new ones at the same time may not fit. Attach a larger disk before this machine runs many images.",
			device, sizeText(clusterStateBytes), sizeText(share))}
	}
	if available >= dataRoleFloor {
		return []plannedRole{ephemeral}, []string{fmt.Sprintf(
			"%s offers %s to the cluster's roles, and a liken node's image store needs %s at the least, so this proposal declares no clusterState and no podStorage. A size under that floor installs without complaint and then fails on an image the node pulls weeks later, which is worse than no role at all. kubelet's scratch space takes this disk. The cluster's state and its volumes stay on the machine's RAM root, so the node imports every image again after every reboot. Attach a larger disk for this machine's durable roles.",
			device, gib(available), gib(clusterStateFloorBytes))}
	}
	return nil, []string{fmt.Sprintf(
		"%s has no room for any data role. The cluster's state, its volumes, and kubelet's scratch space all stay on the machine's RAM root. Attach a larger disk before this machine runs real workloads.", device)}
}

// spareAfter is what a disk has left once a reservation is taken out
// of it, rounded down so the number reads well in a manifest. The
// arithmetic is unsigned, so a reservation larger than the disk has to
// return zero rather than wrap to an enormous size.
func spareAfter(available, reserved uint64) uint64 {
	if available <= reserved {
		return 0
	}
	return (available - reserved) / dataShareUnit * dataShareUnit
}

// largestDisk is the biggest of a set, for the message that says what
// the machine offers against what liken needs.
func largestDisk(disks []reportDisk) reportDisk {
	largest := reportDisk{}
	for _, d := range disks {
		if d.SizeBytes > largest.SizeBytes {
			largest = d
		}
	}
	return largest
}

// sizeText renders a byte count as the manifest's own quantity: the
// largest binary unit that divides it exactly. The spec accepts only
// the power-of-two suffixes, so "1Gi" here means the same 2^30 bytes
// that the partition math allocates.
func sizeText(bytes uint64) string {
	switch {
	case bytes%(1<<30) == 0:
		return fmt.Sprintf("%dGi", bytes>>30)
	case bytes%(1<<20) == 0:
		return fmt.Sprintf("%dMi", bytes>>20)
	case bytes%(1<<10) == 0:
		return fmt.Sprintf("%dKi", bytes>>10)
	}
	return fmt.Sprintf("%d", bytes)
}
