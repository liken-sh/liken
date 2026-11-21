package main

// treediff.go moves a read-only volume's published tree from one commit
// to the next by writing only the files that differ between the two
// commits. An unchanged file keeps its inode, so a pod that watches its
// files sees only the files that changed.

import (
	"context"
	"errors"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"golang.org/x/sys/unix"
)

// treeDiff is what changed between two commits within a volume's
// entries.
type treeDiff struct {
	// updated has each path that the new commit has and the old one does
	// not have in the same form: a file, symlink, or submodule that was
	// added, or that changed its content, its mode, or its type.
	updated []string
	// deleted has each path that only the old commit has.
	deleted []string
	// attributes is true when a .gitattributes file that applies to the
	// entries changed. A changed attribute can change the bytes of a file
	// that did not change in git, so only a full checkout is correct.
	attributes bool
}

func (d treeDiff) empty() bool {
	return len(d.updated) == 0 && len(d.deleted) == 0
}

// attributesFile is the name of the file whose rules, such as
// `text eol=crlf`, change the bytes that git checks out.
const attributesFile = ".gitattributes"

// changesSince returns what changed within the entries between the
// commit that the tree was placed from and the commit. It returns false
// when only a full checkout makes the tree correct: the tree or its
// recorded commit is missing, the repository no longer has the old
// commit, or a .gitattributes file that applies to the entries changed.
//
// git exits with 128 when the old commit is gone, for example after a
// forced push and a sweep of the store. It exits with 128 for its other
// fatal errors too, so any failure of diff-tree leads to a full
// checkout, which reports the fault itself when the repository cannot
// be read.
func (r *repository) changesSince(
	ctx context.Context, directory, tree, commit string, entries []string,
) (treeDiff, bool) {
	old := placedCommit(directory)
	if old == "" {
		return treeDiff{}, false
	}
	if _, err := os.Stat(tree); err != nil {
		return treeDiff{}, false
	}
	args := []string{"diff-tree", "-r", "-z", "--no-renames", "--raw", old, commit, "--"}
	output, err := runGit(ctx, r.dir, literalPathspecs, append(args, diffPathspec(entries)...)...)
	if err != nil {
		return treeDiff{}, false
	}
	changes := parseDiff(output.stdout)
	return changes, !changes.attributes
}

// diffPathspec returns the entries and every .gitattributes file on the
// way from the root of the repository to each entry, because a rule in
// any of them applies to the entry's files. A .gitattributes inside an
// entry is under the entry already. With no entries, the pathspec is
// empty and diff-tree compares the whole tree, so a .gitattributes
// anywhere is in its output.
func diffPathspec(entries []string) []string {
	pathspec := slices.Clone(entries)
	for _, entry := range entries {
		for dir := path.Dir(entry); ; dir = path.Dir(dir) {
			pathspec = append(pathspec, path.Join(dir, attributesFile))
			if dir == "." {
				break
			}
		}
	}
	slices.Sort(pathspec)
	return slices.Compact(pathspec)
}

// parseDiff reads the output of diff-tree -z --raw. Each change is a
// header such as ":100644 000000 <old> <new> D" and then the path, each
// ended by a NUL byte. A new mode of 000000 means that the new commit
// has no such path.
func parseDiff(output string) treeDiff {
	fields := strings.Split(output, "\x00")
	changes := treeDiff{}
	for i := 0; i+1 < len(fields); i += 2 {
		header, name := strings.Fields(fields[i]), fields[i+1]
		if path.Base(name) == attributesFile {
			changes.attributes = true
		}
		if len(header) > 1 && header[1] == "000000" {
			changes.deleted = append(changes.deleted, name)
			continue
		}
		changes.updated = append(changes.updated, name)
	}
	return changes
}

// update writes the changes into the published tree. It checks out the
// updated paths into nextTree first, so a failed checkout leaves the
// published tree and placedFile as they were. A commit that changes
// nothing within the entries writes no file in the tree and only
// records the new commit.
func (r *repository) update(ctx context.Context, commit, directory, tree string, changes treeDiff) error {
	if changes.empty() {
		return recordPlaced(directory, commit)
	}
	fresh := filepath.Join(directory, nextTree)
	if len(changes.updated) > 0 {
		if err := r.checkoutChanged(ctx, commit, fresh, changes.updated); err != nil {
			return err
		}
	}
	if err := forgetPlaced(directory); err != nil {
		return err
	}
	if err := applyChanges(fresh, tree, changes); err != nil {
		return err
	}
	// The next call to place removes fresh before it checks out, so a
	// failed removal here is harmless.
	_ = os.RemoveAll(fresh)
	return recordPlaced(directory, commit)
}

// checkoutChanged checks out only the paths into dir. git reads the
// paths from a file, because a commit can change more files than fit
// on one command line. The file is beside dir and deleted afterward.
func (r *repository) checkoutChanged(ctx context.Context, commit, dir string, paths []string) error {
	list := dir + ".paths"
	defer os.Remove(list)
	if err := os.WriteFile(list, []byte(strings.Join(paths, "\x00")), 0o600); err != nil {
		return err
	}
	return r.checkoutFrom(ctx, commit, dir,
		[]string{"--pathspec-from-file=" + list, "--pathspec-file-nul"})
}

// applyChanges deletes each deleted path, moves each updated path from
// fresh into published with one rename, and then removes each
// directory that the deletes left empty. The deletes come first,
// because a path whose type changed from a file to a directory, or the
// other way, is a delete of the old path and an update of the new one.
// The empty directories go last, so a directory that loses one file and
// gains another keeps its inode, and a pod with a subPath mount of it
// keeps working.
func applyChanges(fresh, published string, changes treeDiff) error {
	for _, name := range changes.deleted {
		if err := os.Remove(filepath.Join(published, name)); err != nil {
			return err
		}
	}
	for _, name := range changes.updated {
		if err := renameInto(fresh, published, name); err != nil {
			return err
		}
	}
	for _, name := range changes.deleted {
		if err := removeEmptyParents(published, name); err != nil {
			return err
		}
	}
	return nil
}

// renameInto renames one path from fresh into published, and makes the
// directories above it that published does not have yet. A rename
// replaces a file or a symlink in one step, so a reader sees the old
// file or the new one. A rename cannot replace a directory with a file,
// or a file with a directory, so renameInto first removes the path in
// published when either side is a directory. A submodule is an empty
// directory in both trees.
func renameInto(fresh, published, name string) error {
	from := filepath.Join(fresh, name)
	to := filepath.Join(published, name)
	if err := os.MkdirAll(filepath.Dir(to), 0o755); err != nil {
		return err
	}
	if there, err := os.Lstat(to); err == nil && (there.IsDir() || isDirectory(from)) {
		if err := os.RemoveAll(to); err != nil {
			return err
		}
	}
	return os.Rename(from, to)
}

func isDirectory(name string) bool {
	info, err := os.Lstat(name)
	return err == nil && info.IsDir()
}

// removeEmptyParents removes each directory above the deleted path that
// is empty, from the deepest up. It stops at the first directory that
// has an entry, that is no longer a directory, or that is gone, because
// every directory above that one has an entry too. It never removes the
// root of the published tree, which the pods have mounted. It never
// removes an entry's own directory either, because place checked that
// the commit has every entry, and git has no empty directories, so an
// entry's directory always has a file or a submodule in it.
func removeEmptyParents(published, deleted string) error {
	for dir := filepath.Dir(deleted); dir != "."; dir = filepath.Dir(dir) {
		err := unix.Rmdir(filepath.Join(published, dir))
		switch {
		case err == nil:
		case errors.Is(err, unix.ENOTEMPTY), errors.Is(err, unix.EEXIST),
			errors.Is(err, unix.ENOTDIR), errors.Is(err, unix.ENOENT):
			return nil
		default:
			return err
		}
	}
	return nil
}
