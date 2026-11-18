package main

// A reinstall writes a new liken install over a disk that already has
// one.
//
// The installer claims only a blank disk (claim.go), so liken never
// destroys data it did not write. The cost is that a disk that liken
// wrote cannot be reinstalled until something else blanks it, and a
// fresh machine has no shell, no second OS, and no network until its
// install succeeds. A liken.reinstall boot blanks the disks that the
// machine's manifest declares, and then installs as liken.install does.
//
// A person asks for this by choosing the stick's "wipe and reinstall
// as <name>" menu entry, for a manifest that declares these disks.
// Choosing it is the confirmation, because the console has no other
// way to ask.
//
// A declared disk can be a name under /dev/disk/by-id/ or
// /dev/disk/by-path/. The wipe resolves it as the claim does. When a
// name matches two disks, the wipe blanks neither, because a wrong
// guess would erase a disk that the manifest does not declare.
//
// Blanking the partition table does not erase the file systems.
// Partitions start a megabyte into the disk, so a claim that writes
// the same layout puts the old file systems back at the same offsets.
// The rule in storage.go completes the erasure: a partition that this
// boot created always gets a new file system. So nothing of the
// previous install survives on any declared disk.

import (
	"fmt"
	"os"

	"golang.org/x/sys/unix"

	"github.com/liken-sh/liken/liken/machine"
)

const (
	// installParam makes a boot install onto a blank disk.
	installParam = "liken.install"
	// reinstallParam makes a boot reclaim its manifest's disks first,
	// then install.
	reinstallParam = "liken.reinstall"
)

// installing reports whether this boot installs liken onto the
// machine's disks, either onto blank disks (liken.install) or after
// blanking them (liken.reinstall).
func installing() bool {
	return bootParam(installParam) || bootParam(reinstallParam)
}

// wipeRegion is how many bytes blankDisk zeroes at each end of a
// disk. isBlank reads only the first 2 KiB, and a claim rewrites the
// whole GPT, so the front region is enough to reclaim the disk. The
// back region covers the GPT's backup header in the last sectors, so
// no tool later reports a damaged table.
const wipeRegion = 1 << 20

// reclaimManifestDisks blanks every disk that the seed manifest
// declares. It loads the seed with the same liken.machine= name that
// the installer uses, so it reclaims the disks that the install will
// claim. When a disk cannot be reclaimed, this function only logs it.
// The claim that follows then refuses the disk it does not recognize
// and stops the boot with its own message.
func reclaimManifestDisks() {
	seed, err := loadSeed(machine.MachineManifestDir, bootParamValue("liken.machine"))
	if err != nil {
		fmt.Fprintf(os.Stderr, "liken: reinstall: reading the seed manifest for its disks: %v\n", err)
		return
	}

	var devices []string
	seen := map[string]bool{}
	for _, role := range seed.m.Spec.Storage.Roles() {
		if role.Device != "" && !seen[role.Device] {
			seen[role.Device] = true
			devices = append(devices, role.Device)
		}
	}
	if len(devices) == 0 {
		fmt.Fprintln(os.Stderr, "liken: reinstall: the seed manifest declares no disks to reclaim")
		return
	}

	for _, device := range devices {
		node, err := awaitDevice(device)
		if err != nil {
			fmt.Fprintf(os.Stderr, "liken: reinstall: %v\n", err)
			continue
		}
		if err := blankDisk(node); err != nil {
			fmt.Fprintf(os.Stderr, "liken: reinstall: blanking %s: %v\n", device, err)
			continue
		}
		fmt.Printf("liken: reinstall: reclaimed %s\n", device)
	}
}

// awaitDevice waits up to declaredDiskDeadline for a declared disk to
// attach, as an install does (resolve.go), and returns the kernel
// device node that blankDisk opens. The manifest declares the disk, so
// a disk that is still missing at the deadline is an error.
func awaitDevice(declared string) (string, error) {
	disk, err := awaitDeclaredDisk(declared,
		fmt.Sprintf("liken: reinstall: waiting for %s to attach", declared))
	switch {
	case err != nil:
		return "", err
	case disk == nil:
		return "", fmt.Errorf("disk %s did not attach within %s", declared, declaredDiskDeadline)
	}
	return devicePath(*disk), nil
}

// blankDisk zeroes the first and last wipeRegion bytes of the disk at
// the kernel device node, then asks the kernel to re-read the
// partition table. The kernel scanned the old table at boot, and its
// partition nodes still carry the old GPT labels. Without the re-read,
// storage would find the old machineState partition, mount it, and
// load its proven manifest, so the machine would boot the install
// that it should replace. After the re-read of a zeroed table, the
// disk has no partitions and the claim finds a blank disk. A disk
// smaller than two wipeRegions is zeroed only at the front, which
// still covers every signature that isBlank reads.
func blankDisk(device string) error {
	f, err := os.OpenFile(device, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	defer f.Close()

	zeros := make([]byte, wipeRegion)
	if _, err := f.WriteAt(zeros, 0); err != nil {
		return fmt.Errorf("zeroing the start of the disk: %w", err)
	}

	if d := diskByPath(device); d != nil && d.SizeBytes >= 2*wipeRegion {
		if _, err := f.WriteAt(zeros, int64(d.SizeBytes)-wipeRegion); err != nil {
			return fmt.Errorf("zeroing the end of the disk: %w", err)
		}
	}

	if err := f.Sync(); err != nil {
		return fmt.Errorf("flushing the disk: %w", err)
	}
	if _, err := unix.IoctlRetInt(int(f.Fd()), unix.BLKRRPART); err != nil {
		return fmt.Errorf("re-reading the partition table: %w", err)
	}
	unix.Sync()
	return nil
}
