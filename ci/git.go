package main

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

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

// gitEnv is this process's environment without its GIT_* variables,
// plus the extra variables. git runs a hook with GIT_DIR and
// GIT_INDEX_FILE set for the repository that runs the hook, and those
// variables win over the directory a command runs in. A git command
// that inherits them reads and writes that repository, not the one in
// Dir: a test run from a pre-commit hook would commit its fixtures into
// the repository under commit and set its core.worktree. Without the
// variables, git finds the repository from Dir alone.
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
// detection is off: a rename lists the old path and the new one, so a
// file that moves from one component to another changes both.
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

// HasTag is true when the tag exists.
func (g Git) HasTag(tag string) bool {
	_, err := g.run("rev-parse", "--verify", "--quiet", "refs/tags/"+tag)
	return err == nil
}

// Commit is the full sha of a revision.
func (g Git) Commit(rev string) (string, error) {
	return g.run("rev-parse", rev+"^{commit}")
}

// Describe names the commit by the newest release tag before it, the
// number of commits since that tag, and the commit. A release tag is
// a bare CalVer version, so the pattern leaves out the tags that the
// components brought with them from their own repositories, such as
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

// PinnedTagAt reads the pinned tag that the component's package.toml
// states at the revision, or "" when the file is not there or states
// no version.
func (g Git) PinnedTagAt(rev string, c *Component) (string, error) {
	if !g.HasCommit(rev) {
		return "", fmt.Errorf("%s names no commit", rev)
	}
	text, err := g.run("show", rev+":"+c.Dir+"/package.toml")
	if err != nil {
		// A component that did not exist at the revision has no
		// package.toml there, and so no tag.
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
