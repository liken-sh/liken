package promote

// A frame takes its name in the archive by a rename that refuses to
// replace a file, so no frame in the archive is ever overwritten, and a
// reader of the archive never sees a partial frame.

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

// errNameTaken is a rename refused because a file holds the new name.
var errNameTaken = errors.New("a file holds the name")

// renameNoReplace gives the file at from the name to, in one step, and
// answers errNameTaken when a file holds to.
//
// Linux does this with renameat2 and RENAME_NOREPLACE. An NFS or SMB
// share refuses that flag with EINVAL, and many archives live on such a
// share, so the fallback is a hard link and an unlink. The link fails
// with EEXIST when the name is taken, which keeps the refusal, and a
// crash between the two leaves the frame under both names, which the
// next run finds (Run).
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
