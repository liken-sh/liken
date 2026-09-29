package main

import (
	"os"
	"path/filepath"
	"testing"
)

// hookRepository is a repository in the role of the one that runs a
// pre-commit hook: its GIT_* variables are in the environment, the way
// git sets them for the hook.
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
			t.Fatal(err)
		}
		return string(data)
	}
	config, index, head := read("config"), read("index"), outside.run("rev-parse", "HEAD")

	r := newRepo(t, map[string]string{"fixture.txt": "fixture\n"})
	r.write("fixture.txt", "changed\n")
	r.commit("a second commit")
	if _, err := r.git.Commit("HEAD"); err != nil {
		t.Fatal(err)
	}

	if count := r.run("rev-list", "--count", "HEAD"); count != "2" {
		t.Errorf("the test's repository has %s commits", count)
	}
	if read("config") != config || read("index") != index || outside.run("rev-parse", "HEAD") != head {
		t.Error("a test's git command changed the hook's repository")
	}
}
