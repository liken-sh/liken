package main

// The btmon process itself.

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

// btmonArgs are the arguments of the trace.
//
// --no-time leaves the timestamps to the container log, which records
// the wall-clock time of each line. --columns sets the width for the
// packet header lines, which btmon cuts to 80 characters by default
// and so cuts the names of management clients.
//
// The container runs with a terminal, and btmon writes to it, because
// the C library then writes each line when btmon finishes it. With a
// pipe, the library holds the output until its buffer fills, and a
// rare line waits in the buffer for hours. On a terminal btmon also
// starts a pager and colors its output, and --no-pager and
// --color never turn both off.
var btmonArgs = []string{"--no-pager", "--color", "never", "--no-time", "--columns", "160"}

// stopTimeout bounds the wait for btmon to exit after TERM. btmon ends
// its main loop on TERM, so the bound covers only a btmon that hangs,
// and KILL ends that one.
const stopTimeout = 5 * time.Second

// startBtmon starts the trace.
func startBtmon() (trace, error) {
	return startProcess(btmonPath, btmonArgs...)
}

// process is one child of this program.
type process struct {
	cmd    *exec.Cmd
	exited chan struct{}
}

// startProcess starts path as a child that writes to this program's
// own standard output and error. Those are the container's terminal,
// so the child writes to the terminal, as the comment at btmonArgs
// requires.
func startProcess(path string, args ...string) (trace, error) {
	cmd := exec.Command(path, args...)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	p := &process{cmd: cmd, exited: make(chan struct{})}
	go func() {
		err := cmd.Wait()
		fmt.Fprintf(os.Stderr, "start-btmon: btmon ended: %v\n", exitText(err))
		close(p.exited)
	}()
	return p, nil
}

func (p *process) done() <-chan struct{} { return p.exited }

// stop signals the child through its process handle, which holds a
// pidfd, so a signal after the child was reaped reaches no other
// process.
func (p *process) stop() {
	if err := p.cmd.Process.Signal(syscall.SIGTERM); err != nil && !errors.Is(err, os.ErrProcessDone) {
		fmt.Fprintf(os.Stderr, "start-btmon: signalling btmon: %v\n", err)
	}
	select {
	case <-p.exited:
		return
	case <-time.After(stopTimeout):
	}
	fmt.Fprintf(os.Stderr, "start-btmon: btmon did not exit within %s of TERM; killing it\n", stopTimeout)
	_ = p.cmd.Process.Kill()
	<-p.exited
}

// exitText describes how a child ended.
func exitText(err error) string {
	if err == nil {
		return "exit status 0"
	}
	return err.Error()
}
