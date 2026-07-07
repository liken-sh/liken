package main

// Actuating spec.storage.
//
// The contract (machine/storage.go documents the API side): every
// boot, each declared role is *recognized* (found by the GPT
// partition name written when it was claimed) or, exactly once,
// *claimed*: a blank disk gets a partition table, fresh filesystems,
// and the roles' names written into it. Two rules make this safe to
// run on every machine, every boot:
//
//   - Reconciling never destroys data. Only a disk with no partition
//     table and no filesystem, nothing we or anyone else ever wrote,
//     may be claimed. Anything unrecognized is refused, with the
//     reason printed to the console.
//
//   - An unsatisfiable role stops the boot. If a declared role can't
//     be recognized or claimed, the machine powers off rather than
//     start k3s with that state in RAM. A machine that's down is
//     recoverable; a cluster that silently wrote its state to memory
//     is not. (Roles *absent* from the spec carry no such obligation:
//     those directories simply stay on the RAM root.)

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"golang.org/x/sys/unix"

	"github.com/chrisguidry/liken/machine"
)

// Where each role lands in the filesystem: liken's private
// translation, deliberately absent from the Machine API.
//
//	systemA/systemB  /var/lib/liken/system/{a,b}: the OS's own boot
//	                 slots, FAT32 because the firmware reads them;
//	                 no suid, no devices, no executables — nothing
//	                 runs *from* a slot, the firmware only loads it
//	machineState     /var/lib/liken/machine: the machine's own durable
//	                 data, chiefly the staged and proven manifests
//	machineEphemeral /tmp, the OS's own scratch; nosuid/nodev and
//	                 world-writable-with-sticky-bit, per long Unix
//	                 tradition
//	clusterState     /var/lib/rancher: all of k3s's state, its
//	                 database, its TLS material, containerd's images
//	podStorage       the local-path provisioner's root; the path also
//	                 appears in the image's k3s config.yaml
//	podEphemeral     kubelet's root directory: emptyDirs, pod scratch
type roleMount struct {
	path   string
	flags  uintptr
	mode   os.FileMode // applied to the mounted root; 0 leaves the default
	fstype string      // "" means ext4, the default for data roles
}

const slotMountFlags = unix.MS_NOSUID | unix.MS_NODEV | unix.MS_NOEXEC

var roleMounts = map[machine.StorageRoleName]roleMount{
	machine.SystemARole:          {path: machine.SystemSlotDir("A"), flags: slotMountFlags, fstype: "vfat"},
	machine.SystemBRole:          {path: machine.SystemSlotDir("B"), flags: slotMountFlags, fstype: "vfat"},
	machine.MachineStateRole:     {path: machine.MachineStateDir},
	machine.MachineEphemeralRole: {path: "/tmp", flags: unix.MS_NOSUID | unix.MS_NODEV, mode: 0o1777},
	machine.ClusterStateRole:     {path: "/var/lib/rancher"},
	machine.PodStorageRole:       {path: "/var/lib/liken/pod-storage"},
	machine.PodEphemeralRole:     {path: "/var/lib/kubelet"},
}

// partitionTypeFor picks a role's GPT partition type: the system
// slots are EFI system partitions (the type GUID is how firmware
// finds them), everything else is ordinary Linux data.
func partitionTypeFor(name machine.StorageRoleName) [16]byte {
	if name == machine.SystemARole || name == machine.SystemBRole {
		return efiSystemPartition
	}
	return linuxFilesystemData
}

// teardownStorage unmounts whatever reconciliation may have mounted,
// in reverse canonical order, returning the machine to the state a
// fresh reconcile expects, so a different spec can be tried. It works
// from the mount table's answers rather than a status: a reconcile
// that failed halfway may have mounted a role it never got to report
// (unmounting a path that isn't a mountpoint answers EINVAL, which is
// "nothing to do", not a problem). Nothing else is running this early
// in boot, so nothing can hold these mounts busy.
func teardownStorage() {
	// mountRole parks clusterState's filesystem here while seeding; a
	// failure mid-dance leaves it parked.
	_ = unix.Unmount("/.liken-claim", 0)
	for _, name := range slices.Backward(machine.StorageRoleNames) {
		target := roleMounts[name].path
		err := unix.Unmount(target, 0)
		if err == nil {
			fmt.Printf("liken: storage: unmounted %s\n", target)
		} else if !errors.Is(err, unix.EINVAL) && !errors.Is(err, fs.ErrNotExist) {
			fmt.Fprintf(os.Stderr, "liken: storage: unmounting %s: %v\n", target, err)
		}
	}
}

// A partition as sysfs presents it: a subdirectory of its disk's
// /sys/block entry. The kernel parsed the GPT; the name it read is in
// the partition's uevent, which is how recognition works without
// re-reading any partition table ourselves.
type partition struct {
	name      string // the kernel's node name: vda1, nvme0n1p2
	disk      string // the parent disk's node name: vda, nvme0n1
	partName  string // the GPT partition name, "" if the table has none
	sizeBytes uint64
}

func discoverPartitions() []partition {
	var parts []partition
	for _, disk := range discoverBlockDevices() {
		dir := filepath.Join(sysBlock, disk.Name)
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			// A disk's partitions appear as subdirectories named after
			// their device (vda → vda1); the `partition` file inside is
			// what distinguishes them from the disk's other attributes.
			if _, err := os.Stat(filepath.Join(dir, entry.Name(), "partition")); err != nil {
				continue
			}
			p := partition{name: entry.Name(), disk: disk.Name}
			if raw, err := os.ReadFile(filepath.Join(dir, entry.Name(), "size")); err == nil {
				if sectors, err := strconv.ParseUint(strings.TrimSpace(string(raw)), 10, 64); err == nil {
					p.sizeBytes = sectors * sectorSize
				}
			}
			// The uevent file is KEY=value lines; PARTNAME appears
			// only for tables that carry names, which GPT does.
			if raw, err := os.ReadFile(filepath.Join(dir, entry.Name(), "uevent")); err == nil {
				for line := range strings.SplitSeq(strings.TrimSpace(string(raw)), "\n") {
					if v, ok := strings.CutPrefix(line, "PARTNAME="); ok {
						p.partName = v
					}
				}
			}
			parts = append(parts, p)
		}
	}
	return parts
}

// isBlank reports whether a disk carries nothing recognizable: no MBR
// or GPT, no ext4 filesystem written straight to the device. Blank is
// the only condition under which claiming is allowed; a disk
// something else formatted fails one of these checks and is left
// alone.
func isBlank(devPath string) (bool, error) {
	f, err := os.Open(devPath)
	if err != nil {
		return false, err
	}
	defer f.Close()

	// Enough to cover all three signatures: the MBR's 0x55AA at 510,
	// GPT's "EFI PART" at 512, ext4's magic at 1080.
	head := make([]byte, 2048)
	if _, err := f.ReadAt(head, 0); err != nil {
		return false, err
	}
	switch {
	case head[510] == 0x55 && head[511] == 0xAA:
		return false, nil
	case string(head[512:520]) == "EFI PART":
		return false, nil
	case head[1080] == 0x53 && head[1081] == 0xEF:
		return false, nil
	}
	return true, nil
}

// reconcileStorage actuates the storage spec. On success every
// declared role is a filesystem mounted at its role's path (ext4 for
// the data roles, FAT32 for the system slots the firmware reads), and
// the returned status records where each role landed: the same facts
// printed to the console, bound for the Machine's status, because
// anything reported only to the serial port is invisible to anyone
// operating the machine remotely. An error means a declared role
// can't be satisfied, and the caller (main, the only place with the
// authority) stops the boot rather than let k3s start with that
// state in RAM.
//
// The shape is plan-everything-then-apply: every claim's layout and
// every growth's table edit is computed before the first byte is
// written to any disk. A spec that will fail should fail before it
// changes the world, because the boot may go on to try a different
// spec (the proven manifest, after a staged one is rejected), and
// partitions half-created under the failed spec would break that
// attempt too. What remains unavoidable is a genuine mid-write I/O
// failure: a disk claimed by a failed attempt stays claimed, and if
// that leaves two partitions carrying one role's name, recognition
// refuses to guess and the boot stops.
func reconcileStorage(spec machine.StorageSpec) (machine.StorageStatus, error) {
	status := machine.AllRolesInMemory()
	roles := spec.Roles()
	if len(roles) == 0 {
		return status, nil
	}
	if err := spec.Validate(); err != nil {
		return status, err
	}

	// Recognition: each declared role, found by the name written on
	// its partition. The device in the spec is not consulted; a disk
	// that moved controllers since it was claimed is still the same
	// disk.
	found, err := recognizeRoles(roles)
	if err != nil {
		return status, err
	}

	// Plan the claims: any role still missing must point at a blank
	// disk. Group by device, since roles sharing a disk are claimed
	// together in one partition table.
	var claims []claimPlan
	planned := map[string]bool{}
	for _, role := range roles {
		if _, ok := found[role.Name]; ok || planned[role.Device] {
			continue
		}
		plan, err := planClaim(role.Device, roles, found)
		if err != nil {
			return status, err
		}
		claims = append(claims, plan)
		planned[role.Device] = true
	}

	// Plan the growth: recognized partitions may be smaller than the
	// spec now declares, or their disks may have grown underneath
	// them. grow.go explains the rules.
	grows, err := planAllGrowth(roles, found)
	if err != nil {
		return status, err
	}

	// Apply. Every plan already validated; from here failures are
	// real I/O trouble.
	for _, plan := range claims {
		if err := applyClaim(plan); err != nil {
			return status, err
		}
	}
	for _, plan := range grows {
		if err := applyGrowth(plan); err != nil {
			return status, err
		}
	}
	if len(claims) > 0 || len(grows) > 0 {
		if found, err = recognizeRoles(roles); err != nil {
			return status, err
		}
		for _, role := range roles {
			if _, ok := found[role.Name]; !ok {
				return status, fmt.Errorf("role %s: partition %s did not appear after claiming %s",
					role.Name, role.PartitionName(), role.Device)
			}
		}
	}

	for _, role := range roles {
		p := found[role.Name]
		if err := mountRole(role, p); err != nil {
			return status, err
		}
		*status.Role(role.Name) = machine.StorageRoleStatus{
			Backing:       machine.BackingPartition,
			Device:        p.name,
			Partition:     p.partName,
			CapacityBytes: p.sizeBytes,
		}
	}
	return status, nil
}

// recognizeRoles matches the declared roles against the machine's
// partitions as sysfs reports them.
func recognizeRoles(roles []machine.DeclaredRole) (map[machine.StorageRoleName]partition, error) {
	return matchRoles(roles, discoverPartitions())
}

// matchRoles matches declared roles to partitions by name. Two
// partitions carrying the same role name is unresolvable ambiguity (a
// transplanted or cloned disk), and guessing wrong about which one
// holds the real cluster would destroy data, so that's an error
// rather than a choice.
func matchRoles(roles []machine.DeclaredRole, parts []partition) (map[machine.StorageRoleName]partition, error) {
	found := map[machine.StorageRoleName]partition{}
	for _, role := range roles {
		for _, p := range parts {
			if p.partName != role.PartitionName() {
				continue
			}
			if existing, ok := found[role.Name]; ok {
				return nil, fmt.Errorf("two partitions claim to be %s (%s and %s); refusing to guess",
					role.PartitionName(), existing.name, p.name)
			}
			found[role.Name] = p
		}
	}
	return found, nil
}

// A claimPlan is one blank disk's pending partition table: validated
// and laid out up front, written only after every disk's plan
// succeeded.
type claimPlan struct {
	device       string
	totalSectors uint64
	parts        []gptPartition
	roleCount    int
}

// planClaim validates that a disk may be claimed for every declared
// role naming it, and lays out its table: sized roles first at their
// exact sizes, in canonical order, and the remainder role taking
// whatever is left. Nothing is written.
func planClaim(device string, roles []machine.DeclaredRole, found map[machine.StorageRoleName]partition) (claimPlan, error) {
	var mine []machine.DeclaredRole
	for _, role := range roles {
		if role.Device == device {
			// A disk where some roles are recognized but others need
			// claiming is a disk whose table liken wrote and then
			// something changed: not blank, not claimable, and not
			// safe to fix automatically.
			if _, ok := found[role.Name]; ok {
				return claimPlan{}, fmt.Errorf("disk %s already carries %s but is missing other declared roles; refusing to modify it",
					device, role.PartitionName())
			}
			mine = append(mine, role)
		}
	}

	disk := diskByPath(device)
	if disk == nil {
		return claimPlan{}, fmt.Errorf("declared device %s is not attached", device)
	}
	blank, err := isBlank(device)
	if err != nil {
		return claimPlan{}, fmt.Errorf("examining %s: %w", device, err)
	}
	if !blank {
		return claimPlan{}, fmt.Errorf("%s carries a partition table or filesystem liken doesn't recognize; refusing to touch it (wipe it yourself if it's expendable)", device)
	}

	totalSectors := disk.SizeBytes / sectorSize
	parts, err := planPartitions(device, mine, totalSectors)
	if err != nil {
		return claimPlan{}, err
	}
	return claimPlan{device: device, totalSectors: totalSectors, parts: parts, roleCount: len(mine)}, nil
}

// applyClaim writes one planned table and waits for the kernel to
// surface its partitions.
func applyClaim(plan claimPlan) error {
	fmt.Printf("liken: storage: claiming %s (%s) for %d role(s)\n",
		plan.device, gib(plan.totalSectors*sectorSize), plan.roleCount)
	if err := writeGPT(plan.device, plan.totalSectors, plan.parts); err != nil {
		return fmt.Errorf("partitioning %s: %w", plan.device, err)
	}
	return waitForPartitions(plan.parts, 5*time.Second)
}

// planPartitions lays out a claimed disk's table: sized roles pack
// from the front in canonical order, each start aligned to 1MiB, and
// the (single, validated) sizeless role takes the rest of the disk.
// The device name appears only in the errors; the math cares about
// sectors alone.
func planPartitions(device string, mine []machine.DeclaredRole, totalSectors uint64) ([]gptPartition, error) {
	lastUsable := gptLastUsableLBA(totalSectors)
	var parts []gptPartition
	next := uint64(partitionAlignment)
	var remainder *machine.DeclaredRole
	for _, role := range mine {
		if role.Size == "" {
			remainder = &role
			continue
		}
		bytes, _ := machine.ParseSize(role.Size) // validated before any disk is touched
		sectors := (bytes + sectorSize - 1) / sectorSize
		p := gptPartition{name: role.PartitionName(), firstLBA: next, lastLBA: next + sectors - 1,
			typeGUID: partitionTypeFor(role.Name)}
		if p.lastLBA > lastUsable {
			return nil, fmt.Errorf("disk %s is too small: %s wants %s at sector %d but the disk's usable space ends at %d",
				device, role.Name, role.Size, p.firstLBA, lastUsable)
		}
		parts = append(parts, p)
		next = alignLBA(p.lastLBA + 1)
	}
	if remainder != nil {
		if next > lastUsable {
			return nil, fmt.Errorf("disk %s is too small: nothing left for %s", device, remainder.Name)
		}
		parts = append(parts, gptPartition{name: remainder.PartitionName(), firstLBA: next, lastLBA: lastUsable,
			typeGUID: partitionTypeFor(remainder.Name)})
	}
	return parts, nil
}

// waitForPartitions gives the kernel a moment to surface the devices
// for a table just written: BLKRRPART is synchronous but the devtmpfs
// nodes and sysfs entries appear slightly later. Each partition must
// appear at its planned size, which is what makes this wait serve
// growth as well as claiming: an old geometry still showing is as
// wrong as no partition at all. Running out of patience is an error
// that names exactly what never showed.
func waitForPartitions(parts []gptPartition, patience time.Duration) error {
	deadline := time.Now().Add(patience)
	for {
		visible := map[string]uint64{}
		for _, p := range discoverPartitions() {
			visible[p.partName] = p.sizeBytes
		}
		var missing []string
		for _, want := range parts {
			wantBytes := (want.lastLBA - want.firstLBA + 1) * sectorSize
			got, ok := visible[want.name]
			switch {
			case !ok:
				missing = append(missing, want.name)
			case got != wantBytes:
				missing = append(missing, fmt.Sprintf("%s (still %d bytes, want %d)", want.name, got, wantBytes))
			}
		}
		if len(missing) == 0 {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("partitions never appeared after their table was written: %s",
				strings.Join(missing, ", "))
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func diskByPath(device string) *machine.BlockDevice {
	for _, d := range discoverBlockDevices() {
		if devicePath(d) == device {
			return &d
		}
	}
	return nil
}

// mountRole puts a role's filesystem where its purpose says it goes,
// making the filesystem first if the partition is fresh from a claim.
// (Recognizing our own name on a partition with no filesystem also
// covers a boot that died between partitioning and mkfs; claiming is
// resumable because the name goes on first.)
func mountRole(role machine.DeclaredRole, p partition) error {
	// The role's translation to a mount is looked up before anything
	// touches the partition: a role liken doesn't know how to mount
	// must be refused before mke2fs writes a filesystem onto it.
	rm, ok := roleMounts[role.Name]
	if !ok {
		return fmt.Errorf("role %s has no mount translation; liken and its manifest disagree about the role vocabulary", role.Name)
	}
	target := rm.path

	// Two filesystems, two makers: the system slots get FAT32 from
	// our own formatter (fat32.go — the firmware reads them, and FAT
	// is all it reads), everything else gets ext4 from the vendored
	// static mke2fs. Recognizing our own name on a partition with no
	// filesystem covers a boot that died between partitioning and
	// mkfs either way.
	dev := devRoot + "/" + p.name
	if rm.fstype == "vfat" {
		if !hasFAT32(dev) {
			fmt.Printf("liken: storage: making a FAT32 filesystem on %s for %s\n", dev, role.Name)
			if err := formatSlot(dev, p.sizeBytes, role.Name); err != nil {
				return fmt.Errorf("formatting %s for %s: %w", dev, role.Name, err)
			}
		}
	} else if !hasExt4(dev) {
		fmt.Printf("liken: storage: making an ext4 filesystem on %s for %s\n", dev, role.Name)
		if !runNarrated("mke2fs | ", "/sbin/mke2fs", "-t", "ext4", dev) {
			return fmt.Errorf("mke2fs on %s for %s failed", dev, role.Name)
		}
	}

	// clusterState is special: the image bakes its seed files (the
	// pre-generated CAs, the operator's manifests and container
	// image) underneath this very mountpoint, and mounting over them
	// would hide them all. So the filesystem is first mounted to the
	// side, seeded from the image's copies, and only then moved into place;
	// MS_MOVE re-attaches a live mount atomically.
	if role.Name == "clusterState" {
		staging := "/.liken-claim"
		if err := os.MkdirAll(staging, 0o755); err != nil {
			return fmt.Errorf("mkdir %s: %w", staging, err)
		}
		if err := unix.Mount(dev, staging, "ext4", 0, ""); err != nil {
			return fmt.Errorf("mounting %s for %s: %w", dev, role.Name, err)
		}
		if err := seedClusterState(staging); err != nil {
			return fmt.Errorf("seeding %s: %w", role.Name, err)
		}
		if err := os.MkdirAll(target, 0o755); err != nil {
			return fmt.Errorf("mkdir %s: %w", target, err)
		}
		if err := unix.Mount(staging, target, "", unix.MS_MOVE, ""); err != nil {
			return fmt.Errorf("moving %s into place at %s: %w", role.Name, target, err)
		}
		_ = os.Remove(staging)
	} else {
		fstype := rm.fstype
		if fstype == "" {
			fstype = "ext4"
		}
		if err := os.MkdirAll(target, 0o755); err != nil {
			return fmt.Errorf("mkdir %s: %w", target, err)
		}
		if err := unix.Mount(dev, target, fstype, rm.flags, ""); err != nil {
			return fmt.Errorf("mounting %s for %s: %w", dev, role.Name, err)
		}
	}
	// A partition grown this boot still carries a filesystem sized for
	// the old extent; now that it's mounted, ext4 can be grown online
	// to fill it (ext4.go explains why mounted is the easy case). FAT
	// has no such move, which is fine: slots are fixed-size by design,
	// and planAllGrowth refuses to grow them at the planning stage.
	if rm.fstype == "" {
		if err := maybeGrowFilesystem(role, p, target); err != nil {
			return err
		}
	}

	// A freshly-made ext4 root is 0755 root-only; roles like /tmp need
	// their conventional permissions applied to the mounted root.
	if rm.mode != 0 {
		if err := os.Chmod(target, rm.mode); err != nil {
			fmt.Fprintf(os.Stderr, "liken: storage: chmod %s: %v\n", target, err)
		}
	}
	fmt.Printf("liken: storage: %s is %s (%s) on %s\n",
		role.Name, dev, p.partName, target)
	return nil
}

// seedClusterState copies the image's seed files into a clusterState
// filesystem. The TLS material is copied only if the disk has none:
// those keys are the cluster's identity, and a disk that already has
// an identity keeps it. The manifests and the operator image are
// refreshed every boot: they're pinned to the liken version of the
// running image, and an upgraded image must deliver its upgraded
// operator.
func seedClusterState(root string) error {
	for _, seed := range []struct {
		rel     string
		refresh bool
	}{
		{"k3s/server/tls", false},
		{"k3s/server/manifests", true},
		{"k3s/agent/images", true},
	} {
		src := filepath.Join("/var/lib/rancher", seed.rel)
		if _, err := os.Stat(src); err != nil {
			continue // an image without k3s has no seed files
		}
		dst := filepath.Join(root, seed.rel)
		if seed.refresh {
			if err := os.RemoveAll(dst); err != nil {
				return err
			}
		} else if _, err := os.Stat(dst); err == nil {
			continue
		}
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}
		if err := os.CopyFS(dst, os.DirFS(src)); err != nil {
			return err
		}
	}
	return nil
}
