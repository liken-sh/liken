package main

// Tests for the installer: payload verification, durable copies,
// partition numbers, and the boot entries it writes. They run against
// temp files, a fake sysfs, and a fake efivarfs. Claiming a real disk
// and powering off are tested only under QEMU.

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

	"github.com/liken-sh/liken/liken/disks"
	"github.com/liken-sh/liken/liken/machine"
)

func TestPartitionNumber(t *testing.T) {
	cases := []struct {
		part partition
		want uint32
	}{
		{partition{name: "vda1", disk: "vda"}, 1},
		{partition{name: "vdc2", disk: "vdc"}, 2},
		{partition{name: "nvme0n1p3", disk: "nvme0n1"}, 3},
		// An mmc partition name has the same "p" separator as NVMe.
		{partition{name: "mmcblk0p12", disk: "mmcblk0"}, 12},
	}
	for _, c := range cases {
		got, err := partitionNumber(c.part)
		if err != nil {
			t.Errorf("partitionNumber(%s) returned the error %v, want none", c.part.name, err)
		}
		if got != c.want {
			t.Errorf("partitionNumber(%s) is %d, want %d", c.part.name, got, c.want)
		}
	}
}

func TestPartitionNumberRefusesMalformedNames(t *testing.T) {
	// A name with no valid partition number must fail the install.
	// GPT numbers start at 1, so a boot entry with 0 would point at
	// no partition.
	cases := []partition{
		{name: "vda", disk: "vda"},           // no suffix at all
		{name: "vdap", disk: "vda"},          // separator with no number
		{name: "vdaXY", disk: "vda"},         // non-numeric suffix
		{name: "mmcblk0", disk: "mmcblk1"},   // name doesn't extend the disk's
		{name: "vda4294967297", disk: "vda"}, // number past a uint32
	}
	for _, c := range cases {
		if _, err := partitionNumber(c); err == nil {
			t.Errorf("partitionNumber(%s on %s) returned no error, want one", c.name, c.disk)
		}
	}
}

// artifactFor writes contents to dir under name and returns the
// ReleaseArtifact describing exactly those bytes.
func artifactFor(t *testing.T, dir, name string, contents []byte) machine.ReleaseArtifact {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), contents, 0o644); err != nil {
		t.Fatalf("writing the artifact: %v", err)
	}
	sum := sha256.Sum256(contents)
	return machine.ReleaseArtifact{
		Name:   name,
		SHA256: hex.EncodeToString(sum[:]),
		Size:   int64(len(contents)),
	}
}

// installedDisk builds the machine that an install starts from: one
// boot disk whose GPT has both system slots, mirrored into the fake
// sysfs as the kernel would show it. The table is written into the fake
// device file directly, because the kernel re-read ioctl in disks.Write
// works only on real block devices.
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
		t.Fatalf("serializing the partition table: %v", err)
	}
	f, err := os.OpenFile(filepath.Join(dev, "vdc"), os.O_RDWR, 0)
	if err != nil {
		t.Fatalf("opening the fake disk: %v", err)
	}
	defer f.Close()
	for _, chunk := range chunks {
		if _, err := f.WriteAt(chunk.Data, int64(chunk.LBA)*disks.SectorSize); err != nil {
			t.Fatalf("writing the partition table: %v", err)
		}
	}
	addPartition(t, sys, "vdc", "vdc1", "liken:systemA", 16384*disks.SectorSize)
	addPartition(t, sys, "vdc", "vdc2", "liken:systemB", 16384*disks.SectorSize)
	return sys, dev
}

// installedBIOSDisk is installedDisk with the biosBoot and bootHome
// partitions before the slots, as on a machine whose spec declares
// those roles.
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
		t.Fatalf("serializing the partition table: %v", err)
	}
	f, err := os.OpenFile(filepath.Join(dev, "vdc"), os.O_RDWR, 0)
	if err != nil {
		t.Fatalf("opening the fake disk: %v", err)
	}
	defer f.Close()
	for _, chunk := range chunks {
		if _, err := f.WriteAt(chunk.Data, int64(chunk.LBA)*disks.SectorSize); err != nil {
			t.Fatalf("writing the partition table: %v", err)
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
		t.Fatalf("finding slot A: %v", err)
	}
	if slot.number != 1 || slot.firstLBA != 2048 || slot.lastLBA != 18431 {
		t.Errorf("slot A is %+v, want number 1 and LBAs 2048 to 18431 from the table", slot)
	}
	if slot.guid == ([16]byte{}) {
		t.Error("slot A has an empty unique GUID, want the GUID of its table entry")
	}
}

func TestFindSlotPartitionRefusesAMissingSlot(t *testing.T) {
	fakeMachine(t)
	if _, err := findSlotPartition(nil, machine.SystemBRole); err == nil {
		t.Error("findSlotPartition returned no error for a slot with no partition, want one")
	}
}

// fakeSlotAMount points the systemA role's mountpoint at a temporary
// directory in place of the filesystem that storage reconciliation
// mounts, and restores the real mountpoint when the test ends.
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

// fakePayload writes an install payload directory: the artifacts, the
// release document that lists them, and the deployment layer with its
// sidecar.
func fakePayload(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	kernel := artifactFor(t, dir, "vmlinuz", []byte("the kernel"))
	cpio := artifactFor(t, dir, "liken.sqfs", []byte("the rest of the OS"))
	// The GRUB images: a full boot.img sector, and a small core image
	// with the load segment where diskboot expects it, which
	// planGRUBBootSectors accepts.
	bootImgBytes := bytes.Repeat([]byte{0xB0}, disks.SectorSize)
	coreImgBytes := bytes.Repeat([]byte{0xC0}, 3*disks.SectorSize)
	binary.LittleEndian.PutUint16(coreImgBytes[grubBlocklistSegment:], grubLoadSegment)
	bootImg := artifactFor(t, dir, "grub-boot.img", bootImgBytes)
	coreImg := artifactFor(t, dir, "grub-core.img", coreImgBytes)
	// writeSlotLoader copies systemd-boot to the slot's default boot
	// path, so the file must exist. Its bytes do not matter.
	bootMenu := artifactFor(t, dir, bootMenuName, []byte("the boot menu"))
	release := `apiVersion: liken.sh/v1alpha1
kind: Release
metadata:
  name: 0.9.9
artifacts:
`
	for _, a := range []machine.ReleaseArtifact{kernel, cpio, bootImg, coreImg, bootMenu} {
		release += fmt.Sprintf("- name: %s\n  sha256: %s\n  size: %d\n", a.Name, a.SHA256, a.Size)
	}
	if err := os.WriteFile(filepath.Join(dir, "release.yaml"), []byte(release), 0o644); err != nil {
		t.Fatalf("writing release.yaml: %v", err)
	}

	layer := []byte("the deployment layer")
	if err := os.WriteFile(filepath.Join(dir, machine.LayerName), layer, 0o644); err != nil {
		t.Fatalf("writing the layer: %v", err)
	}
	digest, err := machine.DigestLayer(bytes.NewReader(layer))
	if err != nil {
		t.Fatalf("digesting the layer: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, machine.LayerSidecarName), machine.FormatLayerSidecar(digest), 0o644); err != nil {
		t.Fatalf("writing the layer sidecar: %v", err)
	}

	old := releasePayloadDir
	releasePayloadDir = dir
	t.Cleanup(func() { releasePayloadDir = old })
	return dir
}

// fakeFirmware points efiVarsDir at a fake store, so a test can read the
// boot entries that the install writes. It also points efiSysDir at the
// store, and firmwareIsUEFI only checks that efiSysDir exists, so the
// machine counts as UEFI on any host.
func fakeFirmware(t *testing.T, vars map[string][]byte) string {
	t.Helper()
	dir := fakeEFIVars(t, vars)
	old, oldSys := efiVarsDir, efiSysDir
	efiVarsDir, efiSysDir = dir, dir
	t.Cleanup(func() { efiVarsDir, efiSysDir = old, oldSys })
	return dir
}

// fakeBIOSFirmware makes the machine count as BIOS by pointing efiSysDir
// at a directory that does not exist, as on a machine that legacy
// firmware booted.
func fakeBIOSFirmware(t *testing.T) {
	t.Helper()
	oldSys := efiSysDir
	efiSysDir = filepath.Join(t.TempDir(), "no-efi-here")
	t.Cleanup(func() { efiSysDir = oldSys })
}

// fakeBootHomeMount points the bootHome role's mountpoint at a
// temporary directory, as fakeSlotAMount does for slot A.
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
	// The firmware already has an entry of its own. It must stay in
	// BootOrder, after this machine's entries.
	firmware := fakeFirmware(t, map[string][]byte{
		"Boot0000":  encodeLoadOption(loadOption{attributes: loadOptionActive, description: "UEFI Shell"}),
		"BootOrder": u16le(0x0000),
	})

	slot, err := installToDisk("node-1")
	if err != nil {
		t.Fatalf("installing: %v", err)
	}
	if slot != "A" {
		t.Errorf("the install wrote slot %q, want A", slot)
	}

	for _, name := range []string{"vmlinuz", "liken.sqfs", machine.LayerName, machine.LayerSidecarName} {
		if _, err := os.Stat(filepath.Join(slotMount, name)); err != nil {
			t.Errorf("%s is missing from the slot: %v, want it copied", name, err)
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
		t.Fatalf("the boot entries are %+v, want one for slot A and one for slot B", entries)
	}
	order := readBootOrder(firmware)
	if len(order) != 3 || order[0] != a || order[1] != b || order[2] != 0x0000 {
		t.Errorf("BootOrder is %v, want slot A, slot B, then the firmware's own entry", order)
	}
}

func TestInstallToDiskNeedsAName(t *testing.T) {
	if _, err := installToDisk(""); err == nil {
		t.Error("installToDisk returned no error for an empty machine name, want one")
	}
}

func TestInstallToDiskLaysDownGRUB(t *testing.T) {
	_, dev := installedBIOSDisk(t)
	fakePayload(t)
	fakeSlotAMount(t)
	home := fakeBootHomeMount(t)
	fakeCmdline(t, "console=ttyS0 rdinit=/liken liken.install\n")
	fakeBIOSFirmware(t)

	if _, err := installToDisk("node-1"); err != nil {
		t.Fatalf("installing: %v", err)
	}

	disk, err := os.Open(filepath.Join(dev, "vdc"))
	if err != nil {
		t.Fatalf("opening the fake disk: %v", err)
	}
	defer disk.Close()
	sector := make([]byte, disks.SectorSize)
	if _, err := disk.ReadAt(sector, 0); err != nil {
		t.Fatalf("reading the MBR: %v", err)
	}
	// The MBR holds the patched boot code: the core image's LBA at its
	// fixed offset, and the first byte of grub-boot.img at sector start.
	if got := binary.LittleEndian.Uint64(sector[grubKernelSectorOffset:]); got != 2048 {
		t.Errorf("the boot code points at sector %d, want 2048, the start of the biosBoot partition", got)
	}
	if sector[0] != 0xB0 {
		t.Errorf("the MBR starts with %#x, want 0xb0, the first byte of the release's grub-boot.img", sector[0])
	}
	// The end of sector 0 still holds the GPT's protective MBR entry and
	// the boot signature.
	if sector[446+4] != 0xEE || sector[510] != 0x55 || sector[511] != 0xAA {
		t.Error("the protective MBR entry or the boot signature changed, want both unchanged")
	}
	core := make([]byte, disks.SectorSize)
	if _, err := disk.ReadAt(core, 2048*disks.SectorSize); err != nil {
		t.Fatalf("reading the core image: %v", err)
	}
	if core[0] != 0xC0 {
		t.Errorf("the biosBoot partition starts with %#x, want 0xc0, the first byte of grub-core.img", core[0])
	}
	if got := binary.LittleEndian.Uint64(core[grubBlocklistStart:]); got != 2049 {
		t.Errorf("the core image's blocklist starts at sector %d, want 2049", got)
	}

	cfg, err := os.ReadFile(filepath.Join(home, "grub", "grub.cfg"))
	if err != nil {
		t.Fatalf("reading grub.cfg: %v", err)
	}
	if !strings.Contains(string(cfg), "liken.machine=node-1") {
		t.Errorf("grub.cfg does not contain liken.machine=node-1, want it:\n%s", cfg)
	}
	vars, err := readGRUBEnv(filepath.Join(home, "grub", "grubenv"))
	if err != nil {
		t.Fatalf("reading grubenv: %v", err)
	}
	if vars["default_slot"] != "A" {
		t.Errorf("grubenv is %v, want default_slot A", vars)
	}
}

func TestInstallToDiskRefusesWithoutAnyActuator(t *testing.T) {
	// A BIOS machine whose spec declares no GRUB roles has nothing that
	// can boot the installed disk. The install must fail instead of
	// powering off a machine that cannot start again.
	installedDisk(t)
	fakePayload(t)
	fakeSlotAMount(t)
	fakeCmdline(t, "console=ttyS0 rdinit=/liken liken.install\n")
	fakeBIOSFirmware(t)

	_, err := installToDisk("node-1")
	if err == nil || !strings.Contains(err.Error(), "biosBoot") {
		t.Errorf("the install error is %v, want one that mentions biosBoot", err)
	}
}

func TestInstallToDiskRefusesACorruptPayload(t *testing.T) {
	installedDisk(t)
	dir := fakePayload(t)
	fakeSlotAMount(t)
	fakeFirmware(t, map[string][]byte{})
	// One flipped byte in an artifact must fail the install before any
	// copy starts.
	if err := os.WriteFile(filepath.Join(dir, "vmlinuz"), []byte("the kernal"), 0o644); err != nil {
		t.Fatalf("corrupting the kernel: %v", err)
	}
	if _, err := installToDisk("node-1"); err == nil {
		t.Error("installToDisk returned no error for a payload that does not match release.yaml, want one")
	}
}

func TestInstallToDiskNeedsBothSlots(t *testing.T) {
	// A disk with only slot A must fail the install, because the
	// fallback slot has to be registered from the first boot.
	sys, dev := fakeMachine(t)
	const totalSectors = 1 << 16
	addDisk(t, sys, dev, "vdc", totalSectors*disks.SectorSize, make([]byte, totalSectors*disks.SectorSize))
	addPartition(t, sys, "vdc", "vdc1", "liken:systemA", 16384*disks.SectorSize)
	fakePayload(t)
	if _, err := installToDisk("node-1"); err == nil {
		t.Error("installToDisk returned no error for a disk with only slot A, want one")
	}
}

func TestInstallToDiskNeedsTheLayerSidecar(t *testing.T) {
	installedDisk(t)
	dir := fakePayload(t)
	fakeSlotAMount(t)
	fakeFirmware(t, map[string][]byte{})
	if err := os.Remove(filepath.Join(dir, machine.LayerSidecarName)); err != nil {
		t.Fatalf("removing the layer sidecar: %v", err)
	}
	_, err := installToDisk("node-1")
	if err == nil || !strings.Contains(err.Error(), "sidecar") {
		t.Errorf("the install error is %v, want one that mentions the sidecar", err)
	}
}

func TestInstallToDiskRefusesATornLayer(t *testing.T) {
	installedDisk(t)
	dir := fakePayload(t)
	fakeSlotAMount(t)
	fakeFirmware(t, map[string][]byte{})
	// A zero-length layer with an intact sidecar is what a write torn
	// by a crash leaves behind.
	if err := os.WriteFile(filepath.Join(dir, machine.LayerName), nil, 0o644); err != nil {
		t.Fatalf("truncating the layer: %v", err)
	}
	if _, err := installToDisk("node-1"); err == nil {
		t.Error("installToDisk returned no error for a layer that does not match its sidecar, want one")
	}
}

func TestInstallToDiskNeedsItsReleaseDocument(t *testing.T) {
	installedDisk(t)
	old := releasePayloadDir
	releasePayloadDir = filepath.Join(t.TempDir(), "no-payload")
	t.Cleanup(func() { releasePayloadDir = old })

	_, err := installToDisk("node-1")
	if err == nil || !strings.Contains(err.Error(), "release document") {
		t.Errorf("the install error is %v, want one that mentions the release document", err)
	}
}

func TestInstallToDiskRefusesAGarbageReleaseDocument(t *testing.T) {
	installedDisk(t)
	dir := fakePayload(t)
	if err := os.WriteFile(filepath.Join(dir, "release.yaml"), []byte("{not yaml"), 0o644); err != nil {
		t.Fatalf("writing release.yaml: %v", err)
	}

	if _, err := installToDisk("node-1"); err == nil {
		t.Error("installToDisk returned no error for a release document that is not valid YAML, want one")
	}
}

func TestConsoleArgsWithNoCommandLine(t *testing.T) {
	fakeCmdline(t, "")
	if got := consoleArgs(); got != nil {
		t.Errorf("consoleArgs is %v, want nil for an empty command line", got)
	}
}
