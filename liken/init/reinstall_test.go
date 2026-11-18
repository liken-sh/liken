package main

// Tests for resolving a declared disk before the reinstall wipe opens
// it. They use the fake sysfs fixtures of resolve_test.go, because
// awaitDevice calls resolveDeclaredDisk.

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
		t.Fatalf("resolving the stable name: %v", err)
	}
	if want := filepath.Join(dev, "sda"); node != want {
		t.Errorf("awaitDevice = %q, want %q", node, want)
	}
}

func TestAwaitDeviceAmbiguousNameRefusesWithoutWaiting(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		// Two disks have the same WWN, as a cloned disk or one moved
		// from another machine would. A disk that attaches later cannot
		// remove the match, so the wait must fail at once and not run
		// to its deadline.
		sys, _ := fakeMachine(t)
		for _, name := range []string{"sda", "sdb"} {
			dir := fakeDisk(t, sys, name, "pci0000:00", "0000:00:1f.2", "ata3", "host2",
				"target2:0:0", "2:0:0:0", "block", name)
			writeSysfs(t, filepath.Join(dir, "device"), "wwid", "naa.5002538d40a45c88\n")
		}

		start := time.Now()
		_, err := awaitDevice("/dev/disk/by-id/wwn-0x5002538d40a45c88")
		if elapsed := time.Since(start); elapsed != 0 {
			t.Errorf("the wait for an ambiguous name took %s, want it to end at once", elapsed)
		}
		if err == nil || !strings.Contains(err.Error(), "refusing to guess") {
			t.Errorf("the error is %v, want one that reports the ambiguity", err)
		}
	})
}

func TestAwaitDeviceReportsTheDeclaredNameAtItsDeadline(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		fakeMachine(t) // a machine with no disks at all

		start := time.Now()
		_, err := awaitDevice("/dev/disk/by-id/wwn-0xdeadbeef")
		if elapsed := time.Since(start); elapsed != declaredDiskDeadline {
			t.Errorf("the wait gave up after %s, want the deadline, %s", elapsed, declaredDiskDeadline)
		}
		if err == nil || !strings.Contains(err.Error(), "/dev/disk/by-id/wwn-0xdeadbeef") {
			t.Errorf("the error is %v, want it to name the declared disk", err)
		}
	})
}
