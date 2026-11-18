package main

// Each daemon in these tests is a real child process.

import (
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
)

// daemons returns a start function for superviseDaemon, and a channel
// that receives each process it starts.
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

func supervising(run func() int) chan int {
	status := make(chan int, 1)
	go func() { status <- run() }()
	return status
}

func TestANewDeclarationRestartsPipeWireInPlace(t *testing.T) {
	start, started := daemons(t, "sleep", "60")
	changes := make(chan struct{}, 1)
	stale := true
	status := supervising(func() int {
		return superviseDaemon("PipeWire", "", start, changes, func() bool { return stale }, nil, nil)
	})
	<-started

	changes <- struct{}{}
	second := <-started
	stale = false

	// A crash of the new PipeWire ends the container.
	_ = second.Process.Signal(syscall.SIGSEGV)
	if got, want := <-status, 128+int(syscall.SIGSEGV); got != want {
		t.Errorf("the container exited with %d, want %d", got, want)
	}
	if len(started) != 0 {
		t.Error("the first process started PipeWire again after a crash, want no start")
	}
}

// One write of the drop-in can deliver two changes, and the second one
// finds a PipeWire that already loaded it.
func TestAChangeToTheDeclarationPipeWireRunsRestartsNothing(t *testing.T) {
	start, started := daemons(t, "sleep", "60")
	changes := make(chan struct{}, 1)
	stop := make(chan os.Signal, 1)
	status := supervising(func() int {
		return superviseDaemon("PipeWire", "", start, changes, func() bool { return false }, nil, stop)
	})
	<-started

	changes <- struct{}{}
	stop <- syscall.SIGTERM

	if got, want := <-status, 128+int(syscall.SIGTERM); got != want {
		t.Errorf("the container exited with %d, want %d", got, want)
	}
	if len(started) != 0 {
		t.Error("the first process started PipeWire again for a declaration it runs, want no start")
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

// A new bind at the same path is a new identity, and a missing socket
// is the zero identity.
func TestAPipeWireIsTheSocketItBound(t *testing.T) {
	socket := filepath.Join(t.TempDir(), "pipewire-0")
	if got := currentPipewire(socket); got != (pipewireIdentity{}) {
		t.Fatalf("a missing socket reads as %+v, want the zero identity", got)
	}
	if awaitPipewire(socket, 0) {
		t.Fatal("awaitPipewire on a missing socket = true, want false")
	}

	first, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatalf("listening on the socket: %v", err)
	}
	before := currentPipewire(socket)
	if before == (pipewireIdentity{}) || !awaitPipewire(socket, 0) {
		t.Fatal("a listening socket reads as no PipeWire, want a PipeWire")
	}
	_ = first.Close()

	second, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatalf("listening on the socket again: %v", err)
	}
	defer func() { _ = second.Close() }()
	if after := currentPipewire(socket); after == before {
		t.Errorf("a new bind reads as %+v, the same PipeWire as before, want a new identity", after)
	}
}
