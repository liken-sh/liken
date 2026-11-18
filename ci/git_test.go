package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// hookRepository returns a repository whose GIT_* variables are set in
// the environment, the way git sets them for a pre-commit hook that it
// runs in that repository.
func hookRepository(t *testing.T) *repo {
	t.Helper()
	outside := newRepo(t, map[string]string{"kept.txt": "kept\n"})
	gitDir := filepath.Join(outside.git.Dir, ".git")
	t.Setenv("GIT_DIR", gitDir)
	t.Setenv("GIT_INDEX_FILE", filepath.Join(gitDir, "index"))
	t.Setenv("GIT_WORK_TREE", outside.git.Dir)
	return outside
}

func TestAHooksGitVariablesReachNoOtherRepository(t *testing.T) {
	outside := hookRepository(t)
	read := func(name string) string {
		data, err := os.ReadFile(filepath.Join(outside.git.Dir, ".git", name))
		if err != nil {
			t.Fatalf("reading .git/%s of the hook's repository: %v", name, err)
		}
		return string(data)
	}
	config, index, head := read("config"), read("index"), outside.run("rev-parse", "HEAD")

	r := newRepo(t, map[string]string{"fixture.txt": "fixture\n"})
	r.write("fixture.txt", "changed\n")
	r.commit("a second commit")
	if _, err := r.git.Commit("HEAD"); err != nil {
		t.Fatalf("reading the commit of HEAD: %v", err)
	}

	if count := r.run("rev-list", "--count", "HEAD"); count != "2" {
		t.Errorf("the test's repository has %s commits, want 2", count)
	}
	if read("config") != config || read("index") != index || outside.run("rev-parse", "HEAD") != head {
		t.Error("a test's git command changed the config, index, or HEAD of the hook's repository, want them unchanged")
	}
}

// The publish dates each output by the committer time of its commit.
// The test commit has different author and committer dates, so the
// test fails when CommitTime reads the author date.
func TestTheCommitTimeIsTheCommittersTime(t *testing.T) {
	r := newRepo(t, map[string]string{"fixture.txt": "fixture\n"})
	r.write("fixture.txt", "changed\n")
	r.run("add", "-A")
	cmd := exec.Command("git", "commit", "--quiet", "-m", "a dated commit")
	cmd.Dir = r.git.Dir
	cmd.Env = gitEnv(append(testGit, "GIT_AUTHOR_DATE=2026-10-01T08:00:00Z", "GIT_COMMITTER_DATE=2026-10-02T09:30:00Z")...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git commit: %v: %s", err, out)
	}
	got, err := r.git.CommitTime("HEAD")
	if err != nil || !got.Equal(fixtureCreated) {
		t.Errorf("CommitTime(HEAD) = %v, %v, want %v", got, err, fixtureCreated)
	}
}
