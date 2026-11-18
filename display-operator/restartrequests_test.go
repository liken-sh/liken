package main

// Each compositor in these tests is a real child process.

import (
	"os"
	"os/exec"
	"syscall"
	"testing"
)

// startsOf returns a start function that runs the given command as the
// compositor, and a channel that receives each process it starts.
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

func superviseInBackground(start func() (*exec.Cmd, error), requests restartRequests, stop <-chan os.Signal) chan int {
	status := make(chan int, 1)
	go func() {
		code, _ := supervise(start, requests, stop)
		status <- code
	}()
	return status
}

func TestTheCompositorStartsAgainAfterTheOperatorEndsIt(t *testing.T) {
	cases := []struct {
		name   string
		signal syscall.Signal
	}{
		{"a mode, a canvas repair, or a compositor with no DRM master", syscall.SIGTERM},
		{"a hung compositor", syscall.SIGKILL},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			requests := restartRequests(t.TempDir())
			start, started := startsOf(t, "sleep", "60")
			status := superviseInBackground(start, requests, nil)
			plugin := &draPlugin{restartRequests: requests, signal: signalProcess}

			first := <-started
			if err := plugin.endCompositor(first.Process.Pid, c.signal); err != nil {
				t.Fatalf("ending the first compositor at pid %d: %v", first.Process.Pid, err)
			}
			second := <-started

			if requests.exists(first.Process.Pid) {
				t.Errorf("the restart request file for pid %d outlived its process", first.Process.Pid)
			}
			// The second compositor crashes, with no file.
			if err := second.Process.Signal(syscall.SIGSEGV); err != nil {
				t.Fatalf("signalling the second compositor at pid %d: %v", second.Process.Pid, err)
			}
			if got, want := <-status, 128+int(syscall.SIGSEGV); got != want {
				t.Errorf("the container exited with %d, want %d", got, want)
			}
			if len(started) != 0 {
				t.Error("the compositor role started weston again after a crash, want it to start none")
			}
		})
	}
}

func TestACompositorThatExitsWithNoOrderEndsItsContainer(t *testing.T) {
	start, started := startsOf(t, "sh", "-c", "exit 3")

	status := <-superviseInBackground(start, restartRequests(t.TempDir()), nil)

	if status != 3 {
		t.Errorf("the container exited with %d, want weston's 3", status)
	}
	if len(started) != 1 {
		t.Errorf("the compositor role started weston %d times, want once", len(started))
	}
}

func TestTheKubeletsStopReachesTheCompositor(t *testing.T) {
	start, started := startsOf(t, "sleep", "60")
	stop := make(chan os.Signal, 1)
	status := superviseInBackground(start, restartRequests(t.TempDir()), stop)
	<-started

	stop <- syscall.SIGTERM

	if got, want := <-status, 128+int(syscall.SIGTERM); got != want {
		t.Errorf("the container exited with %d, want %d", got, want)
	}
	if len(started) != 0 {
		t.Error("the compositor role started weston again after the stop, want it to start none")
	}
}

func TestAnEndThatFindsNoProcessLeavesNoRequestFile(t *testing.T) {
	requests := restartRequests(t.TempDir())
	plugin := &draPlugin{
		restartRequests: requests,
		signal:          func(int, syscall.Signal) error { return os.ErrProcessDone },
	}

	if err := plugin.endCompositor(14, syscall.SIGTERM); err == nil {
		t.Fatal("endCompositor returned no error for a process that was gone, want one")
	}
	if requests.exists(14) {
		t.Error("the restart request file for a process that was gone remains, want none")
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
			plugin := &draPlugin{
				compositors:     func() []int { return c.running },
				signal:          func(int, syscall.Signal) error { return c.signal },
				restartRequests: restartRequests(t.TempDir()),
			}

			if err := plugin.endCompositors(syscall.SIGTERM); err == nil {
				t.Fatal("endCompositors returned no error when it ended no compositor, want one")
			}
		})
	}
}

func TestClearRemovesOrdersFromAPreviousContainer(t *testing.T) {
	requests := restartRequests(t.TempDir())
	if err := requests.write(14); err != nil {
		t.Fatalf("writing the restart request file for pid 14: %v", err)
	}

	if err := requests.clear(); err != nil {
		t.Fatalf("clearing the restart request files: %v", err)
	}

	if requests.exists(14) {
		t.Error("a restart request file from the previous container remains, want none")
	}
}
