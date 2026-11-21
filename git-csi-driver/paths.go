package main

// paths.go reads the paths attribute, which limits a volume to some
// directories and files of its repository, and checks that a commit has
// each entry.

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Every git command that takes the entries of paths runs with
// literalPathspecs in its environment, the same as git's
// --literal-pathspecs. git then reads each entry as a path and not as a
// pattern, so "pages/[id]" names one directory and ":odd" names one
// file.

// missingPath is the error of a commit that has no file, directory,
// symlink, or submodule at an entry of paths.
type missingPath struct {
	entry, commit string
}

func (m *missingPath) Error() string {
	return fmt.Sprintf("commit %s has no %q", short(m.commit), m.entry)
}

// checkEntries returns a *missingPath for the first entry that the
// commit does not have, and nil when it has every entry or the volume
// has no paths.
//
// One git rev-parse checks every entry. It accepts a submodule entry,
// which git cat-file -e refuses, because a submodule's commit is in
// another repository. rev-parse prints one object name for each entry
// in order. At the first entry that the commit does not have, it
// prints that argument as it was given and stops, so that line names
// the entry. Any other failure, such as a repository that git cannot
// read, prints no such line and is returned as git's own error.
func (r *repository) checkEntries(ctx context.Context, commit string, entries []string) error {
	if len(entries) == 0 {
		return nil
	}
	args := []string{"rev-parse"}
	for _, entry := range entries {
		args = append(args, commit+":"+entry)
	}
	output, err := runGit(ctx, r.dir, literalPathspecs, args...)
	if err == nil {
		return nil
	}
	printed := strings.Split(output.stdout, "\n")
	for _, entry := range entries {
		if slices.Contains(printed, commit+":"+entry) {
			return &missingPath{entry: entry, commit: commit}
		}
	}
	return err
}

// parsePaths reads the paths attribute into a sorted list with no
// duplicates, so two volumes with the same entries in another order
// record the same value. It strips a trailing slash, because git treats
// a/file/ as missing.
//
// It refuses an entry that could name a place outside the repository's
// tree or inside git's own directory, before git runs. It does not
// refuse glob characters, brackets, backslashes, or a leading colon,
// because the driver runs git with --literal-pathspecs and git then
// reads each of them as part of a file name.
//
// A value with no entry is refused. An empty list would quietly check
// out the whole tree, which is what an absent attribute means.
func parsePaths(value string) ([]string, error) {
	entries := splitList(value)
	if len(entries) == 0 {
		return nil, status.Errorf(codes.InvalidArgument,
			"paths: %q has no entry, and an empty list would check out the whole tree", value)
	}
	for i, entry := range entries {
		entries[i] = strings.TrimSuffix(entry, "/")
		if err := checkPath(entry, entries[i]); err != nil {
			return nil, err
		}
	}
	slices.Sort(entries)
	return slices.Compact(entries), nil
}

// checkPath refuses an entry that is absolute, that has a component
// that is empty, ".", or "..", or that has ".git" as a component. The
// message quotes the entry as the volume wrote it.
func checkPath(written, trimmed string) error {
	if strings.HasPrefix(written, "/") {
		return status.Errorf(codes.InvalidArgument,
			"paths: %q starts with a slash, and an entry is relative to the root of the repository", written)
	}
	for _, component := range strings.Split(trimmed, "/") {
		switch component {
		case "", ".", "..":
			return status.Errorf(codes.InvalidArgument,
				"paths: %q has an empty, . or .. component", written)
		case ".git":
			return status.Errorf(codes.InvalidArgument,
				"paths: %q has .git as a component, and the driver never checks out git's own directory", written)
		}
	}
	return nil
}
