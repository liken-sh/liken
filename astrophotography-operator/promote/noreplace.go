package promote

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

var errNameTaken = errors.New("the destination name already exists")

// renameNoReplace renames from to to, and returns errNameTaken when to
// already exists.
//
// It calls renameat2 with RENAME_NOREPLACE. Many archives are on NFS or
// SMB shares, which fail that call with EINVAL. When the file system or
// the kernel does not support the call, renameNoReplace creates a hard
// link and then removes the old name instead. The link fails with
// EEXIST when to exists, so this path never replaces a file either. A
// crash between the link and the removal leaves the frame under both
// names, and the next run finds the archived file, removes the other
// name, and deletes the frame from the outbox (finishPlaced in
// promote.go).
func renameNoReplace(from, to string) error {
	err := unix.Renameat2(unix.AT_FDCWD, from, unix.AT_FDCWD, to, unix.RENAME_NOREPLACE)
	if errors.Is(err, unix.EINVAL) || errors.Is(err, unix.ENOSYS) || errors.Is(err, unix.EOPNOTSUPP) {
		err = linkThenUnlink(from, to)
	}
	if errors.Is(err, unix.EEXIST) {
		return errNameTaken
	}
	return err
}

func linkThenUnlink(from, to string) error {
	if err := unix.Link(from, to); err != nil {
		return err
	}
	return os.Remove(from)
}
