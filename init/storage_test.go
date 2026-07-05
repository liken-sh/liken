package main

// Tests for the storage decisions: which partitions carry which
// roles, how a blank disk gets carved, and every reason a spec must
// be refused. Everything here runs as an ordinary process against
// plain files and tempdir trees, because the decisions are separable
// from the machinery they authorize. The actions themselves (writing
// a GPT through the kernel, mke2fs, the mount syscalls, and the
// power-off a refusal triggers) need a real machine, and belong to
// the QEMU harness in dev-machine/, which watches for the same
// refusal messages these tests pin, on the serial console.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/chrisguidry/liken/machine"
)

// declared builds the DeclaredRole the spec's Roles() would produce,
// letting each test state just the fields it's about.
func declared(name, device, size string) machine.DeclaredRole {
	return machine.DeclaredRole{
		Name:        name,
		StorageRole: machine.StorageRole{Device: device, Size: size},
	}
}

func TestMatchRolesFindsRolesByPartitionName(t *testing.T) {
	roles := []machine.DeclaredRole{
		declared("clusterState", "/dev/vda", ""),
		declared("podStorage", "/dev/vdb", ""),
	}
	parts := []partition{
		{name: "vda1", partName: "liken:clusterState", sizeBytes: 2 << 30},
		{name: "vdb1", partName: "some-other-os"}, // foreign, ignored
		{name: "vdb2", partName: ""},              // a table with no names at all
	}
	found, err := matchRoles(roles, parts)
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 1 {
		t.Fatalf("matched %d roles, want 1: %v", len(found), found)
	}
	if found["clusterState"].name != "vda1" {
		t.Errorf("clusterState matched %q, want vda1", found["clusterState"].name)
	}
}

func TestMatchRolesRefusesDuplicatePartitionNames(t *testing.T) {
	// Two partitions with the same role name is what a cloned or
	// transplanted disk looks like; guessing which one holds the real
	// cluster would destroy data on the other.
	roles := []machine.DeclaredRole{declared("clusterState", "/dev/vda", "")}
	parts := []partition{
		{name: "vda1", partName: "liken:clusterState"},
		{name: "vdb1", partName: "liken:clusterState"},
	}
	_, err := matchRoles(roles, parts)
	if err == nil {
		t.Fatal("expected an error for duplicate partition names")
	}
	for _, want := range []string{"liken:clusterState", "vda1", "vdb1"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error should mention %q: %v", want, err)
		}
	}
}

func TestPlanPartitionsPacksSizedRolesAligned(t *testing.T) {
	// A 2 GiB disk (4,194,304 sectors) carrying a 1 KiB role, a
	// 512 MiB role, and a remainder. The 1 KiB role occupies two
	// sectors but the next role still starts on the following MiB
	// boundary; the remainder runs to the last usable sector.
	mine := []machine.DeclaredRole{
		declared("machineEphemeral", "/dev/vdb", "1Ki"),
		declared("podStorage", "/dev/vdb", "512Mi"),
		declared("podEphemeral", "/dev/vdb", ""),
	}
	parts, err := planPartitions("/dev/vdb", mine, 4_194_304)
	if err != nil {
		t.Fatal(err)
	}
	want := []gptPartition{
		{name: "liken:machineEphemeral", firstLBA: 2_048, lastLBA: 2_049},
		{name: "liken:podStorage", firstLBA: 4_096, lastLBA: 1_052_671},
		{name: "liken:podEphemeral", firstLBA: 1_052_672, lastLBA: 4_194_269},
	}
	if len(parts) != len(want) {
		t.Fatalf("planned %d partitions, want %d: %v", len(parts), len(want), parts)
	}
	for i, w := range want {
		if parts[i] != w {
			t.Errorf("partition %d: got %+v, want %+v", i, parts[i], w)
		}
	}
}

func TestPlanPartitionsRejectsDiskTooSmall(t *testing.T) {
	// 4,096 sectors is 2 MiB of disk, but the table's reservations
	// leave less than that usable; a 2 MiB role can't fit.
	mine := []machine.DeclaredRole{declared("clusterState", "/dev/vda", "2Mi")}
	_, err := planPartitions("/dev/vda", mine, 4_096)
	if err == nil {
		t.Fatal("expected an error for a role bigger than the disk")
	}
	for _, want := range []string{"too small", "clusterState", "2Mi"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error should mention %q: %v", want, err)
		}
	}
}

func TestPlanPartitionsRejectsNoRoomForRemainder(t *testing.T) {
	// The sized role fits exactly, to the last usable sector; the
	// remainder role is left with nothing.
	mine := []machine.DeclaredRole{
		declared("machineEphemeral", "/dev/vdb", "1Mi"),
		declared("podStorage", "/dev/vdb", ""),
	}
	_, err := planPartitions("/dev/vdb", mine, 4_130)
	if err == nil {
		t.Fatal("expected an error when nothing is left for the remainder")
	}
	if !strings.Contains(err.Error(), "podStorage") {
		t.Errorf("error should name the role that lost out: %v", err)
	}
}

// signedDevice writes a fake device file: 2 KiB of zeros with an
// optional signature stamped in, which is all isBlank and hasExt4
// ever read.
func signedDevice(t *testing.T, stamp func(b []byte)) string {
	t.Helper()
	buf := make([]byte, 2_048)
	if stamp != nil {
		stamp(buf)
	}
	path := filepath.Join(t.TempDir(), "disk")
	if err := os.WriteFile(path, buf, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestIsBlank(t *testing.T) {
	cases := []struct {
		name  string
		stamp func(b []byte)
		blank bool
	}{
		{"zeros", nil, true},
		{"mbr", func(b []byte) { b[510], b[511] = 0x55, 0xAA }, false},
		{"gpt", func(b []byte) { copy(b[512:], "EFI PART") }, false},
		{"ext4", func(b []byte) { b[1080], b[1081] = 0x53, 0xEF }, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			blank, err := isBlank(signedDevice(t, c.stamp))
			if err != nil {
				t.Fatal(err)
			}
			if blank != c.blank {
				t.Errorf("isBlank = %v, want %v", blank, c.blank)
			}
		})
	}
}

func TestIsBlankReportsMissingDevices(t *testing.T) {
	if _, err := isBlank(filepath.Join(t.TempDir(), "absent")); err == nil {
		t.Error("expected an error for a device that doesn't exist")
	}
}

func TestIsBlankReportsUnreadableDevices(t *testing.T) {
	// A device that can't supply even the first 2 KiB can't be judged
	// blank, and claiming an unjudgeable disk is not allowed.
	path := filepath.Join(t.TempDir(), "truncated")
	if err := os.WriteFile(path, make([]byte, 100), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := isBlank(path); err == nil {
		t.Error("expected an error for a device too short to probe")
	}
}

func TestHasExt4(t *testing.T) {
	withMagic := signedDevice(t, func(b []byte) { b[1080], b[1081] = 0x53, 0xEF })
	if !hasExt4(withMagic) {
		t.Error("expected the superblock magic to be recognized")
	}
	if hasExt4(signedDevice(t, nil)) {
		t.Error("zeros are not an ext4 filesystem")
	}
	if hasExt4(filepath.Join(t.TempDir(), "absent")) {
		t.Error("a missing device is not an ext4 filesystem")
	}
}

func TestReconcileStorageEmptySpecStaysInMemory(t *testing.T) {
	status, err := reconcileStorage(machine.StorageSpec{})
	if err != nil {
		t.Fatal(err)
	}
	if status != machine.AllRolesInMemory() {
		t.Errorf("an empty spec should leave every role in memory: %+v", status)
	}
}

func TestReconcileStorageRejectsInvalidSpec(t *testing.T) {
	// Validation runs before any discovery, so a bad spec is refused
	// without this test needing a fake machine at all.
	_, err := reconcileStorage(machine.StorageSpec{ClusterState: &machine.StorageRole{}})
	if err == nil {
		t.Fatal("expected an error for a role with no device")
	}
	if !strings.Contains(err.Error(), "clusterState") {
		t.Errorf("error should name the role: %v", err)
	}
}

func TestPlanClaimRefusesPartialClaim(t *testing.T) {
	// One of this disk's roles was recognized but another is missing:
	// the table was liken's and then something changed, which is not
	// safe to repair automatically.
	roles := []machine.DeclaredRole{
		declared("clusterState", "/dev/vda", "1Mi"),
		declared("podStorage", "/dev/vda", ""),
	}
	found := map[string]partition{
		"clusterState": {name: "vda1", partName: "liken:clusterState"},
	}
	_, err := planClaim("/dev/vda", roles, found)
	if err == nil {
		t.Fatal("expected an error for a partially-claimed disk")
	}
	for _, want := range []string{"liken:clusterState", "refusing to modify"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error should mention %q: %v", want, err)
		}
	}
}

func TestPlanClaimRefusesUnattachedDevices(t *testing.T) {
	fakeMachine(t) // a machine with no disks at all
	roles := []machine.DeclaredRole{declared("clusterState", "/dev/vda", "")}
	_, err := planClaim("/dev/vda", roles, nil)
	if err == nil || !strings.Contains(err.Error(), "not attached") {
		t.Errorf("expected a not-attached error: %v", err)
	}
}

func TestPlanClaimRefusesForeignDisks(t *testing.T) {
	cases := []struct {
		name  string
		stamp func(b []byte)
	}{
		{"mbr", func(b []byte) { b[510], b[511] = 0x55, 0xAA }},
		{"gpt", func(b []byte) { copy(b[512:], "EFI PART") }},
		{"ext4", func(b []byte) { b[1080], b[1081] = 0x53, 0xEF }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			sys, dev := fakeMachine(t)
			contents := make([]byte, 2_048)
			c.stamp(contents)
			addDisk(t, sys, dev, "vda", 1<<30, contents)
			device := filepath.Join(dev, "vda")
			roles := []machine.DeclaredRole{declared("clusterState", device, "")}
			_, err := planClaim(device, roles, nil)
			if err == nil || !strings.Contains(err.Error(), "refusing to touch") {
				t.Errorf("expected a refusal for a foreign disk: %v", err)
			}
		})
	}
}

func TestPlanClaimReportsUnreadableDevices(t *testing.T) {
	// The disk shows up in sysfs but its device node can't supply
	// enough bytes to judge blankness; an unjudgeable disk must not
	// be claimed.
	sys, dev := fakeMachine(t)
	addDisk(t, sys, dev, "vda", 1<<30, make([]byte, 100))
	device := filepath.Join(dev, "vda")
	roles := []machine.DeclaredRole{declared("clusterState", device, "")}
	_, err := planClaim(device, roles, nil)
	if err == nil || !strings.Contains(err.Error(), "examining") {
		t.Errorf("expected an examination error: %v", err)
	}
}

func TestWaitForPartitionsReportsMissingPartitions(t *testing.T) {
	sys, dev := fakeMachine(t)
	addDisk(t, sys, dev, "vda", 1<<30, nil)
	addPartition(t, sys, "vda", "vda1", "liken:clusterState", 1<<20)

	parts := []gptPartition{
		// clusterState's extent matches the 1 MiB the fixture reports.
		{name: "liken:clusterState", firstLBA: 2_048, lastLBA: 2_048 + (1<<20)/sectorSize - 1},
		{name: "liken:podStorage", firstLBA: 4_096, lastLBA: 8_191},
	}
	err := waitForPartitions(parts, 50*time.Millisecond)
	if err == nil {
		t.Fatal("expected an error when a partition never appears")
	}
	if !strings.Contains(err.Error(), "liken:podStorage") {
		t.Errorf("error should name the missing partition: %v", err)
	}
	if strings.Contains(err.Error(), "liken:clusterState") {
		t.Errorf("error should not name the partition that did appear: %v", err)
	}
}

func TestWaitForPartitionsReportsStaleSizes(t *testing.T) {
	// The partition exists but sysfs still shows its old geometry: as
	// wrong as no partition at all, and named as such.
	sys, dev := fakeMachine(t)
	addDisk(t, sys, dev, "vda", 1<<30, nil)
	addPartition(t, sys, "vda", "vda1", "liken:clusterState", 1<<20)

	parts := []gptPartition{
		{name: "liken:clusterState", firstLBA: 2_048, lastLBA: 2_048 + (2<<20)/sectorSize - 1},
	}
	err := waitForPartitions(parts, 50*time.Millisecond)
	if err == nil {
		t.Fatal("expected an error for a stale partition size")
	}
	if !strings.Contains(err.Error(), "liken:clusterState") || !strings.Contains(err.Error(), "still") {
		t.Errorf("error should describe the stale size: %v", err)
	}
}

func TestMountRoleRejectsUnknownRoleVocabulary(t *testing.T) {
	// The mount translation is checked before anything touches the
	// partition, so an unknown role is refused without this test
	// needing a device to exist.
	err := mountRole(declared("archive", "/dev/vda", ""), partition{name: "vda9"})
	if err == nil || !strings.Contains(err.Error(), "no mount translation") {
		t.Errorf("expected a vocabulary error: %v", err)
	}
}
