package main

// The fixtures build the sysfs files that the kernel writes for an
// iSCSI session or a local disk in temporary directories.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/liken-sh/liken/liken/disks"
)

// fakeInitiator points the three link trees and the two iSCSI class
// directories at empty temporary directories, and restores the real
// paths when the test ends. The roots are package variables, so tests
// in this package must not run in parallel. It returns the by-path
// directory.
func fakeInitiator(t *testing.T) string {
	t.Helper()
	sessions, connections, links, ids, uuids := t.TempDir(), t.TempDir(), t.TempDir(), t.TempDir(), t.TempDir()
	oldSessions, oldConnections, oldLinks, oldIDs, oldUUIDs :=
		iscsiSessionClass, iscsiConnectionClass, diskLinksDir, diskIDsDir, diskUUIDsDir
	iscsiSessionClass, iscsiConnectionClass, diskLinksDir, diskIDsDir, diskUUIDsDir =
		sessions, connections, links, ids, uuids
	t.Cleanup(func() {
		iscsiSessionClass, iscsiConnectionClass, diskLinksDir, diskIDsDir, diskUUIDsDir =
			oldSessions, oldConnections, oldLinks, oldIDs, oldUUIDs
	})
	return links
}

// addSession adds one logged-in session with its target name, the
// address and port of its connection 0, and one SCSI target. It
// returns the target's directory for addLUN.
func addSession(t *testing.T, number, targetName, address, port string) string {
	t.Helper()
	session := filepath.Join(iscsiSessionClass, "session"+number)
	if err := os.MkdirAll(session, 0o755); err != nil {
		t.Fatalf("creating the session directory: %v", err)
	}
	writeSysfs(t, session, "targetname", targetName+"\n")

	connection := filepath.Join(iscsiConnectionClass, "connection"+number+":0")
	if err := os.MkdirAll(connection, 0o755); err != nil {
		t.Fatalf("creating the connection directory: %v", err)
	}
	writeSysfs(t, connection, "persistent_address", address+"\n")
	writeSysfs(t, connection, "persistent_port", port+"\n")

	// The kernel also puts connection and class entries in the
	// session's device directory, and sessionLUNs must skip them.
	device := filepath.Join(session, "device")
	scsiTarget := filepath.Join(device, "target"+number+":0:0")
	if err := os.MkdirAll(scsiTarget, 0o755); err != nil {
		t.Fatalf("creating the SCSI target directory: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(device, "connection"+number+":0"), 0o755); err != nil {
		t.Fatalf("creating the connection directory of the SCSI device: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(device, "iscsi_session", "session"+number), 0o755); err != nil {
		t.Fatalf("creating the iscsi_session directory: %v", err)
	}
	writeSysfs(t, device, "uevent", "DEVTYPE=scsi_target\n")
	return scsiTarget
}

// addLUN adds a disk at one LUN under a SCSI target, as the directory
// host:0:0:lun/block/device. It returns the disk's directory.
func addLUN(t *testing.T, scsiTarget, host, lun, device string) string {
	t.Helper()
	disk := filepath.Join(scsiTarget, host+":0:0:"+lun, "block", device)
	if err := os.MkdirAll(disk, 0o755); err != nil {
		t.Fatalf("creating the disk directory: %v", err)
	}
	return disk
}

// addLUNPartition adds a partition directory to a disk directory, with
// the partition file that holds its number.
func addLUNPartition(t *testing.T, disk, name, number string) {
	t.Helper()
	dir := filepath.Join(disk, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("creating the partition directory: %v", err)
	}
	writeSysfs(t, dir, "partition", number+"\n")
}

func linkTarget(t *testing.T, dir, name string) string {
	t.Helper()
	target, err := os.Readlink(filepath.Join(dir, name))
	if err != nil {
		t.Fatalf("reading %s: %v", name, err)
	}
	return target
}

// publishedNames returns nil when the directory does not exist.
func publishedNames(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		t.Fatalf("reading the directory %s: %v", dir, err)
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	return names
}

func TestOneSessionNamesItsLUNTheWayUdevWould(t *testing.T) {
	links := fakeInitiator(t)
	scsiTarget := addSession(t, "1", "iqn.2026-07.sh.liken.lab:pvc-0001", "10.0.0.18", "3260")
	addLUN(t, scsiTarget, "2", "1", "sdb")

	if err := reconcileLinks(links, iscsiPaths()); err != nil {
		t.Fatalf("reconciling the links: %v", err)
	}
	name := "ip-10.0.0.18:3260-iscsi-iqn.2026-07.sh.liken.lab:pvc-0001-lun-1"
	if target := linkTarget(t, links, name); target != "../../sdb" {
		t.Errorf("link points at %q, want ../../sdb", target)
	}
}

func TestNULPaddedSysfsValuesStillNameTheirLUN(t *testing.T) {
	// A target that reports these values from fixed-width buffers pads
	// them with NUL. A symlink name cannot contain NUL, so the link
	// would fail if the padding reached the name.
	links := fakeInitiator(t)
	scsiTarget := addSession(t, "1", "iqn.2026-07.sh.liken.lab:pvc-0001\x00", "10.0.0.18\x00", "3260\x00")
	addLUN(t, scsiTarget, "2", "1", "sdb")

	if err := reconcileLinks(links, iscsiPaths()); err != nil {
		t.Fatalf("reconciling the links: %v", err)
	}
	name := "ip-10.0.0.18:3260-iscsi-iqn.2026-07.sh.liken.lab:pvc-0001-lun-1"
	if target := linkTarget(t, links, name); target != "../../sdb" {
		t.Errorf("link points at %q, want ../../sdb", target)
	}
}

func TestAPartitionTakesThePartSuffix(t *testing.T) {
	links := fakeInitiator(t)
	scsiTarget := addSession(t, "1", "iqn.2026-07.sh.liken.lab:storage", "10.10.0.100", "3260")
	disk := addLUN(t, scsiTarget, "2", "1", "sdb")
	addLUNPartition(t, disk, "sdb1", "1")
	// A disk whose second partition was deleted still names its third
	// one part3, because the suffix is the kernel's partition number
	// and not a count of what is there.
	addLUNPartition(t, disk, "sdb3", "3")

	if err := reconcileLinks(links, iscsiPaths()); err != nil {
		t.Fatalf("reconciling the links: %v", err)
	}
	base := "ip-10.10.0.100:3260-iscsi-iqn.2026-07.sh.liken.lab:storage-lun-1"
	if target := linkTarget(t, links, base+"-part1"); target != "../../sdb1" {
		t.Errorf("part1 points at %q, want ../../sdb1", target)
	}
	if target := linkTarget(t, links, base+"-part3"); target != "../../sdb3" {
		t.Errorf("part3 points at %q, want ../../sdb3", target)
	}
	if got := len(publishedNames(t, links)); got != 3 {
		t.Errorf("published %d names, want the disk and its two partitions", got)
	}
}

func TestTwoSessionsNameTheirOwnDisks(t *testing.T) {
	links := fakeInitiator(t)
	first := addSession(t, "1", "iqn.2026-07.sh.liken.lab:storage", "10.10.0.100", "3260")
	addLUN(t, first, "2", "1", "sdb")
	second := addSession(t, "2", "iqn.2026-07.sh.liken.lab:archive", "10.10.0.101", "3261")
	addLUN(t, second, "3", "7", "sdc")

	if err := reconcileLinks(links, iscsiPaths()); err != nil {
		t.Fatalf("reconciling the links: %v", err)
	}
	if target := linkTarget(t, links, "ip-10.10.0.100:3260-iscsi-iqn.2026-07.sh.liken.lab:storage-lun-1"); target != "../../sdb" {
		t.Errorf("the first session's disk points at %q, want ../../sdb", target)
	}
	if target := linkTarget(t, links, "ip-10.10.0.101:3261-iscsi-iqn.2026-07.sh.liken.lab:archive-lun-7"); target != "../../sdc" {
		t.Errorf("the second session's disk points at %q, want ../../sdc", target)
	}
}

func TestASessionThatLogsOutLosesItsNames(t *testing.T) {
	links := fakeInitiator(t)
	scsiTarget := addSession(t, "1", "iqn.2026-07.sh.liken.lab:storage", "10.10.0.100", "3260")
	disk := addLUN(t, scsiTarget, "2", "1", "sdb")
	addLUNPartition(t, disk, "sdb1", "1")
	if err := reconcileLinks(links, iscsiPaths()); err != nil {
		t.Fatalf("reconciling the links: %v", err)
	}

	// A logout removes the session and its disks from sysfs.
	if err := os.RemoveAll(filepath.Join(iscsiSessionClass, "session1")); err != nil {
		t.Fatalf("removing the session directory: %v", err)
	}
	if err := reconcileLinks(links, iscsiPaths()); err != nil {
		t.Fatalf("reconciling the links: %v", err)
	}
	if names := publishedNames(t, links); len(names) != 0 {
		t.Errorf("the links %v remain after the session logged out, want none", names)
	}
}

func TestADiskThatChangesLettersKeepsItsNamePointedRight(t *testing.T) {
	links := fakeInitiator(t)
	scsiTarget := addSession(t, "1", "iqn.2026-07.sh.liken.lab:storage", "10.10.0.100", "3260")
	addLUN(t, scsiTarget, "2", "1", "sdb")
	if err := reconcileLinks(links, iscsiPaths()); err != nil {
		t.Fatalf("reconciling the links: %v", err)
	}

	// After a reboot that probes a local disk first, the same LUN is
	// sdc. The by-path name stays the same.
	if err := os.RemoveAll(filepath.Join(scsiTarget, "2:0:0:1")); err != nil {
		t.Fatalf("removing the old disk directory: %v", err)
	}
	addLUN(t, scsiTarget, "2", "1", "sdc")
	if err := reconcileLinks(links, iscsiPaths()); err != nil {
		t.Fatalf("reconciling the links: %v", err)
	}
	name := "ip-10.10.0.100:3260-iscsi-iqn.2026-07.sh.liken.lab:storage-lun-1"
	if target := linkTarget(t, links, name); target != "../../sdc" {
		t.Errorf("link points at %q, want ../../sdc", target)
	}
	if got := len(publishedNames(t, links)); got != 1 {
		t.Errorf("published %d names, want one", got)
	}
}

func TestReconcileRemovesEntriesItDidNotProduce(t *testing.T) {
	links := fakeInitiator(t)
	// A crash between reconcileLinks creating the temporary symlink
	// and renaming it leaves an entry like this one.
	if err := os.Symlink("../../sdz", filepath.Join(links, ".ip-stale.new")); err != nil {
		t.Fatalf("creating the leftover temporary link: %v", err)
	}
	if err := os.Symlink("../../sdy", filepath.Join(links, "ip-10.0.0.1:3260-iscsi-gone-lun-0")); err != nil {
		t.Fatalf("creating the link of a session that is gone: %v", err)
	}
	scsiTarget := addSession(t, "1", "iqn.2026-07.sh.liken.lab:storage", "10.10.0.100", "3260")
	addLUN(t, scsiTarget, "2", "1", "sdb")

	if err := reconcileLinks(links, iscsiPaths()); err != nil {
		t.Fatalf("reconciling the links: %v", err)
	}
	names := publishedNames(t, links)
	if len(names) != 1 || names[0] != "ip-10.10.0.100:3260-iscsi-iqn.2026-07.sh.liken.lab:storage-lun-1" {
		t.Errorf("the directory holds %v, want only this session's disk", names)
	}
}

func TestASessionMidLoginPublishesNothing(t *testing.T) {
	links := fakeInitiator(t)
	scsiTarget := addSession(t, "1", "iqn.2026-07.sh.liken.lab:storage", "10.10.0.100", "3260")
	addLUN(t, scsiTarget, "2", "1", "sdb")
	// The connection has no persistent_address until the login gets
	// that far, and a name without it would be wrong.
	if err := os.Remove(filepath.Join(iscsiConnectionClass, "connection1:0", "persistent_address")); err != nil {
		t.Fatalf("removing persistent_address: %v", err)
	}

	if err := reconcileLinks(links, iscsiPaths()); err != nil {
		t.Fatalf("reconciling the links: %v", err)
	}
	if names := publishedNames(t, links); len(names) != 0 {
		t.Errorf("the links %v remain from a session that was not fully published, want none", names)
	}
}

func TestNoSessionsLeaveNoDirectoryBehind(t *testing.T) {
	links := fakeInitiator(t)
	// The fixture already created the by-path directory, so point
	// diskLinksDir at a path that does not exist.
	diskLinksDir = filepath.Join(links, "by-path")

	if err := reconcileLinks(diskLinksDir, iscsiPaths()); err != nil {
		t.Fatalf("reconciling the links: %v", err)
	}
	if _, err := os.Stat(diskLinksDir); !os.IsNotExist(err) {
		t.Errorf("stat of an unused by-path tree returned %v, want it absent", err)
	}
}

func TestAnUnwritableTreeReportsAndDoesNotPanic(t *testing.T) {
	links := fakeInitiator(t)
	scsiTarget := addSession(t, "1", "iqn.2026-07.sh.liken.lab:storage", "10.10.0.100", "3260")
	addLUN(t, scsiTarget, "2", "1", "sdb")
	if err := os.Chmod(links, 0o555); err != nil {
		t.Fatalf("making the tree read-only: %v", err)
	}
	t.Cleanup(func() { os.Chmod(links, 0o755) })

	if err := reconcileLinks(links, iscsiPaths()); err == nil {
		t.Error("reconcileLinks returned no error when it could not write into a read-only tree, want one")
	}
}

// The tests below build local disks with fakeDisk from
// diskids_test.go. addLUNPartition works on any disk directory.

func TestALocalDiskSharesByPathWithAnISCSISession(t *testing.T) {
	sys, _ := fakeMachine(t)
	links := fakeInitiator(t)
	fakeDisk(t, sys, "sda", "pci0000:00", "0000:00:1f.2", "ata3", "host2",
		"target2:0:0", "2:0:0:0", "block", "sda")
	scsiTarget := addSession(t, "1", "iqn.2026-07.sh.liken.lab:storage", "10.10.0.100", "3260")
	addLUN(t, scsiTarget, "2", "1", "sdb")

	want, _ := localPaths()
	if err := reconcileLinks(links, want); err != nil {
		t.Fatalf("reconciling the links: %v", err)
	}
	if target := linkTarget(t, links, "pci-0000:00:1f.2-ata-3"); target != "../../sda" {
		t.Errorf("the local disk's link points at %q, want ../../sda", target)
	}
	iscsiName := "ip-10.10.0.100:3260-iscsi-iqn.2026-07.sh.liken.lab:storage-lun-1"
	if target := linkTarget(t, links, iscsiName); target != "../../sdb" {
		t.Errorf("the iSCSI session's link points at %q, want ../../sdb", target)
	}
}

func TestByIDLinksIncludeTheirPartitions(t *testing.T) {
	sys, _ := fakeMachine(t)
	fakeInitiator(t)
	dir := fakeDisk(t, sys, "sda", "pci0000:00", "0000:00:1f.2", "ata3", "host2",
		"target2:0:0", "2:0:0:0", "block", "sda")
	writeSysfs(t, filepath.Join(dir, "device"), "wwid", "naa.5002538d40a45c88\n")
	addLUNPartition(t, dir, "sda1", "1")

	want, _ := idPaths()
	if err := reconcileLinks(diskIDsDir, want); err != nil {
		t.Fatalf("reconciling the links: %v", err)
	}
	if target := linkTarget(t, diskIDsDir, "wwn-0x5002538d40a45c88-part1"); target != "../../sda1" {
		t.Errorf("the by-id partition link points at %q, want ../../sda1", target)
	}
}

func TestByUUIDLinksAPartitionsFilesystem(t *testing.T) {
	sys, dev := fakeMachine(t)
	fakeInitiator(t)
	addDisk(t, sys, dev, "vda", 2<<30, nil)
	addPartition(t, sys, "vda", "vda1", "", 1<<20)
	uuid := []byte{0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08, 0x09, 0x0a, 0x0b, 0x0c, 0x0d, 0x0e, 0x0f, 0x10}
	if err := os.WriteFile(filepath.Join(dev, "vda1"), ext4DeviceWithUUID(uuid), 0o644); err != nil {
		t.Fatalf("writing the vda1 device: %v", err)
	}

	links, _ := uuidPaths()
	if err := reconcileLinks(diskUUIDsDir, links); err != nil {
		t.Fatalf("reconciling the links: %v", err)
	}
	want := "01020304-0506-0708-090a-0b0c0d0e0f10"
	if target := linkTarget(t, diskUUIDsDir, want); target != "../../vda1" {
		t.Errorf("the by-uuid link points at %q, want ../../vda1", target)
	}
}

func TestTwoDisksWithOneWWIDPublishNoWWNButKeepBothByPathNames(t *testing.T) {
	sys, _ := fakeMachine(t)
	fakeInitiator(t)
	dirA := fakeDisk(t, sys, "sda", "pci0000:00", "0000:00:1f.2", "ata3", "host2",
		"target2:0:0", "2:0:0:0", "block", "sda")
	writeSysfs(t, filepath.Join(dirA, "device"), "wwid", "naa.5002538d40a45c88\n")
	dirB := fakeDisk(t, sys, "sdb", "pci0000:00", "0000:00:1f.2", "ata4", "host3",
		"target3:0:0", "3:0:0:0", "block", "sdb")
	writeSysfs(t, filepath.Join(dirB, "device"), "wwid", "naa.5002538d40a45c88\n")

	idLinks, _ := idPaths()
	if err := reconcileLinks(diskIDsDir, idLinks); err != nil {
		t.Fatalf("reconciling the links: %v", err)
	}
	if names := publishedNames(t, diskIDsDir); len(names) != 0 {
		t.Errorf("two disks sharing a wwid published %v, want no by-id names for either", names)
	}

	pathLinks, _ := localPaths()
	if err := reconcileLinks(diskLinksDir, pathLinks); err != nil {
		t.Fatalf("reconciling the links: %v", err)
	}
	if target := linkTarget(t, diskLinksDir, "pci-0000:00:1f.2-ata-3"); target != "../../sda" {
		t.Errorf("sda's by-path link points at %q, want ../../sda", target)
	}
	if target := linkTarget(t, diskLinksDir, "pci-0000:00:1f.2-ata-4"); target != "../../sdb" {
		t.Errorf("sdb's by-path link points at %q, want ../../sdb", target)
	}
}

// twoClonedPartitions builds two disks with one partition each, and
// gives both partitions the same filesystem UUID. It returns the
// device root, so a test can change one UUID to clear the collision.
func twoClonedPartitions(t *testing.T) (dev string, uuid []byte) {
	t.Helper()
	sys, dev := fakeMachine(t)
	fakeInitiator(t)
	addDisk(t, sys, dev, "vda", 2<<30, nil)
	addPartition(t, sys, "vda", "vda1", "", 1<<20)
	addDisk(t, sys, dev, "vdb", 2<<30, nil)
	addPartition(t, sys, "vdb", "vdb1", "", 1<<20)
	uuid = []byte{0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08, 0x09, 0x0a, 0x0b, 0x0c, 0x0d, 0x0e, 0x0f, 0x10}
	if err := os.WriteFile(filepath.Join(dev, "vda1"), ext4DeviceWithUUID(uuid), 0o644); err != nil {
		t.Fatalf("writing the vda1 device: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dev, "vdb1"), ext4DeviceWithUUID(uuid), 0o644); err != nil {
		t.Fatalf("writing the vdb1 device: %v", err)
	}
	return dev, uuid
}

func TestTwoClonedPartitionsPublishNoByUUIDLinkForEither(t *testing.T) {
	// Cloning a disk copies the filesystem UUID with the data, and a
	// by-uuid link cannot point at two partitions.
	twoClonedPartitions(t)

	links, collisions := uuidPaths()
	if err := reconcileLinks(diskUUIDsDir, links); err != nil {
		t.Fatalf("reconciling the links: %v", err)
	}
	if names := publishedNames(t, diskUUIDsDir); len(names) != 0 {
		t.Errorf("two partitions sharing a UUID published %v, want no by-uuid link for either", names)
	}
	if len(collisions) != 1 {
		t.Fatalf("the walk reported %d collisions, want 1", len(collisions))
	}
	got := collisions[0]
	if !((got.first == "vda1" && got.second == "vdb1") || (got.first == "vdb1" && got.second == "vda1")) {
		t.Errorf("the collision names %q and %q, want vda1 and vdb1", got.first, got.second)
	}
}

func TestAPersistentCollisionPrintsOnce(t *testing.T) {
	twoClonedPartitions(t)
	logs := fakeLogStream(t)

	_, first := uuidPaths()
	seen := reportNewCollisions(first, nil)
	if got := strings.Count(logs(), "would point at both"); got != 1 {
		t.Fatalf("the first walk printed %d lines, want 1", got)
	}

	_, second := uuidPaths()
	reportNewCollisions(second, seen)
	if got := strings.Count(logs(), "would point at both"); got != 1 {
		t.Errorf("a walk that repeats the same collision printed %d lines total, want still 1", got)
	}
}

func TestACollisionThatClearsAndReturnsPrintsAgain(t *testing.T) {
	dev, _ := twoClonedPartitions(t)
	logs := fakeLogStream(t)

	_, first := uuidPaths()
	seen := reportNewCollisions(first, nil)
	if got := strings.Count(logs(), "would point at both"); got != 1 {
		t.Fatalf("the first walk printed %d lines, want 1", got)
	}

	// Give vdb1 its own UUID. The walk that finds no collision must
	// print nothing.
	clear := []byte{0x02, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08, 0x09, 0x0a, 0x0b, 0x0c, 0x0d, 0x0e, 0x0f, 0x10}
	if err := os.WriteFile(filepath.Join(dev, "vdb1"), ext4DeviceWithUUID(clear), 0o644); err != nil {
		t.Fatalf("writing the vdb1 device: %v", err)
	}
	_, second := uuidPaths()
	seen = reportNewCollisions(second, seen)
	if got := strings.Count(logs(), "would point at both"); got != 1 {
		t.Fatalf("the walk that cleared the collision printed %d lines total, want still 1", got)
	}

	// Restore the shared UUID. The previous walk found no collision,
	// so this one prints it again.
	shared := []byte{0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08, 0x09, 0x0a, 0x0b, 0x0c, 0x0d, 0x0e, 0x0f, 0x10}
	if err := os.WriteFile(filepath.Join(dev, "vdb1"), ext4DeviceWithUUID(shared), 0o644); err != nil {
		t.Fatalf("writing the vdb1 device: %v", err)
	}
	_, third := uuidPaths()
	reportNewCollisions(third, seen)
	if got := strings.Count(logs(), "would point at both"); got != 2 {
		t.Errorf("the walk that reintroduced the collision printed %d lines total, want 2", got)
	}
}

// threeSlotsSharingAVolumeID formats three FAT32 partitions, like
// bootHome and the two system slots, with the same volume ID. It
// returns the partition names in the order discoverPartitions reports
// them.
func threeSlotsSharingAVolumeID(t *testing.T) []string {
	t.Helper()
	sys, dev := fakeMachine(t)
	fakeInitiator(t)
	addDisk(t, sys, dev, "vdc", 2<<30, nil)
	names := []string{"vdc2", "vdc3", "vdc4"}
	for _, name := range names {
		addPartition(t, sys, "vdc", name, "", 512<<20)
		f, err := os.OpenFile(filepath.Join(dev, name), os.O_RDWR|os.O_CREATE, 0o644)
		if err != nil {
			t.Fatalf("opening the partition device: %v", err)
		}
		if err := f.Truncate(512 << 20); err != nil {
			t.Fatalf("resizing the partition device: %v", err)
		}
		if err := disks.FormatFAT32(f, 512<<20, "SLOT", 0x6A6CDB4B); err != nil {
			t.Fatalf("formatting the partition: %v", err)
		}
		if err := f.Close(); err != nil {
			t.Fatalf("closing the partition device: %v", err)
		}
	}
	return names
}

// The third volume finds the name already blocked, so the first walk
// prints one line, for the first two volumes.
func TestWalkDiskLinksPrintsAPersistentCollisionOnceOnItsFirstWalk(t *testing.T) {
	threeSlotsSharingAVolumeID(t)
	logs := fakeLogStream(t)

	seen := walkDiskLinks(diskLinkSeen{})
	if got := strings.Count(logs(), "would point at both"); got != 1 {
		t.Fatalf("the first walk printed %d lines, want 1", got)
	}
	if !strings.Contains(logs(), "vdc2 and vdc3") {
		t.Errorf("stderr is %q, want it to name vdc2 and vdc3", logs())
	}
	if names := publishedNames(t, diskUUIDsDir); len(names) != 0 {
		t.Errorf("three slots sharing a volume id published %v, want no by-uuid link for any of them", names)
	}

	walkDiskLinks(seen)
	if got := strings.Count(logs(), "would point at both"); got != 1 {
		t.Errorf("a second walk over the same collision printed %d lines total, want still 1", got)
	}
}

func TestAStaleLinkInEachNewTreeIsPruned(t *testing.T) {
	fakeMachine(t)
	fakeInitiator(t)
	cases := []struct {
		name  string
		dir   *string
		build func() (map[string]string, []diskLinkCollision)
	}{
		{"by-path", &diskLinksDir, localPaths},
		{"by-id", &diskIDsDir, idPaths},
		{"by-uuid", &diskUUIDsDir, uuidPaths},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			stale := filepath.Join(*c.dir, "stale-name")
			if err := os.Symlink("../../sdz", stale); err != nil {
				t.Fatalf("creating the stale link: %v", err)
			}
			want, _ := c.build()
			if err := reconcileLinks(*c.dir, want); err != nil {
				t.Fatalf("reconciling the links: %v", err)
			}
			if names := publishedNames(t, *c.dir); len(names) != 0 {
				t.Errorf("the %s tree has %v with no disks behind them, want none", c.name, names)
			}
		})
	}
}
