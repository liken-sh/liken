package main

// Every file and directory the operator lands on a library volume takes the
// owner and the group of the directory it lands in. Radarr and Sonarr own the
// folders they make, and they rewrite and delete the files in them on an
// upgrade. Radarr's import writes movie.nfo again after it replaces a movie's
// file, and when that file belongs to the operator's root user the write
// fails with EACCES and Radarr records the import as failed. A file that
// takes its folder's owner is one the tool that owns the folder can change,
// and no setting has to name that tool's uid and gid.

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"
)

// Give path the owner and the group of directory. The hand-over never fails
// a write: a file it cannot hand over keeps the owner it was written with,
// which is what every file was before this rule. A run as a user other than
// root, and a share that maps root to another user, both refuse the change,
// and a directory that is gone fails the write's own rename after this.
func adoptOwner(path, directory string) {
	info, err := os.Stat(directory)
	if err != nil {
		return
	}
	owner := info.Sys().(*syscall.Stat_t)
	_ = os.Lchown(path, int(owner.Uid), int(owner.Gid))
}

// The same for a staged tree and everything under it, before the rename lands
// it, so no reader sees the tree under its real name with the wrong owner.
func adoptTreeOwner(root, directory string) {
	_ = filepath.WalkDir(root, func(path string, _ fs.DirEntry, _ error) error {
		adoptOwner(path, directory)
		return nil
	})
}

// Make a directory and each missing directory above it, and give every
// directory it made the owner of the nearest one that was already there.
func (w *volumeWriter) makeDirectory(directory string) error {
	var made []string
	held := filepath.Clean(directory)
	for {
		if _, err := os.Lstat(held); err == nil {
			break
		} else if !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		made = append(made, held)
		held = filepath.Dir(held)
	}
	if err := os.MkdirAll(directory, volumeDirectoryPerm); err != nil {
		return err
	}
	for _, path := range made {
		adoptOwner(path, held)
	}
	return nil
}
