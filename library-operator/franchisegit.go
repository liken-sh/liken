package main

// franchisegit.go is the fetch a franchises scan Job makes: one shallow
// clone of one ref into the Job's own emptyDir. The files are a few hundred
// kilobytes, so nothing keeps a checkout and every scan clones again. The
// clone is anonymous over HTTPS, which tangled.org and GitHub both serve,
// and a private repository waits until someone asks for one. The scanner
// runs git rather than fetching a tarball, because the archive URL differs
// per forge and carries no commit id.

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// The two variables the operator writes into a franchises scanner. The
// scanner learns the repository from these alone, because the pod carries
// no API credential to read the Library with.
const (
	libraryGitURLVariable = "LIBRARY_GIT_URL"
	libraryGitRefVariable = "LIBRARY_GIT_REF"
)

// cloneTimeout is how long the clone may take before the Job gives up on
// the forge. A forge that answers slowly must not hold the Job open past
// its own schedule; the next scan retries. It is a variable so a test can
// drive a clone that runs out of time.
var cloneTimeout = 5 * time.Minute

// cloneRepository clones one ref into dir and returns the commit the
// checkout holds. The depth is 1, so the clone carries one commit and no
// history. A clone that fails returns the error, and the caller writes no
// row.
func cloneRepository(ctx context.Context, url, ref, dir string) (string, error) {
	bounded, cancel := context.WithTimeout(ctx, cloneTimeout)
	defer cancel()

	if _, err := runGit(bounded, "clone", "--depth", "1", "--single-branch",
		"--branch", ref, "--", url, dir); err != nil {
		return "", fmt.Errorf("cloning %s at %s: %w", url, ref, err)
	}
	head, err := runGit(bounded, "-C", dir, "rev-parse", "HEAD")
	if err != nil {
		return "", fmt.Errorf("reading the commit of %s: %w", url, err)
	}
	return head, nil
}

// runGit runs one git command and returns what it printed, with the
// standard error folded into a failure a person can read in the pod log.
// The terminal prompt is off, so a repository that asks for a password
// fails at once instead of holding the Job open. It reads no configuration
// file, so the clone runs the same in the image, which carries none, and on
// a workstation, which carries one. The one setting it carries is that
// every directory is safe: the emptyDir the Job clones into belongs to
// root, and the scanner runs as another user, so without it git refuses
// to read the commit of the checkout it just wrote.
func runGit(ctx context.Context, args ...string) (string, error) {
	command := exec.CommandContext(ctx, "git", args...)
	command.Env = append(command.Environ(),
		"GIT_TERMINAL_PROMPT=0", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null",
		"GIT_CONFIG_COUNT=1", "GIT_CONFIG_KEY_0=safe.directory", "GIT_CONFIG_VALUE_0=*")
	out, err := command.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git %s: %w: %s", args[0], err, strings.TrimSpace(string(out)))
	}
	return strings.TrimSpace(string(out)), nil
}
