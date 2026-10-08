package main

// Restarting PipeWire and WirePlumber inside their containers.
//
// A new channel layout takes effect only in a new PipeWire, and a
// PipeWire that goes away takes its WirePlumber with it. The kubelet
// puts every exit of a container through its crash backoff, whatever
// the exit code, so a restart that ended the containers would leave
// every sink on the machine silent for 10 seconds the second time
// within ten minutes, 20 seconds the third time, and so on up to 5
// minutes. A receiver's ELD changes with its power, so turning a
// receiver on and off again within ten minutes reaches that backoff.
//
// So each container's first process runs its daemon as a child and
// starts it again in place:
//
//   - The PipeWire container watches the drop-in directory. When the
//     drop-in is newer than PipeWire's socket (declarationloaded.go),
//     it ends PipeWire and starts it again.
//   - The WirePlumber container starts WirePlumber again when it exits
//     because a new PipeWire replaced the one it served. PipeWire binds
//     its socket again at each start, so a new PipeWire is a new file at
//     the socket's path.
//
// Any other exit ends the container with the daemon's status, so the
// kubelet's backoff still bounds a crash loop, and each container's
// restart count is the count of crashes.

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"
)

// The arguments that select the two first processes.
const (
	pipewireMode    = "pipewire"
	wireplumberMode = "wireplumber"
)

// The daemons, in full because the first process starts them with no
// PATH lookup.
const (
	pipewireBinary    = "/usr/bin/pipewire"
	wireplumberBinary = "/usr/bin/wireplumber"
)

// pipewireReturnLimit bounds the WirePlumber container's wait for a new
// PipeWire to answer. A PipeWire that crashed waits in the kubelet's
// crash backoff, which doubles up to 5 minutes, so the limit is past
// that cap.
const pipewireReturnLimit = 6 * time.Minute

// The first and the longest pause between two looks at PipeWire's
// socket while it does not answer. PipeWire writes no event a client
// can wait on while it is down, so each look is one connect.
const (
	firstSocketPause   = 100 * time.Millisecond
	longestSocketPause = 2 * time.Second
)

// runPipewire is the PipeWire container's first process.
func runPipewire() {
	dropIn := filepath.Join(pipewireConfigDir, dropInName)
	// The watch opens before PipeWire starts, so a drop-in written while
	// PipeWire starts is not lost.
	writes := newVolumeSwaps(pipewireConfigDir, dropInName)
	if err := writes.arm(); err != nil {
		fatal("watching %s: %v", pipewireConfigDir, err)
	}
	stale := func() bool {
		newer, err := declarationNewer(dropIn, socketPath)
		return err == nil && newer
	}
	start := func() (*exec.Cmd, error) {
		return startDaemon(pipewireBinary)
	}
	os.Exit(superviseDaemon("PipeWire", socketPath, start, writes.swapped, stale, nil, stops()))
}

// runWireplumber is the WirePlumber container's first process. Its
// arguments go to WirePlumber.
func runWireplumber(arguments []string) {
	start := func() (*exec.Cmd, error) {
		return startDaemon(wireplumberBinary, arguments...)
	}
	replaced := func(before pipewireIdentity) bool {
		if !awaitPipewire(socketPath, pipewireReturnLimit) {
			fmt.Fprintf(os.Stderr, "PipeWire did not answer within %s\n", pipewireReturnLimit)
			return false
		}
		return currentPipewire(socketPath) != before
	}
	os.Exit(superviseDaemon("WirePlumber", socketPath, start, nil, nil, replaced, stops()))
}

func startDaemon(binary string, arguments ...string) (*exec.Cmd, error) {
	daemon := exec.Command(binary, arguments...)
	daemon.Stdout, daemon.Stderr = os.Stdout, os.Stderr
	return daemon, daemon.Start()
}

// stops is the kubelet's SIGTERM to the container, and a SIGINT.
func stops() <-chan os.Signal {
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGTERM, syscall.SIGINT)
	return stop
}

// superviseDaemon runs one daemon and starts it again in place. It
// returns the status the container exits with.
//
// When a wake arrives and stale answers true, it ends the daemon with
// SIGTERM and starts it again. When the daemon exits on its own and
// replaced answers true for the PipeWire the daemon started against, it
// starts the daemon again. Every other exit, and a stop, returns the
// daemon's status: its exit code, or 128 and the signal that ended it,
// as a shell reports it.
func superviseDaemon(
	name, socket string,
	start func() (*exec.Cmd, error),
	wakes <-chan struct{},
	stale func() bool,
	replaced func(before pipewireIdentity) bool,
	stop <-chan os.Signal,
) int {
	for {
		before := currentPipewire(socket)
		daemon, err := start()
		if err != nil {
			fmt.Fprintf(os.Stderr, "starting %s: %v\n", name, err)
			return 1
		}
		exited := make(chan struct{})
		go func() {
			_ = daemon.Wait()
			close(exited)
		}()
		ordered := false
	running:
		for {
			select {
			case <-exited:
				break running
			case signal := <-stop:
				_ = daemon.Process.Signal(signal)
				<-exited
				return daemonStatus(daemon.ProcessState)
			case <-wakes:
				if ordered || !stale() {
					continue
				}
				ordered = true
				fmt.Printf("the PipeWire declaration changed, so %s at pid %d restarts to load it\n", name, daemon.Process.Pid)
				_ = daemon.Process.Signal(syscall.SIGTERM)
			}
		}
		if ordered {
			continue
		}
		if replaced != nil && replaced(before) {
			fmt.Printf("%s at pid %d exited because a new PipeWire started, so it starts again\n", name, daemon.Process.Pid)
			continue
		}
		fmt.Fprintf(os.Stderr, "%s at pid %d exited (%s), so its container exits\n", name, daemon.Process.Pid, daemon.ProcessState)
		return daemonStatus(daemon.ProcessState)
	}
}

// daemonStatus is the status a shell reports for a daemon that ended.
func daemonStatus(state *os.ProcessState) int {
	if status, ok := state.Sys().(syscall.WaitStatus); ok && status.Signaled() {
		return 128 + int(status.Signal())
	}
	return state.ExitCode()
}

// pipewireIdentity is one PipeWire: the device, the inode, and the
// change time of the socket it bound, or the zero value while no
// PipeWire answers. The kernel can give a new socket the inode of the
// one it replaced, so the change time is what tells them apart.
type pipewireIdentity struct {
	device, inode uint64
	changed       syscall.Timespec
}

func currentPipewire(socket string) pipewireIdentity {
	connection, err := net.Dial("unix", socket)
	if err != nil {
		return pipewireIdentity{}
	}
	_ = connection.Close()
	var stat syscall.Stat_t
	if err := syscall.Stat(socket, &stat); err != nil {
		return pipewireIdentity{}
	}
	return pipewireIdentity{device: uint64(stat.Dev), inode: stat.Ino, changed: stat.Ctim}
}

// awaitPipewire waits until PipeWire accepts a connection, up to the
// limit.
func awaitPipewire(socket string, limit time.Duration) bool {
	deadline := time.Now().Add(limit)
	pause := firstSocketPause
	for currentPipewire(socket) == (pipewireIdentity{}) {
		if !time.Now().Before(deadline) {
			return false
		}
		time.Sleep(pause)
		pause = min(pause*2, longestSocketPause)
	}
	return true
}
