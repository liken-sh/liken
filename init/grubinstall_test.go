package main

// Tests for the boot-sector patcher. The offsets under test are
// grub-bios-setup's own (grub-core/boot/i386/pc/{boot,diskboot}.S fix
// them forever); the grub domain's introduction proved the arithmetic
// end to end by booting a hand-patched disk under SeaBIOS, and these
// tests pin it against synthetic images where every byte is known.

import (
	"bytes"
	"encoding/binary"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/liken-sh/liken/disks"
)

// testGRUBImages builds a recognizable fake boot.img and core.img:
// patterned bytes, the load segment planted where diskboot carries
// it.
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
		t.Fatal(err)
	}

	if len(plan.mbr) != 440 {
		t.Fatalf("the MBR half is %d bytes, want 440", len(plan.mbr))
	}
	if got := binary.LittleEndian.Uint64(plan.mbr[grubKernelSectorOffset:]); got != 2_048 {
		t.Errorf("kernel sector: got %d, want the partition's first LBA", got)
	}
	if plan.mbr[grubDriveCheckOffset] != 0x90 || plan.mbr[grubDriveCheckOffset+1] != 0x90 {
		t.Error("the drive check should be disabled with NOPs")
	}
	// Everything not deliberately patched is boot.img's own bytes.
	if plan.mbr[0] != 0xB0 || plan.mbr[439] != 0xB0 {
		t.Error("unpatched boot code bytes must come through unchanged")
	}

	if got := binary.LittleEndian.Uint64(plan.core[grubBlocklistStart:]); got != 2_049 {
		t.Errorf("blocklist start: got %d, want the sector after diskboot", got)
	}
	// 1200 bytes is 3 sectors; the blocklist counts the sectors after
	// the first.
	if got := binary.LittleEndian.Uint16(plan.core[grubBlocklistLength:]); got != 2 {
		t.Errorf("blocklist length: got %d, want 2", got)
	}
	if got := binary.LittleEndian.Uint16(plan.core[grubBlocklistSegment:]); got != grubLoadSegment {
		t.Errorf("the load segment is compiled in and must not change: %#x", got)
	}
	// The inputs were not mutated: healing recomputes from the slot's
	// pristine artifacts every time.
	if bootImg[grubKernelSectorOffset] != 0xB0 || coreImg[grubBlocklistStart] != 0xC0 {
		t.Error("the release's artifact bytes must not be modified in place")
	}
}

func TestPlanGRUBBootSectorsRefusesBadInputs(t *testing.T) {
	bootImg, coreImg := testGRUBImages(1_200)
	part := testBIOSBootPartition()

	if _, err := planGRUBBootSectors(bootImg[:100], coreImg, part); err == nil {
		t.Error("a boot.img that isn't one sector must be refused")
	}
	if _, err := planGRUBBootSectors(bootImg, coreImg[:100], part); err == nil {
		t.Error("a core image below one sector must be refused")
	}

	wrongSegment := bytes.Clone(coreImg)
	binary.LittleEndian.PutUint16(wrongSegment[grubBlocklistSegment:], 0x7c0)
	if _, err := planGRUBBootSectors(bootImg, wrongSegment, part); err == nil {
		t.Error("a core image without the i386-pc load segment must be refused")
	}

	tiny := &slotPartition{firstLBA: 2_048, lastLBA: 2_049}
	_, err := planGRUBBootSectors(bootImg, coreImg, tiny)
	if err == nil || !strings.Contains(err.Error(), "sectors") {
		t.Errorf("a core image that outgrows its partition must be refused: %v", err)
	}
}

func TestGRUBBootSectorsVerifyAndHeal(t *testing.T) {
	bootImg, coreImg := testGRUBImages(1_200)
	part := testBIOSBootPartition()
	plan, err := planGRUBBootSectors(bootImg, coreImg, part)
	if err != nil {
		t.Fatal(err)
	}

	disk, err := os.Create(filepath.Join(t.TempDir(), "disk"))
	if err != nil {
		t.Fatal(err)
	}
	defer disk.Close()
	if err := disk.Truncate(8 << 20); err != nil {
		t.Fatal(err)
	}
	// Sector 0's tail belongs to the partition table; plant a
	// sentinel there to prove the boot-code write stays in its lane.
	sentinel := bytes.Repeat([]byte{0xEE}, disks.SectorSize-440)
	if _, err := disk.WriteAt(sentinel, 440); err != nil {
		t.Fatal(err)
	}

	if ok, err := plan.inPlace(disk); err != nil || ok {
		t.Fatalf("a blank disk must not verify: %v %v", ok, err)
	}
	if err := plan.write(disk); err != nil {
		t.Fatal(err)
	}
	if ok, err := plan.inPlace(disk); err != nil || !ok {
		t.Fatalf("a freshly written chain must verify: %v %v", ok, err)
	}

	tail := make([]byte, disks.SectorSize-440)
	if _, err := disk.ReadAt(tail, 440); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(tail, sentinel) {
		t.Error("the write must not touch sector 0 past the boot code")
	}

	// The Linode wound: the boot code zeroed out from under the
	// machine. Healing is just noticing and writing again.
	if _, err := disk.WriteAt(make([]byte, 440), 0); err != nil {
		t.Fatal(err)
	}
	if ok, _ := plan.inPlace(disk); ok {
		t.Fatal("a zeroed MBR must not verify")
	}
	if err := plan.write(disk); err != nil {
		t.Fatal(err)
	}
	if ok, _ := plan.inPlace(disk); !ok {
		t.Error("healing must restore the chain")
	}
}
