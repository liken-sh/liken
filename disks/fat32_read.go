package disks

// Reading a FAT32 volume back: the inverse of the writer, kept
// deliberately small. Its consumers are verification — tests that
// prove the writer's output, and tooling that wants to look inside
// an install image without mounting it — so it reads whole files and
// resolves long names, and does nothing else a real driver would.

import (
	"encoding/binary"
	"fmt"
	"io"
	"slices"
	"strings"
	"unicode/utf16"
)

// A FATVolume is an opened FAT32 filesystem: its geometry, its
// allocation table, and the free-space accounting the writer left.
type FATVolume struct {
	dev               io.ReaderAt
	sectorsPerCluster uint32
	dataStart         uint32
	fat               []uint32

	// FreeClusters and NextFree echo the FSInfo sector, the writer's
	// accounting, for callers checking it against the table itself.
	FreeClusters uint32
	NextFree     uint32
}

// OpenFATVolume parses a volume's boot sector and allocation table,
// verifying that the two FAT copies agree — FAT's only redundancy is
// that pair, so disagreement means a writer failed halfway.
func OpenFATVolume(dev io.ReaderAt) (*FATVolume, error) {
	boot := make([]byte, SectorSize)
	if _, err := dev.ReadAt(boot, 0); err != nil {
		return nil, fmt.Errorf("reading the boot sector: %w", err)
	}
	if boot[510] != 0x55 || boot[511] != 0xAA || string(boot[82:90]) != "FAT32   " {
		return nil, fmt.Errorf("no FAT32 boot sector")
	}
	reserved := uint32(binary.LittleEndian.Uint16(boot[14:16]))
	fatSectors := binary.LittleEndian.Uint32(boot[36:40])
	numFATs := uint32(boot[16])

	v := &FATVolume{
		dev:               dev,
		sectorsPerCluster: uint32(boot[13]),
		dataStart:         reserved + numFATs*fatSectors,
	}

	table := make([]byte, int64(fatSectors)*SectorSize)
	if _, err := dev.ReadAt(table, int64(reserved)*SectorSize); err != nil {
		return nil, fmt.Errorf("reading the FAT: %w", err)
	}
	second := make([]byte, len(table))
	if _, err := dev.ReadAt(second, int64(reserved+fatSectors)*SectorSize); err != nil {
		return nil, fmt.Errorf("reading the second FAT: %w", err)
	}
	if !slices.Equal(table, second) {
		return nil, fmt.Errorf("the two FAT copies disagree")
	}

	totalSectors := binary.LittleEndian.Uint32(boot[32:36])
	clusters := (totalSectors - v.dataStart) / v.sectorsPerCluster
	v.fat = make([]uint32, clusters+2)
	for i := range v.fat {
		v.fat[i] = binary.LittleEndian.Uint32(table[i*4:]) & 0x0FFFFFFF
	}

	info := make([]byte, SectorSize)
	if _, err := dev.ReadAt(info, 1*SectorSize); err != nil {
		return nil, fmt.Errorf("reading FSInfo: %w", err)
	}
	v.FreeClusters = binary.LittleEndian.Uint32(info[488:492])
	v.NextFree = binary.LittleEndian.Uint32(info[492:496])
	return v, nil
}

// UsedClusters counts occupied entries in the table itself, for
// checking the FSInfo accounting against reality.
func (v *FATVolume) UsedClusters() uint32 {
	used := uint32(0)
	for _, entry := range v.fat[2:] {
		if entry != 0 {
			used++
		}
	}
	return used
}

// chain reads a whole cluster chain's bytes, rounded up to clusters;
// callers trim by the directory record's size.
func (v *FATVolume) chain(first uint32) ([]byte, error) {
	var out []byte
	clusterBytes := int64(v.sectorsPerCluster) * SectorSize
	for cluster := first; cluster < 0x0FFFFFF8; cluster = v.fat[cluster] {
		if cluster < 2 || int(cluster) >= len(v.fat) {
			return nil, fmt.Errorf("cluster chain runs off the table at %d", cluster)
		}
		buf := make([]byte, clusterBytes)
		off := (int64(v.dataStart) + int64(cluster-2)*int64(v.sectorsPerCluster)) * SectorSize
		if _, err := v.dev.ReadAt(buf, off); err != nil {
			return nil, err
		}
		out = append(out, buf...)
	}
	return out, nil
}

// A FATEntry is one directory child, its long name resolved.
type FATEntry struct {
	Name         string
	IsDir        bool
	FirstCluster uint32
	Size         uint32
}

// Entries decodes one directory's records: long-name chains resolved
// and checksum-verified, dot entries and the volume label skipped.
func (v *FATVolume) Entries(firstCluster uint32) ([]FATEntry, error) {
	raw, err := v.chain(firstCluster)
	if err != nil {
		return nil, err
	}
	var out []FATEntry
	var longName []uint16
	for off := 0; off+32 <= len(raw); off += 32 {
		rec := raw[off : off+32]
		switch {
		case rec[0] == 0x00:
			return out, nil // end of directory
		case rec[11] == 0x0F:
			var units []uint16
			for _, span := range [][2]int{{1, 11}, {14, 26}, {28, 32}} {
				for i := span[0]; i < span[1]; i += 2 {
					units = append(units, binary.LittleEndian.Uint16(rec[i:]))
				}
			}
			longName = append(units, longName...)
		case rec[11] == 0x08:
			longName = nil // the volume label
		default:
			name := shortNameOf(rec)
			if longName != nil {
				sum := byte(0)
				for i := range 11 {
					sum = (sum>>1 | sum<<7) + rec[i]
				}
				if raw[off-32+13] != sum {
					return nil, fmt.Errorf("long-name checksum mismatch before %q", name)
				}
				var units []uint16
				for _, u := range longName {
					if u == 0 || u == 0xFFFF {
						break
					}
					units = append(units, u)
				}
				name = string(utf16.Decode(units))
				longName = nil
			}
			if name == "." || name == ".." {
				continue
			}
			out = append(out, FATEntry{
				Name:         name,
				IsDir:        rec[11]&0x10 != 0,
				FirstCluster: uint32(binary.LittleEndian.Uint16(rec[20:22]))<<16 | uint32(binary.LittleEndian.Uint16(rec[26:28])),
				Size:         binary.LittleEndian.Uint32(rec[28:32]),
			})
		}
	}
	return out, nil
}

func shortNameOf(rec []byte) string {
	base := strings.TrimRight(string(rec[0:8]), " ")
	ext := strings.TrimRight(string(rec[8:11]), " ")
	if base == "." || base == ".." {
		return base
	}
	if ext == "" {
		return base
	}
	return base + "." + ext
}

// Find walks a slash-separated path from the root.
func (v *FATVolume) Find(path string) (FATEntry, error) {
	cluster := uint32(2)
	parts := strings.Split(strings.Trim(path, "/"), "/")
	for i, part := range parts {
		entries, err := v.Entries(cluster)
		if err != nil {
			return FATEntry{}, err
		}
		idx := slices.IndexFunc(entries, func(e FATEntry) bool { return e.Name == part })
		if idx < 0 {
			return FATEntry{}, fmt.Errorf("%q not found (component %q)", path, part)
		}
		if i == len(parts)-1 {
			return entries[idx], nil
		}
		cluster = entries[idx].FirstCluster
	}
	return FATEntry{}, fmt.Errorf("empty path %q", path)
}

// ReadFile reads one file's exact bytes.
func (v *FATVolume) ReadFile(path string) ([]byte, error) {
	e, err := v.Find(path)
	if err != nil {
		return nil, err
	}
	if e.IsDir {
		return nil, fmt.Errorf("%q is a directory", path)
	}
	raw, err := v.chain(e.FirstCluster)
	if err != nil {
		return nil, err
	}
	return raw[:e.Size], nil
}
