package main

// The driver commits what a pod wrote to an armed volume, a writeable
// volume whose claim has a VolumeAttributesClass of this driver. It
// commits when the tree has been quiet for push.quiesce, and when the
// pod unpublishes the volume.

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	corev1 "k8s.io/api/core/v1"
)

// excludeFile holds the class's ignore patterns. Git reads it in
// addition to the repository's .gitignore, and it is in the git
// directory and not the tree, so the patterns are never committed.
const excludeFile = "info/exclude"

// stageBatch is how many paths one git add receives, so a tree with
// thousands of changes does not exceed the kernel's limit on the length
// of a command line.
const stageBatch = 100

// literalPathspecs makes git add treat each path as a literal file
// name. The paths come from git status, so a file whose name has glob
// characters must match only itself.
var literalPathspecs = []string{"GIT_LITERAL_PATHSPECS=1"}

// commit records the metadata, then commits the tree. A failure to
// record the metadata does not stop the commit, because losing the
// file contents is worse than losing the file modes.
func (n *node) commit(ctx context.Context, v *volume, rules *policy) {
	if rules.metadata {
		if err := v.work.recordMetadata(ctx, rules); err != nil {
			n.logger.WarnContext(ctx, "The file metadata was not recorded",
				"volume", v.id, "error", err)
		}
	}
	staged, err := n.stageAndCommit(ctx, v, rules)
	if err != nil {
		n.logger.WarnContext(ctx, "The work tree was not committed",
			"volume", v.id, "error", err)
		return
	}
	if len(staged) == 0 {
		return
	}
	n.logger.InfoContext(ctx, "Committed the work tree", "volume", v.id, "paths", len(staged))
}

// stageAndCommit returns the paths it committed, and none when nothing
// changed.
func (n *node) stageAndCommit(
	ctx context.Context, v *volume, rules *policy,
) ([]string, error) {
	// The ignore list changes which paths git status reports, so it is
	// written first.
	if err := v.work.exclude(rules.ignore); err != nil {
		return nil, err
	}
	found, err := v.work.pending(ctx)
	if err != nil {
		return nil, err
	}
	staged, skipped, err := v.work.stageChanges(ctx, found, rules)
	n.skipping(v, skipped, rules.maxFileSize)
	if err != nil || len(staged) == 0 {
		return nil, err
	}
	return staged, v.work.commit(ctx, rules, staged)
}

// skipping posts one Event for each path over commit.maxFileSize that
// the previous pass did not already skip.
func (n *node) skipping(v *volume, skipped []change, limit int64) {
	claim, _, _ := v.gaugeValues()
	for _, one := range v.reportSkipped(skipped) {
		n.report(v, claim, corev1.EventTypeWarning, reasonSkipped,
			fmt.Sprintf("Skipped %s because it is %d bytes, over the %s of %d bytes",
				one.path, one.size, maxFileSizeParameter, limit))
	}
}

// skipOutside posts one Event for each path outside the volume's paths
// that the pod wrote for the first time. The driver never commits such a
// path, so the Event is how a person learns that the pod writes where
// the volume does not keep its files.
func (n *node) skipOutside(v *volume, written []string) {
	fresh := v.reportOutside(written)
	if len(fresh) == 0 {
		return
	}
	claim, _, _ := v.gaugeValues()
	for _, name := range fresh {
		n.report(v, claim, corev1.EventTypeWarning, reasonSkipped,
			fmt.Sprintf("Skipped %q because it is outside the volume's paths %s",
				name, describePaths(v.work.paths)))
	}
	n.metrics.record(v)
}

// exclude writes the ignore patterns to the exclude file, and leaves
// the file unchanged when it already has them.
func (w *workTree) exclude(patterns []string) error {
	path := filepath.Join(w.gitDir, excludeFile)
	content := ""
	if len(patterns) > 0 {
		content = strings.Join(patterns, "\n") + "\n"
	}
	if current, err := os.ReadFile(path); err == nil && string(current) == content {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(content), 0o644)
}

// stageChanges runs git add on each change within the policy's
// maxFileSize, where zero means no limit. It returns every path that is
// now staged, and the changes it skipped as too large.
func (w *workTree) stageChanges(
	ctx context.Context, found []change, rules *policy,
) ([]string, []change, error) {
	paths, skipped := []string{}, []change{}
	for _, one := range found {
		if rules.maxFileSize > 0 && one.size > rules.maxFileSize {
			skipped = append(skipped, one)
			continue
		}
		paths = append(paths, one.path)
	}
	for batch := range slices.Chunk(paths, stageBatch) {
		if _, err := w.gitWith(ctx, literalPathspecs,
			append([]string{"add", "--"}, batch...)...); err != nil {
			return nil, skipped, err
		}
	}
	staged, err := w.staged(ctx)
	return staged, skipped, err
}

// staged returns the paths whose index entry differs from HEAD.
func (w *workTree) staged(ctx context.Context) ([]string, error) {
	output, err := w.git(ctx, "diff", "--cached", "--name-only", "-z")
	if err != nil {
		return nil, err
	}
	return splitZero(output.stdout), nil
}

// splitZero splits the output of a git command run with -z, where each
// path ends with a NUL byte.
func splitZero(report string) []string {
	found := []string{}
	for _, entry := range strings.Split(report, "\x00") {
		if entry != "" {
			found = append(found, entry)
		}
	}
	return found
}

// commit commits the index with a message that lists the paths, so the
// history shows what each commit changed.
func (w *workTree) commit(ctx context.Context, rules *policy, staged []string) error {
	message := fmt.Sprintf("Update %d paths\n\n%s", len(staged), strings.Join(staged, "\n"))
	_, err := w.gitWith(ctx, rules.author(), "commit", "--quiet", "-m", message)
	return err
}
