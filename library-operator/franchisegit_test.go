package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// gitRepository is a repository the scanner can clone: one commit on one
// branch, holding the files the test names. The scanner clones it over a
// file:// url, so the test drives the real git the image carries.
func gitRepository(t *testing.T, branch string, files map[string]string) string {
	t.Helper()
	dir := franchiseCheckout(t, files)
	run := func(args ...string) {
		t.Helper()
		command := exec.Command("git", append([]string{"-C", dir,
			"-c", "user.name=the author", "-c", "user.email=author@example.com",
			"-c", "commit.gpgsign=false"}, args...)...)
		if out, err := command.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	run("init", "--initial-branch", branch)
	run("add", "--all")
	run("commit", "--message", "the first franchises")
	return "file://" + dir
}

// headOf is the commit the repository's branch points at, read the way the
// scanner reads it.
func headOf(t *testing.T, url, branch string) string {
	t.Helper()
	out, err := exec.Command("git", "-C", strings.TrimPrefix(url, "file://"),
		"rev-parse", branch).CombinedOutput()
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(out))
}

// The clone reads one ref into the Job's own directory and returns the commit
// it read. The checkout holds the files of that commit, which the walk then
// reads.
func TestTheCloneReadsARefAndItsCommit(t *testing.T) {
	url := gitRepository(t, "main", map[string]string{
		"Star Wars/franchise.yaml": wholeFranchiseFile,
	})
	into := filepath.Join(t.TempDir(), "checkout")

	head, err := cloneRepository(t.Context(), url, "main", into)
	if err != nil {
		t.Fatal(err)
	}

	if head != headOf(t, url, "main") {
		t.Errorf("head = %q, want the commit the branch points at", head)
	}
	if _, err := os.Stat(filepath.Join(into, "Star Wars", franchiseFileName)); err != nil {
		t.Errorf("the checkout holds no franchise file: %v", err)
	}
}

// A clone that cannot reach the forge returns an error and leaves no checkout.
// The error names the url, so the pod log says which repository failed.
func TestTheCloneFailsOnAUrlItCannotReach(t *testing.T) {
	cases := []struct {
		name string
		url  string
		ref  string
	}{
		{"a host that does not resolve", "https://forge.invalid/guid.foo/fiction-franchises.git", "main"},
		{"a path that holds no repository", "file:///nonexistent-repository-of-franchises", "main"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			into := filepath.Join(t.TempDir(), "checkout")

			head, err := cloneRepository(t.Context(), testCase.url, testCase.ref, into)

			if err == nil {
				t.Fatalf("the clone read %q, want it failed on an unreachable url", head)
			}
			if !strings.Contains(err.Error(), testCase.url) {
				t.Errorf("the error is %q, want it to name the url", err)
			}
			if _, err := os.Stat(filepath.Join(into, "Star Wars")); err == nil {
				t.Error("the clone left a checkout, want none")
			}
		})
	}
}

// A ref the repository does not carry is a failure the Library reports.
func TestTheCloneFailsOnARefTheRepositoryDoesNotCarry(t *testing.T) {
	url := gitRepository(t, "main", map[string]string{
		"Star Wars/franchise.yaml": wholeFranchiseFile,
	})

	_, err := cloneRepository(t.Context(), url, "release", filepath.Join(t.TempDir(), "checkout"))

	if err == nil {
		t.Fatal("the clone read a ref the repository does not carry")
	}
}

// The clone is bounded, so a forge that never answers fails the Job instead of
// holding it open past its own schedule.
func TestTheCloneGivesUpOnATimeout(t *testing.T) {
	url := gitRepository(t, "main", map[string]string{"Alien/franchise.yaml": "name: Alien\n"})
	was := cloneTimeout
	cloneTimeout = time.Nanosecond
	t.Cleanup(func() { cloneTimeout = was })

	_, err := cloneRepository(context.Background(), url, "main", filepath.Join(t.TempDir(), "checkout"))

	if err == nil {
		t.Fatal("the clone finished inside a nanosecond, want it gave up")
	}
}
