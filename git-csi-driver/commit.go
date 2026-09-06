package main

// commit.go is what an armed volume does when the quiesce fires:
// the ignore list, the size guard, and the commit.

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	corev1 "k8s.io/api/core/v1"
)

// excludeFile is where git reads the patterns the class adds
// beside the repository's own .gitignore.
const excludeFile = "info/exclude"

// stageBatch is how many paths one git add carries, so a tree of
// thousands never makes a command line the kernel refuses.
const stageBatch = 100

// literalPathspecs makes git read a path as a path and not a pattern.
// Every path here comes from git's own report.
var literalPathspecs = []string{"GIT_LITERAL_PATHSPECS=1"}

// commit records the metadata, then commits what the pod wrote.
// A metadata record that fails is reported and does not stop the commit,
// because the tree's content is worth more than the record of its modes.
func (n *node) commit(ctx context.Context, held *volume, rules *policy) {
	if rules.metadata {
		if err := held.work.recordMetadata(ctx, rules); err != nil {
			n.logger.WarnContext(ctx, "the metadata was not recorded",
				"volume", held.id, "error", err)
		}
	}
	staged, err := n.stageAndCommit(ctx, held, rules)
	if err != nil {
		n.logger.WarnContext(ctx, "the tree was not committed",
			"volume", held.id, "error", err)
		return
	}
	if len(staged) == 0 {
		return
	}
	n.logger.InfoContext(ctx, "committed", "volume", held.id, "paths", len(staged))
}

// stageAndCommit writes the ignore list, stages every path under
// the size guard, and commits what the index holds. It answers the paths
// the commit carried, and none where nothing was staged.
func (n *node) stageAndCommit(
	ctx context.Context, held *volume, rules *policy,
) ([]string, error) {
	// The ignore list is written before the status is read,
	// because it decides what the status names.
	if err := held.work.exclude(rules.ignore); err != nil {
		return nil, err
	}
	found, err := held.work.pending(ctx)
	if err != nil {
		return nil, err
	}
	staged, skipped, err := held.work.stageChanges(ctx, found, rules)
	n.skipping(ctx, held, skipped)
	if err != nil || len(staged) == 0 {
		return nil, err
	}
	return staged, held.work.commit(ctx, rules, staged)
}

// skipping records what the size guard left out and posts one
// Event per path the last pass did not already hold.
func (n *node) skipping(ctx context.Context, held *volume, skipped []change) {
	claim, _, _ := held.reading()
	for _, one := range held.reportSkipped(skipped) {
		n.report(ctx, held, claim, corev1.EventTypeWarning, reasonSkipped,
			fmt.Sprintf("%s is %d bytes, over %s", one.path, one.size, maxFileSizeParameter))
	}
}

// exclude writes the class's patterns where git reads them, and
// writes nothing when the file already holds them.
func (w *workTree) exclude(patterns []string) error {
	path := filepath.Join(w.gitDir, excludeFile)
	content := ""
	if len(patterns) > 0 {
		content = strings.Join(patterns, "\n") + "\n"
	}
	if standing, err := os.ReadFile(path); err == nil && string(standing) == content {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(content), 0o644)
}

// stageChanges stages every path under commit.maxFileSize and
// answers what the index holds and what the guard left out.
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

// staged is what the index holds that the commit does not, which
// is what the next commit carries.
func (w *workTree) staged(ctx context.Context) ([]string, error) {
	output, err := w.git(ctx, "diff", "--cached", "--name-only", "-z")
	if err != nil {
		return nil, err
	}
	return splitZero(output.stdout), nil
}

// splitZero reads git's -z output, which ends every path with a
// zero byte and holds no empty path.
func splitZero(report string) []string {
	found := []string{}
	for _, entry := range strings.Split(report, "\x00") {
		if entry != "" {
			found = append(found, entry)
		}
	}
	return found
}

// commit makes the commit. The message names the count and then every
// path, so a person reading the history sees what one rest of the tree
// carried.
func (w *workTree) commit(ctx context.Context, rules *policy, staged []string) error {
	message := fmt.Sprintf("Update %d paths\n\n%s", len(staged), strings.Join(staged, "\n"))
	_, err := w.gitWith(ctx, rules.author(), "commit", "--quiet", "-m", message)
	return err
}
