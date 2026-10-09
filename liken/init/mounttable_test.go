package main

// A stand-in for the mount table, shared by the tests of storage
// reconciliation, the FAT slot check, and the early slot mount. A test
// process cannot call mount(2), so these tests replace the
// mountFilesystem and unmountFilesystem seams with a recorder that
// behaves the way the kernel does in the two ways the code depends on:
// a writable FAT mount sets the volume's mark, and a mounted device's
// files appear at the target.

import (
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"

	"github.com/liken-sh/liken/liken/disks"
	"github.com/liken-sh/liken/liken/machine"
)

// storageMount is one call to the mount seam.
type storageMount struct {
	source, target, fstype string
	flags                  uintptr
}

// storageUnmount is one call to the unmount seam.
type storageUnmount struct {
	target string
	flags  int
}

// storageMountTable records mounts and unmounts. A device listed in
// contents shows that directory's files at the target when it mounts.
// A path listed in unmountErrs answers its unmount with that error,
// and mountErr, when set, answers every mount.
type storageMountTable struct {
	mounts      []storageMount
	unmounts    []storageUnmount
	contents    map[string]string
	mountErr    error
	unmountErrs map[string]error
}

// fakeStorageMounts installs a storageMountTable in place of the mount
// syscalls, and restores the real syscalls when the test ends.
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
		if err := setFATMark(source, true); err != nil {
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

// unmountedPaths lists the targets of every unmount, in order.
func (m *storageMountTable) unmountedPaths() []string {
	var paths []string
	for _, u := range m.unmounts {
		paths = append(paths, u.target)
	}
	return paths
}

// fatStateByte is where a FAT32 boot sector keeps the volume's mark,
// the same offset disks/fat32_state.go reads.
const fatStateByte = 0x41

// setFATMark sets or clears the mark on a FAT32 volume, the way the
// kernel's driver does when it mounts or releases the volume.
func setFATMark(path string, marked bool) error {
	f, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	defer f.Close()
	b := make([]byte, 1)
	if _, err := f.ReadAt(b, fatStateByte); err != nil {
		return err
	}
	if marked {
		b[0] |= 0x01
	} else {
		b[0] &^= 0x01
	}
	_, err = f.WriteAt(b, fatStateByte)
	return err
}

// fatVolumeAt formats a FAT32 volume at path, with or without the mark
// of a stop that did not release it, and returns the path. The file is
// sparse, so its 64 MiB cost only the blocks the formatter writes.
func fatVolumeAt(t *testing.T, path string, marked bool) string {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := f.Truncate(64 << 20); err != nil {
		t.Fatal(err)
	}
	if err := disks.FormatFAT32(f, 64<<20, "TEST", 0x12345678); err != nil {
		t.Fatal(err)
	}
	if err := setFATMark(path, marked); err != nil {
		t.Fatal(err)
	}
	return path
}

// markedVolume writes a FAT32 volume that carries the mark, and returns
// its path.
func markedVolume(t *testing.T) string {
	t.Helper()
	return fatVolumeAt(t, filepath.Join(t.TempDir(), "volume.img"), true)
}

// isMarked reads a volume's mark through the same function the boot
// uses.
func isMarked(t *testing.T, path string) bool {
	t.Helper()
	marked, err := disks.FAT32Dirty(path)
	if err != nil {
		t.Fatal(err)
	}
	return marked
}

// fakeRoleMountPath points one role's mount point at a temporary
// directory, the substitution fakeSlotAMount makes for slot A, and
// restores the real mapping afterward.
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

// modeOf reads a directory's permission bits, including the sticky bit.
func modeOf(t *testing.T, path string) fs.FileMode {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return info.Mode() & (fs.ModePerm | fs.ModeSticky)
}
