package disks

import (
	"os"
	"path/filepath"
	"testing"
)

// fat32Volume writes a small FAT32 volume to a file and returns its
// path. Formatting through liken's own formatter keeps these tests
// honest about the layout the machine actually writes.
func fat32Volume(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "volume.img")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := f.Truncate(64 << 20); err != nil {
		t.Fatal(err)
	}
	if err := FormatFAT32(f, 64<<20, "TEST", 0x12345678); err != nil {
		t.Fatalf("formatting: %v", err)
	}
	return path
}

// setStateByte forces the mark on, the way a mount would.
func setStateByte(t *testing.T, path string, value byte) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.WriteAt([]byte{value}, fat32StateOffset); err != nil {
		t.Fatal(err)
	}
}

func TestFAT32DirtyIsFalseOnAFreshVolume(t *testing.T) {
	// A volume liken just formatted has never been mounted, so it
	// carries no mark.
	dirty, err := FAT32Dirty(fat32Volume(t))
	if err != nil {
		t.Fatal(err)
	}
	if dirty {
		t.Error("a freshly formatted volume must not be marked")
	}
}

func TestFAT32DirtyReadsTheMark(t *testing.T) {
	path := fat32Volume(t)
	setStateByte(t, path, fatStateDirty)
	dirty, err := FAT32Dirty(path)
	if err != nil {
		t.Fatal(err)
	}
	if !dirty {
		t.Error("a marked volume must read as marked")
	}
}

func TestClearFAT32DirtyClearsOnlyTheOneBit(t *testing.T) {
	// The state byte has other bits. Clearing the mark must leave
	// them alone, because liken does not own their meaning.
	path := fat32Volume(t)
	setStateByte(t, path, fatStateDirty|0x02)
	if err := ClearFAT32Dirty(path); err != nil {
		t.Fatal(err)
	}
	sector, err := readBootSector(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := sector[fat32StateOffset]; got != 0x02 {
		t.Errorf("state byte is %#x, want %#x", got, 0x02)
	}
}

func TestClearFAT32DirtyLeavesTheRestOfTheBootSector(t *testing.T) {
	// One byte changes and nothing else does, so a volume this
	// machine did not format keeps every field it arrived with.
	path := fat32Volume(t)
	before, err := readBootSector(path)
	if err != nil {
		t.Fatal(err)
	}
	setStateByte(t, path, fatStateDirty)
	if err := ClearFAT32Dirty(path); err != nil {
		t.Fatal(err)
	}
	after, err := readBootSector(path)
	if err != nil {
		t.Fatal(err)
	}
	for i := range before {
		if before[i] != after[i] {
			t.Fatalf("byte %#x changed from %#x to %#x", i, before[i], after[i])
		}
	}
}

func TestClearFAT32DirtyOnAnUnmarkedVolumeDoesNothing(t *testing.T) {
	if err := ClearFAT32Dirty(fat32Volume(t)); err != nil {
		t.Errorf("clearing an unmarked volume returned %v", err)
	}
}

func TestFAT32StateRefusesAVolumeThatIsNotFAT32(t *testing.T) {
	// On FAT12 and FAT16 this offset holds a count, not a flag, so
	// both calls must refuse rather than read a number as a mark.
	path := filepath.Join(t.TempDir(), "other.img")
	if err := os.WriteFile(path, make([]byte, SectorSize), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := FAT32Dirty(path); err == nil {
		t.Error("FAT32Dirty accepted a volume with no FAT32 boot sector")
	}
	if err := ClearFAT32Dirty(path); err == nil {
		t.Error("ClearFAT32Dirty accepted a volume with no FAT32 boot sector")
	}
}
