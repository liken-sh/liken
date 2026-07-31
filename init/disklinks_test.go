package main

// Tests for the by-path tree, run against a fake machine: the two
// iSCSI class directories and a /dev/disk/by-path, all built in
// tempdirs. The fixture writes the same files the kernel writes, in
// the same shape, so a test describes a session and the fixture owns
// the layout. This is the point of the tests: the names liken
// publishes come from nothing but a handful of small text files, and
// these tests show which ones and what they compose into.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeInitiator points the three link trees, and the two iSCSI class
// directories that feed the by-path tree, at empty tempdirs, and
// restores the real paths when the test ends. Because the roots are
// package variables, tests in this package must not run in parallel.
// It returns the by-path directory, the one every test before this
// task already expected back.
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

// addSession gives the fake machine one logged-in session: the
// session's target name, its first connection's address and port, and
// one SCSI target under it. It returns the target's directory, so
// addLUN can hang devices off it.
func addSession(t *testing.T, number, targetName, address, port string) string {
	t.Helper()
	session := filepath.Join(iscsiSessionClass, "session"+number)
	if err := os.MkdirAll(session, 0o755); err != nil {
		t.Fatal(err)
	}
	writeSysfs(t, session, "targetname", targetName+"\n")

	connection := filepath.Join(iscsiConnectionClass, "connection"+number+":0")
	if err := os.MkdirAll(connection, 0o755); err != nil {
		t.Fatal(err)
	}
	writeSysfs(t, connection, "persistent_address", address+"\n")
	writeSysfs(t, connection, "persistent_port", port+"\n")

	// The session's device directory holds one entry per SCSI target
	// it reached, plus the connection and the class entry that the
	// walk has to pass over. The kernel writes those too, so the
	// fixture does.
	device := filepath.Join(session, "device")
	scsiTarget := filepath.Join(device, "target"+number+":0:0")
	if err := os.MkdirAll(scsiTarget, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(device, "connection"+number+":0"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(device, "iscsi_session", "session"+number), 0o755); err != nil {
		t.Fatal(err)
	}
	writeSysfs(t, device, "uevent", "DEVTYPE=scsi_target\n")
	return scsiTarget
}

// addLUN gives a SCSI target one disk at one LUN, named the way the
// kernel names it: a four-part SCSI address holding a block directory
// holding the disk.
func addLUN(t *testing.T, scsiTarget, host, lun, device string) string {
	t.Helper()
	disk := filepath.Join(scsiTarget, host+":0:0:"+lun, "block", device)
	if err := os.MkdirAll(disk, 0o755); err != nil {
		t.Fatal(err)
	}
	return disk
}

// addLUNPartition gives a disk one partition, with the partition file
// that marks it as one.
func addLUNPartition(t *testing.T, disk, name, number string) {
	t.Helper()
	dir := filepath.Join(disk, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeSysfs(t, dir, "partition", number+"\n")
}

// linkTarget reads one published name, and fails the test when the
// name is missing.
func linkTarget(t *testing.T, dir, name string) string {
	t.Helper()
	target, err := os.Readlink(filepath.Join(dir, name))
	if err != nil {
		t.Fatalf("reading %s: %v", name, err)
	}
	return target
}

// publishedNames lists what the by-path directory holds, so a test
// can claim that a name is gone as easily as that one is present.
func publishedNames(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		t.Fatal(err)
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	return names
}

func TestOneSessionNamesItsLUNTheWayUdevWould(t *testing.T) {
	links := fakeInitiator(t)
	scsiTarget := addSession(t, "1", "iqn.2000-01.com.synology:syn.pvc-5a35461e", "10.0.0.18", "3260")
	addLUN(t, scsiTarget, "2", "1", "sdb")

	if err := reconcileLinks(links, iscsiPaths()); err != nil {
		t.Fatal(err)
	}
	name := "ip-10.0.0.18:3260-iscsi-iqn.2000-01.com.synology:syn.pvc-5a35461e-lun-1"
	if target := linkTarget(t, links, name); target != "../../sdb" {
		t.Errorf("link points at %q, want ../../sdb", target)
	}
}

func TestNULPaddedSysfsValuesStillNameTheirLUN(t *testing.T) {
	// A target that reports these values from fixed-width buffers pads
	// the remainder with NUL. The kernel refuses a path holding a NUL,
	// so padding that reached the name would cost this disk its link.
	links := fakeInitiator(t)
	scsiTarget := addSession(t, "1", "iqn.2000-01.com.synology:syn.pvc-5a35461e\x00", "10.0.0.18\x00", "3260\x00")
	addLUN(t, scsiTarget, "2", "1", "sdb")

	if err := reconcileLinks(links, iscsiPaths()); err != nil {
		t.Fatal(err)
	}
	name := "ip-10.0.0.18:3260-iscsi-iqn.2000-01.com.synology:syn.pvc-5a35461e-lun-1"
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
		t.Fatal(err)
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
		t.Fatal(err)
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
		t.Fatal(err)
	}

	// A logout takes the whole session out of sysfs, along with the
	// disks it carried.
	if err := os.RemoveAll(filepath.Join(iscsiSessionClass, "session1")); err != nil {
		t.Fatal(err)
	}
	if err := reconcileLinks(links, iscsiPaths()); err != nil {
		t.Fatal(err)
	}
	if names := publishedNames(t, links); len(names) != 0 {
		t.Errorf("the logged-out session left %v behind", names)
	}
}

func TestADiskThatChangesLettersKeepsItsNamePointedRight(t *testing.T) {
	links := fakeInitiator(t)
	scsiTarget := addSession(t, "1", "iqn.2026-07.sh.liken.lab:storage", "10.10.0.100", "3260")
	addLUN(t, scsiTarget, "2", "1", "sdb")
	if err := reconcileLinks(links, iscsiPaths()); err != nil {
		t.Fatal(err)
	}

	// The same LUN, reached again after a reboot that probed a local
	// disk first, is sdc this time. The by-path name is the one thing
	// that did not change, which is why a CSI driver uses it.
	if err := os.RemoveAll(filepath.Join(scsiTarget, "2:0:0:1")); err != nil {
		t.Fatal(err)
	}
	addLUN(t, scsiTarget, "2", "1", "sdc")
	if err := reconcileLinks(links, iscsiPaths()); err != nil {
		t.Fatal(err)
	}
	name := "ip-10.10.0.100:3260-iscsi-iqn.2026-07.sh.liken.lab:storage-lun-1"
	if target := linkTarget(t, links, name); target != "../../sdc" {
		t.Errorf("link points at %q, want ../../sdc", target)
	}
	if got := len(publishedNames(t, links)); got != 1 {
		t.Errorf("published %d names, want one", got)
	}
}

func TestTheWalkOwnsTheDirectoryOutright(t *testing.T) {
	links := fakeInitiator(t)
	// Nothing else on a liken machine writes here, so an entry this
	// walk did not produce is stale by definition. A crash between
	// the symlink and the rename leaves exactly this kind of entry.
	if err := os.Symlink("../../sdz", filepath.Join(links, ".ip-stale.new")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("../../sdy", filepath.Join(links, "ip-10.0.0.1:3260-iscsi-gone-lun-0")); err != nil {
		t.Fatal(err)
	}
	scsiTarget := addSession(t, "1", "iqn.2026-07.sh.liken.lab:storage", "10.10.0.100", "3260")
	addLUN(t, scsiTarget, "2", "1", "sdb")

	if err := reconcileLinks(links, iscsiPaths()); err != nil {
		t.Fatal(err)
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
	// The connection publishes its address only once the login gets
	// that far. A name built without it would be wrong, and a wrong
	// name is worse than no name, because a CSI driver would mount it.
	if err := os.Remove(filepath.Join(iscsiConnectionClass, "connection1:0", "persistent_address")); err != nil {
		t.Fatal(err)
	}

	if err := reconcileLinks(links, iscsiPaths()); err != nil {
		t.Fatal(err)
	}
	if names := publishedNames(t, links); len(names) != 0 {
		t.Errorf("a half-published session named %v", names)
	}
}

func TestNoSessionsLeaveNoDirectoryBehind(t *testing.T) {
	links := fakeInitiator(t)
	// A machine that runs no network storage should not carry a tree
	// that claims to name its disks. The fixture made the directory,
	// so this test asks the walk for a path that does not exist.
	diskLinksDir = filepath.Join(links, "by-path")

	if err := reconcileLinks(diskLinksDir, iscsiPaths()); err != nil {
		t.Fatal(err)
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
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(links, 0o755) })

	if err := reconcileLinks(links, iscsiPaths()); err == nil {
		t.Error("writing into a read-only tree reported no error")
	}
}

// A local disk has no session and no connection: it is a plain sysfs
// tree under /sys/block, the same fixture diskpaths_test.go and
// diskids_test.go already build. These tests reuse fakeDisk and
// addLUNPartition (generic despite its name: it only needs a
// directory and a partition number) rather than inventing a second
// set of fixtures for the same shape of data.

func TestALocalDiskSharesByPathWithAnISCSISession(t *testing.T) {
	sys, _ := fakeMachine(t)
	links := fakeInitiator(t)
	fakeDisk(t, sys, "sda", "pci0000:00", "0000:00:1f.2", "ata3", "host2",
		"target2:0:0", "2:0:0:0", "block", "sda")
	scsiTarget := addSession(t, "1", "iqn.2026-07.sh.liken.lab:storage", "10.10.0.100", "3260")
	addLUN(t, scsiTarget, "2", "1", "sdb")

	if err := reconcileLinks(links, localPaths()); err != nil {
		t.Fatal(err)
	}
	if target := linkTarget(t, links, "pci-0000:00:1f.2-ata-3"); target != "../../sda" {
		t.Errorf("the local disk's link points at %q, want ../../sda", target)
	}
	iscsiName := "ip-10.10.0.100:3260-iscsi-iqn.2026-07.sh.liken.lab:storage-lun-1"
	if target := linkTarget(t, links, iscsiName); target != "../../sdb" {
		t.Errorf("the iSCSI session's link points at %q, want ../../sdb", target)
	}
}

func TestByIDLinksCarryTheirPartitions(t *testing.T) {
	sys, _ := fakeMachine(t)
	fakeInitiator(t)
	dir := fakeDisk(t, sys, "sda", "pci0000:00", "0000:00:1f.2", "ata3", "host2",
		"target2:0:0", "2:0:0:0", "block", "sda")
	writeSysfs(t, filepath.Join(dir, "device"), "wwid", "naa.5002538d40a45c88\n")
	addLUNPartition(t, dir, "sda1", "1")

	if err := reconcileLinks(diskIDsDir, idPaths()); err != nil {
		t.Fatal(err)
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
		t.Fatal(err)
	}

	if err := reconcileLinks(diskUUIDsDir, uuidPaths()); err != nil {
		t.Fatal(err)
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

	if err := reconcileLinks(diskIDsDir, idPaths()); err != nil {
		t.Fatal(err)
	}
	if names := publishedNames(t, diskIDsDir); len(names) != 0 {
		t.Errorf("two disks sharing a wwid published %v, want no by-id names for either", names)
	}

	if err := reconcileLinks(diskLinksDir, localPaths()); err != nil {
		t.Fatal(err)
	}
	if target := linkTarget(t, diskLinksDir, "pci-0000:00:1f.2-ata-3"); target != "../../sda" {
		t.Errorf("sda's by-path link points at %q, want ../../sda", target)
	}
	if target := linkTarget(t, diskLinksDir, "pci-0000:00:1f.2-ata-4"); target != "../../sdb" {
		t.Errorf("sdb's by-path link points at %q, want ../../sdb", target)
	}
}

func TestTwoClonedPartitionsPublishNoByUUIDLinkForEither(t *testing.T) {
	// Cloning a disk, or restoring the same image onto two disks,
	// copies the filesystem's UUID along with its bytes. Two
	// partitions then carry the same identity, and the by-uuid tree
	// cannot say which one a mount by that UUID means.
	sys, dev := fakeMachine(t)
	fakeInitiator(t)
	logs := fakeLogStream(t)
	addDisk(t, sys, dev, "vda", 2<<30, nil)
	addPartition(t, sys, "vda", "vda1", "", 1<<20)
	addDisk(t, sys, dev, "vdb", 2<<30, nil)
	addPartition(t, sys, "vdb", "vdb1", "", 1<<20)
	uuid := []byte{0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08, 0x09, 0x0a, 0x0b, 0x0c, 0x0d, 0x0e, 0x0f, 0x10}
	if err := os.WriteFile(filepath.Join(dev, "vda1"), ext4DeviceWithUUID(uuid), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dev, "vdb1"), ext4DeviceWithUUID(uuid), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := reconcileLinks(diskUUIDsDir, uuidPaths()); err != nil {
		t.Fatal(err)
	}
	if names := publishedNames(t, diskUUIDsDir); len(names) != 0 {
		t.Errorf("two partitions sharing a UUID published %v, want no by-uuid link for either", names)
	}
	if got := logs(); !strings.Contains(got, "vda1") || !strings.Contains(got, "vdb1") {
		t.Errorf("stderr %q does not name both partitions", got)
	}
}

func TestAStaleLinkInEachNewTreeIsPruned(t *testing.T) {
	fakeMachine(t)
	fakeInitiator(t)
	cases := []struct {
		name  string
		dir   *string
		build func() map[string]string
	}{
		{"by-path", &diskLinksDir, localPaths},
		{"by-id", &diskIDsDir, idPaths},
		{"by-uuid", &diskUUIDsDir, uuidPaths},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			stale := filepath.Join(*c.dir, "stale-name")
			if err := os.Symlink("../../sdz", stale); err != nil {
				t.Fatal(err)
			}
			if err := reconcileLinks(*c.dir, c.build()); err != nil {
				t.Fatal(err)
			}
			if names := publishedNames(t, *c.dir); len(names) != 0 {
				t.Errorf("the %s tree kept %v with no disks behind it", c.name, names)
			}
		})
	}
}
