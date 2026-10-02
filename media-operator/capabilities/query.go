package main

// The agent's side of the query: it runs the query of one render node
// as a child process and reads the report from its output.
//
// A VA-API driver is a shared library that runs inside the process
// that opens it. A driver that hangs in vaInitialize would hang the
// agent, and a driver that crashes would end it. The child holds the
// driver instead: the agent kills a child that runs past
// queryTimeout, and reads a crash as a failed query.

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

// queryMode is the argument that runs the program as the query of one
// render node.
const queryMode = "query"

// queryTimeout bounds one query. A healthy driver answers in well under
// a second, because the query reads lists and creates one config.
const queryTimeout = 20 * time.Second

// querier runs the query of one render node.
type querier struct {
	// Command builds the child process. The agent runs its own
	// executable in queryMode, and a test runs a stand-in.
	command func(ctx context.Context, path string) *exec.Cmd
	timeout time.Duration
}

// selfQuerier runs this program's own executable as the child.
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

// query runs the child and decodes its report. A child that fails
// returns an error that holds its stderr word for word, and a child
// that runs past the timeout returns an error that says so.
func (q *querier) query(ctx context.Context, path string) (report, error) {
	ctx, cancel := context.WithTimeout(ctx, q.timeout)
	defer cancel()
	var stdout, stderr bytes.Buffer
	cmd := q.command(ctx, path)
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	// A process that the child started can hold its output open after
	// the kill. WaitDelay bounds the wait for that output to close.
	cmd.WaitDelay = time.Second
	err := cmd.Run()
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return report{}, fmt.Errorf("the query of %s ran past %s and was killed: %s",
			path, q.timeout, strings.TrimSpace(stderr.String()))
	}
	if err != nil {
		return report{}, fmt.Errorf("the query of %s failed (%v): %s", path, err, strings.TrimSpace(stderr.String()))
	}
	var facts report
	if err := json.Unmarshal(stdout.Bytes(), &facts); err != nil {
		return report{}, fmt.Errorf("the query of %s wrote a report that does not decode: %w", path, err)
	}
	return facts, nil
}

// runQuery is the child's side: it queries one render node and writes
// the report to stdout as JSON. It returns the exit code.
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
