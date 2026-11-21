package main

// paths.go reads the paths attribute, which limits a volume to some
// directories and files of its repository.

import (
	"slices"
	"strings"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

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
