package disks

// Writing files into a FAT32 volume, without mounting it.
//
// On a running machine the kernel's vfat driver does this; a build
// tool laying out an install image has no kernel to ask, so it plays
// the driver's role directly. The job is smaller than it sounds
// because of what a FAT filesystem is: the FAT itself is one 32-bit
// entry per cluster, each naming the next cluster of its file, and a
// directory is just a file whose bytes are 32-byte records. Writing
// a file is: pick clusters, chain them in the table, put the bytes
// in the clusters, and add a record to the parent directory's bytes.
//
// This writer is deliberately narrower than a driver. It only ever
// fills a freshly formatted volume, once, so allocation is a bump
// pointer (the next free cluster is always the next cluster), there
// are no deletes and no fragmentation, and everything about the
// directory tree can be held in memory until Close lays it down.
//
// The one genuinely fiddly part is names. FAT's native names are the
// DOS 8.3 form, eleven uppercase bytes; everything longer or
// lowercase rides the VFAT retrofit: a chain of "long name" records
// ahead of the real one, each carrying thirteen UTF-16 characters
// and marked with an attribute combination (read-only + hidden +
// system + volume label) that DOS-era readers were guaranteed to
// skip. Firmware and every OS read them; loader.conf and
// deployment.cpio don't fit 8.3, so this writer speaks them.

import (
	"encoding/binary"
	"fmt"
	"io"
	"path"
	"strings"
	"unicode/utf16"
)

// endOfChain is the FAT entry marking a file's last cluster. Values
// at and above 0x0FFFFFF8 all mean end-of-chain; this is the
// conventional one every formatter writes.
const endOfChain = 0x0FFFFFF7 + 1

// A fatFile is one file waiting for its directory record: where its
// chain starts and how long it really is (FAT records exact sizes;
// the chain rounds up to clusters).
type fatFile struct {
	name         string
	firstCluster uint32
	size         int64
}

// A fatDir accumulates a directory's children until Close writes the
// records. Directories get their clusters at Close, after every
// file's chain is known, because a record needs its child's first
// cluster.
type fatDir struct {
	name         string
	parent       *fatDir
	files        []fatFile
	subdirs      []*fatDir
	firstCluster uint32
}

// A FATWriter fills a formatted FAT32 volume with files. The
// geometry comes from the volume's own boot sector, so the writer
// agrees with the format about where everything is by construction.
type FATWriter struct {
	dev interface {
		io.ReaderAt
		io.WriterAt
		Sync() error
	}
	sectorsPerCluster uint32
	reservedSectors   uint32
	fatSectors        uint32
	dataStart         uint32 // sector of cluster 2
	fat               []uint32
	next              uint32 // the bump allocator: next free cluster
	root              *fatDir
	dirs              map[string]*fatDir
	labelEntry        []byte // the volume-label record the format wrote
}

// NewFATWriter opens a freshly formatted volume for filling, reading
// the geometry back from the boot sector the format just wrote.
func NewFATWriter(dev interface {
	io.ReaderAt
	io.WriterAt
	Sync() error
}) (*FATWriter, error) {
	boot := make([]byte, SectorSize)
	if _, err := dev.ReadAt(boot, 0); err != nil {
		return nil, fmt.Errorf("reading the boot sector: %w", err)
	}
	if boot[510] != 0x55 || boot[511] != 0xAA || string(boot[82:90]) != "FAT32   " {
		return nil, fmt.Errorf("the volume does not carry a FAT32 boot sector")
	}

	w := &FATWriter{
		dev:               dev,
		sectorsPerCluster: uint32(boot[13]),
		reservedSectors:   uint32(binary.LittleEndian.Uint16(boot[14:16])),
		fatSectors:        binary.LittleEndian.Uint32(boot[36:40]),
	}
	numFATs := uint32(boot[16])
	if numFATs != 2 {
		return nil, fmt.Errorf("expected 2 FATs, found %d", numFATs)
	}
	w.dataStart = w.reservedSectors + numFATs*w.fatSectors

	totalSectors := binary.LittleEndian.Uint32(boot[32:36])
	clusters := (totalSectors - w.dataStart) / w.sectorsPerCluster

	// The whole table in memory: clusters plus the two flag entries.
	// Entries 0 and 1 echo what the format wrote; cluster 2 is the
	// root directory's single starting cluster.
	w.fat = make([]uint32, clusters+2)
	w.fat[0] = 0x0FFFFFF8
	w.fat[1] = 0x0FFFFFFF
	w.fat[2] = endOfChain
	w.next = 3

	// The format left exactly one record in the root: the volume
	// label. Keep it, so Close can lay the root back down label-first
	// the way tools expect.
	w.labelEntry = make([]byte, 32)
	if _, err := dev.ReadAt(w.labelEntry, int64(w.dataStart)*SectorSize); err != nil {
		return nil, fmt.Errorf("reading the volume label: %w", err)
	}
	if w.labelEntry[11] != 0x08 {
		return nil, fmt.Errorf("the root directory does not begin with the volume label the format writes")
	}

	w.root = &fatDir{firstCluster: 2}
	w.dirs = map[string]*fatDir{"": w.root}
	return w, nil
}

func (w *FATWriter) clusterBytes() int64 {
	return int64(w.sectorsPerCluster) * SectorSize
}

// clusterOffset is a cluster's first byte on the volume. Clusters 0
// and 1 don't exist; the data region begins at cluster 2.
func (w *FATWriter) clusterOffset(n uint32) int64 {
	return (int64(w.dataStart) + int64(n-2)*int64(w.sectorsPerCluster)) * SectorSize
}

// allocate takes n consecutive clusters and chains them, returning
// the first. Write-once is what makes this a bump pointer.
func (w *FATWriter) allocate(n int64) (uint32, error) {
	if n == 0 {
		n = 1 // even an empty directory owns one cluster
	}
	first := w.next
	if int64(first)+n > int64(len(w.fat)) {
		return 0, fmt.Errorf("the volume is full: %d clusters wanted, %d left", n, int64(len(w.fat))-int64(w.next))
	}
	for i := int64(0); i < n-1; i++ {
		w.fat[first+uint32(i)] = first + uint32(i) + 1
	}
	w.fat[first+uint32(n-1)] = endOfChain
	w.next += uint32(n)
	return first, nil
}

// Mkdir registers a directory. Parents must already exist, the way
// the archive writers here expect to be driven.
func (w *FATWriter) Mkdir(dirPath string) error {
	dirPath = strings.Trim(dirPath, "/")
	if _, exists := w.dirs[dirPath]; exists {
		return fmt.Errorf("directory %q already exists", dirPath)
	}
	parent, ok := w.dirs[parentOf(dirPath)]
	if !ok {
		return fmt.Errorf("directory %q has no parent yet", dirPath)
	}
	d := &fatDir{name: path.Base(dirPath), parent: parent}
	parent.subdirs = append(parent.subdirs, d)
	w.dirs[dirPath] = d
	return nil
}

// WriteFile streams one file's bytes into freshly allocated clusters
// and remembers it for its directory record. Streaming matters: the
// files here are hundreds of megabytes of OS image.
func (w *FATWriter) WriteFile(filePath string, r io.Reader, size int64) error {
	filePath = strings.Trim(filePath, "/")
	dir, ok := w.dirs[parentOf(filePath)]
	if !ok {
		return fmt.Errorf("file %q has no directory yet", filePath)
	}

	clusterBytes := w.clusterBytes()
	first, err := w.allocate((size + clusterBytes - 1) / clusterBytes)
	if err != nil {
		return err
	}

	buf := make([]byte, clusterBytes)
	remaining := size
	for cluster := first; remaining > 0; cluster++ {
		n := min(remaining, clusterBytes)
		if _, err := io.ReadFull(r, buf[:n]); err != nil {
			return fmt.Errorf("reading %q: %w", filePath, err)
		}
		if _, err := w.dev.WriteAt(buf[:n], w.clusterOffset(cluster)); err != nil {
			return fmt.Errorf("writing %q: %w", filePath, err)
		}
		remaining -= n
	}

	dir.files = append(dir.files, fatFile{name: path.Base(filePath), firstCluster: first, size: size})
	return nil
}

// Close lays down what only the whole picture determines: each
// directory's records (which need every child's first cluster), both
// copies of the FAT, and the free-space accounting. Directories are
// assigned clusters first, in one pass, so parent and child records
// can point at each other regardless of order.
func (w *FATWriter) Close() error {
	var all []*fatDir
	var walk func(d *fatDir)
	walk = func(d *fatDir) {
		all = append(all, d)
		for _, sub := range d.subdirs {
			walk(sub)
		}
	}
	walk(w.root)

	// Assignment pass: every directory gets a chain sized for its
	// records. The root's chain starts at its fixed cluster 2 and is
	// extended only if the label and children outgrow one cluster.
	clusterBytes := w.clusterBytes()
	for _, d := range all {
		size := int64(len(w.dirRecords(d, 0))) // cluster numbers don't change the length
		if d == w.root {
			if size > clusterBytes {
				extra, err := w.allocate((size - clusterBytes + clusterBytes - 1) / clusterBytes)
				if err != nil {
					return err
				}
				w.fat[2] = extra
			}
			continue
		}
		first, err := w.allocate((size + clusterBytes - 1) / clusterBytes)
		if err != nil {
			return err
		}
		d.firstCluster = first
	}

	// Content pass: with every cluster known, write each directory's
	// records along its chain.
	for _, d := range all {
		records := w.dirRecords(d, d.firstCluster)
		cluster := d.firstCluster
		for off := int64(0); off < int64(len(records)); off += clusterBytes {
			end := min(off+clusterBytes, int64(len(records)))
			if _, err := w.dev.WriteAt(records[off:end], w.clusterOffset(cluster)); err != nil {
				return fmt.Errorf("writing directory records: %w", err)
			}
			cluster = w.fat[cluster]
		}
	}

	// Both FATs, byte for byte the same: the second copy is FAT's
	// only redundancy, so writing it identically is the whole point.
	table := make([]byte, int64(w.fatSectors)*SectorSize)
	for i, entry := range w.fat {
		binary.LittleEndian.PutUint32(table[i*4:], entry)
	}
	for copyN := range 2 {
		off := int64(w.reservedSectors+uint32(copyN)*w.fatSectors) * SectorSize
		if _, err := w.dev.WriteAt(table, off); err != nil {
			return fmt.Errorf("writing FAT copy %d: %w", copyN+1, err)
		}
	}

	// FSInfo, primary and backup: the free count is exact because the
	// bump allocator knows precisely what it used.
	free := uint32(len(w.fat)) - w.next
	info := buildFAT32FSInfo(free, w.next)
	for _, sector := range []int64{1, 7} {
		if _, err := w.dev.WriteAt(info, sector*SectorSize); err != nil {
			return fmt.Errorf("writing FSInfo: %w", err)
		}
	}
	return w.dev.Sync()
}

// dirRecords lays out one directory's 32-byte records: the label
// (root only), dot entries (subdirectories only), then every child,
// each preceded by its long-name records when the 8.3 form can't
// carry the name alone. Short names must be unique within their
// directory — fsck treats a duplicate as corruption — so collisions
// among the truncated stand-ins get the classic ~N tail.
func (w *FATWriter) dirRecords(d *fatDir, self uint32) []byte {
	var records []byte
	if d == w.root {
		records = append(records, w.labelEntry...)
	} else {
		// "." and ".." are ordinary records with fixed names. By
		// specification, ".." pointing at the root writes cluster 0,
		// not 2 — a DOS-era quirk every reader expects.
		parent := d.parent.firstCluster
		if d.parent == w.root {
			parent = 0
		}
		records = append(records, plainRecord(".          ", 0x10, self, 0)...)
		records = append(records, plainRecord("..         ", 0x10, parent, 0)...)
	}
	taken := map[string]bool{}
	for _, sub := range d.subdirs {
		records = append(records, namedRecords(sub.name, 0x10, sub.firstCluster, 0, taken)...)
	}
	for _, f := range d.files {
		records = append(records, namedRecords(f.name, 0x20, f.firstCluster, f.size, taken)...)
	}
	return records
}

// plainRecord is one 32-byte record whose 8.3 name is already known.
// The timestamps are all zero, for the same reason the cpio writer's
// are: the image's bytes should depend on its contents, not on when
// it was built. (FAT can't express a year before 1980, so zero dates
// read back as nonsense-but-harmless 1980-00-00; nothing on an
// install stick consults them.)
func plainRecord(shortName string, attr byte, firstCluster uint32, size int64) []byte {
	r := make([]byte, 32)
	copy(r[0:11], shortName)
	r[11] = attr
	binary.LittleEndian.PutUint16(r[20:22], uint16(firstCluster>>16))
	binary.LittleEndian.PutUint16(r[26:28], uint16(firstCluster))
	binary.LittleEndian.PutUint32(r[28:32], uint32(size))
	return r
}

// namedRecords is a child's full record set: the plain record under
// its 8.3 name, preceded by long-name records when the real name
// needs them. taken tracks the directory's short names so no two
// children share one.
func namedRecords(name string, attr byte, firstCluster uint32, size int64, taken map[string]bool) []byte {
	short, needsLong := shortNameFor(name, taken)
	taken[short] = true
	record := plainRecord(short, attr, firstCluster, size)
	if !needsLong {
		return record
	}
	return append(longNameRecords(name, short), record...)
}

// shortNameFor derives the 11-byte 8.3 form. A name that is already
// a valid uppercase 8.3 name needs no long-name records; everything
// else gets an uppercased, truncated stand-in and rides with them.
// The extension comes from the last dot, the DOS convention, and a
// stand-in that collides with an earlier sibling's gets the classic
// ~N tail: readers match on the long names, but the short names
// still have to be unique or fsck calls the directory corrupt.
func shortNameFor(name string, taken map[string]bool) (string, bool) {
	base, ext := name, ""
	if i := strings.LastIndex(name, "."); i > 0 {
		base, ext = name[:i], name[i+1:]
	}
	fits := len(base) >= 1 && len(base) <= 8 && len(ext) <= 3 && !strings.Contains(base, ".")
	upper := strings.ToUpper(name)
	simple := strings.IndexFunc(upper, func(r rune) bool {
		return (r < 'A' || r > 'Z') && (r < '0' || r > '9') && r != '.' && r != '-' && r != '_'
	}) < 0
	needsLong := !fits || !simple || name != upper

	base = strings.ToUpper(strings.ReplaceAll(base, ".", ""))
	ext = strings.ToUpper(ext)
	if len(base) > 8 {
		base = base[:8]
	}
	if len(ext) > 3 {
		ext = ext[:3]
	}
	short := fmt.Sprintf("%-8s%-3s", base, ext)
	for tail := 1; taken[short]; tail++ {
		suffix := fmt.Sprintf("~%d", tail)
		trimmed := base[:min(len(base), 8-len(suffix))] + suffix
		short = fmt.Sprintf("%-8s%-3s", trimmed, ext)
		needsLong = true
	}
	return short, needsLong
}

// longNameRecords encodes a name as VFAT long-name records: thirteen
// UTF-16 characters per record, stored last-first, the final piece
// flagged 0x40, every record carrying a checksum of the 8.3 name it
// belongs to so a reader can tell an orphaned long name from a live
// one.
func longNameRecords(name, short string) []byte {
	units := utf16.Encode([]rune(name))
	units = append(units, 0) // NUL-terminated, then padded with 0xFFFF
	for len(units)%13 != 0 {
		units = append(units, 0xFFFF)
	}

	sum := byte(0)
	for i := range 11 {
		sum = (sum>>1 | sum<<7) + short[i]
	}

	pieces := len(units) / 13
	var records []byte
	for piece := pieces; piece >= 1; piece-- {
		r := make([]byte, 32)
		r[0] = byte(piece)
		if piece == pieces {
			r[0] |= 0x40 // the last piece of the name, stored first
		}
		r[11] = 0x0F // the attribute combination DOS readers skip
		r[13] = sum
		chunk := units[(piece-1)*13 : piece*13]
		for i, u := range chunk[0:5] {
			binary.LittleEndian.PutUint16(r[1+2*i:], u)
		}
		for i, u := range chunk[5:11] {
			binary.LittleEndian.PutUint16(r[14+2*i:], u)
		}
		for i, u := range chunk[11:13] {
			binary.LittleEndian.PutUint16(r[28+2*i:], u)
		}
		records = append(records, r...)
	}
	return records
}

func parentOf(p string) string {
	dir := path.Dir(p)
	if dir == "." || dir == "/" {
		return ""
	}
	return dir
}
