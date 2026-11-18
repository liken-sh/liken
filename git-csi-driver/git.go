package main

// Every git invocation of the driver goes through runGit, so each one
// gets the same deadline, environment, and error format.

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

// gitDeadline limits one git invocation, so a remote that stops
// responding cannot block a kubelet call or a fetch loop forever.
const gitDeadline = 60 * time.Second

// gitWaitDelay is how long exec waits for a killed git to exit before it
// closes git's output pipes.
const gitWaitDelay = 5 * time.Second

type gitOutput struct {
	stdout string
	stderr string
	code   int
}

// runGit runs git in dir with env added to gitEnvironment. The error
// includes git's stderr, because that is where git explains the
// failure.
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

// gitEnvironment leaves out HOME, the ssh agent socket, and the node's
// global and system git configuration, and disables prompts. So a fetch
// behaves the same on every node and never waits for a password.
func gitEnvironment() []string {
	return []string{
		"PATH=" + os.Getenv("PATH"),
		"GIT_CONFIG_GLOBAL=/dev/null",
		"GIT_CONFIG_SYSTEM=/dev/null",
		"GIT_TERMINAL_PROMPT=0",
		"LC_ALL=C",
	}
}

// gitReason joins the non-empty lines of git's stderr into one line, or
// returns the exec error when stderr is empty. It keeps every line,
// because git prints the cause before its general advice. For a refused
// ssh key, the last line is "and the repository exists." and the line
// with "Permission denied (publickey)" comes first.
func gitReason(output gitOutput, err error) string {
	lines := []string{}
	for _, line := range strings.Split(output.stderr, "\n") {
		if trimmed := strings.TrimSpace(line); trimmed != "" {
			lines = append(lines, trimmed)
		}
	}
	if len(lines) == 0 {
		return err.Error()
	}
	return strings.Join(lines, " ")
}
