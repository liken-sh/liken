package main

// Tests for the installer's pieces: payload verification, durable
// copies, partition addressing, and the boot entries it writes. The
// full install (claiming a real disk, powering off) is QEMU
// territory; everything here runs against temp files, the fake
// sysfs, and the fake efivarfs.

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/liken-sh/liken/machine"
)

func TestPartitionNumber(t *testing.T) {
	cases := []struct {
		part partition
		want uint32
	}{
		{partition{name: "vda1", disk: "vda"}, 1},
		{partition{name: "vdc2", disk: "vdc"}, 2},
		{partition{name: "nvme0n1p3", disk: "nvme0n1"}, 3},
	}
	for _, c := range cases {
		got, err := partitionNumber(c.part)
		if err != nil {
			t.Errorf("%s: %v", c.part.name, err)
		}
		if got != c.want {
			t.Errorf("%s: got %d, want %d", c.part.name, got, c.want)
		}
	}
}

func TestPartitionNumberRefusesMalformedNames(t *testing.T) {
	// An index the kernel's node name can't supply must stop the
	// install: 0 is not a valid GPT slot, and a boot entry carrying it
	// would be garbage the firmware trusts.
	cases := []partition{
		{name: "vda", disk: "vda"},         // no suffix at all
		{name: "vdap", disk: "vda"},        // separator with no number
		{name: "vdaXY", disk: "vda"},       // non-numeric suffix
		{name: "mmcblk0", disk: "mmcblk1"}, // name doesn't extend the disk's
	}
	for _, c := range cases {
		if _, err := partitionNumber(c); err == nil {
			t.Errorf("%s on %s: expected an error", c.name, c.disk)
		}
	}
}

// artifactFor writes contents to dir under name and returns the
// ReleaseArtifact describing exactly those bytes.
func artifactFor(t *testing.T, dir, name string, contents []byte) machine.ReleaseArtifact {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), contents, 0o644); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(contents)
	return machine.ReleaseArtifact{
		Name:   name,
		SHA256: hex.EncodeToString(sum[:]),
		Size:   int64(len(contents)),
	}
}

func TestVerifyFile(t *testing.T) {
	dir := t.TempDir()
	artifact := artifactFor(t, dir, "vmlinuz", []byte("the kernel"))
	if err := verifyFile(artifact, filepath.Join(dir, "vmlinuz")); err != nil {
		t.Errorf("matching bytes verify: %v", err)
	}
	artifact.SHA256 = "0000000000000000000000000000000000000000000000000000000000000000"
	if err := verifyFile(artifact, filepath.Join(dir, "vmlinuz")); err == nil {
		t.Error("a wrong digest must fail verification")
	}
	if err := verifyFile(artifact, filepath.Join(dir, "missing")); err == nil {
		t.Error("a missing file must fail verification")
	}
}

func TestCopyDurably(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "source")
	if err := os.WriteFile(source, []byte("payload"), 0o644); err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(dir, "dest")
	if err := copyDurably(source, dest); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(dest)
	if err != nil || string(got) != "payload" {
		t.Errorf("the copy carries the bytes: %q, %v", got, err)
	}
	if _, err := os.Stat(dest + ".partial"); err == nil {
		t.Error("no .partial file may remain after the rename")
	}
	if err := copyDurably(filepath.Join(dir, "missing"), dest); err == nil {
		t.Error("a missing source is an error")
	}
}

func TestConsoleArgs(t *testing.T) {
	fakeCmdline(t, "console=ttyS0 console=tty0 rdinit=/liken liken.machine=node-1\n")
	got := consoleArgs()
	want := []string{"console=ttyS0", "console=tty0"}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("every console= argument is copied, nothing else: %v", got)
	}
}

func TestWriteSlotBootEntry(t *testing.T) {
	fakeCmdline(t, "console=ttyS0 rdinit=/liken\n")
	dir := fakeEFIVars(t, map[string][]byte{})
	part := &slotPartition{number: 1, firstLBA: 2048, lastLBA: 4095}

	number, err := writeSlotBootEntry(dir, "liken slot A", "A", part, "node-1")
	if err != nil {
		t.Fatal(err)
	}
	option, ok := listBootEntries(dir)[number]
	if !ok {
		t.Fatalf("the entry decodes back out of the store")
	}
	if option.description != "liken slot A" {
		t.Errorf("description: got %q", option.description)
	}
	if option.filePath != `\vmlinuz` {
		t.Errorf("file path: got %q", option.filePath)
	}
	wantArgs := `console=ttyS0 rdinit=/liken initrd=\liken.cpio liken.machine=node-1 liken.slot=A panic=10`
	if !bytes.Equal(option.optionalData, encodeUTF16Z(wantArgs)) {
		t.Errorf("the baked command line is assembled from scratch: % x", option.optionalData)
	}
	if option.hardDrive == nil || option.hardDrive.partitionNumber != 1 ||
		option.hardDrive.sectors != 2048 {
		t.Errorf("the entry pins the partition: %+v", option.hardDrive)
	}
}

// installedDisk builds the fake machine an install expects to find:
// one boot disk whose GPT carries both system slots, mirrored into
// the fake sysfs the way the kernel would surface it. The table's
// chunks are written into the fake device file directly, because
// writeGPT's kernel re-read ioctl only works on real block devices.
func installedDisk(t *testing.T) (sys, dev string) {
	t.Helper()
	sys, dev = fakeMachine(t)
	const totalSectors = 1 << 16 // a 32MiB disk is plenty for a table
	addDisk(t, sys, dev, "vdc", totalSectors*sectorSize, make([]byte, totalSectors*sectorSize))
	table := &gptTable{
		diskGUID: mustGUID("11111111-2222-3333-4455-66778899AABB"),
		entries: []gptEntry{
			{typeGUID: efiSystemPartition, uniqueGUID: mustGUID("AAAAAAAA-BBBB-CCCC-DDEE-FF0011223344"),
				firstLBA: 2048, lastLBA: 18431, name: "liken:systemA"},
			{typeGUID: efiSystemPartition, uniqueGUID: mustGUID("99999999-8888-7777-6655-443322110000"),
				firstLBA: 18432, lastLBA: 34815, name: "liken:systemB"},
		},
	}
	chunks, err := serializeGPT(table, totalSectors)
	if err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(filepath.Join(dev, "vdc"), os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	for _, chunk := range chunks {
		if _, err := f.WriteAt(chunk.data, int64(chunk.lba)*sectorSize); err != nil {
			t.Fatal(err)
		}
	}
	addPartition(t, sys, "vdc", "vdc1", "liken:systemA", 16384*sectorSize)
	addPartition(t, sys, "vdc", "vdc2", "liken:systemB", 16384*sectorSize)
	return sys, dev
}

func TestFindSlotPartition(t *testing.T) {
	installedDisk(t)
	parts := discoverPartitions()

	slot, err := findSlotPartition(parts, machine.SystemARole)
	if err != nil {
		t.Fatal(err)
	}
	if slot.number != 1 || slot.firstLBA != 2048 || slot.lastLBA != 18431 {
		t.Errorf("slot A's identity comes from the table: %+v", slot)
	}
	if slot.guid == ([16]byte{}) {
		t.Error("the entry's unique GUID is what pins the boot entry")
	}
}

func TestFindSlotPartitionRefusesAMissingSlot(t *testing.T) {
	fakeMachine(t)
	if _, err := findSlotPartition(nil, machine.SystemBRole); err == nil {
		t.Error("no partition carries the slot; the install must refuse")
	}
}

// fakeSlotAMount points the systemA role's mountpoint at a tempdir,
// standing in for the filesystem storage reconciliation would have
// mounted, and restores the real translation afterward.
func fakeSlotAMount(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	old := roleMounts[machine.SystemARole]
	rm := old
	rm.path = dir
	roleMounts[machine.SystemARole] = rm
	t.Cleanup(func() { roleMounts[machine.SystemARole] = old })
	return dir
}

// fakePayload assembles an install payload directory: the artifacts
// and the release document that vouches for them.
func fakePayload(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	kernel := artifactFor(t, dir, "vmlinuz", []byte("the kernel"))
	cpio := artifactFor(t, dir, "liken.cpio", []byte("the rest of the OS"))
	release := fmt.Sprintf(`apiVersion: liken.sh/v1alpha1
kind: Release
metadata:
  name: 0.9.9
artifacts:
- name: %s
  sha256: %s
  size: %d
- name: %s
  sha256: %s
  size: %d
`, kernel.Name, kernel.SHA256, kernel.Size, cpio.Name, cpio.SHA256, cpio.Size)
	if err := os.WriteFile(filepath.Join(dir, "release.yaml"), []byte(release), 0o644); err != nil {
		t.Fatal(err)
	}
	old := releasePayloadDir
	releasePayloadDir = dir
	t.Cleanup(func() { releasePayloadDir = old })
	return dir
}

// fakeFirmware points the efivars path at a fake store so the
// install's boot-entry writes land somewhere observable.
func fakeFirmware(t *testing.T, vars map[string][]byte) string {
	t.Helper()
	dir := fakeEFIVars(t, vars)
	old := efiVarsDir
	efiVarsDir = dir
	t.Cleanup(func() { efiVarsDir = old })
	return dir
}

func TestInstallToDisk(t *testing.T) {
	installedDisk(t)
	fakePayload(t)
	slotMount := fakeSlotAMount(t)
	fakeCmdline(t, "console=ttyS0 rdinit=/liken liken.install\n")
	// The firmware already had an entry of its own; it must survive,
	// after ours, in BootOrder.
	firmware := fakeFirmware(t, map[string][]byte{
		"Boot0000":  encodeLoadOption(loadOption{attributes: loadOptionActive, description: "UEFI Shell"}),
		"BootOrder": u16le(0x0000),
	})

	if err := installToDisk("node-1"); err != nil {
		t.Fatal(err)
	}

	for _, name := range []string{"vmlinuz", "liken.cpio"} {
		if _, err := os.Stat(filepath.Join(slotMount, name)); err != nil {
			t.Errorf("%s lands on the slot: %v", name, err)
		}
	}
	entries := listBootEntries(firmware)
	var a, b uint16
	found := 0
	for n, option := range entries {
		switch option.description {
		case "liken slot A":
			a, found = n, found+1
		case "liken slot B":
			b, found = n, found+1
		}
	}
	if found != 2 {
		t.Fatalf("both slots get entries: %+v", entries)
	}
	order := readBootOrder(firmware)
	if len(order) != 3 || order[0] != a || order[1] != b || order[2] != 0x0000 {
		t.Errorf("BootOrder prefers slot A, then B, then the firmware's own: %v", order)
	}
}

func TestInstallToDiskNeedsAName(t *testing.T) {
	if err := installToDisk(""); err == nil {
		t.Error("an anonymous install would be wrong on every later boot")
	}
}

func TestInstallToDiskRefusesACorruptPayload(t *testing.T) {
	installedDisk(t)
	dir := fakePayload(t)
	fakeSlotAMount(t)
	fakeFirmware(t, map[string][]byte{})
	// One byte flipped in an artifact: the copy must never start.
	if err := os.WriteFile(filepath.Join(dir, "vmlinuz"), []byte("the kernal"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := installToDisk("node-1"); err == nil {
		t.Error("a payload that fails its own release document must refuse to install")
	}
}

func TestCopyDurablyReportsAnUnwritableDestination(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "source")
	if err := os.WriteFile(source, []byte("payload"), 0o644); err != nil {
		t.Fatal(err)
	}
	sealed := filepath.Join(dir, "sealed")
	if err := os.Mkdir(sealed, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(sealed, 0o755) })
	if err := copyDurably(source, filepath.Join(sealed, "dest")); err == nil {
		t.Error("an unwritable slot is an error the install must surface")
	}
}

func TestInstallToDiskNeedsBothSlots(t *testing.T) {
	// A disk carrying only slot A: the design depends on a fallback
	// slot being registered from the start, so the install refuses.
	sys, dev := fakeMachine(t)
	const totalSectors = 1 << 16
	addDisk(t, sys, dev, "vdc", totalSectors*sectorSize, make([]byte, totalSectors*sectorSize))
	addPartition(t, sys, "vdc", "vdc1", "liken:systemA", 16384*sectorSize)
	fakePayload(t)
	if err := installToDisk("node-1"); err == nil {
		t.Error("one slot is not blue-green; the install must refuse")
	}
}

func TestInstallToDiskNeedsItsReleaseDocument(t *testing.T) {
	installedDisk(t)
	old := releasePayloadDir
	releasePayloadDir = filepath.Join(t.TempDir(), "no-payload")
	t.Cleanup(func() { releasePayloadDir = old })

	err := installToDisk("node-1")
	if err == nil || !strings.Contains(err.Error(), "release document") {
		t.Errorf("a payload without its document must refuse: %v", err)
	}
}

func TestInstallToDiskRefusesAGarbageReleaseDocument(t *testing.T) {
	installedDisk(t)
	dir := fakePayload(t)
	if err := os.WriteFile(filepath.Join(dir, "release.yaml"), []byte("{not yaml"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := installToDisk("node-1"); err == nil {
		t.Error("a release document that won't parse must refuse the install")
	}
}

func TestCopyDurablyReportsAMissingSource(t *testing.T) {
	dir := t.TempDir()
	err := copyDurably(filepath.Join(dir, "absent"), filepath.Join(dir, "dest"))
	if err == nil {
		t.Error("a source that doesn't exist can't be copied")
	}
}

func TestConsoleArgsWithNoCommandLine(t *testing.T) {
	fakeCmdline(t, "")
	if got := consoleArgs(); got != nil {
		t.Errorf("no console= arguments, no copies: %v", got)
	}
}
