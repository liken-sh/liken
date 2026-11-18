package main

import (
	"context"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// repositoryWithACommit creates a repository with one commit and returns
// its directory.
func repositoryWithACommit(t testing.TB, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	git(t, dir, "init", "--quiet", "--initial-branch=main")
	writeFiles(t, dir, files)
	git(t, dir, "add", "--all")
	git(t, dir, "-c", "user.name=lab", "-c", "user.email=lab@liken.sh", "commit", "--quiet", "-m", "one")
	return dir
}

// bareRemote creates a bare repository with the files committed on main,
// so a test can push to it as it would to a forge.
func bareRemote(t *testing.T, files map[string]string) string {
	t.Helper()
	source := repositoryWithACommit(t, files)
	bare := t.TempDir()
	git(t, bare, "init", "--quiet", "--bare", "--initial-branch=main")
	git(t, source, "push", "--quiet", bare, "main")
	return bare
}

// commitFiles adds every file to the repository, commits, and returns
// the new commit.
func commitFiles(t *testing.T, dir string, files map[string]string) string {
	t.Helper()
	writeFiles(t, dir, files)
	git(t, dir, "add", "--all")
	git(t, dir, "-c", "user.name=lab", "-c", "user.email=lab@liken.sh", "commit", "--quiet", "-m", "more")
	return strings.TrimSpace(git(t, dir, "rev-parse", "HEAD"))
}

// git runs git without runGit, so a test does not use the driver's code
// to check the driver.
func git(t testing.TB, dir string, args ...string) string {
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

func TestRunGitReturnsWhatGitPrinted(t *testing.T) {
	dir := repositoryWithACommit(t, map[string]string{"a.txt": "one"})
	output, err := runGit(t.Context(), dir, nil, "rev-parse", "--is-bare-repository")
	if err != nil {
		t.Fatalf("running git: %v", err)
	}
	if strings.TrimSpace(output.stdout) != "false" {
		t.Errorf("runGit returned %q, want %q", output.stdout, "false")
	}
	if output.code != 0 {
		t.Errorf("runGit returned code %d, want 0", output.code)
	}
}

func TestRunGitRunsInTheDirectoryItIsGiven(t *testing.T) {
	dir := repositoryWithACommit(t, map[string]string{"a.txt": "one"})
	output, err := runGit(t.Context(), dir, nil, "rev-parse", "--show-toplevel")
	if err != nil {
		t.Fatalf("running git: %v", err)
	}
	if got := strings.TrimSpace(output.stdout); !strings.HasSuffix(got, dirName(dir)) {
		t.Errorf("runGit ran in %q, want %q", got, dir)
	}
}

func dirName(path string) string {
	parts := strings.Split(strings.TrimSuffix(path, "/"), "/")
	return parts[len(parts)-1]
}

func TestRunGitUsesTheEnvironmentItIsGiven(t *testing.T) {
	dir := repositoryWithACommit(t, map[string]string{"a.txt": "one"})
	output, err := runGit(t.Context(), dir, []string{
		"GIT_CONFIG_COUNT=1",
		"GIT_CONFIG_KEY_0=user.name",
		"GIT_CONFIG_VALUE_0=the driver",
	}, "config", "--get", "user.name")
	if err != nil {
		t.Fatalf("running git: %v", err)
	}
	if got := strings.TrimSpace(output.stdout); got != "the driver" {
		t.Errorf("runGit returned %q, want %q", got, "the driver")
	}
}

func TestRunGitReportsWhatGitPrintedOnStderr(t *testing.T) {
	output, err := runGit(t.Context(), t.TempDir(), nil, "rev-parse", "--verify", "refs/heads/main")
	if err == nil {
		t.Fatal("runGit returned no error outside a repository")
	}
	if !strings.Contains(err.Error(), "rev-parse --verify refs/heads/main") {
		t.Errorf("runGit returned the error %q, want the arguments in it", err)
	}
	if output.code == 0 {
		t.Errorf("runGit returned code %d, want a failure", output.code)
	}
	if output.stderr == "" {
		t.Error("runGit returned no stderr")
	}
}

func TestRunGitReportsAContextThatIsOver(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	output, err := runGit(ctx, t.TempDir(), nil, "version")
	if err == nil {
		t.Fatal("runGit returned no error under a context that is over")
	}
	if output.code != -1 {
		t.Errorf("runGit returned code %d, want -1 for a command that never ran", output.code)
	}
}

func TestGitReasonKeepsEveryLineGitWrote(t *testing.T) {
	for _, c := range []struct {
		name   string
		stderr string
		want   string
	}{
		{
			name: "a refused key",
			stderr: "git@git-server: Permission denied (publickey).\r\n" +
				"fatal: Could not read from remote repository.\n\n" +
				"Please make sure you have the correct access rights\n" +
				"and the repository exists.\n",
			want: "git@git-server: Permission denied (publickey). " +
				"fatal: Could not read from remote repository. " +
				"Please make sure you have the correct access rights " +
				"and the repository exists.",
		},
		{name: "one line", stderr: "fatal: bad revision\n", want: "fatal: bad revision"},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := gitReason(gitOutput{stderr: c.stderr}, context.Canceled); got != c.want {
				t.Errorf("gitReason returned %q, want %q", got, c.want)
			}
		})
	}
}

func TestAFetchFromNoRepositoryReportsGitsReason(t *testing.T) {
	_, repo := storeWith(t, fileURL(filepath.Join(t.TempDir(), "nothing")))
	if err := repo.create(t.Context()); err != nil {
		t.Fatalf("creating the repository: %v", err)
	}
	err := repo.fetch(t.Context(), nil, "main", 0)
	if err == nil || !strings.Contains(err.Error(), "does not appear to be a git repository") {
		t.Errorf("the fetch returned %v, want the line where git gives the reason", err)
	}
}

func TestGitReasonFallsBackToTheError(t *testing.T) {
	if got := gitReason(gitOutput{}, context.Canceled); got != context.Canceled.Error() {
		t.Errorf("gitReason returned %q, want %q", got, context.Canceled)
	}
}

func TestGitEnvironmentHasNoVariableOfTheNode(t *testing.T) {
	for _, name := range []string{"SSH_AUTH_SOCK=", "GIT_SSH_COMMAND=", "HOME="} {
		for _, entry := range gitEnvironment() {
			if strings.HasPrefix(entry, name) {
				t.Errorf("gitEnvironment returned %q, want no variable that starts with %s", entry, name)
			}
		}
	}
}
