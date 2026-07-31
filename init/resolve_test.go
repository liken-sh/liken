package main

// Tests for resolving a declared disk name in the claim path. They
// run against the same fake sysfs fixtures diskids_test.go and
// diskpaths_test.go use, because resolveDeclaredDisk recomputes a
// disk's names the same way those files do, rather than reading a
// link tree.

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/liken-sh/liken/disks"
	"github.com/liken-sh/liken/machine"
)

func TestResolveDeclaredDiskPlainKernelPath(t *testing.T) {
	sys, dev := fakeMachine(t)
	addDisk(t, sys, dev, "vda", 1<<30, nil)

	disk, err := resolveDeclaredDisk(filepath.Join(dev, "vda"))
	if err != nil {
		t.Fatal(err)
	}
	if disk == nil || disk.Name != "vda" {
		t.Errorf("got %+v, want vda", disk)
	}
}

func TestResolveDeclaredDiskByIDNameMatchesTheDiskCarryingIt(t *testing.T) {
	sys, _ := fakeMachine(t)
	dir := fakeDisk(t, sys, "sda", "pci0000:00", "0000:00:1f.2", "ata3", "host2",
		"target2:0:0", "2:0:0:0", "block", "sda")
	writeSysfs(t, filepath.Join(dir, "device"), "wwid", "naa.5002538d40a45c88\n")

	disk, err := resolveDeclaredDisk("/dev/disk/by-id/wwn-0x5002538d40a45c88")
	if err != nil {
		t.Fatal(err)
	}
	if disk == nil || disk.Name != "sda" {
		t.Errorf("got %+v, want sda", disk)
	}
}

func TestResolveDeclaredDiskByPathNameMatchesTheDiskOnThatPort(t *testing.T) {
	sys, _ := fakeMachine(t)
	fakeDisk(t, sys, "vda", "pci0000:00", "0000:00:05.0", "virtio2", "block", "vda")

	disk, err := resolveDeclaredDisk("/dev/disk/by-path/pci-0000:00:05.0")
	if err != nil {
		t.Fatal(err)
	}
	if disk == nil || disk.Name != "vda" {
		t.Errorf("got %+v, want vda", disk)
	}
}

func TestResolveDeclaredDiskUnattachedNameResolvesNil(t *testing.T) {
	fakeMachine(t) // a machine with no disks at all
	disk, err := resolveDeclaredDisk("/dev/disk/by-id/wwn-0xdeadbeef")
	if err != nil {
		t.Fatal(err)
	}
	if disk != nil {
		t.Errorf("got %+v, want no match", disk)
	}
}

func TestResolveDeclaredDiskAmbiguousNameRefusesToGuess(t *testing.T) {
	// Two disks report the same WWN, the way a clone or a disk moved
	// from another machine would. Guessing which one the spec meant
	// could partition the wrong disk and destroy whatever it holds, so
	// the resolver refuses instead of choosing.
	sys, _ := fakeMachine(t)
	for _, name := range []string{"sda", "sdb"} {
		dir := fakeDisk(t, sys, name, "pci0000:00", "0000:00:1f.2", "ata3", "host2",
			"target2:0:0", "2:0:0:0", "block", name)
		writeSysfs(t, filepath.Join(dir, "device"), "wwid", "naa.5002538d40a45c88\n")
	}

	_, err := resolveDeclaredDisk("/dev/disk/by-id/wwn-0x5002538d40a45c88")
	if err == nil {
		t.Fatal("expected an error for a name that matches two disks")
	}
	for _, want := range []string{"refusing to guess", "2 disks", "sda", "sdb"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error should mention %q: %v", want, err)
		}
	}
}

func TestResolveDeclaredDiskRefusesOtherDiskTrees(t *testing.T) {
	// Validate refuses a by-uuid name, and every tree but by-id and
	// by-path, before a spec ever reaches init. The resolver checks
	// again anyway: the code about to partition a disk must not depend
	// on an earlier gate having run.
	fakeMachine(t)
	_, err := resolveDeclaredDisk("/dev/disk/by-uuid/deadbeef-dead-beef-dead-beefdeadbeef")
	if err == nil || !strings.Contains(err.Error(), "by-uuid") {
		t.Errorf("expected a refusal naming the tree: %v", err)
	}
}

func TestPlanAllClaimsGroupsTwoSpellingsOfOneDiskIntoOneClaim(t *testing.T) {
	// One role names its disk by kernel path, and another names the
	// same disk by its by-id name. Both roles are missing, and both
	// belong to the same blank disk, so claiming must produce one plan
	// that carries both roles, not two plans fighting over one disk.
	sys, dev := fakeMachine(t)
	dir := fakeDisk(t, sys, "vda", "pci0000:00", "0000:00:05.0", "virtio2", "block", "vda")
	writeSysfs(t, dir, "serial", "liken-a\n")
	writeSysfs(t, dir, "size", fmt.Sprintf("%d\n", (2<<30)/disks.SectorSize))
	if err := os.WriteFile(filepath.Join(dev, "vda"), make([]byte, 2_048), 0o600); err != nil {
		t.Fatal(err)
	}

	roles := []machine.DeclaredRole{
		declared("machineState", filepath.Join(dev, "vda"), "512Mi"),
		declared("clusterState", "/dev/disk/by-id/virtio-liken-a", ""),
	}
	claims, err := planAllClaims(roles, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(claims) != 1 {
		t.Fatalf("got %d claims, want 1: %+v", len(claims), claims)
	}
	if len(claims[0].roles) != 2 {
		t.Errorf("the one claim should carry both roles: %+v", claims[0].roles)
	}
}
