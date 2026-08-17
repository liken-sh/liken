package main

// The daemons the operator runs, and the coupling between their lives
// and its own.
//
// PipeWire owns the card, and it creates the sinks whose names this
// operator publishes, from the node declarations the operator writes
// before it starts them (see nodes.go). WirePlumber is the session
// manager beside it: it links each client's stream to the sink the
// client asks for. Neither belongs in the read-only root that every
// liken machine boots, so they ship in this image, and the operator
// starts them as its own children.
//
// A daemon that dies ends the container with a nonzero status. The
// operator holds the card's exclusive claim, and PipeWire is what
// makes the claim useful, so an operator that outlived PipeWire would
// publish outputs it can no longer play and hold the hardware
// away from a pod that could. The kubelet's restart is the repair.

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"slices"
	"strings"
	"time"
)

// runtimeDir is where PipeWire creates its socket. It is a hostPath
// mount, so that a consumer's CDI spec can bind the same directory
// into a container on the same node.
const runtimeDir = "/var/run/audio.liken.sh"

// socketPath is the absolute path of the socket a client connects to.
// A PIPEWIRE_REMOTE that starts with a slash is used as a path, and
// the runtime directory is not consulted, so one absolute path is the
// whole of what a consumer needs.
const socketPath = runtimeDir + "/pipewire-0"

// pipewireReadyTimeout bounds the wait for PipeWire to answer at
// startup. A PipeWire that never answers is a failure to report, and
// the pod's restart is the retry.
const pipewireReadyTimeout = 60 * time.Second

// pipewireReadyInterval is how often the startup wait asks again.
// PipeWire raises no event that says it is ready, and the operator
// has no connection to it until it is, so this one wait polls. Every
// later read is driven by an event.
const pipewireReadyInterval = time.Second

// nodeReadyTimeout bounds the wait for the declared sink nodes to
// appear in the graph. PipeWire creates the objects its configuration
// declares while it loads that configuration, which is before it
// serves the socket, so the nodes are there on the first read or they
// are not coming.
const nodeReadyTimeout = 15 * time.Second

// startDaemons starts PipeWire and WirePlumber and returns a channel
// that carries the first one to exit. Nothing is ever sent on that
// channel while both run.
func startDaemons(ctx context.Context) (<-chan error, error) {
	if err := os.MkdirAll(runtimeDir, 0o755); err != nil {
		return nil, fmt.Errorf("making the runtime directory: %w", err)
	}
	// PipeWire looks for its runtime directory in PIPEWIRE_RUNTIME_DIR
	// first, and the operator's own children and its pw-dump calls all
	// inherit the value from here.
	if err := os.Setenv("PIPEWIRE_RUNTIME_DIR", runtimeDir); err != nil {
		return nil, err
	}

	died := make(chan error, len(daemons))
	for _, d := range daemons {
		if err := start(ctx, d, died); err != nil {
			return nil, err
		}
	}
	return died, nil
}

// daemons are the two processes the operator runs, in the order it
// starts them.
//
// WirePlumber loads the profile this image names, and not its
// default. main-embedded is systemwide and keeps no state across
// restarts, which is the shape a pod needs, and the image's
// configuration turns off every hardware monitor, including the ALSA
// one that finds nothing without udev. See
// config/50-audio-operator.conf.
var daemons = []daemon{
	{name: "pipewire"},
	{name: "wireplumber", args: []string{"--profile=main-embedded"}},
}

type daemon struct {
	name string
	args []string
}

// start runs one daemon and reports its exit on died.
func start(ctx context.Context, d daemon, died chan<- error) error {
	command := exec.Command(d.name, d.args...)
	command.Stdout = os.Stderr
	command.Stderr = os.Stderr
	// Nothing here asks the kernel to kill the child when the parent
	// dies. The operator is the container's first process, so its exit
	// takes the whole PID namespace with it, and no daemon can outlive
	// it to hold the card open while the kubelet starts the
	// replacement pod. A parent-death signal would also name the
	// thread that forked the child rather than the process, and the Go
	// runtime retires a thread whenever it likes, which would kill the
	// daemons under a running operator.
	if err := command.Start(); err != nil {
		return fmt.Errorf("starting %s: %w", d.name, err)
	}
	go func() {
		err := command.Wait()
		if ctx.Err() != nil {
			// The operator is shutting down, and its own exit is what
			// ends the children.
			return
		}
		died <- fmt.Errorf("%s exited: %v", d.name, err)
	}()
	return nil
}

// waitForNodes blocks until every output has a sink in the graph, or
// until the timeout passes. It reports what it found and never fails.
//
// An output whose node PipeWire could not create is a fact the slice
// carries as the no-sink taint, so the operator publishes it rather
// than refusing to start. Failing here instead would restart the pod
// over one PCM device that cannot open, and take the card's working
// outputs down with it on every attempt.
func waitForNodes(ctx context.Context, read func(context.Context) (map[pcmAddress]string, error), outputs []alsaOutput, timeout time.Duration) {
	deadline := time.After(timeout)
	tick := time.NewTicker(pipewireReadyInterval)
	defer tick.Stop()
	var report string
	for {
		sinks, err := read(ctx)
		if err != nil {
			report = fmt.Sprintf("PipeWire's graph did not read: %v", err)
		} else {
			missing := missingNodes(outputs, sinks)
			if len(missing) == 0 {
				return
			}
			report = "PipeWire holds no sink for " + strings.Join(missing, ", ")
		}
		select {
		case <-ctx.Done():
			return
		case <-deadline:
			fmt.Fprintf(os.Stderr, "%s after %s; those outputs publish with the no-sink taint\n",
				report, timeout)
			return
		case <-tick.C:
		}
	}
}

// missingNodes names the outputs that have no sink, sorted by name.
func missingNodes(outputs []alsaOutput, sinks map[pcmAddress]string) []string {
	var missing []string
	for _, output := range outputs {
		if _, has := sinks[pcmAddress{Card: output.Card, PCM: output.PCM}]; !has {
			missing = append(missing, output.Name())
		}
	}
	slices.Sort(missing)
	return missing
}

// waitForPipeWire blocks until PipeWire answers a graph read, or
// until the timeout passes.
func waitForPipeWire(ctx context.Context, timeout time.Duration) error {
	deadline := time.After(timeout)
	tick := time.NewTicker(pipewireReadyInterval)
	defer tick.Stop()
	var last error
	for {
		_, err := readSinks(ctx)
		if err == nil {
			return nil
		}
		last = err
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline:
			return fmt.Errorf("PipeWire did not answer within %s: %w", timeout, last)
		case <-tick.C:
		}
	}
}
