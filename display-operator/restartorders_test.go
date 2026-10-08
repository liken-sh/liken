package main

// These tests cover the restart inside the compositor's container: the
// compositor role starts weston again after an exit the operator
// ordered, and exits with weston's status after any other exit, so the
// kubelet's crash backoff applies to crashes alone. Each compositor is
// a real child process that runs until a signal ends it.

import (
	"os"
	"os/exec"
	"syscall"
	"testing"
)

// A compositor role that starts the command it is given, and reports
// each process it starts.
func startsOf(t *testing.T, name string, args ...string) (func() (*exec.Cmd, error), chan *exec.Cmd) {
	t.Helper()
	started := make(chan *exec.Cmd, 4)
	return func() (*exec.Cmd, error) {
		compositor := exec.Command(name, args...)
		err := compositor.Start()
		if err == nil {
			t.Cleanup(func() { _ = compositor.Process.Kill() })
			started <- compositor
		}
		return compositor, err
	}, started
}

// superviseInBackground runs the compositor role and reports the
// status it exits with.
func superviseInBackground(start func() (*exec.Cmd, error), orders restartOrders, stop <-chan os.Signal) chan int {
	status := make(chan int, 1)
	go func() {
		code, _ := supervise(start, orders, stop)
		status <- code
	}()
	return status
}

func TestTheCompositorStartsAgainAfterTheOperatorEndsIt(t *testing.T) {
	cases := []struct {
		name   string
		signal syscall.Signal
	}{
		{"a mode, a heal, or a compositor with no DRM master", syscall.SIGTERM},
		{"a hung compositor", syscall.SIGKILL},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			orders := restartOrders(t.TempDir())
			start, started := startsOf(t, "sleep", "60")
			status := superviseInBackground(start, orders, nil)
			plugin := &draPlugin{orders: orders, signal: signalProcess}

			first := <-started
			if err := plugin.endCompositor(first.Process.Pid, c.signal); err != nil {
				t.Fatal(err)
			}
			second := <-started

			if orders.placed(first.Process.Pid) {
				t.Errorf("the order for pid %d outlived its process", first.Process.Pid)
			}
			// The second compositor crashes, with no order, and the
			// container exits with the status of the crash.
			_ = second.Process.Signal(syscall.SIGSEGV)
			if got, want := <-status, 128+int(syscall.SIGSEGV); got != want {
				t.Errorf("the container exited with %d, want %d", got, want)
			}
			if len(started) != 0 {
				t.Errorf("the compositor role started weston again after a crash")
			}
		})
	}
}

func TestACompositorThatExitsWithNoOrderEndsItsContainer(t *testing.T) {
	start, started := startsOf(t, "sh", "-c", "exit 3")

	status := <-superviseInBackground(start, restartOrders(t.TempDir()), nil)

	if status != 3 {
		t.Errorf("the container exited with %d, want weston's 3", status)
	}
	if len(started) != 1 {
		t.Errorf("the compositor role started weston %d times, want once", len(started))
	}
}

// The kubelet stops the container with SIGTERM, and the compositor
// takes the same signal and does not start again.
func TestTheKubeletsStopReachesTheCompositor(t *testing.T) {
	start, started := startsOf(t, "sleep", "60")
	stop := make(chan os.Signal, 1)
	status := superviseInBackground(start, restartOrders(t.TempDir()), stop)
	<-started

	stop <- syscall.SIGTERM

	if got, want := <-status, 128+int(syscall.SIGTERM); got != want {
		t.Errorf("the container exited with %d, want %d", got, want)
	}
	if len(started) != 0 {
		t.Errorf("the compositor role started weston again after the stop")
	}
}

// A signal that finds the process gone takes its order back, so the
// exit is still read as the crash it was.
func TestAnEndThatFindsNoProcessLeavesNoOrder(t *testing.T) {
	orders := restartOrders(t.TempDir())
	plugin := &draPlugin{
		orders: orders,
		signal: func(int, syscall.Signal) error { return os.ErrProcessDone },
	}

	if err := plugin.endCompositor(14, syscall.SIGTERM); err == nil {
		t.Fatal("the end reported no error for a process that was gone")
	}
	if orders.placed(14) {
		t.Error("the order for a process that was gone stayed in place")
	}
}

func TestEndingTheCompositorsReportsThatItFoundNone(t *testing.T) {
	cases := []struct {
		name    string
		running []int
		signal  error
	}{
		{"no compositor runs", nil, nil},
		{"every compositor exited before the signal", []int{14}, os.ErrProcessDone},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			// A restart the operator ordered has to be a restart or a
			// failure. A search that found nothing and said nothing
			// would leave a prepare waiting for a mode change that
			// nothing started.
			plugin := &draPlugin{
				compositors: func() []int { return c.running },
				signal:      func(int, syscall.Signal) error { return c.signal },
				orders:      restartOrders(t.TempDir()),
			}

			if err := plugin.endCompositors(syscall.SIGTERM); err == nil {
				t.Fatal("the end found no compositor and reported no error")
			}
		})
	}
}

// A pid from the container before this one can belong to a new
// process, so the compositor role starts with no orders.
func TestANewContainerStartsWithNoOrders(t *testing.T) {
	orders := restartOrders(t.TempDir())
	if err := orders.place(14); err != nil {
		t.Fatal(err)
	}

	if err := orders.clear(); err != nil {
		t.Fatal(err)
	}

	if orders.placed(14) {
		t.Error("an order from the container before stayed in place")
	}
}
