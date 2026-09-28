package main

// store.go holds the driver's two directories on the node: one copy per
// handle under copies/, and one hold per held handle under holds/.

import (
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

// copiesDirectory holds one directory per handle, the copy.
// holdsDirectory holds one file per held handle, the record of the pod
// that holds it.
const (
	copiesDirectory = "copies"
	holdsDirectory  = "holds"
)

// copyMode is the mode of every copy. A pod runs as any user and the
// CSIDriver object sets fsGroupPolicy to None, so the directory itself
// has to let any user write it.
const copyMode = 0o777

// store is the root directory the two subdirectories live under.
type store struct {
	root string
}

func newStore(root string) *store {
	return &store{root: root}
}

// copyPath is the directory the handle names on this node.
func (s *store) copyPath(handle string) string {
	return filepath.Join(s.root, copiesDirectory, handle)
}

// holdPath is the file that records which pod holds the handle on this
// node.
func (s *store) holdPath(handle string) string {
	return filepath.Join(s.root, holdsDirectory, handle)
}

// makeCopy creates the handle's directory when it is absent. It sets
// the mode again after MkdirAll, because the umask removes bits from
// the mode MkdirAll asks for.
func (s *store) makeCopy(handle string) error {
	path := s.copyPath(handle)
	if err := os.MkdirAll(path, copyMode); err != nil {
		return err
	}
	return os.Chmod(path, copyMode)
}

// handles returns every handle that has a copy in the store, and nil
// when the store has never held one.
func (s *store) handles() []string {
	entries, err := os.ReadDir(filepath.Join(s.root, copiesDirectory))
	if err != nil {
		return nil
	}
	found := make([]string, 0, len(entries))
	for _, entry := range entries {
		found = append(found, entry.Name())
	}
	return found
}

// removeCopy removes the handle's directory and everything in it. The
// sweep is its only caller. An unpublish removes nothing.
func (s *store) removeCopy(handle string) error {
	return os.RemoveAll(s.copyPath(handle))
}

// copySize returns the bytes the copy's regular files hold, which is
// what NodeGetVolumeStats reports as used.
func copySize(dir string) (int64, error) {
	var total int64
	err := filepath.Walk(dir, func(_ string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.Mode().IsRegular() {
			total += info.Size()
		}
		return nil
	})
	return total, err
}

// spaceOf returns the available and total bytes of the filesystem that
// holds the path. Every copy on the node shares that filesystem.
func spaceOf(path string) (int64, int64, error) {
	var filesystem unix.Statfs_t
	if err := unix.Statfs(path, &filesystem); err != nil {
		return 0, 0, err
	}
	block := int64(filesystem.Bsize)
	return int64(filesystem.Bavail) * block, int64(filesystem.Blocks) * block, nil
}
