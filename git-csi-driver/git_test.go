package main

import (
	"context"
	"os/exec"
	"strings"
	"testing"
)

// repositoryWithACommit makes a repository with one commit and returns
// its directory.
func repositoryWithACommit(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	git(t, dir, "init", "--quiet", "--initial-branch=main")
	writeFiles(t, dir, files)
	git(t, dir, "add", "--all")
	git(t, dir, "-c", "user.name=lab", "-c", "user.email=lab@liken.sh", "commit", "--quiet", "-m", "one")
	return dir
}

// commitFiles adds every file to the repository and commits them,
// returning the commit.
func commitFiles(t *testing.T, dir string, files map[string]string) string {
	t.Helper()
	writeFiles(t, dir, files)
	git(t, dir, "add", "--all")
	git(t, dir, "-c", "user.name=lab", "-c", "user.email=lab@liken.sh", "commit", "--quiet", "-m", "more")
	return strings.TrimSpace(git(t, dir, "rev-parse", "HEAD"))
}

// git runs the tests' own git, so a test never proves the driver right
// with the driver's own code.
func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	command := exec.Command("git", args...)
	command.Dir = dir
	command.Env = gitEnvironment()
	out, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, out)
	}
	return string(out)
}

func TestRunGitAnswersWhatGitPrinted(t *testing.T) {
	dir := repositoryWithACommit(t, map[string]string{"a.txt": "one"})
	output, err := runGit(t.Context(), dir, nil, "rev-parse", "--is-bare-repository")
	if err != nil {
		t.Fatalf("runGit: %v", err)
	}
	if strings.TrimSpace(output.stdout) != "false" {
		t.Errorf("runGit answered %q, want %q", output.stdout, "false")
	}
	if output.code != 0 {
		t.Errorf("runGit answered code %d, want 0", output.code)
	}
}

func TestRunGitRunsInTheDirectoryItIsGiven(t *testing.T) {
	dir := repositoryWithACommit(t, map[string]string{"a.txt": "one"})
	output, err := runGit(t.Context(), dir, nil, "rev-parse", "--show-toplevel")
	if err != nil {
		t.Fatalf("runGit: %v", err)
	}
	if got := strings.TrimSpace(output.stdout); !strings.HasSuffix(got, dirName(dir)) {
		t.Errorf("runGit ran in %q, want %q", got, dir)
	}
}

// dirName is the last element of a path, which a temporary directory's
// own prefix never reaches.
func dirName(path string) string {
	parts := strings.Split(strings.TrimSuffix(path, "/"), "/")
	return parts[len(parts)-1]
}

func TestRunGitTakesTheEnvironmentItIsGiven(t *testing.T) {
	dir := repositoryWithACommit(t, map[string]string{"a.txt": "one"})
	output, err := runGit(t.Context(), dir, []string{
		"GIT_CONFIG_COUNT=1",
		"GIT_CONFIG_KEY_0=user.name",
		"GIT_CONFIG_VALUE_0=the driver",
	}, "config", "--get", "user.name")
	if err != nil {
		t.Fatalf("runGit: %v", err)
	}
	if got := strings.TrimSpace(output.stdout); got != "the driver" {
		t.Errorf("runGit answered %q, want %q", got, "the driver")
	}
}

func TestRunGitReportsWhatGitSaidOnStderr(t *testing.T) {
	output, err := runGit(t.Context(), t.TempDir(), nil, "rev-parse", "--verify", "refs/heads/main")
	if err == nil {
		t.Fatal("runGit answered no error outside a repository")
	}
	if !strings.Contains(err.Error(), "rev-parse --verify refs/heads/main") {
		t.Errorf("runGit said %q, want the arguments in it", err)
	}
	if output.code == 0 {
		t.Errorf("runGit answered code %d, want a failure", output.code)
	}
	if output.stderr == "" {
		t.Error("runGit answered no stderr")
	}
}

func TestRunGitReportsAContextThatIsOver(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	output, err := runGit(ctx, t.TempDir(), nil, "version")
	if err == nil {
		t.Fatal("runGit answered no error under a context that is over")
	}
	if output.code != -1 {
		t.Errorf("runGit answered code %d, want -1 for a command that never ran", output.code)
	}
}

func TestGitReasonFallsBackToTheError(t *testing.T) {
	if got := gitReason(gitOutput{}, context.Canceled); got != context.Canceled.Error() {
		t.Errorf("gitReason answered %q, want %q", got, context.Canceled)
	}
}

func TestGitEnvironmentCarriesNothingOfTheNode(t *testing.T) {
	for _, name := range []string{"SSH_AUTH_SOCK=", "GIT_SSH_COMMAND=", "HOME="} {
		for _, entry := range gitEnvironment() {
			if strings.HasPrefix(entry, name) {
				t.Errorf("gitEnvironment carries %q", entry)
			}
		}
	}
}
