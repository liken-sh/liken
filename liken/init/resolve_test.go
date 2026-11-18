package main

// These tests build disks with the fake sysfs fixtures of
// diskids_test.go and diskpaths_test.go, because resolveDeclaredDisk
// computes disk names from sysfs and reads no links.

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/liken-sh/liken/liken/disks"
	"github.com/liken-sh/liken/liken/machine"
)

func TestResolveDeclaredDiskPlainKernelPath(t *testing.T) {
	sys, dev := fakeMachine(t)
	addDisk(t, sys, dev, "vda", 1<<30, nil)

	disk, err := resolveDeclaredDisk(filepath.Join(dev, "vda"))
	if err != nil {
		t.Fatalf("resolving the kernel path: %v", err)
	}
	if disk == nil || disk.Name != "vda" {
		t.Errorf("the disk is %+v, want vda", disk)
	}
}

func TestResolveDeclaredDiskByIDNameMatchesTheDiskWithThatID(t *testing.T) {
	sys, _ := fakeMachine(t)
	dir := fakeDisk(t, sys, "sda", "pci0000:00", "0000:00:1f.2", "ata3", "host2",
		"target2:0:0", "2:0:0:0", "block", "sda")
	writeSysfs(t, filepath.Join(dir, "device"), "wwid", "naa.5002538d40a45c88\n")

	disk, err := resolveDeclaredDisk("/dev/disk/by-id/wwn-0x5002538d40a45c88")
	if err != nil {
		t.Fatalf("resolving the by-id name: %v", err)
	}
	if disk == nil || disk.Name != "sda" {
		t.Errorf("the disk is %+v, want sda", disk)
	}
}

func TestResolveDeclaredDiskByPathNameMatchesTheDiskOnThatPort(t *testing.T) {
	sys, _ := fakeMachine(t)
	fakeDisk(t, sys, "vda", "pci0000:00", "0000:00:05.0", "virtio2", "block", "vda")

	disk, err := resolveDeclaredDisk("/dev/disk/by-path/pci-0000:00:05.0")
	if err != nil {
		t.Fatalf("resolving the by-path name: %v", err)
	}
	if disk == nil || disk.Name != "vda" {
		t.Errorf("the disk is %+v, want vda", disk)
	}
}

func TestResolveDeclaredDiskUnattachedNameResolvesNil(t *testing.T) {
	fakeMachine(t) // a machine with no disks at all
	disk, err := resolveDeclaredDisk("/dev/disk/by-id/wwn-0xdeadbeef")
	if err != nil {
		t.Fatalf("resolving a name that no disk has: %v", err)
	}
	if disk != nil {
		t.Errorf("the disk is %+v, want nil", disk)
	}
}

func TestResolveDeclaredDiskAmbiguousNameRefusesToGuess(t *testing.T) {
	// Two disks report the same WWN, as a cloned disk would.
	sys, _ := fakeMachine(t)
	for _, name := range []string{"sda", "sdb"} {
		dir := fakeDisk(t, sys, name, "pci0000:00", "0000:00:1f.2", "ata3", "host2",
			"target2:0:0", "2:0:0:0", "block", name)
		writeSysfs(t, filepath.Join(dir, "device"), "wwid", "naa.5002538d40a45c88\n")
	}

	_, err := resolveDeclaredDisk("/dev/disk/by-id/wwn-0x5002538d40a45c88")
	if err == nil {
		t.Fatal("resolving a name that matches two disks returned no error, want one")
	}
	for _, want := range []string{"refusing to guess", "2 disks", "sda", "sdb"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the error is %q, want it to contain %q", err, want)
		}
	}
}

func TestResolveDeclaredDiskRefusesOtherDiskTrees(t *testing.T) {
	fakeMachine(t)
	_, err := resolveDeclaredDisk("/dev/disk/by-uuid/deadbeef-dead-beef-dead-beefdeadbeef")
	if err == nil || !strings.Contains(err.Error(), "by-uuid") {
		t.Errorf("the error is %v, want one that names by-uuid", err)
	}
}

func TestPlanAllClaimsWaitsForALateDeclaredDisk(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		sys, dev := fakeMachine(t)
		// The disk is built in another directory, so that it attaches
		// with one rename.
		staged := t.TempDir()
		addDisk(t, staged, dev, "mmcblk0", 2<<30, make([]byte, 2_048))

		attached := make(chan error, 1)
		go func() {
			time.Sleep(20 * time.Millisecond)
			attached <- os.Rename(filepath.Join(staged, "mmcblk0"), filepath.Join(sys, "mmcblk0"))
		}()

		roles := []machine.DeclaredRole{declared("machineState", filepath.Join(dev, "mmcblk0"), "")}
		begin := time.Now()
		claims, err := planAllClaims(roles, nil)
		if err := <-attached; err != nil {
			t.Fatalf("attaching the disk: %v", err)
		}
		if err != nil {
			t.Fatalf("planning the claims: %v", err)
		}
		if len(claims) != 1 || claims[0].device != filepath.Join(dev, "mmcblk0") {
			t.Errorf("the claims are %+v, want one claim on the card", claims)
		}
		if elapsed := time.Since(begin); elapsed != declaredDiskPoll {
			t.Errorf("the wait took %s, want the first poll after the disk attached, at %s", elapsed, declaredDiskPoll)
		}
	})
}

func TestPlanAllClaimsFailsWhenADeclaredDiskNeverAttaches(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		_, dev := fakeMachine(t)

		roles := []machine.DeclaredRole{declared("machineState", filepath.Join(dev, "mmcblk0"), "")}
		begin := time.Now()
		_, err := planAllClaims(roles, nil)
		if err == nil || !strings.Contains(err.Error(), "is not attached") {
			t.Errorf("the error is %v, want one that says the disk is not attached", err)
		}
		if elapsed := time.Since(begin); elapsed != declaredDiskDeadline {
			t.Errorf("the wait ended after %s, want the deadline, %s", elapsed, declaredDiskDeadline)
		}
	})
}

func TestPlanAllClaimsGroupsTwoSpellingsOfOneDiskIntoOneClaim(t *testing.T) {
	// Neither role has a partition yet. One names the disk by kernel
	// path and the other by its by-id name.
	sys, dev := fakeMachine(t)
	dir := fakeDisk(t, sys, "vda", "pci0000:00", "0000:00:05.0", "virtio2", "block", "vda")
	writeSysfs(t, dir, "serial", "liken-a\n")
	writeSysfs(t, dir, "size", fmt.Sprintf("%d\n", (2<<30)/disks.SectorSize))
	if err := os.WriteFile(filepath.Join(dev, "vda"), make([]byte, 2_048), 0o600); err != nil {
		t.Fatalf("writing the disk's device file: %v", err)
	}

	roles := []machine.DeclaredRole{
		declared("machineState", filepath.Join(dev, "vda"), "512Mi"),
		declared("clusterState", "/dev/disk/by-id/virtio-liken-a", ""),
	}
	claims, err := planAllClaims(roles, nil)
	if err != nil {
		t.Fatalf("planning the claims: %v", err)
	}
	if len(claims) != 1 {
		t.Fatalf("got %d claims, want 1: %+v", len(claims), claims)
	}
	if len(claims[0].roles) != 2 {
		t.Errorf("the claim has roles %+v, want both roles", claims[0].roles)
	}
}
