package main

// These tests cover the restarts inside the PipeWire and WirePlumber
// containers. Each daemon is a real child process that runs until a
// signal ends it.

import (
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
)

// daemons starts the command it is given, and reports each process it
// starts.
func daemons(t *testing.T, name string, args ...string) (func() (*exec.Cmd, error), chan *exec.Cmd) {
	t.Helper()
	started := make(chan *exec.Cmd, 4)
	return func() (*exec.Cmd, error) {
		daemon := exec.Command(name, args...)
		err := daemon.Start()
		if err == nil {
			t.Cleanup(func() { _ = daemon.Process.Kill() })
			started <- daemon
		}
		return daemon, err
	}, started
}

// supervising runs the first process and reports the status it exits
// with.
func supervising(run func() int) chan int {
	status := make(chan int, 1)
	go func() { status <- run() }()
	return status
}

func TestANewDeclarationRestartsPipeWireInPlace(t *testing.T) {
	start, started := daemons(t, "sleep", "60")
	wakes := make(chan struct{}, 1)
	stale := true
	status := supervising(func() int {
		return superviseDaemon("PipeWire", "", start, wakes, func() bool { return stale }, nil, nil)
	})
	<-started

	wakes <- struct{}{}
	second := <-started
	stale = false

	// The new PipeWire crashes, and the container exits with it.
	_ = second.Process.Signal(syscall.SIGSEGV)
	if got, want := <-status, 128+int(syscall.SIGSEGV); got != want {
		t.Errorf("the container exited with %d, want %d", got, want)
	}
	if len(started) != 0 {
		t.Error("the first process started PipeWire again after a crash")
	}
}

// A wake whose drop-in PipeWire already runs, such as the second event
// of one write, restarts nothing.
func TestAWakeForTheDeclarationPipeWireRunsRestartsNothing(t *testing.T) {
	start, started := daemons(t, "sleep", "60")
	wakes := make(chan struct{}, 1)
	stop := make(chan os.Signal, 1)
	status := supervising(func() int {
		return superviseDaemon("PipeWire", "", start, wakes, func() bool { return false }, nil, stop)
	})
	<-started

	wakes <- struct{}{}
	stop <- syscall.SIGTERM

	if got, want := <-status, 128+int(syscall.SIGTERM); got != want {
		t.Errorf("the container exited with %d, want %d", got, want)
	}
	if len(started) != 0 {
		t.Error("the first process started PipeWire again for a declaration it runs")
	}
}

func TestWirePlumberStartsAgainOnlyAfterANewPipeWire(t *testing.T) {
	cases := []struct {
		name     string
		replaced bool
		starts   int
	}{
		{"a new PipeWire replaced the one it served", true, 2},
		{"the PipeWire it served still runs", false, 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			start, started := daemons(t, "sh", "-c", "exit 3")
			replacements := 0
			replaced := func(pipewireIdentity) bool {
				replacements++
				return c.replaced && replacements == 1
			}

			status := superviseDaemon("WirePlumber", "", start, nil, nil, replaced, nil)

			if status != 3 {
				t.Errorf("the container exited with %d, want WirePlumber's 3", status)
			}
			if len(started) != c.starts {
				t.Errorf("the first process started WirePlumber %d times, want %d", len(started), c.starts)
			}
		})
	}
}

func TestAStartThatFailsEndsTheContainer(t *testing.T) {
	start := func() (*exec.Cmd, error) {
		daemon := exec.Command(filepath.Join(t.TempDir(), "absent"))
		return daemon, daemon.Start()
	}

	if status := superviseDaemon("PipeWire", "", start, nil, nil, nil, nil); status != 1 {
		t.Errorf("the container exited with %d, want 1", status)
	}
}

// A PipeWire is the socket it bound: a new bind at the same path is a
// new PipeWire, and a socket nobody listens on is none.
func TestAPipeWireIsTheSocketItBound(t *testing.T) {
	socket := filepath.Join(t.TempDir(), "pipewire-0")
	if got := currentPipewire(socket); got != (pipewireIdentity{}) {
		t.Fatalf("a missing socket reads as %+v", got)
	}
	if awaitPipewire(socket, 0) {
		t.Fatal("the wait found a PipeWire at a missing socket")
	}

	first, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	before := currentPipewire(socket)
	if before == (pipewireIdentity{}) || !awaitPipewire(socket, 0) {
		t.Fatal("a listening socket reads as no PipeWire")
	}
	_ = first.Close()

	second, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = second.Close() }()
	if after := currentPipewire(socket); after == before {
		t.Errorf("a new bind reads as the same PipeWire, %+v", after)
	}
}
