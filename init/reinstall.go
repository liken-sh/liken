package main

// Reinstalling over a disk that already carries a liken install.
//
// The installer only ever claims a blank disk (claim.go), which is
// the right rule: it means liken never destroys data it did not put
// there. The cost of that rule is that a disk liken itself wrote can
// only be reinstalled after something outside liken blanks it, and a
// fresh machine has no such thing: no shell, no second OS, and, until
// its own install succeeds, no network. liken.reinstall is the escape
// hatch. It is liken.install, plus one thing first: it blanks the
// disks this machine's manifest declares, so the claim that follows
// finds them blank.
//
// A person asks for this by picking the stick's "wipe and reinstall
// as <name>" entry, over a manifest that declares these exact disks.
// Picking it is the confirmation, and it is as explicit as a person
// can be at a console with no other tools.
//
// A declared disk may name a stable identity under /dev/disk/by-id/
// or /dev/disk/by-path/ instead of a kernel path, so the wipe
// resolves it through resolveDeclaredDisk, the same computation the
// claim uses. A name that matches two disks wipes neither: guessing
// which one the manifest meant risks the disk the manifest did not
// mean.
//
// Blanking a disk's table is not the whole erasure, and on its own it
// would not be one. Partitions start a megabyte in, so their file
// systems survive the wipe, and a claim that writes the same layout
// back puts them at the same offsets again. The erasure is completed
// by the rule in storage.go: a partition this boot created always
// gets a new file system. Nothing of the previous install survives a
// reinstall, on any disk the manifest declares.
//
// The reclaim runs after loadModules (so a real controller's driver
// has created the device nodes) and before settleStorage (so nothing
// claims or mounts a disk this boot is about to erase). It does not
// power off: the boot goes on to install, the same as a plain
// install boot.

import (
	"fmt"
	"os"
	"time"

	"golang.org/x/sys/unix"

	"github.com/liken-sh/liken/machine"
)

const (
	// installParam makes a boot install onto a blank disk.
	installParam = "liken.install"
	// reinstallParam makes a boot reclaim its manifest's disks first,
	// then install.
	reinstallParam = "liken.reinstall"
)

// installing reports whether this boot's one job is to put liken on
// the machine's own disk, by either door: a plain install onto blank
// disks, or a reinstall that blanks them first.
func installing() bool {
	return bootParam(installParam) || bootParam(reinstallParam)
}

// wipeRegion is how much is zeroed at each end of a disk. isBlank
// reads only the first 2 KiB (the MBR, the GPT header, and the ext4
// magic), and a claim rewrites the whole GPT anyway, so a megabyte at
// the front already reclaims the disk. The same megabyte at the back
// removes the GPT's backup header, which lives in the last sectors,
// so no tool later reports a torn table.
const wipeRegion = 1 << 20

// reclaimManifestDisks blanks every disk the seed manifest declares.
// It reads the seed directly, by the same liken.machine= identity the
// installer uses, because the disks it must reclaim are exactly the
// ones the install that follows will claim. A disk that fails to
// reclaim is left to the ordinary claim, which refuses a disk it
// cannot recognize and stops the boot with its own message; this
// function does not power off on its own.
func reclaimManifestDisks() {
	seed, err := loadSeed(machine.MachineManifestDir, bootParamValue("liken.machine"))
	if err != nil {
		fmt.Fprintf(os.Stderr, "liken: reinstall: cannot read the seed manifest to find its disks: %v\n", err)
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
		fmt.Fprintln(os.Stderr, "liken: reinstall: the manifest declares no disks to reclaim")
		return
	}

	for _, device := range devices {
		node, err := awaitDevice(device)
		if err != nil {
			fmt.Fprintf(os.Stderr, "liken: reinstall: %v\n", err)
			continue
		}
		if err := blankDisk(node); err != nil {
			fmt.Fprintf(os.Stderr, "liken: reinstall: %s: %v\n", device, err)
			continue
		}
		fmt.Printf("liken: reinstall: reclaimed %s\n", device)
	}
}

// awaitDevice waits, boundedly, for a declared device to attach, and
// resolves it to the kernel node that a wipe can open. A reinstall
// hits the same probe race an install does: the controller's driver
// has loaded, but a SATA link or a USB device finishes negotiating a
// moment later. A device the manifest names is expected to exist, so
// its continued absence at the deadline is an error, not a silent
// skip.
func awaitDevice(declared string) (string, error) {
	return awaitDeviceDeadline(declared, 30*time.Second)
}

// awaitDeviceDeadline is awaitDevice with its deadline exposed as an
// argument, so a test can prove the timeout behavior in well under a
// second instead of waiting out the real 30.
func awaitDeviceDeadline(declared string, deadline time.Duration) (string, error) {
	const poll = 500 * time.Millisecond

	// resolve re-runs resolveDeclaredDisk on every poll, the same
	// computation the claim path uses, rather than opening the
	// declared string directly: a stable name is not a device node a
	// wipe can open, and a name that matches two disks must refuse at
	// once rather than wait out the deadline over an ambiguity that
	// waiting cannot fix.
	resolve := func() (string, error) {
		disk, err := resolveDeclaredDisk(declared)
		if err != nil || disk == nil {
			return "", err
		}
		return devicePath(*disk), nil
	}

	if node, err := resolve(); err != nil || node != "" {
		return node, err
	}
	fmt.Printf("liken: reinstall: waiting for %s to attach\n", declared)
	for begin := time.Now(); time.Since(begin) < deadline; {
		time.Sleep(poll)
		if node, err := resolve(); err != nil || node != "" {
			return node, err
		}
	}
	return "", fmt.Errorf("%s did not attach within %s", declared, deadline)
}

// blankDisk makes a disk blank in the two ways that matter: on the
// platters and in the kernel's memory of them. It zeros the first and
// last megabyte (the front carries every signature isBlank inspects;
// the back carries the GPT's backup header), then asks the kernel to
// re-read the table. The re-read is not cosmetic. The kernel scanned
// this disk's old table at boot, so its partition nodes and their
// cached GPT labels still name the previous install's roles; without
// the re-read, storage still finds a machineState partition to mount
// and a proven manifest inside it, and reinstalls the machine into
// the boot it was trying to replace. After the re-read of a zeroed
// table, the disk has no partitions, and the claim that follows sees
// a blank disk. A disk shorter than two wipe regions is blanked in
// one pass from the front, which still covers every signature.
// blankDisk takes the resolved kernel node, never the declared
// string: a stable name under /dev/disk/ is not a node this function
// can open.
func blankDisk(device string) error {
	f, err := os.OpenFile(device, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	defer f.Close()

	zeros := make([]byte, wipeRegion)
	if _, err := f.WriteAt(zeros, 0); err != nil {
		return fmt.Errorf("zeroing the front: %w", err)
	}

	if d := diskByPath(device); d != nil && d.SizeBytes >= 2*wipeRegion {
		if _, err := f.WriteAt(zeros, int64(d.SizeBytes)-wipeRegion); err != nil {
			return fmt.Errorf("zeroing the back: %w", err)
		}
	}

	if err := f.Sync(); err != nil {
		return fmt.Errorf("flushing: %w", err)
	}
	if _, err := unix.IoctlRetInt(int(f.Fd()), unix.BLKRRPART); err != nil {
		return fmt.Errorf("re-reading the partition table: %w", err)
	}
	unix.Sync()
	return nil
}
