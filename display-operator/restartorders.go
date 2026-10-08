package main

// Restarting the compositor inside its container.
//
// The operator restarts the compositor to change a mode, to heal a
// canvas, and to end a compositor that hangs or has no DRM master.
// The kubelet puts every exit of a container through its crash
// backoff, whatever the exit code, so a restart that ended the
// container would leave every screen on the card dark for 10 seconds
// the second time within ten minutes, 20 seconds the third time, and
// so on up to 5 minutes.
//
// So the compositor role stays up and runs weston as its child. Before
// the operator signals a weston process, it writes an order that names
// the process's pid in the pod's config volume. When weston exits, the
// compositor role looks for the order. An order means the operator
// ended weston, and the role starts weston again at once. No order
// means weston crashed, and the role exits with weston's status, so
// the kubelet's backoff still bounds a crash loop, and the container's
// restart count is the count of crashes.
//
// The order is a file, not a signal to the compositor role, because
// the operator ends one pid and never the process that replaces it: a
// compositor on its way out can still be reported with no DRM master,
// and the order for its pid is what keeps that report from ending the
// next one. The file outlives a restart of the operator's container,
// and the compositor role removes it when the pid exits, so the
// directory holds an order only for a process that still runs.

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"
)

// restartOrdersPath is the directory of the orders, in the volume that
// the compositor's container and the operator's container share. It is
// a variable so the tests can point it at a directory they control.
var restartOrdersPath = "/etc/weston/restarts"

// restartOrders is a directory with one empty file for each compositor
// process the operator ended, named by its pid. The pod shares one
// process namespace, so both containers read the same pid. A plugin
// with no directory places no orders, which is an operator a person
// runs by hand, outside the pod and its compositor role.
type restartOrders string

func (o restartOrders) order(pid int) string {
	return filepath.Join(string(o), strconv.Itoa(pid))
}

// place records that the operator ends one compositor process. It runs
// before the signal, so the order is there when the process exits.
func (o restartOrders) place(pid int) error {
	if o == "" {
		return nil
	}
	if err := os.MkdirAll(string(o), 0o755); err != nil {
		return err
	}
	return os.WriteFile(o.order(pid), nil, 0o644)
}

// placed reports whether the operator ended one compositor process.
func (o restartOrders) placed(pid int) bool {
	if o == "" {
		return false
	}
	_, err := os.Stat(o.order(pid))
	return err == nil
}

// take removes the order for one process and reports whether there
// was one.
func (o restartOrders) take(pid int) bool {
	if o == "" {
		return false
	}
	return os.Remove(o.order(pid)) == nil
}

// clear removes every order. The compositor role clears them when its
// container starts, because a pid of a container before it can be the
// pid of a new process now.
func (o restartOrders) clear() error {
	return os.RemoveAll(string(o))
}

// supervise runs the compositor, and starts it again after each exit
// the operator ordered. It returns the exit status of the first exit
// that no order names, or of the compositor that a stop ended: the
// status weston exited with, or 128 and the signal that ended it, as
// a shell reports it. The stop is the kubelet's SIGTERM to the
// container, and the compositor takes the same signal.
func supervise(start func() (*exec.Cmd, error), orders restartOrders, stop <-chan os.Signal) (int, error) {
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
			orders.take(pid)
			return exitStatus(compositor.ProcessState), nil
		}
		if !orders.take(pid) {
			fmt.Fprintf(os.Stderr, "the compositor at pid %d exited (%s) with no order from the operator, so its container exits\n",
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

// endCompositors ends every compositor process that runs now, with an
// order for each. A search that finds nothing is a failure to report:
// a prepare that waited for a mode change that nothing started would
// hold the pod until its timeout with no reason a person can read.
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
		return fmt.Errorf("no process runs %s", westonBinary)
	}
	return nil
}

// endCompositor orders the restart of one compositor process and sends
// it the signal. A signal that does not arrive takes the order back,
// so a process that exited on its own is still a crash.
//
// SIGTERM is the restart for a mode, a heal, and a compositor with no
// DRM master. SIGKILL is the restart for a compositor that accepts on
// its socket and answers nothing, because a stopped process runs no
// signal handler until something continues it.
func (p *draPlugin) endCompositor(pid int, signal syscall.Signal) error {
	if err := p.orders.place(pid); err != nil {
		return fmt.Errorf("ordering the restart of %s at pid %d: %w", westonBinary, pid, err)
	}
	err := p.signal(pid, signal)
	if err != nil {
		p.orders.take(pid)
	}
	return err
}

// signalProcess sends a signal to one process.
//
// The handle from os.FindProcess holds a pidfd on Linux, so from the
// moment it opens, the handle refers to the process and not to its
// number: a process that exits after the handle opens answers
// os.ErrProcessDone, and the signal never reaches a new process that
// reuses the number. A number reused between the caller's read of
// /proc and the open is not covered, and that gap is one read of /proc
// long.
func signalProcess(pid int, signal syscall.Signal) error {
	process, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	defer func() { _ = process.Release() }()
	return process.Signal(signal)
}
