package main

// Tests for the installer's pieces: payload verification, durable
// copies, partition addressing, and the boot entries it writes. The
// full install (claiming a real disk, powering off) runs only under
// QEMU. Everything here runs against temp files, the fake sysfs, and
// the fake efivarfs.

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/liken-sh/liken/disks"
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
	// An index that the kernel's node name cannot supply must stop
	// the install. 0 is not a valid GPT slot, and a boot entry that
	// carries it would be garbage that the firmware trusts.
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
	wantArgs := `console=ttyS0 rdinit=/liken initrd=\microcode.cpio initrd=\boot.cpio initrd=\deployment.cpio liken.machine=node-1 liken.slot=A panic=10`
	if !bytes.Equal(option.optionalData, encodeUTF16Z(wantArgs)) {
		t.Errorf("the baked command line is assembled from scratch: % x", option.optionalData)
	}
	if option.hardDrive == nil || option.hardDrive.partitionNumber != 1 ||
		option.hardDrive.sectors != 2048 {
		t.Errorf("the entry pins the partition: %+v", option.hardDrive)
	}
}

// installedDisk builds the fake machine that an install expects to
// find: one boot disk whose GPT carries both system slots, mirrored
// into the fake sysfs the way the kernel would present it. The
// table's chunks are written into the fake device file directly,
// because disks.Write's kernel re-read ioctl only works on real
// block devices.
func installedDisk(t *testing.T) (sys, dev string) {
	t.Helper()
	sys, dev = fakeMachine(t)
	const totalSectors = 1 << 16 // a 32MiB disk is plenty for a table
	addDisk(t, sys, dev, "vdc", totalSectors*disks.SectorSize, make([]byte, totalSectors*disks.SectorSize))
	table := &disks.Table{
		DiskGUID: disks.MustGUID("11111111-2222-3333-4455-66778899AABB"),
		Entries: []disks.Entry{
			{TypeGUID: disks.EFISystemPartition, UniqueGUID: disks.MustGUID("AAAAAAAA-BBBB-CCCC-DDEE-FF0011223344"),
				FirstLBA: 2048, LastLBA: 18431, Name: "liken:systemA"},
			{TypeGUID: disks.EFISystemPartition, UniqueGUID: disks.MustGUID("99999999-8888-7777-6655-443322110000"),
				FirstLBA: 18432, LastLBA: 34815, Name: "liken:systemB"},
		},
	}
	chunks, err := disks.SerializeGPT(table, totalSectors)
	if err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(filepath.Join(dev, "vdc"), os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	for _, chunk := range chunks {
		if _, err := f.WriteAt(chunk.Data, int64(chunk.LBA)*disks.SectorSize); err != nil {
			t.Fatal(err)
		}
	}
	addPartition(t, sys, "vdc", "vdc1", "liken:systemA", 16384*disks.SectorSize)
	addPartition(t, sys, "vdc", "vdc2", "liken:systemB", 16384*disks.SectorSize)
	return sys, dev
}

// installedBIOSDisk is installedDisk with the two GRUB roles ahead of
// the slots. This is the disk of a machine whose spec declared those
// roles.
func installedBIOSDisk(t *testing.T) (sys, dev string) {
	t.Helper()
	sys, dev = fakeMachine(t)
	const totalSectors = 1 << 16
	addDisk(t, sys, dev, "vdc", totalSectors*disks.SectorSize, make([]byte, totalSectors*disks.SectorSize))
	table := &disks.Table{
		DiskGUID: disks.MustGUID("11111111-2222-3333-4455-66778899AABB"),
		Entries: []disks.Entry{
			{TypeGUID: disks.BIOSBootPartition, UniqueGUID: disks.MustGUID("0B105B00-0000-4000-8000-000000000001"),
				FirstLBA: 2048, LastLBA: 4095, Name: "liken:biosBoot"},
			{TypeGUID: disks.LinuxFilesystemData, UniqueGUID: disks.MustGUID("0B105B00-0000-4000-8000-000000000002"),
				FirstLBA: 4096, LastLBA: 20479, Name: "liken:bootHome"},
			{TypeGUID: disks.EFISystemPartition, UniqueGUID: disks.MustGUID("AAAAAAAA-BBBB-CCCC-DDEE-FF0011223344"),
				FirstLBA: 20480, LastLBA: 36863, Name: "liken:systemA"},
			{TypeGUID: disks.EFISystemPartition, UniqueGUID: disks.MustGUID("99999999-8888-7777-6655-443322110000"),
				FirstLBA: 36864, LastLBA: 53247, Name: "liken:systemB"},
		},
	}
	chunks, err := disks.SerializeGPT(table, totalSectors)
	if err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(filepath.Join(dev, "vdc"), os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	for _, chunk := range chunks {
		if _, err := f.WriteAt(chunk.Data, int64(chunk.LBA)*disks.SectorSize); err != nil {
			t.Fatal(err)
		}
	}
	addPartition(t, sys, "vdc", "vdc1", "liken:biosBoot", 2048*disks.SectorSize)
	addPartition(t, sys, "vdc", "vdc2", "liken:bootHome", 16384*disks.SectorSize)
	addPartition(t, sys, "vdc", "vdc3", "liken:systemA", 16384*disks.SectorSize)
	addPartition(t, sys, "vdc", "vdc4", "liken:systemB", 16384*disks.SectorSize)
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

// fakeSlotAMount points the systemA role's mountpoint at a temporary
// directory, which substitutes for the filesystem that storage
// reconciliation would have mounted. It restores the real mapping
// afterward.
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

// fakePayload assembles an install payload directory: the artifacts,
// the release document that vouches for them, and the deployment
// layer beside its sidecar.
func fakePayload(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	kernel := artifactFor(t, dir, "vmlinuz", []byte("the kernel"))
	cpio := artifactFor(t, dir, "liken.sqfs", []byte("the rest of the OS"))
	// The GRUB pair that every release carries: a full boot.img sector
	// and a small core image with the load segment where diskboot
	// expects it. This is enough for the patcher to accept them.
	bootImgBytes := bytes.Repeat([]byte{0xB0}, disks.SectorSize)
	coreImgBytes := bytes.Repeat([]byte{0xC0}, 3*disks.SectorSize)
	binary.LittleEndian.PutUint16(coreImgBytes[grubBlocklistSegment:], grubLoadSegment)
	bootImg := artifactFor(t, dir, "grub-boot.img", bootImgBytes)
	coreImg := artifactFor(t, dir, "grub-core.img", coreImgBytes)
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
- name: %s
  sha256: %s
  size: %d
- name: %s
  sha256: %s
  size: %d
`, kernel.Name, kernel.SHA256, kernel.Size, cpio.Name, cpio.SHA256, cpio.Size,
		bootImg.Name, bootImg.SHA256, bootImg.Size, coreImg.Name, coreImg.SHA256, coreImg.Size)
	if err := os.WriteFile(filepath.Join(dir, "release.yaml"), []byte(release), 0o644); err != nil {
		t.Fatal(err)
	}

	layer := []byte("the deployment layer")
	if err := os.WriteFile(filepath.Join(dir, machine.LayerName), layer, 0o644); err != nil {
		t.Fatal(err)
	}
	digest, err := machine.DigestLayer(bytes.NewReader(layer))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, machine.LayerSidecarName), machine.FormatLayerSidecar(digest), 0o644); err != nil {
		t.Fatal(err)
	}

	old := releasePayloadDir
	releasePayloadDir = dir
	t.Cleanup(func() { releasePayloadDir = old })
	return dir
}

// fakeFirmware points the efivars path at a fake store, so the
// install's boot-entry writes land somewhere observable, and pins
// the regime to UEFI. The store's directory existing satisfies the
// same test that the kernel's /sys/firmware/efi satisfies, so the
// test means the same thing on any machine that runs it.
func fakeFirmware(t *testing.T, vars map[string][]byte) string {
	t.Helper()
	dir := fakeEFIVars(t, vars)
	old, oldSys := efiVarsDir, efiSysDir
	efiVarsDir, efiSysDir = dir, dir
	t.Cleanup(func() { efiVarsDir, efiSysDir = old, oldSys })
	return dir
}

// fakeBIOSRegime pins the regime to BIOS: no firmware directory and
// no boot variables. This is exactly what a machine booted by legacy
// firmware looks like to the kernel.
func fakeBIOSRegime(t *testing.T) {
	t.Helper()
	oldSys := efiSysDir
	efiSysDir = filepath.Join(t.TempDir(), "no-efi-here")
	t.Cleanup(func() { efiSysDir = oldSys })
}

// fakeBootHomeMount points the bootHome role's mountpoint at a
// temporary directory, the same substitution that fakeSlotAMount
// provides for slot A.
func fakeBootHomeMount(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	old := roleMounts[machine.BootHomeRole]
	rm := old
	rm.path = dir
	roleMounts[machine.BootHomeRole] = rm
	t.Cleanup(func() { roleMounts[machine.BootHomeRole] = old })
	return dir
}

func TestInstallToDisk(t *testing.T) {
	installedDisk(t)
	fakePayload(t)
	slotMount := fakeSlotAMount(t)
	fakeCmdline(t, "console=ttyS0 rdinit=/liken liken.install\n")
	// The firmware already had an entry of its own. This test checks
	// that the entry survives in BootOrder, after this machine's own
	// entries.
	firmware := fakeFirmware(t, map[string][]byte{
		"Boot0000":  encodeLoadOption(loadOption{attributes: loadOptionActive, description: "UEFI Shell"}),
		"BootOrder": u16le(0x0000),
	})

	if err := installToDisk("node-1"); err != nil {
		t.Fatal(err)
	}

	for _, name := range []string{"vmlinuz", "liken.sqfs", machine.LayerName, machine.LayerSidecarName} {
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
		t.Fatalf("both slots get Entries: %+v", entries)
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

func TestInstallToDiskLaysDownGRUB(t *testing.T) {
	_, dev := installedBIOSDisk(t)
	fakePayload(t)
	fakeSlotAMount(t)
	home := fakeBootHomeMount(t)
	fakeCmdline(t, "console=ttyS0 rdinit=/liken liken.install\n")
	fakeBIOSRegime(t)

	if err := installToDisk("node-1"); err != nil {
		t.Fatal(err)
	}

	disk, err := os.Open(filepath.Join(dev, "vdc"))
	if err != nil {
		t.Fatal(err)
	}
	defer disk.Close()
	sector := make([]byte, disks.SectorSize)
	if _, err := disk.ReadAt(sector, 0); err != nil {
		t.Fatal(err)
	}
	// The MBR carries the patched boot code: the core image's LBA at
	// its fixed offset, and the payload's own bytes elsewhere.
	if got := binary.LittleEndian.Uint64(sector[grubKernelSectorOffset:]); got != 2048 {
		t.Errorf("the boot code should point at the biosBoot partition: sector %d", got)
	}
	if sector[0] != 0xB0 {
		t.Error("the boot code's unpatched bytes should be the release's grub-boot.img")
	}
	// Sector 0's tail still belongs to the GPT: the protective entry
	// and the signature stay intact.
	if sector[446+4] != 0xEE || sector[510] != 0x55 || sector[511] != 0xAA {
		t.Error("the install must not disturb the protective MBR")
	}
	core := make([]byte, disks.SectorSize)
	if _, err := disk.ReadAt(core, 2048*disks.SectorSize); err != nil {
		t.Fatal(err)
	}
	if core[0] != 0xC0 {
		t.Error("the core image should land at the biosBoot partition's start")
	}
	if got := binary.LittleEndian.Uint64(core[grubBlocklistStart:]); got != 2049 {
		t.Errorf("the core image's blocklist should be patched: start %d", got)
	}

	cfg, err := os.ReadFile(filepath.Join(home, "grub", "grub.cfg"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(cfg), "liken.machine=node-1") {
		t.Error("grub.cfg should carry the machine's identity")
	}
	vars, err := readGRUBEnv(filepath.Join(home, "grub", "grubenv"))
	if err != nil {
		t.Fatal(err)
	}
	if vars["default_slot"] != "A" {
		t.Errorf("a fresh install prefers slot A: %v", vars)
	}
}

func TestInstallToDiskRefusesWithoutAnyActuator(t *testing.T) {
	// A BIOS machine whose spec declares no GRUB roles: nothing could
	// ever boot the installed disk. The install must report this
	// instead of powering off a machine that would never come back.
	installedDisk(t)
	fakePayload(t)
	fakeSlotAMount(t)
	fakeCmdline(t, "console=ttyS0 rdinit=/liken liken.install\n")
	fakeBIOSRegime(t)

	err := installToDisk("node-1")
	if err == nil || !strings.Contains(err.Error(), "biosBoot") {
		t.Errorf("an install with no actuator must be refused with the reason: %v", err)
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
	addDisk(t, sys, dev, "vdc", totalSectors*disks.SectorSize, make([]byte, totalSectors*disks.SectorSize))
	addPartition(t, sys, "vdc", "vdc1", "liken:systemA", 16384*disks.SectorSize)
	fakePayload(t)
	if err := installToDisk("node-1"); err == nil {
		t.Error("one slot is not blue-green; the install must refuse")
	}
}

func TestInstallToDiskNeedsTheLayerSidecar(t *testing.T) {
	installedDisk(t)
	dir := fakePayload(t)
	fakeSlotAMount(t)
	fakeFirmware(t, map[string][]byte{})
	if err := os.Remove(filepath.Join(dir, machine.LayerSidecarName)); err != nil {
		t.Fatal(err)
	}
	err := installToDisk("node-1")
	if err == nil || !strings.Contains(err.Error(), "sidecar") {
		t.Errorf("media without the layer's sidecar is incomplete and must refuse: %v", err)
	}
}

func TestInstallToDiskRefusesATornLayer(t *testing.T) {
	installedDisk(t)
	dir := fakePayload(t)
	fakeSlotAMount(t)
	fakeFirmware(t, map[string][]byte{})
	// A zero-length layer with an intact sidecar: this is the shape
	// that a crash-torn write leaves behind.
	if err := os.WriteFile(filepath.Join(dir, machine.LayerName), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := installToDisk("node-1"); err == nil {
		t.Error("a layer that fails its sidecar must refuse to install")
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
