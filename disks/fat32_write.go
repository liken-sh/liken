package disks

// This file writes files into a FAT32 volume, without mounting it.
//
// On a running machine, the kernel's vfat driver does this. A build
// tool that lays out an install image has no kernel to ask, so it
// performs the driver's role directly. The job is smaller than it
// sounds, because of what a FAT filesystem is. The FAT itself is one
// 32-bit entry for each cluster, and each entry names the next
// cluster of its file. A directory is only a file whose bytes are
// 32-byte records. Writing a file means: pick clusters, chain them
// in the table, put the bytes in the clusters, and add a record to
// the parent directory's bytes.
//
// This writer is deliberately narrower than a driver. It only ever
// fills a freshly formatted volume, once. Because of this,
// allocation is a bump pointer: the next free cluster is always the
// next cluster in sequence. There are no deletes and no
// fragmentation, and this code can hold the whole directory tree in
// memory until Close writes it out.
//
// The one genuinely complex part is names. FAT's native names use
// the DOS 8.3 form: eleven uppercase bytes. Any name that is longer,
// or that uses lowercase letters, uses the VFAT extension instead: a
// chain of "long name" records placed ahead of the real one. Each
// long-name record carries thirteen UTF-16 characters, and carries
// an attribute combination (read-only + hidden + system + volume
// label) that DOS-era readers were guaranteed to skip. Firmware and
// every current OS read these long-name records. loader.conf and
// deployment.cpio do not fit the 8.3 form, so this writer produces
// long-name records for them.

import (
	"encoding/binary"
	"fmt"
	"io"
	"path"
	"strings"
	"unicode/utf16"
)

// endOfChain is the FAT entry that marks a file's last cluster.
// Every value at and above 0x0FFFFFF8 means end-of-chain. This is
// the conventional value that every formatter writes.
const endOfChain = 0x0FFFFFF7 + 1

// A fatFile is one file waiting for its directory record. It
// records where its chain starts and how long the file really is.
// FAT records exact sizes, but the chain rounds up to full clusters.
type fatFile struct {
	name         string
	firstCluster uint32
	size         int64
}

// A fatDir accumulates a directory's children until Close writes
// the records. Directories get their clusters at Close, after this
// code knows every file's chain, because a record needs its child's
// first cluster.
type fatDir struct {
	name         string
	parent       *fatDir
	files        []fatFile
	subdirs      []*fatDir
	firstCluster uint32
}

// A FATWriter fills a formatted FAT32 volume with files. The
// geometry comes from the volume's own boot sector, so the writer
// and the format always agree about where everything is.
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

// NewFATWriter opens a freshly formatted volume for filling. It
// reads the geometry back from the boot sector that the format just
// wrote.
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

	// This holds the whole table in memory: the clusters, plus the
	// two flag entries. Entries 0 and 1 repeat what the format
	// wrote. Cluster 2 is the root directory's single starting
	// cluster.
	w.fat = make([]uint32, clusters+2)
	w.fat[0] = 0x0FFFFFF8
	w.fat[1] = 0x0FFFFFFF
	w.fat[2] = endOfChain
	w.next = 3

	// The format left exactly one record in the root: the volume
	// label. This code keeps it, so that Close can write the root
	// back with the label first, the way tools expect.
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
// and 1 do not exist. The data region begins at cluster 2.
func (w *FATWriter) clusterOffset(n uint32) int64 {
	return (int64(w.dataStart) + int64(n-2)*int64(w.sectorsPerCluster)) * SectorSize
}

// allocate takes n consecutive clusters and chains them, and
// returns the first one. Because the writer writes each volume only
// once, allocate can work as a simple bump pointer.
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

// Mkdir registers a directory. Its parent must already exist. This
// matches how the archive writers in this package expect to be
// called.
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

// WriteFile streams one file's bytes into freshly allocated
// clusters, and records the file for its directory record. Streaming
// matters here, because the files in an OS image can be hundreds of
// megabytes.
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

// Close writes the data that depends on the complete directory
// tree: each directory's records, which need every child's first
// cluster, both copies of the FAT, and the free-space accounting.
// Close assigns clusters to every directory first, in one pass, so
// that parent and child records can point at each other regardless
// of order.
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

	// This is the assignment pass. Every directory gets a chain
	// sized for its records. The root's chain starts at its fixed
	// cluster 2, and Close extends it only if the label and children
	// outgrow one cluster.
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

	// This is the content pass. With every cluster known, this code
	// writes each directory's records along its chain.
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

	// This writes both FATs, byte for byte the same. The second
	// copy is FAT's only redundancy, so writing it identically is
	// the whole purpose of the copy.
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

	// This writes the primary and backup FSInfo sectors. The free
	// count is exact, because the bump allocator tracks precisely
	// what it used.
	free := uint32(len(w.fat)) - w.next
	info := buildFAT32FSInfo(free, w.next)
	for _, sector := range []int64{1, 7} {
		if _, err := w.dev.WriteAt(info, sector*SectorSize); err != nil {
			return fmt.Errorf("writing FSInfo: %w", err)
		}
	}
	return w.dev.Sync()
}

// dirRecords lays out one directory's 32-byte records. It writes
// the label (root only), the dot entries (subdirectories only), and
// then every child. Each child record is preceded by its long-name
// records when the 8.3 form cannot carry the name alone. Short
// names must be unique within their directory, because fsck treats
// a duplicate as corruption. For this reason, when two truncated
// stand-in names collide, this code adds the classic ~N tail.
func (w *FATWriter) dirRecords(d *fatDir, self uint32) []byte {
	var records []byte
	if d == w.root {
		records = append(records, w.labelEntry...)
	} else {
		// "." and ".." are ordinary records with fixed names. By
		// specification, ".." pointing at the root writes cluster 0,
		// not 2. This is a DOS-era exception that every reader
		// expects.
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

// plainRecord is one 32-byte record whose 8.3 name is already
// known. The timestamps are all zero, for the same reason as the
// cpio writer's timestamps: the image's bytes should depend on its
// contents, not on when it was built. FAT cannot express a year
// before 1980, so a zero date reads back as 1980-00-00. This value
// is meaningless but harmless, because nothing on an install stick
// reads it.
func plainRecord(shortName string, attr byte, firstCluster uint32, size int64) []byte {
	r := make([]byte, 32)
	copy(r[0:11], shortName)
	r[11] = attr
	binary.LittleEndian.PutUint16(r[20:22], uint16(firstCluster>>16))
	binary.LittleEndian.PutUint16(r[26:28], uint16(firstCluster))
	binary.LittleEndian.PutUint32(r[28:32], uint32(size))
	return r
}

// namedRecords builds a child's full record set: the plain record
// under its 8.3 name, preceded by long-name records when the real
// name needs them. taken tracks the directory's short names, so
// that no two children share one.
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
// a valid uppercase 8.3 name needs no long-name records. Every
// other name gets an uppercased, truncated stand-in, and carries
// long-name records alongside it. The extension comes from the last
// dot, following the DOS convention. When a stand-in collides with
// an earlier sibling's, this code adds the classic ~N tail. Readers
// match names using the long-name records, but the short names
// still must be unique, or fsck reports the directory as corrupt.
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

// longNameRecords encodes a name as VFAT long-name records. Each
// record holds thirteen UTF-16 characters, and the records are
// stored last piece first. The final piece is flagged 0x40. Every
// record carries a checksum of the 8.3 name that it belongs to, so
// a reader can tell a stray long-name record from one that belongs
// to a live entry.
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
