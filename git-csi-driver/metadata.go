package main

// metadata.go records what a git checkout cannot carry: the
// modes, the owners, and the empty directories.

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

// The driver-owned ref, the one file in its tree, and the two
// files a commit on it needs beside the work tree.
const (
	metadataRef        = refPrefix + "metadata"
	metadataFile       = "metadata"
	metadataRecordFile = "metadata.record"
	metadataIndexFile  = "metadata.index"
)

// remoteMetadataRef is where a fetch puts the record the remote
// holds, beside the volume's own record. A rebase needs both: the
// other writer's modes to replay, and their record to put the
// volume's own record on top of.
const remoteMetadataRef = metadataRef + ".remote"

// metadataMessage is the message every commit on the metadata ref
// carries, because the record is the whole change.
const metadataMessage = "Update the metadata record"

// The modes a checkout already gives, so a path with either of
// them is recorded nowhere.
const (
	defaultFileMode fs.FileMode = 0o644
	defaultDirMode  fs.FileMode = 0o755
)

// metadataRecord is one path the checkout cannot carry.
type metadataRecord struct {
	path      string
	directory bool
	mode      fs.FileMode
	uid       int
	gid       int
}

// walkTreeMetadata records every path with a mode other than the
// default, an owner other than the driver's own, or no entries.
func walkTreeMetadata(tree string) ([]metadataRecord, error) {
	records := []metadataRecord{}
	uid, gid := os.Getuid(), os.Getgid()
	err := filepath.Walk(tree, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if path == tree {
			return nil
		}
		name := filepath.ToSlash(strings.TrimPrefix(path, tree+string(filepath.Separator)))
		// A newline in a path would make two lines of one record,
		// so such a path is recorded nowhere.
		if strings.ContainsAny(name, "\n\r") {
			return nil
		}
		if one, keep := pathRecord(name, path, info, uid, gid); keep {
			records = append(records, one)
		}
		return nil
	})
	sort.Slice(records, func(i, j int) bool { return records[i].path < records[j].path })
	return records, err
}

// pathRecord answers the record for one path and whether the
// checkout already gives what it says.
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

// ownerOf reads the owner and group. On Linux os.Lstat always answers
// a Stat_t, and the driver runs nowhere else.
func ownerOf(info os.FileInfo) (int, int) {
	stat := info.Sys().(*syscall.Stat_t)
	return int(stat.Uid), int(stat.Gid)
}

// emptyDirectory reports a directory with no entries, which git cannot
// carry.
func emptyDirectory(path string) bool {
	entries, err := os.ReadDir(path)
	return err == nil && len(entries) == 0
}

// metadataContent writes one record per line: the mode, the owner, the
// group, and the path, with a trailing slash on a directory.
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

// parseMetadataContent reads the record. A line the driver cannot read
// is left out, so one bad record never stops a restore.
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
	// A record names a path inside the tree, so anything that
	// climbs out of it is not a record.
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

// recordMetadata commits the record on the driver-owned ref as a
// single-file tree, and only when its content changed.
func (w *workTree) recordMetadata(ctx context.Context, rules *policy) error {
	records, err := walkTreeMetadata(w.tree)
	if err != nil {
		return err
	}
	content := metadataContent(records)
	if standing, err := w.metadataRecord(ctx, metadataRef); err == nil && standing == content {
		return nil
	}
	return w.commitMetadata(ctx, rules, content)
}

// metadataRecord is the record the ref holds now: the volume's own,
// or the one fetched from the remote.
func (w *workTree) metadataRecord(ctx context.Context, ref string) (string, error) {
	output, err := w.git(ctx, "show", "--no-textconv", ref+":"+metadataFile)
	if err != nil {
		return "", err
	}
	return output.stdout, nil
}

// gitSteps runs a plumbing sequence and holds the first failure,
// so a caller states the sequence once and checks it once.
type gitSteps struct {
	work *workTree
	err  error
}

// run answers the invocation's one line of output, and does nothing
// once a step before it has failed.
func (s *gitSteps) run(ctx context.Context, env []string, args ...string) string {
	if s.err != nil {
		return ""
	}
	output, err := s.work.gitWith(ctx, env, args...)
	s.err = err
	return trimLine(output.stdout)
}

// The commit is made with plumbing and an index of its own, so
// nothing here touches the tree the pod writes or the index the pod's
// changes are staged in.
func (w *workTree) commitMetadata(ctx context.Context, rules *policy, content string) error {
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
	// The new record stands on the last one, so the ref carries the
	// history of the modes as well as the modes.
	if parent := w.refCommit(ctx, metadataRef); parent != "" {
		args = append(args, "-p", parent)
	}
	commit := steps.run(ctx, rules.author(), append(args, tree)...)
	steps.run(ctx, nil, "update-ref", metadataRef, commit)
	return steps.err
}

// replayMetadata gives a checkout the modes, the owners, and the
// empty directories the ref records. owners is false where the driver
// is not root, and a chown it cannot make is not a failure of the
// restore.
func (w *workTree) replayMetadata(ctx context.Context, logger *slog.Logger, owners bool) error {
	content, err := w.metadataRecord(ctx, metadataRef)
	if err != nil {
		return err
	}
	w.replayRecords(ctx, logger, owners, parseMetadataContent(content))
	return nil
}

// replayMetadataFor replays the remote's record for the paths an
// update rewrote, and no other path. Those paths are another writer's
// files, so their modes come from that writer's record, and the modes
// the pod set on its own files stand.
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

// recordsUnder selects the records of the paths an update rewrote:
// the paths themselves, the directories that hold them, and the
// directories the record names inside those, which is where an empty
// directory another writer added is. The root's own children are
// never selected, so a record line for one of the pod's own top-level
// paths is not replayed.
func recordsUnder(records []metadataRecord, changed []string) []metadataRecord {
	rewritten, holding := map[string]bool{}, map[string]bool{}
	for _, one := range changed {
		rewritten[one] = true
		for dir := path.Dir(one); dir != "."; dir = path.Dir(dir) {
			holding[dir] = true
		}
	}
	found := []metadataRecord{}
	for _, one := range records {
		inside := one.directory && (holding[one.path] || holding[path.Dir(one.path)])
		if rewritten[one.path] || inside {
			found = append(found, one)
		}
	}
	return found
}

// reparentMetadata makes the volume's record a child of the record
// the remote holds now. A record is a snapshot of the whole tree, so
// a new parent is the whole rebase it needs, and it can never
// conflict. A record already on top of the remote's is left alone.
func (w *workTree) reparentMetadata(ctx context.Context, author []string) error {
	remote, local := w.refCommit(ctx, remoteMetadataRef), w.refCommit(ctx, metadataRef)
	if remote == "" || local == "" || w.ancestor(ctx, remote, local) {
		return nil
	}
	steps := &gitSteps{work: w}
	tree := steps.run(ctx, nil, "rev-parse", "--verify", "--end-of-options", local+"^{tree}")
	commit := steps.run(ctx, author, "commit-tree", "-m", metadataMessage, "-p", remote, tree)
	steps.run(ctx, nil, "update-ref", metadataRef, commit)
	return steps.err
}

// replayRecords writes what a checkout cannot carry, one record at
// a time. A record it cannot write is logged and does not stop the
// ones after it.
func (w *workTree) replayRecords(
	ctx context.Context, logger *slog.Logger, owners bool, records []metadataRecord,
) {
	if !owners {
		logger.InfoContext(ctx, "the owners are not replayed", "tree", w.tree)
	}
	for _, one := range records {
		where := filepath.Join(w.tree, filepath.FromSlash(one.path))
		if one.directory {
			if err := os.MkdirAll(where, one.mode); err != nil {
				logger.WarnContext(ctx, "the directory was not made",
					"path", one.path, "error", err)
				continue
			}
		}
		if err := os.Chmod(where, one.mode); err != nil {
			logger.WarnContext(ctx, "the mode was not replayed",
				"path", one.path, "error", err)
			continue
		}
		if !owners {
			continue
		}
		if err := os.Lchown(where, one.uid, one.gid); err != nil {
			logger.WarnContext(ctx, "the owner was not replayed",
				"path", one.path, "error", err)
		}
	}
}
