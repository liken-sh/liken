package main

// A test process cannot call mount(2), so the storage tests replace
// mountFilesystem and unmountFilesystem with a recorder. The recorder
// copies two kernel behaviors that the code under test depends on: a
// read-write FAT mount sets the volume's dirty bit, and a mounted
// device's files appear at the target.

import (
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"

	"github.com/liken-sh/liken/liken/disks"
	"github.com/liken-sh/liken/liken/machine"
)

// storageMount is one recorded mount call.
type storageMount struct {
	source, target, fstype string
	flags                  uintptr
}

// storageUnmount is one recorded unmount call.
type storageUnmount struct {
	target string
	flags  int
}

// storageMountTable records mounts and unmounts. When a device in
// contents mounts, the files of its directory are copied to the target.
type storageMountTable struct {
	mounts      []storageMount
	unmounts    []storageUnmount
	contents    map[string]string
	mountErr    error
	unmountErrs map[string]error
}

func fakeStorageMounts(t *testing.T) *storageMountTable {
	t.Helper()
	table := &storageMountTable{contents: map[string]string{}, unmountErrs: map[string]error{}}
	oldMount, oldUnmount := mountFilesystem, unmountFilesystem
	mountFilesystem = table.mount
	unmountFilesystem = table.unmount
	t.Cleanup(func() { mountFilesystem, unmountFilesystem = oldMount, oldUnmount })
	return table
}

func (m *storageMountTable) mount(source, target, fstype string, flags uintptr, _ string) error {
	m.mounts = append(m.mounts, storageMount{source: source, target: target, fstype: fstype, flags: flags})
	if m.mountErr != nil {
		return m.mountErr
	}
	if fstype == "vfat" && flags&unix.MS_RDONLY == 0 && disks.HasFAT32(source) {
		if err := setFATDirty(source, true); err != nil {
			return err
		}
	}
	if dir, ok := m.contents[source]; ok {
		return os.CopyFS(target, os.DirFS(dir))
	}
	return nil
}

func (m *storageMountTable) unmount(target string, flags int) error {
	m.unmounts = append(m.unmounts, storageUnmount{target: target, flags: flags})
	return m.unmountErrs[target]
}

func (m *storageMountTable) unmountedPaths() []string {
	var paths []string
	for _, u := range m.unmounts {
		paths = append(paths, u.target)
	}
	return paths
}

// fatStateByte is the offset of the dirty bit in a FAT32 boot sector,
// the same offset that disks/fat32_state.go reads.
const fatStateByte = 0x41

// setFATDirty sets or clears the dirty bit on a FAT32 volume, as the
// kernel's driver does when it mounts or unmounts the volume.
func setFATDirty(path string, dirty bool) error {
	f, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	defer f.Close()
	b := make([]byte, 1)
	if _, err := f.ReadAt(b, fatStateByte); err != nil {
		return err
	}
	if dirty {
		b[0] |= 0x01
	} else {
		b[0] &^= 0x01
	}
	_, err = f.WriteAt(b, fatStateByte)
	return err
}

// fatVolumeAt formats a 64 MiB FAT32 volume at path and returns the
// path. When dirty is true, the volume has the dirty bit set that an
// unclean stop leaves. The file is sparse, so it uses only the blocks
// that the formatter writes.
func fatVolumeAt(t *testing.T, path string, dirty bool) string {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("creating the volume file: %v", err)
	}
	defer f.Close()
	if err := f.Truncate(64 << 20); err != nil {
		t.Fatalf("sizing the volume file: %v", err)
	}
	if err := disks.FormatFAT32(f, 64<<20, "TEST", 0x12345678); err != nil {
		t.Fatalf("formatting the FAT32 volume: %v", err)
	}
	if err := setFATDirty(path, dirty); err != nil {
		t.Fatalf("setting the dirty bit: %v", err)
	}
	return path
}

func dirtyVolume(t *testing.T) string {
	t.Helper()
	return fatVolumeAt(t, filepath.Join(t.TempDir(), "volume.img"), true)
}

// isDirty reads the dirty bit with the same function as the boot
// code.
func isDirty(t *testing.T, path string) bool {
	t.Helper()
	dirty, err := disks.FAT32Dirty(path)
	if err != nil {
		t.Fatalf("reading the dirty bit: %v", err)
	}
	return dirty
}

// fakeRoleMountPath changes one role's mount point to a temporary
// directory for one test, and returns the directory.
func fakeRoleMountPath(t *testing.T, name machine.StorageRoleName) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), string(name))
	old := roleMounts[name]
	rm := old
	rm.path = dir
	roleMounts[name] = rm
	t.Cleanup(func() { roleMounts[name] = old })
	return dir
}

func modeOf(t *testing.T, path string) fs.FileMode {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("reading the file mode: %v", err)
	}
	return info.Mode() & (fs.ModePerm | fs.ModeSticky)
}
