package main

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
)

// Git runs git commands in one repository.
type Git struct {
	Dir string
}

func (g Git) run(args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = g.Dir
	cmd.Env = gitEnv()
	out, err := cmd.Output()
	if err != nil {
		if exit, ok := err.(*exec.ExitError); ok {
			return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(exit.Stderr)))
		}
		return "", fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
	}
	return strings.TrimSpace(string(out)), nil
}

// gitEnv returns this process's environment without its GIT_*
// variables, plus extra. git runs a hook with GIT_DIR and
// GIT_INDEX_FILE set for the repository that runs the hook, and those
// variables take precedence over the directory that a command runs in.
// A git command that inherited them would read and write that
// repository and not the one in Dir. A test run from a pre-commit hook
// would then commit its fixtures into the repository being committed
// to, and set its core.worktree.
func gitEnv(extra ...string) []string {
	var env []string
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "GIT_") {
			env = append(env, kv)
		}
	}
	return append(env, extra...)
}

// Changed lists the files that differ between two commits. Rename
// detection is off, so a rename lists both the old path and the new
// one, and a file that moves from one component to another changes
// both components.
func (g Git) Changed(from, to string) ([]string, error) {
	out, err := g.run("diff", "--name-only", "--no-renames", from, to)
	if err != nil || out == "" {
		return nil, err
	}
	return strings.Split(out, "\n"), nil
}

// Show reads a file at a revision. found is false when the revision has
// no such file.
func (g Git) Show(rev, path string) (text string, found bool, err error) {
	if _, err := g.run("cat-file", "-e", rev+":"+path); err != nil {
		return "", false, nil
	}
	cmd := exec.Command("git", "show", rev+":"+path)
	cmd.Dir = g.Dir
	cmd.Env = gitEnv()
	out, err := cmd.Output()
	if err != nil {
		return "", false, fmt.Errorf("git show %s:%s: %w", rev, path, err)
	}
	return string(out), true, nil
}

// HasCommit is true when the repository has the commit. A push's
// "before" commit is missing after a force push, and it is all zeros
// for a new branch.
func (g Git) HasCommit(rev string) bool {
	if rev == "" || strings.Trim(rev, "0") == "" {
		return false
	}
	_, err := g.run("cat-file", "-e", rev+"^{commit}")
	return err == nil
}

// IsAncestor is true when the first commit is an ancestor of the
// second one, or the same commit.
func (g Git) IsAncestor(ancestor, rev string) bool {
	_, err := g.run("merge-base", "--is-ancestor", ancestor, rev)
	return err == nil
}

// MergeBase is the best common ancestor of two revisions.
func (g Git) MergeBase(a, b string) (string, error) {
	return g.run("merge-base", a, b)
}

func (g Git) HasTag(tag string) bool {
	_, err := g.run("rev-parse", "--verify", "--quiet", "refs/tags/"+tag)
	return err == nil
}

// Commit is the full sha of a revision.
func (g Git) Commit(rev string) (string, error) {
	return g.run("rev-parse", rev+"^{commit}")
}

// CommitTime returns the committer time of a revision. The commit
// records it, so every checkout reads the same value. A rebase sets the
// committer time to the time of the rebase and keeps the author time.
func (g Git) CommitTime(rev string) (time.Time, error) {
	out, err := g.run("show", "-s", "--format=%ct", rev+"^{commit}")
	if err != nil {
		return time.Time{}, err
	}
	seconds, err := strconv.ParseInt(out, 10, 64)
	if err != nil {
		return time.Time{}, fmt.Errorf("the committer time of %s: %w", rev, err)
	}
	return time.Unix(seconds, 0).UTC(), nil
}

// Describe returns the newest release tag reachable from the commit and
// the number of commits since that tag. The match pattern accepts only
// a bare CalVer tag, so it skips a tag with a component prefix, such as
// display-operator/2026.09.27-001.
func (g Git) Describe(rev string) (tag string, count int, err error) {
	out, err := g.run("describe", "--tags", "--long", "--match", releaseTagGlob, rev)
	if err != nil {
		return "", 0, err
	}
	// The output is <tag>-<count>-g<sha>, and the tag has a hyphen of
	// its own, so the fields come off the end.
	rest := out[:max(strings.LastIndex(out, "-"), 0)]
	i := strings.LastIndex(rest, "-")
	if i < 0 {
		return "", 0, fmt.Errorf("git describe printed %q", out)
	}
	if _, err := fmt.Sscanf(rest[i+1:], "%d", &count); err != nil {
		return "", 0, fmt.Errorf("git describe printed %q: %w", out, err)
	}
	return rest[:i], count, nil
}

// PinnedTagAt returns the pinned tag from the component's package.toml
// at the revision, or "" when the file does not exist there or the
// component is not pinned.
func (g Git) PinnedTagAt(rev string, c *Component) (string, error) {
	if !g.HasCommit(rev) {
		return "", fmt.Errorf("%s is not a commit in the repository", rev)
	}
	text, err := g.run("show", rev+":"+c.Dir+"/package.toml")
	if err != nil {
		if _, lsErr := g.run("cat-file", "-e", rev+":"+c.Dir+"/package.toml"); lsErr != nil {
			return "", nil
		}
		return "", err
	}
	var then Component
	if _, err := toml.Decode(text, &then); err != nil {
		return "", fmt.Errorf("%s:%s/package.toml: %w", rev, c.Dir, err)
	}
	if !then.Pinned() {
		return "", nil
	}
	return then.PinnedTag(), nil
}
