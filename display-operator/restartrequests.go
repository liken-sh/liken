package main

// The operator restarts weston to change a mode, to repair the canvas
// after an output was destroyed and created again, and to end a weston
// that hangs or has no DRM master. The kubelet applies its crash
// backoff to every container exit, whatever the exit code, so a
// restart that ended the container would leave every screen on the
// card dark for up to 5 minutes.
//
// So the compositor container's main process runs weston as a child
// process. Before the operator signals weston, it writes a file named
// for weston's pid into a volume that both containers mount. When
// weston exits, the main process removes the file and starts weston
// again. When there is no file, weston crashed, and the main process
// exits with weston's status, so the kubelet's backoff limits a crash
// loop and the container's restart count counts only crashes.
//
// The file names one pid, so it applies only to that process and not
// to the weston that replaces it. A weston that is shutting down can
// still read as having no DRM master, and endMasterless skips a pid
// that has a file, so the operator does not signal it a second time.

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"
)

var restartRequestsPath = "/etc/weston/restarts"

// restartRequests is a directory with one empty file, named by pid, for
// each weston process that the operator ended. The containers of the
// pod share one process namespace, so both read the same pid. An empty
// name turns the files off, for an operator run by hand outside the pod.
type restartRequests string

func (o restartRequests) path(pid int) string {
	return filepath.Join(string(o), strconv.Itoa(pid))
}

func (o restartRequests) write(pid int) error {
	if o == "" {
		return nil
	}
	if err := os.MkdirAll(string(o), 0o755); err != nil {
		return err
	}
	return os.WriteFile(o.path(pid), nil, 0o644)
}

func (o restartRequests) exists(pid int) bool {
	if o == "" {
		return false
	}
	_, err := os.Stat(o.path(pid))
	return err == nil
}

// remove reports whether the pid had a file, and removes it.
func (o restartRequests) remove(pid int) bool {
	if o == "" {
		return false
	}
	return os.Remove(o.path(pid)) == nil
}

// clear runs when the compositor container starts, because a pid from
// the previous container can belong to a different process now.
func (o restartRequests) clear() error {
	return os.RemoveAll(string(o))
}

// supervise starts weston again after each exit that has a file. It
// returns after an exit with no file, or after it forwards a signal
// from stop to weston and weston exits. The status is weston's exit
// code, or 128 plus the signal number, as a shell reports it.
func supervise(start func() (*exec.Cmd, error), requests restartRequests, stop <-chan os.Signal) (int, error) {
	for {
		compositor, err := start()
		if err != nil {
			return 0, err
		}
		pid := compositor.Process.Pid
		exited := make(chan struct{})
		go func() {
			_ = compositor.Wait()
			close(exited)
		}()
		select {
		case <-exited:
		case signal := <-stop:
			_ = compositor.Process.Signal(signal)
			<-exited
			requests.remove(pid)
			return exitStatus(compositor.ProcessState), nil
		}
		if !requests.remove(pid) {
			fmt.Fprintf(os.Stderr, "the compositor at pid %d exited (%s) and the operator did not request the restart, so its container exits\n",
				pid, compositor.ProcessState)
			return exitStatus(compositor.ProcessState), nil
		}
		fmt.Printf("%s: the operator ended the compositor at pid %d, and it starts again\n", DriverName, pid)
	}
}

func exitStatus(state *os.ProcessState) int {
	if status, ok := state.Sys().(syscall.WaitStatus); ok && status.Signaled() {
		return 128 + int(status.Signal())
	}
	return state.ExitCode()
}

// endCompositors returns an error when it ends no process, because a
// prepare that waits for a restart that never started would fail only
// at its timeout, with no error to read.
func (p *draPlugin) endCompositors(signal syscall.Signal) error {
	var pids []int
	if p.compositors != nil {
		pids = p.compositors()
	}
	ended := 0
	for _, pid := range pids {
		err := p.endCompositor(pid, signal)
		if errors.Is(err, os.ErrProcessDone) {
			continue
		}
		if err != nil {
			return err
		}
		ended++
	}
	if ended == 0 {
		return fmt.Errorf("no %s process is running", westonBinary)
	}
	return nil
}

// endCompositor removes the file again when the signal fails, so a
// weston that exited on its own still counts as a crash.
//
// A weston that accepts connections on its socket and does not respond
// gets SIGKILL, because a stopped process runs no signal handler until
// it is continued. Every other restart sends SIGTERM.
func (p *draPlugin) endCompositor(pid int, signal syscall.Signal) error {
	if err := p.restartRequests.write(pid); err != nil {
		return fmt.Errorf("ordering the restart of %s at pid %d: %w", westonBinary, pid, err)
	}
	err := p.signal(pid, signal)
	if err != nil {
		p.restartRequests.remove(pid)
	}
	return err
}

// signalProcess signals through a pidfd, which os.FindProcess opens on
// Linux. When the process exits after the open, Signal returns
// os.ErrProcessDone and does not signal a new process with the same pid.
// A pid reused before the open is not covered.
func signalProcess(pid int, signal syscall.Signal) error {
	process, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	defer func() { _ = process.Release() }()
	return process.Signal(signal)
}
