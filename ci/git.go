package main

import (
	"fmt"
	"os/exec"
	"strings"
)

// Git runs git commands in one repository.
type Git struct {
	Dir string
}

func (g Git) run(args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = g.Dir
	out, err := cmd.Output()
	if err != nil {
		if exit, ok := err.(*exec.ExitError); ok {
			return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(exit.Stderr)))
		}
		return "", fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
	}
	return strings.TrimSpace(string(out)), nil
}

// Changed lists the files that differ between two commits.
func (g Git) Changed(from, to string) ([]string, error) {
	out, err := g.run("diff", "--name-only", from, to)
	if err != nil || out == "" {
		return nil, err
	}
	return strings.Split(out, "\n"), nil
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
