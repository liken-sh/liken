package main

// metadata.go records and restores what git does not store: file modes
// other than 0644 and 0755, file owners, and empty directories.

import (
	"context"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
)

// metadataRef is a ref that only the driver writes. Each commit on it
// has a tree with the single file metadataFile. commitMetadata writes
// metadataRecordFile and metadataIndexFile in the volume's directory, next
// to its git directory and its tree, and deletes them when it returns.
const (
	metadataRef        = refPrefix + "metadata"
	metadataFile       = "metadata"
	metadataRecordFile = "metadata.record"
	metadataIndexFile  = "metadata.index"
)

// remoteMetadataRef is where a fetch stores the remote's metadata
// record. A rebase reads it for the other writer's modes to replay, and
// uses its commit as the parent of the volume's own record.
const remoteMetadataRef = metadataRef + ".remote"

// metadataMessage is the message of every commit on metadataRef. Each
// commit replaces the whole record, so the message does not describe a
// change.
const metadataMessage = "Update the metadata record"

// The modes that a git checkout gives a file and a directory. The record
// omits a path that has its default mode.
const (
	defaultFileMode fs.FileMode = 0o644
	defaultDirMode  fs.FileMode = 0o755
)

// metadataRecord is the mode and owner of one path, or an empty
// directory.
type metadataRecord struct {
	path      string
	directory bool
	mode      fs.FileMode
	uid       int
	gid       int
}

// walkTreeMetadata returns a record for each path in the tree that has a
// non-default mode, an owner other than the driver's user and group, or,
// for a directory, no entries. The records are sorted by path.
//
// With entries, it walks only the entries, including each entry itself,
// so a file that the pod wrote outside the volume's paths gets no
// record. An entry that is not in the tree has no records.
func walkTreeMetadata(tree string, entries []string) ([]metadataRecord, error) {
	records := []metadataRecord{}
	uid, gid := os.Getuid(), os.Getgid()
	visit := func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if path == tree {
			return nil
		}
		name := filepath.ToSlash(strings.TrimPrefix(path, tree+string(filepath.Separator)))
		// The record has one line per path, so it cannot hold a path
		// that contains a newline.
		if strings.ContainsAny(name, "\n\r") {
			return nil
		}
		if one, keep := pathRecord(name, path, info, uid, gid); keep {
			records = append(records, one)
		}
		return nil
	}
	var err error
	for _, start := range walkStarts(tree, entries) {
		if err = filepath.Walk(start, visit); err != nil {
			break
		}
	}
	sort.Slice(records, func(i, j int) bool { return records[i].path < records[j].path })
	return records, err
}

// walkStarts returns the directories and files where the walk starts:
// the tree for a volume with no paths, or each entry that is in the
// tree and not under another entry.
func walkStarts(tree string, entries []string) []string {
	if entries == nil {
		return []string{tree}
	}
	starts := []string{}
	for _, entry := range topEntries(entries) {
		start := filepath.Join(tree, filepath.FromSlash(entry))
		if _, err := os.Lstat(start); !absent(err) {
			starts = append(starts, start)
		}
	}
	return starts
}

// pathRecord returns the record for one path, and whether the record is
// needed because a checkout would not restore the path's mode, owner, or
// empty directory. Only regular files and directories get a record.
func pathRecord(name, path string, info os.FileInfo, uid, gid int) (metadataRecord, bool) {
	owner, group := ownerOf(info)
	one := metadataRecord{
		path:      name,
		directory: info.IsDir(),
		mode:      info.Mode().Perm(),
		uid:       owner,
		gid:       group,
	}
	own := owner != uid || group != gid
	switch {
	case info.IsDir():
		return one, one.mode != defaultDirMode || own || emptyDirectory(path)
	case info.Mode().IsRegular():
		return one, one.mode != defaultFileMode || own
	}
	return one, false
}

// ownerOf returns the user and group ids of a file. The driver runs
// only on Linux, where Sys returns a *syscall.Stat_t.
func ownerOf(info os.FileInfo) (int, int) {
	stat := info.Sys().(*syscall.Stat_t)
	return int(stat.Uid), int(stat.Gid)
}

// emptyDirectory reports whether the directory has no entries. git does
// not store an empty directory, so the record must.
func emptyDirectory(path string) bool {
	entries, err := os.ReadDir(path)
	return err == nil && len(entries) == 0
}

// metadataContent formats the records, one per line, as the octal mode,
// the user id, the group id, and the path. A directory path ends with a
// slash.
func metadataContent(records []metadataRecord) string {
	lines := &strings.Builder{}
	for _, one := range records {
		name := one.path
		if one.directory {
			name += "/"
		}
		fmt.Fprintf(lines, "%04o %d %d %s\n", one.mode, one.uid, one.gid, name)
	}
	return lines.String()
}

// parseMetadataContent parses the text of a metadata record. It skips a
// line that does not parse, so one bad line does not stop a restore.
func parseMetadataContent(content string) []metadataRecord {
	records := []metadataRecord{}
	for _, line := range strings.Split(content, "\n") {
		if one, ok := parseMetadataLine(line); ok {
			records = append(records, one)
		}
	}
	return records
}

func parseMetadataLine(line string) (metadataRecord, bool) {
	fields := strings.SplitN(line, " ", 4)
	if len(fields) != 4 {
		return metadataRecord{}, false
	}
	mode, err := strconv.ParseUint(fields[0], 8, 32)
	if err != nil {
		return metadataRecord{}, false
	}
	uid, err := strconv.Atoi(fields[1])
	if err != nil {
		return metadataRecord{}, false
	}
	gid, err := strconv.Atoi(fields[2])
	if err != nil {
		return metadataRecord{}, false
	}
	name := strings.TrimSuffix(fields[3], "/")
	// A record names a path inside the tree. A path that leaves the tree
	// would make a restore change files outside it, so it is rejected.
	if !filepath.IsLocal(filepath.FromSlash(name)) {
		return metadataRecord{}, false
	}
	return metadataRecord{
		path:      name,
		directory: strings.HasSuffix(fields[3], "/"),
		mode:      fs.FileMode(mode),
		uid:       uid,
		gid:       gid,
	}, true
}

// recordMetadata commits the tree's current metadata to the metadata
// ref, unless the ref already holds the same record.
//
// Every volume of a URL writes the same ref. A volume with paths walks
// only its entries, so it keeps the lines of its current record that
// are outside its entries. Those lines came from the remote's record
// when the driver last fetched it, at a stage or a rebase, and belong
// to volumes with other paths.
func (w *workTree) recordMetadata(ctx context.Context, rules *policy) error {
	records, err := walkTreeMetadata(w.tree, w.paths)
	if err != nil {
		return err
	}
	current, readErr := w.metadataRecord(ctx, metadataRef)
	content := w.mergedRecord(current, records)
	if readErr == nil && current == content {
		return nil
	}
	return w.commitMetadata(ctx, rules.author(), content, w.refCommit(ctx, metadataRef))
}

// mergedRecord returns the record that this volume writes: the lines of
// base outside the volume's paths, followed by the volume's own records
// inside them. A volume with no paths lists the whole tree, so its
// record is its own records alone.
func (w *workTree) mergedRecord(base string, own []metadataRecord) string {
	if w.paths == nil {
		return metadataContent(own)
	}
	others := []metadataRecord{}
	for _, one := range parseMetadataContent(base) {
		if !inside(w.paths, one.path) {
			others = append(others, one)
		}
	}
	return metadataContent(others) + metadataContent(own)
}

// recordsInside returns the records inside the volume's paths. The
// driver never changes a path outside them.
func (w *workTree) recordsInside(records []metadataRecord) []metadataRecord {
	found := []metadataRecord{}
	for _, one := range records {
		if inside(w.paths, one.path) {
			found = append(found, one)
		}
	}
	return found
}

// metadataRecord returns the text of the record at ref, which is
// metadataRef for the volume's own record or remoteMetadataRef for the
// remote's.
func (w *workTree) metadataRecord(ctx context.Context, ref string) (string, error) {
	output, err := w.git(ctx, "show", "--no-textconv", ref+":"+metadataFile)
	if err != nil {
		return "", err
	}
	return output.stdout, nil
}

// gitSteps runs a sequence of git commands and keeps the first error, so
// a caller checks err once after the sequence.
type gitSteps struct {
	work *workTree
	err  error
}

// run runs git with args and returns its output with the trailing
// newline removed. After an earlier step has failed it runs nothing and
// returns an empty string.
func (s *gitSteps) run(ctx context.Context, env []string, args ...string) string {
	if s.err != nil {
		return ""
	}
	output, err := s.work.gitWith(ctx, env, args...)
	s.err = err
	return trimLine(output.stdout)
}

// commitMetadata commits content to the metadata ref, on top of parent,
// with git plumbing commands and a separate index file. It never
// touches the files that the pod writes or the index that holds the
// pod's staged changes. An empty parent makes the first commit of the
// ref.
func (w *workTree) commitMetadata(ctx context.Context, author []string, content, parent string) error {
	record := filepath.Join(w.directory, metadataRecordFile)
	index := filepath.Join(w.directory, metadataIndexFile)
	defer func() {
		_ = os.Remove(record)
		_ = os.Remove(index)
	}()
	if err := os.WriteFile(record, []byte(content), 0o600); err != nil {
		return err
	}

	steps := &gitSteps{work: w}
	indexed := []string{"GIT_INDEX_FILE=" + index}
	blob := steps.run(ctx, nil, "hash-object", "-w", "--", record)
	steps.run(ctx, indexed, "update-index", "--add",
		"--cacheinfo", "100644,"+blob+","+metadataFile)
	tree := steps.run(ctx, indexed, "write-tree")

	args := []string{"commit-tree", "-m", metadataMessage}
	// The previous record is the parent, so the ref keeps the history of
	// the metadata.
	if parent != "" {
		args = append(args, "-p", parent)
	}
	commit := steps.run(ctx, author, append(args, tree)...)
	steps.run(ctx, nil, "update-ref", metadataRef, commit)
	return steps.err
}

// replayMetadata applies the modes, owners, and empty directories in
// metadataRef to the checkout. owners is false when the driver does not
// run as root and so cannot change owners. That is not an error.
func (w *workTree) replayMetadata(ctx context.Context, logger *slog.Logger, owners bool) error {
	content, err := w.metadataRecord(ctx, metadataRef)
	if err != nil {
		return err
	}
	w.replayRecords(ctx, logger, owners, parseMetadataContent(content))
	return nil
}

// replayMetadataFor applies the remote's record to the paths that an
// update changed, and to no other path. Another writer wrote those
// files, so their modes come from that writer's record, and the modes
// that the pod set on its other files stay.
func (w *workTree) replayMetadataFor(
	ctx context.Context, logger *slog.Logger, owners bool, changed []string,
) error {
	content, err := w.metadataRecord(ctx, remoteMetadataRef)
	if err != nil {
		return err
	}
	w.replayRecords(ctx, logger, owners, recordsUnder(parseMetadataContent(content), changed))
	return nil
}

// recordsUnder returns the records of the changed paths, and the records
// of the directories that contain a changed path or whose parent does.
// The second rule restores an empty directory that another writer added
// beside a file it changed. The root is never an ancestor here, so a
// top-level directory that contains no changed path is not replayed.
func recordsUnder(records []metadataRecord, changed []string) []metadataRecord {
	rewritten, ancestors := map[string]bool{}, map[string]bool{}
	for _, one := range changed {
		rewritten[one] = true
		for dir := path.Dir(one); dir != "."; dir = path.Dir(dir) {
			ancestors[dir] = true
		}
	}
	found := []metadataRecord{}
	for _, one := range records {
		inside := one.directory && (ancestors[one.path] || ancestors[path.Dir(one.path)])
		if rewritten[one.path] || inside {
			found = append(found, one)
		}
	}
	return found
}

// reparentMetadata commits the volume's record again with the remote's
// record as its parent. The record of a volume with no paths lists the
// whole tree, so a rebase only changes the parent and cannot conflict.
// A volume with paths writes the remote's lines outside its entries and
// its own lines inside them, as recordMetadata does. It does nothing
// when either side has no record, or when the remote's commit is
// already an ancestor of the volume's.
func (w *workTree) reparentMetadata(ctx context.Context, author []string) error {
	remote, local := w.refCommit(ctx, remoteMetadataRef), w.refCommit(ctx, metadataRef)
	if remote == "" || local == "" || w.ancestor(ctx, remote, local) {
		return nil
	}
	if w.paths != nil {
		return w.mergeRemoteMetadata(ctx, author, remote)
	}
	steps := &gitSteps{work: w}
	tree := steps.run(ctx, nil, "rev-parse", "--verify", "--end-of-options", local+"^{tree}")
	commit := steps.run(ctx, author, "commit-tree", "-m", metadataMessage, "-p", remote, tree)
	steps.run(ctx, nil, "update-ref", metadataRef, commit)
	return steps.err
}

// mergeRemoteMetadata commits the remote's lines outside the volume's
// paths and the volume's own lines inside them, on top of the remote's
// record.
func (w *workTree) mergeRemoteMetadata(ctx context.Context, author []string, remote string) error {
	theirs, err := w.metadataRecord(ctx, remoteMetadataRef)
	if err != nil {
		return err
	}
	ours, err := w.metadataRecord(ctx, metadataRef)
	if err != nil {
		return err
	}
	own := w.recordsInside(parseMetadataContent(ours))
	return w.commitMetadata(ctx, author, w.mergedRecord(theirs, own), remote)
}

// replayRecords applies each record inside the volume's paths to the
// checkout. A record outside them belongs to a volume with other paths,
// and its path is not in this checkout. It logs a record that it cannot
// apply and continues with the next.
func (w *workTree) replayRecords(
	ctx context.Context, logger *slog.Logger, owners bool, records []metadataRecord,
) {
	if !owners {
		logger.InfoContext(ctx, "Kept the current owners of the checkout, because the driver does not run as root", "tree", w.tree)
	}
	for _, one := range w.recordsInside(records) {
		where := filepath.Join(w.tree, filepath.FromSlash(one.path))
		if one.directory {
			if err := os.MkdirAll(where, one.mode); err != nil {
				logger.WarnContext(ctx, "A directory of the metadata record was not created",
					"path", one.path, "error", err)
				continue
			}
		}
		if err := os.Chmod(where, one.mode); err != nil {
			logger.WarnContext(ctx, "The mode of a metadata record was not set",
				"path", one.path, "error", err)
			continue
		}
		if !owners {
			continue
		}
		if err := os.Lchown(where, one.uid, one.gid); err != nil {
			logger.WarnContext(ctx, "The owner of a metadata record was not set",
				"path", one.path, "error", err)
		}
	}
}
