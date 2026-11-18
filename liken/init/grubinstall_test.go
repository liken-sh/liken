package main

// These tests check the patched offsets and sector arithmetic against
// synthetic images in which every byte is known. The offsets are the
// ones that grub-bios-setup uses, from boot.S and diskboot.S in
// grub-core/boot/i386/pc.

import (
	"bytes"
	"encoding/binary"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/liken-sh/liken/liken/disks"
)

// testGRUBImages returns a fake boot.img and core.img, each filled
// with one repeated byte, with the load segment where diskboot has it.
func testGRUBImages(coreBytes int) ([]byte, []byte) {
	bootImg := bytes.Repeat([]byte{0xB0}, disks.SectorSize)
	coreImg := bytes.Repeat([]byte{0xC0}, coreBytes)
	binary.LittleEndian.PutUint16(coreImg[grubBlocklistSegment:], grubLoadSegment)
	return bootImg, coreImg
}

func testBIOSBootPartition() *slotPartition {
	return &slotPartition{firstLBA: 2_048, lastLBA: 4_095}
}

func TestPlanGRUBBootSectorsPatchesTheChain(t *testing.T) {
	bootImg, coreImg := testGRUBImages(1_200) // three sectors, one partial
	plan, err := planGRUBBootSectors(bootImg, coreImg, testBIOSBootPartition())
	if err != nil {
		t.Fatalf("planning the boot sectors: %v", err)
	}

	if len(plan.mbr) != 440 {
		t.Fatalf("the MBR boot code is %d bytes, want 440", len(plan.mbr))
	}
	if got := binary.LittleEndian.Uint64(plan.mbr[grubKernelSectorOffset:]); got != 2_048 {
		t.Errorf("the kernel sector is %d, want the first LBA of the partition (2048)", got)
	}
	if plan.mbr[grubDriveCheckOffset] != 0x90 || plan.mbr[grubDriveCheckOffset+1] != 0x90 {
		t.Errorf("the drive check bytes are % x, want 90 90, the NOPs that disable it", plan.mbr[grubDriveCheckOffset:grubDriveCheckOffset+2])
	}
	// Bytes that are not patched are copied from boot.img.
	if plan.mbr[0] != 0xB0 || plan.mbr[439] != 0xB0 {
		t.Error("the boot code bytes that planGRUBBootSectors does not patch differ from boot.img, want them copied")
	}

	if got := binary.LittleEndian.Uint64(plan.core[grubBlocklistStart:]); got != 2_049 {
		t.Errorf("the blocklist start is %d, want 2049, the sector after diskboot", got)
	}
	// 1200 bytes is 3 sectors, and the blocklist counts the sectors
	// after the first.
	if got := binary.LittleEndian.Uint16(plan.core[grubBlocklistLength:]); got != 2 {
		t.Errorf("the blocklist length is %d, want 2", got)
	}
	if got := binary.LittleEndian.Uint16(plan.core[grubBlocklistSegment:]); got != grubLoadSegment {
		t.Errorf("the load segment is %#x, want %#x, as in the input", got, grubLoadSegment)
	}
	// The function patches copies, because it runs again from the
	// same artifacts on every boot.
	if bootImg[grubKernelSectorOffset] != 0xB0 || coreImg[grubBlocklistStart] != 0xC0 {
		t.Error("planGRUBBootSectors changed the bytes of its input images, want them unchanged")
	}
}

func TestPlanGRUBBootSectorsRefusesBadInputs(t *testing.T) {
	bootImg, coreImg := testGRUBImages(1_200)
	part := testBIOSBootPartition()

	if _, err := planGRUBBootSectors(bootImg[:100], coreImg, part); err == nil {
		t.Error("planGRUBBootSectors accepted a boot.img that is not one sector, want an error")
	}
	if _, err := planGRUBBootSectors(bootImg, coreImg[:100], part); err == nil {
		t.Error("planGRUBBootSectors accepted a core image smaller than one sector, want an error")
	}

	wrongSegment := bytes.Clone(coreImg)
	binary.LittleEndian.PutUint16(wrongSegment[grubBlocklistSegment:], 0x7c0)
	if _, err := planGRUBBootSectors(bootImg, wrongSegment, part); err == nil {
		t.Error("planGRUBBootSectors accepted a core image without the i386-pc load segment, want an error")
	}

	tiny := &slotPartition{firstLBA: 2_048, lastLBA: 2_049}
	_, err := planGRUBBootSectors(bootImg, coreImg, tiny)
	if err == nil || !strings.Contains(err.Error(), "sectors") {
		t.Errorf("the error for a core image larger than its partition is %v, want one that mentions sectors", err)
	}
}

func TestGRUBBootSectorsAreDetectedAndRewrittenAfterTheBootCodeIsZeroed(t *testing.T) {
	bootImg, coreImg := testGRUBImages(1_200)
	part := testBIOSBootPartition()
	plan, err := planGRUBBootSectors(bootImg, coreImg, part)
	if err != nil {
		t.Fatalf("planning the boot sectors: %v", err)
	}

	disk, err := os.Create(filepath.Join(t.TempDir(), "disk"))
	if err != nil {
		t.Fatalf("creating the disk file: %v", err)
	}
	defer disk.Close()
	if err := disk.Truncate(8 << 20); err != nil {
		t.Fatalf("sizing the disk file: %v", err)
	}
	// The rest of sector 0 belongs to the partition table. The test
	// fills it with a known value to check that write leaves it alone.
	sentinel := bytes.Repeat([]byte{0xEE}, disks.SectorSize-440)
	if _, err := disk.WriteAt(sentinel, 440); err != nil {
		t.Fatalf("writing the disk file: %v", err)
	}

	if ok, err := plan.inPlace(disk); err != nil || ok {
		t.Fatalf("inPlace on a blank disk returns %v, %v, want false and no error", ok, err)
	}
	if err := plan.write(disk); err != nil {
		t.Fatalf("writing the boot sectors: %v", err)
	}
	if ok, err := plan.inPlace(disk); err != nil || !ok {
		t.Fatalf("inPlace after write returns %v, %v, want true and no error", ok, err)
	}

	tail := make([]byte, disks.SectorSize-440)
	if _, err := disk.ReadAt(tail, 440); err != nil {
		t.Fatalf("reading the disk file: %v", err)
	}
	if !bytes.Equal(tail, sentinel) {
		t.Error("write changed the bytes of sector 0 after the boot code, want them unchanged")
	}

	// A Linode image deploy zeroes the boot code. inPlace must see
	// that, and write must restore it.
	if _, err := disk.WriteAt(make([]byte, 440), 0); err != nil {
		t.Fatalf("writing the disk file: %v", err)
	}
	if ok, _ := plan.inPlace(disk); ok {
		t.Fatal("inPlace is true for a zeroed MBR, want false")
	}
	if err := plan.write(disk); err != nil {
		t.Fatalf("writing the boot sectors: %v", err)
	}
	if ok, _ := plan.inPlace(disk); !ok {
		t.Error("inPlace is false after write on a zeroed MBR, want true")
	}
}
