package main

// sparse.go limits the work tree of a writeable volume with paths to
// those paths, with git's sparse checkout, and decides whether a path
// in the tree is inside them.

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// sparseCheckoutFile is the file in the git directory that lists the
// patterns git checks out. git worktree add copies it into the scratch
// tree's own git directory, so a rebase checks out only the same paths.
const sparseCheckoutFile = "info/sparse-checkout"

// everyPath is the pattern that matches every path at the root, and so
// the whole tree. A tree that leaves its paths writes it before it
// turns the sparse checkout off, as git sparse-checkout disable does,
// so that read-tree clears every skip-worktree bit first.
const everyPath = "/*\n"

// inside reports whether name, a path relative to the root of the tree
// with forward slashes, is one of the entries or is under one. Every
// path is inside when entries is nil, because a volume with no paths
// checks out the whole tree.
func inside(entries []string, name string) bool {
	if entries == nil {
		return true
	}
	for _, entry := range entries {
		if name == entry || strings.HasPrefix(name, entry+"/") {
			return true
		}
	}
	return false
}

// topEntries returns the entries that are not under another entry. A
// walk of entry a covers a/b, so walking both would record a/b twice.
func topEntries(entries []string) []string {
	top := []string{}
	for _, entry := range entries {
		if !inside(top, entry) {
			top = append(top, entry)
		}
	}
	return top
}

// sparsePatterns formats one pattern for each entry. A pattern that
// starts with a slash matches only at the root, and a pattern with no
// slash at its end matches a file or a directory with all its contents.
// So one form serves a directory entry, a file entry, and an entry that
// the commit does not have yet, whose kind nobody knows.
func sparsePatterns(entries []string) string {
	lines := &strings.Builder{}
	for _, entry := range entries {
		fmt.Fprintf(lines, "/%s\n", escapePattern(entry))
	}
	return lines.String()
}

// escapePattern puts a backslash before each character that git reads
// as a wildcard or an escape in a pattern, so an entry such as
// pages/[id] matches only the directory of that name. A ! or a # is
// special only as the first character of a line, and every line starts
// with a slash, so neither one needs an escape.
func escapePattern(entry string) string {
	escaped := &strings.Builder{}
	for _, r := range entry {
		if strings.ContainsRune(`*?[\`, r) {
			escaped.WriteByte('\\')
		}
		escaped.WriteRune(r)
	}
	return escaped.String()
}

// parseSparsePatterns reads the entries back from the lines that
// sparsePatterns wrote.
func parseSparsePatterns(content string) []string {
	entries := []string{}
	for _, line := range strings.Split(content, "\n") {
		if line == "" {
			continue
		}
		entry := &strings.Builder{}
		escaped := false
		for _, r := range strings.TrimPrefix(line, "/") {
			if r == '\\' && !escaped {
				escaped = true
				continue
			}
			escaped = false
			entry.WriteRune(r)
		}
		entries = append(entries, entry.String())
	}
	return entries
}

// describePaths names the entries in a message, each one quoted so an
// entry with a space reads as one entry.
func describePaths(entries []string) string {
	if entries == nil {
		return "the whole tree"
	}
	quoted := make([]string, len(entries))
	for i, entry := range entries {
		quoted[i] = strconv.Quote(entry)
	}
	return strings.Join(quoted, ", ")
}

// sparse turns on the sparse checkout in pattern mode and writes the
// patterns. The driver writes the configuration itself, because git
// sparse-checkout set also turns on extensions.worktreeConfig.
//
// Pattern mode checks out exactly the entries. Cone mode also checks
// out every file at the root and every file directly inside a parent
// directory of an entry, and it cannot name a file entry.
//
// sparse.expectFilesOutsideOfPatterns makes git ignore a file that the
// pod writes outside the patterns. Without it, git clears the
// skip-worktree bit of a tracked file that the pod writes there. git
// status then reports the file, the tree never shows clean, and a later
// read-tree -m -u fails with "Entry not uptodate".
func (w *workTree) sparse(ctx context.Context, patterns string) error {
	for _, setting := range [][]string{
		{"core.sparseCheckout", "true"},
		{"core.sparseCheckoutCone", "false"},
		{"sparse.expectFilesOutsideOfPatterns", "true"},
	} {
		if _, err := w.git(ctx, "config", "--local", "--", setting[0], setting[1]); err != nil {
			return err
		}
	}
	path := filepath.Join(w.gitDir, sparseCheckoutFile)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(patterns), 0o644)
}

// checkedOut returns the entries that the tree checks out now, read
// from its patterns, or nil when the tree checks out the whole tree.
func (w *workTree) checkedOut() ([]string, error) {
	content, err := os.ReadFile(filepath.Join(w.gitDir, sparseCheckoutFile))
	if absent(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return parseSparsePatterns(string(content)), nil
}

// changePaths runs at each stage of a work tree that already exists. A
// tree kept under the Retain reclaim policy can come back under a new
// PersistentVolume with other paths, and the tree then checks out the
// new paths. git read-tree -m -u HEAD checks out the files of the new
// entries and removes the files of the old entries that the pod did not
// change.
//
// A change that the pod made inside an old entry and the driver has not
// committed refuses the stage, because under the new paths the driver
// would never commit that change.
func (w *workTree) changePaths(ctx context.Context) error {
	old, err := w.checkedOut()
	if err != nil {
		return status.Error(codes.Internal, err.Error())
	}
	if slices.Equal(old, w.paths) {
		return nil
	}
	found, err := w.pendingIn(ctx, old)
	if err != nil {
		return status.Error(codes.Internal, err.Error())
	}
	if len(found) > 0 {
		return status.Errorf(codes.FailedPrecondition,
			"paths: the work tree on this node checks out %s, and the volume names %s. "+
				"The tree has %d uncommitted paths inside %s. "+
				"The driver never commits a path outside the volume's paths, so the stage leaves the tree as it is",
			describePaths(old), describePaths(w.paths), len(found), describePaths(old))
	}
	if err := w.checkOutPaths(ctx); err != nil {
		return status.Error(codes.Internal, err.Error())
	}
	return nil
}

// checkOutPaths writes the volume's patterns and brings the files of
// the tree to them. A volume with no paths gets the pattern for every
// path first, and then the sparse checkout is turned off.
func (w *workTree) checkOutPaths(ctx context.Context) error {
	patterns := everyPath
	if w.paths != nil {
		patterns = sparsePatterns(w.paths)
	}
	if err := w.sparse(ctx, patterns); err != nil {
		return err
	}
	if _, err := w.git(ctx, "read-tree", "-m", "-u", "--end-of-options", "HEAD"); err != nil {
		return err
	}
	if w.paths != nil {
		return nil
	}
	if _, err := w.git(ctx, "config", "--local", "--", "core.sparseCheckout", "false"); err != nil {
		return err
	}
	return os.Remove(filepath.Join(w.gitDir, sparseCheckoutFile))
}

// present reports whether the path, relative to the root of the tree,
// is in the checkout.
func (w *workTree) present(name string) bool {
	_, err := os.Lstat(filepath.Join(w.tree, filepath.FromSlash(name)))
	return !absent(err)
}
