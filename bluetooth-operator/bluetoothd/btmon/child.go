package main

// This file starts and stops the btmon child process.

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"syscall"
	"time"
)

// btmonPath is where the image installs btmon.
const btmonPath = "/usr/bin/btmon"

// --no-time drops btmon's timestamps because the container log already
// timestamps each line. --columns 160 widens the packet header lines,
// which btmon truncates at 80 columns by default, and that cuts off
// the names of management clients.
//
// btmon writes to the container's terminal, because the C library
// flushes output to a terminal at each newline. Output to a pipe waits
// until the buffer fills, which can delay a rare line for hours. On a
// terminal, btmon starts a pager and writes color codes, so --no-pager
// and --color never turn those off.
var btmonArgs = []string{"--no-pager", "--color", "never", "--no-time", "--columns", "160"}

// stopTimeout is how long stop waits for btmon to exit after TERM
// before it sends KILL.
const stopTimeout = 5 * time.Second

func startBtmon() (trace, error) {
	return startProcess(btmonPath, btmonArgs...)
}

// process is a running child. It implements trace.
type process struct {
	cmd    *exec.Cmd
	exited chan struct{}
}

// startProcess starts path as a child that writes to this program's
// standard output and error. In the container, those are the terminal
// that btmonArgs depends on.
func startProcess(path string, args ...string) (trace, error) {
	cmd := exec.Command(path, args...)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	p := &process{cmd: cmd, exited: make(chan struct{})}
	go func() {
		err := cmd.Wait()
		fmt.Fprintf(os.Stderr, "start-btmon: btmon exited: %v\n", exitText(err))
		close(p.exited)
	}()
	return p, nil
}

func (p *process) done() <-chan struct{} { return p.exited }

// stop sends TERM, waits up to stopTimeout, and then sends KILL. It
// signals through the process handle, which holds a pidfd, so a signal
// sent after the child was reaped cannot reach a process that reused
// its PID.
func (p *process) stop() {
	if err := p.cmd.Process.Signal(syscall.SIGTERM); err != nil && !errors.Is(err, os.ErrProcessDone) {
		fmt.Fprintf(os.Stderr, "start-btmon: sending TERM to btmon: %v\n", err)
	}
	select {
	case <-p.exited:
		return
	case <-time.After(stopTimeout):
	}
	fmt.Fprintf(os.Stderr, "start-btmon: btmon did not exit within %s after TERM, so it is being killed\n", stopTimeout)
	_ = p.cmd.Process.Kill()
	<-p.exited
}

// exitText returns the text to log for the error from cmd.Wait.
func exitText(err error) string {
	if err == nil {
		return "exit status 0"
	}
	return err.Error()
}
