package main

// PipeWire applies a channel layout only when it starts, and WirePlumber
// exits when its PipeWire goes away. If a restart of either daemon ended
// its container, the kubelet's crash backoff would apply whatever the exit
// code, and every sink on the machine would stay silent for 10 seconds
// on the second restart within ten minutes, 20 seconds on the third, and
// so on up to 5 minutes. A receiver's ELD changes when the receiver
// turns on or off, so turning a receiver off and on again within ten
// minutes would reach that backoff.
//
// So the first process of each container runs its daemon as a child
// and restarts it in place:
//
//   - The PipeWire container watches the drop-in directory. When the
//     drop-in is newer than PipeWire's socket (declarationloaded.go),
//     it sends PipeWire SIGTERM and starts it again.
//   - The WirePlumber container starts WirePlumber again when it exits
//     because a new PipeWire replaced its old one. PipeWire binds its
//     socket at each start, so a new PipeWire is a new file at the
//     socket's path.
//
// Any other exit ends the container with the daemon's status. The
// kubelet's backoff then still limits a crash loop, and the restart
// count of each container counts only crashes.

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

// The first argument of the operator binary selects the daemon that
// the container's first process supervises.
const (
	pipewireMode    = "pipewire"
	wireplumberMode = "wireplumber"
)

// The first process starts the daemons by full path and does not
// search PATH.
const (
	pipewireBinary    = "/usr/bin/pipewire"
	wireplumberBinary = "/usr/bin/wireplumber"
)

// pipewireReturnLimit is how long the WirePlumber container waits for
// PipeWire to accept connections again after WirePlumber exits. When
// PipeWire crashes, its container restarts only after the kubelet's
// crash backoff, which doubles up to 5 minutes, so the limit is longer
// than that.
const pipewireReturnLimit = 6 * time.Minute

// awaitPipewire polls the socket, because PipeWire sends no event that a
// client can wait on while it is down. The pause doubles from
// firstSocketPause up to longestSocketPause.
const (
	firstSocketPause   = 100 * time.Millisecond
	longestSocketPause = 2 * time.Second
)

// runPipewire is the first process of the PipeWire container.
func runPipewire() {
	dropIn := filepath.Join(pipewireConfigDir, dropInName)
	// The watch opens before PipeWire starts, so a drop-in written while
	// PipeWire starts is not missed.
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

// runWireplumber is the first process of the WirePlumber container.
func runWireplumber(arguments []string) {
	start := func() (*exec.Cmd, error) {
		return startDaemon(wireplumberBinary, arguments...)
	}
	replaced := func(before pipewireIdentity) bool {
		if !awaitPipewire(socketPath, pipewireReturnLimit) {
			fmt.Fprintf(os.Stderr, "PipeWire accepted no connection within %s\n", pipewireReturnLimit)
			return false
		}
		return currentPipewire(socketPath) != before
	}
	os.Exit(superviseDaemon("WirePlumber", socketPath, start, nil, nil, replaced, stops()))
}

// startDaemon sends the daemon's output to the container's log.
func startDaemon(binary string, arguments ...string) (*exec.Cmd, error) {
	daemon := exec.Command(binary, arguments...)
	daemon.Stdout, daemon.Stderr = os.Stdout, os.Stderr
	return daemon, daemon.Start()
}

func stops() <-chan os.Signal {
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGTERM, syscall.SIGINT)
	return stop
}

// superviseDaemon runs one daemon and returns the status that the
// container exits with. When changes delivers and stale returns true,
// it sends the daemon SIGTERM and starts it again. When the daemon exits
// on its own and replaced returns true for the PipeWire that was running
// when the daemon started, it starts the daemon again. After any other
// exit, or a stop signal, it returns the daemon's status.
func superviseDaemon(
	name, socket string,
	start func() (*exec.Cmd, error),
	changes <-chan struct{},
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
		restartRequested := false
	running:
		for {
			select {
			case <-exited:
				break running
			case signal := <-stop:
				_ = daemon.Process.Signal(signal)
				<-exited
				return daemonStatus(daemon.ProcessState)
			case <-changes:
				if restartRequested || !stale() {
					continue
				}
				restartRequested = true
				fmt.Printf("the PipeWire declaration changed, so %s at pid %d restarts to load it\n", name, daemon.Process.Pid)
				_ = daemon.Process.Signal(syscall.SIGTERM)
			}
		}
		if restartRequested {
			// The daemon exited because of the SIGTERM above.
			continue
		}
		if replaced != nil && replaced(before) {
			fmt.Printf("%s at pid %d exited because a new PipeWire started, so it starts again\n", name, daemon.Process.Pid)
			continue
		}
		fmt.Fprintf(os.Stderr, "%s at pid %d exited, so its container exits: %s\n", name, daemon.Process.Pid, daemon.ProcessState)
		return daemonStatus(daemon.ProcessState)
	}
}

// daemonStatus returns the status that a shell reports: the exit code,
// or 128 plus the number of the signal that ended the process.
func daemonStatus(state *os.ProcessState) int {
	if status, ok := state.Sys().(syscall.WaitStatus); ok && status.Signaled() {
		return 128 + int(status.Signal())
	}
	return state.ExitCode()
}

// pipewireIdentity identifies a PipeWire by the socket file it bound.
// The kernel can give a new socket the inode of the one it replaced, so
// the change time tells the two apart. The zero value means that no
// PipeWire accepts connections.
type pipewireIdentity struct {
	device, inode uint64
	changed       syscall.Timespec
}

// currentPipewire connects before it reads the file's attributes,
// because a socket file can remain after PipeWire exits.
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

// awaitPipewire returns false when PipeWire accepts no connection at
// socket within limit.
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
