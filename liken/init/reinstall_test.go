package main

// Tests for resolving a declared device before the reinstall wipe
// opens it. They reuse the fake sysfs fixtures resolve_test.go
// exercises the claim path with, because awaitDevice is built
// directly on resolveDeclaredDisk.

import (
	"path/filepath"
	"strings"
	"testing"
	"testing/synctest"
	"time"
)

func TestAwaitDeviceResolvesAStableNameToItsKernelNode(t *testing.T) {
	sys, dev := fakeMachine(t)
	dir := fakeDisk(t, sys, "sda", "pci0000:00", "0000:00:1f.2", "ata3", "host2",
		"target2:0:0", "2:0:0:0", "block", "sda")
	writeSysfs(t, filepath.Join(dir, "device"), "wwid", "naa.5002538d40a45c88\n")

	node, err := awaitDevice("/dev/disk/by-id/wwn-0x5002538d40a45c88")
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(dev, "sda"); node != want {
		t.Errorf("got %q, want %q", node, want)
	}
}

func TestAwaitDeviceAmbiguousNameRefusesWithoutWaiting(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		// Two disks answer to the same WWN, the way a clone or a disk
		// moved from another machine would. A third disk attaching cannot
		// un-match the two that already do, so the wait must give up at
		// once instead of running out its deadline.
		sys, _ := fakeMachine(t)
		for _, name := range []string{"sda", "sdb"} {
			dir := fakeDisk(t, sys, name, "pci0000:00", "0000:00:1f.2", "ata3", "host2",
				"target2:0:0", "2:0:0:0", "block", name)
			writeSysfs(t, filepath.Join(dir, "device"), "wwid", "naa.5002538d40a45c88\n")
		}

		start := time.Now()
		_, err := awaitDevice("/dev/disk/by-id/wwn-0x5002538d40a45c88")
		if elapsed := time.Since(start); elapsed != 0 {
			t.Errorf("an ambiguous name should end the wait at once, took %s", elapsed)
		}
		if err == nil || !strings.Contains(err.Error(), "refusing to guess") {
			t.Errorf("expected the ambiguity to surface: %v", err)
		}
	})
}

// The wait runs in a synctest bubble, so its 30-second deadline
// passes on the fake clock.
func TestAwaitDeviceReportsTheDeclaredNameAtItsDeadline(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		fakeMachine(t) // a machine with no disks at all

		start := time.Now()
		_, err := awaitDevice("/dev/disk/by-id/wwn-0xdeadbeef")
		if elapsed := time.Since(start); elapsed != declaredDiskDeadline {
			t.Errorf("the wait gave up after %s, want the deadline, %s", elapsed, declaredDiskDeadline)
		}
		if err == nil || !strings.Contains(err.Error(), "/dev/disk/by-id/wwn-0xdeadbeef") {
			t.Errorf("error should name the declared string: %v", err)
		}
	})
}
