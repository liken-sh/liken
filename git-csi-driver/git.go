package main

// git.go holds the one function every git invocation goes through, so
// every call has the same deadline, the same environment, and the same
// error shape.

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

// gitDeadline bounds one git invocation. A remote that never answers
// must not hold a kubelet call open forever.
const gitDeadline = 60 * time.Second

// gitWaitDelay is how long a killed git has to exit before its output
// pipes are abandoned.
const gitWaitDelay = 5 * time.Second

// gitOutput is what one git invocation answers.
type gitOutput struct {
	stdout string
	stderr string
	code   int
}

// runGit runs git in dir, with env added to the hermetic environment
// below, under the deadline. The error carries git's own last line of
// stderr, which is where git says why it failed.
func runGit(ctx context.Context, dir string, env []string, args ...string) (gitOutput, error) {
	ctx, cancel := context.WithTimeout(ctx, gitDeadline)
	defer cancel()

	command := exec.CommandContext(ctx, "git", args...)
	command.Dir = dir
	command.Env = append(gitEnvironment(), env...)
	command.WaitDelay = gitWaitDelay

	var stdout, stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr

	err := command.Run()
	output := gitOutput{stdout: stdout.String(), stderr: stderr.String(), code: -1}
	if command.ProcessState != nil {
		output.code = command.ProcessState.ExitCode()
	}
	if err != nil {
		return output, fmt.Errorf("git %s: %s", strings.Join(args, " "), gitReason(output, err))
	}
	return output, nil
}

// gitEnvironment is the environment every invocation starts from.
// Nothing of the node's own reaches git: no HOME, no agent socket, no
// global or system configuration, and no prompt, so a fetch behaves the
// same on every node and never waits for a password.
func gitEnvironment() []string {
	return []string{
		"PATH=" + os.Getenv("PATH"),
		"GIT_CONFIG_GLOBAL=/dev/null",
		"GIT_CONFIG_SYSTEM=/dev/null",
		"GIT_TERMINAL_PROMPT=0",
		"LC_ALL=C",
	}
}

// gitReason is the last non-empty line of stderr, where git states its
// reason, or the exec error when git wrote nothing.
func gitReason(output gitOutput, err error) string {
	lines := strings.Split(strings.TrimSpace(output.stderr), "\n")
	if last := strings.TrimSpace(lines[len(lines)-1]); last != "" {
		return last
	}
	return err.Error()
}
