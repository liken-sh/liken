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
	"testing"
)

// fakeInitiator points the by-path walk at empty class directories
// and an empty /dev/disk/by-path, and restores the real paths when
// the test ends. Because the roots are package variables, tests in
// this package must not run in parallel.
func fakeInitiator(t *testing.T) string {
	t.Helper()
	sessions, connections, links := t.TempDir(), t.TempDir(), t.TempDir()
	oldSessions, oldConnections, oldLinks := iscsiSessionClass, iscsiConnectionClass, diskLinksDir
	iscsiSessionClass, iscsiConnectionClass, diskLinksDir = sessions, connections, links
	t.Cleanup(func() {
		iscsiSessionClass, iscsiConnectionClass, diskLinksDir = oldSessions, oldConnections, oldLinks
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
