package main

// store.go lays out the driver's directory on the node. copies/ has
// one directory for each volume handle, which is the volume's copy.
// holds/ has one directory for each handle that a pod has published,
// with one mount record file for each target path the handle is
// published at. The directory keeps the name holds/ because the records
// on a node's disk must still load after an upgrade.

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

const (
	copiesDirectory  = "copies"
	recordsDirectory = "holds"
)

// A pod can run as any user, and the CSIDriver sets fsGroupPolicy to
// None, so the kubelet does not change the copy's group. So every user
// must be able to write the copy.
const copyMode = 0o777

type store struct {
	root string
}

func newStore(root string) *store {
	return &store{root: root}
}

func (s *store) copyPath(handle string) string {
	return filepath.Join(s.root, copiesDirectory, handle)
}

func (s *store) recordsPath(handle string) string {
	return filepath.Join(s.root, recordsDirectory, handle)
}

// recordPath names the record file after a hash of the target, because the
// target is a path and has slashes in it.
func (s *store) recordPath(handle, target string) string {
	sum := sha256.Sum256([]byte(target))
	return filepath.Join(s.recordsPath(handle), hex.EncodeToString(sum[:16]))
}

// makeCopy calls Chmod after MkdirAll, because the umask removes bits
// from the mode that MkdirAll sets.
func (s *store) makeCopy(handle string) error {
	path := s.copyPath(handle)
	if err := os.MkdirAll(path, copyMode); err != nil {
		return fmt.Errorf("creating the copy directory: %w", err)
	}
	return os.Chmod(path, copyMode)
}

// handles returns the handle of every copy in the store. It returns
// nil when the copies directory cannot be read, such as when no copy
// was ever made.
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

func (s *store) removeCopy(handle string) error {
	return os.RemoveAll(s.copyPath(handle))
}

// copySize returns the total size of the regular files under dir.
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
// contains path. Every copy on the node shares that filesystem.
func spaceOf(path string) (int64, int64, error) {
	var filesystem unix.Statfs_t
	if err := unix.Statfs(path, &filesystem); err != nil {
		return 0, 0, err
	}
	block := int64(filesystem.Bsize)
	return int64(filesystem.Bavail) * block, int64(filesystem.Blocks) * block, nil
}
