package main

// The agent queries each render node in a child process and reads the
// report from the child's stdout. A VA-API driver is a shared library
// that runs in the process that loads it, so a driver that hangs in
// vaInitialize or crashes would hang or end the agent. The agent kills
// a child that runs longer than queryTimeout and treats a crash as a
// failed query.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"
)

// queryMode is the first argument that makes the program run as the
// child.
const queryMode = "query"

// queryTimeout limits one query. The query only lists profiles and
// formats and creates one config, so a query that takes this long has a
// hung driver.
const queryTimeout = 20 * time.Second

type querier struct {
	// command builds the child process. A test replaces it.
	command func(ctx context.Context, path string) *exec.Cmd
	timeout time.Duration
}

// selfQuerier returns a querier that runs this program's own executable
// with queryMode.
func selfQuerier() (*querier, error) {
	self, err := os.Executable()
	if err != nil {
		return nil, err
	}
	return &querier{
		command: func(ctx context.Context, path string) *exec.Cmd {
			return exec.CommandContext(ctx, self, queryMode, path)
		},
		timeout: queryTimeout,
	}, nil
}

// query runs the child and decodes its report. When the child fails or
// is killed, the error includes the child's stderr.
func (q *querier) query(ctx context.Context, path string) (report, error) {
	ctx, cancel := context.WithTimeout(ctx, q.timeout)
	defer cancel()
	var stdout, stderr bytes.Buffer
	cmd := q.command(ctx, path)
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	// A process that the child started can keep the output pipes open
	// after the child exits or is killed, and Run would wait for them.
	cmd.WaitDelay = time.Second
	err := cmd.Run()
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return report{}, fmt.Errorf("the query of %s did not finish within %s and was killed, child stderr: %s",
			path, q.timeout, strings.TrimSpace(stderr.String()))
	}
	if err != nil {
		return report{}, fmt.Errorf("the query of %s exited with %v, child stderr: %s", path, err, strings.TrimSpace(stderr.String()))
	}
	var facts report
	if err := json.Unmarshal(stdout.Bytes(), &facts); err != nil {
		return report{}, fmt.Errorf("decoding the report that the query of %s wrote: %w", path, err)
	}
	return facts, nil
}

// runQuery runs in the child. It queries one render node and writes the
// report to stdout as JSON. It returns the exit code: 2 for wrong
// arguments and 1 for a failed query.
func runQuery(args []string, stdout, stderr io.Writer) int {
	if len(args) != 1 {
		fmt.Fprintf(stderr, "usage: %s %s <render node>\n", os.Args[0], queryMode)
		return 2
	}
	facts, err := queryRenderNode(args[0])
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	encoder := json.NewEncoder(stdout)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(facts); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	return 0
}
