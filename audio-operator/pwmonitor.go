package main

// The graph feed keeps the operator's copy of the PipeWire graph
// current from a pw-dump -m that runs for the life of the operator, so
// a pass starts no process, and every change in the graph wakes the
// reconcile loop.
//
// pw-dump -m prints the whole graph once, then a JSON array for each
// batch of changes. Each element is a changed object in the shape that
// plain pw-dump prints, and a removed global is an object whose info is
// null. A batch contains only what changed, so the feed keeps every
// object that the stream sent and builds the graph from all of them
// after each batch.
//
// The startup waits at the bottom of this file read on a timer, because
// a PipeWire that has not started yet raises no event.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"slices"
	"strings"
	"sync"
	"time"
)

// pwMonitor is one running pw-dump -m. Its channel receives the whole
// graph after every batch, and closes when the process ends.
type pwMonitor struct {
	graphs chan pwGraph
	mu     sync.Mutex
	err    error
}

func (m *pwMonitor) Graphs() <-chan pwGraph {
	return m.graphs
}

// Err returns why the stream ended, after Graphs closes.
func (m *pwMonitor) Err() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.err
}

// stop sets the error before it closes the channel, so a reader that
// sees the close always finds the error.
func (m *pwMonitor) stop(err error) {
	m.mu.Lock()
	m.err = err
	m.mu.Unlock()
	close(m.graphs)
}

// watchGraph starts pw-dump -m. Canceling ctx kills the process. When
// pw-dump exits on its own, as it does when PipeWire stops, the channel
// closes with the reason in Err, and the caller starts a new monitor.
func watchGraph(ctx context.Context) (*pwMonitor, error) {
	command := exec.CommandContext(ctx, "pw-dump", "-m")
	// WaitDelay keeps command.Wait from blocking after the kill when
	// another process inherited the stdout pipe, as in readGraph.
	command.WaitDelay = time.Second
	stream, err := command.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("connecting to the output of pw-dump -m: %w", err)
	}
	var stderr bytes.Buffer
	command.Stderr = &stderr
	if err := command.Start(); err != nil {
		return nil, fmt.Errorf("starting pw-dump -m: %w", err)
	}

	monitor := &pwMonitor{graphs: make(chan pwGraph)}
	go func() {
		decoded := decodeGraphs(stream, func(graph pwGraph) error {
			select {
			case monitor.graphs <- graph:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		})
		monitor.stop(monitorError(decoded, command.Wait(), stderr.String()))
	}()
	return monitor, nil
}

// monitorError prefers a decode error, because it describes what was
// wrong with the stream. Otherwise it combines the exit error with
// pw-dump's stderr text.
func monitorError(decoded, exit error, stderr string) error {
	if decoded != nil {
		return decoded
	}
	text := strings.TrimSpace(stderr)
	switch {
	case exit != nil && text != "":
		return fmt.Errorf("pw-dump -m stopped: %w: %s", exit, text)
	case exit != nil:
		return fmt.Errorf("pw-dump -m stopped: %w", exit)
	case text != "":
		return fmt.Errorf("pw-dump -m stopped: %s", text)
	}
	return errors.New("pw-dump -m stopped")
}

// decodeGraphs calls deliver with the whole graph after each JSON array
// that pw-dump -m prints. It returns nil when the stream ends, and
// otherwise the error of the decoder or of deliver. pw-dump -m prints
// the arrays one after another with no outer array, so a json.Decoder
// reads each one as it arrives.
func decodeGraphs(stream io.Reader, deliver func(pwGraph) error) error {
	decoder := json.NewDecoder(stream)
	view := pwView{objects: map[int]pwObject{}}
	for {
		var batch []pwObject
		if err := decoder.Decode(&batch); err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return fmt.Errorf("reading pw-dump -m's output: %w", err)
		}
		view.apply(batch)
		if err := deliver(view.graph()); err != nil {
			return err
		}
	}
}

// pwView is the newest copy of every object that the stream has sent,
// by id.
type pwView struct {
	objects map[int]pwObject
}

// apply deletes each object whose info is null, which is how pw-dump
// prints a removed global. An object that arrives with no info for
// another reason is deleted too, which loses nothing, because
// buildGraph skips every object with no info.
func (v *pwView) apply(batch []pwObject) {
	for _, object := range batch {
		if object.Info == nil {
			delete(v.objects, object.ID)
			continue
		}
		v.objects[object.ID] = object
	}
}

// graph passes the objects to buildGraph in map order, which buildGraph
// accepts.
func (v *pwView) graph() pwGraph {
	objects := make([]pwObject, 0, len(v.objects))
	for _, object := range v.objects {
		objects = append(objects, object)
	}
	return buildGraph(objects)
}

// graphRetryDelay is short and does not back off, because pw-dump -m
// ends when PipeWire stops, and Kubernetes restarts PipeWire's
// container beside this one.
const graphRetryDelay = 2 * time.Second

// graphFeed keeps the newest graph that the monitor delivered.
//
// When the feed has no graph from a running monitor, as on the first
// pass and while the monitor restarts, a pass runs a plain pw-dump. A
// failed pw-dump fails the pass, so a PipeWire that is down is reported
// the same way whether or not the monitor runs.
type graphFeed struct {
	mutex    sync.Mutex
	graph    pwGraph
	hasGraph bool

	// nextChange is closed and set to nil when a graph arrives or the
	// monitor ends. The next call to changes makes a new one.
	nextChange chan struct{}

	// poll is a field so that a test can drive the feed without
	// PipeWire.
	poll func(context.Context) (pwGraph, error)
}

func newGraphFeed() *graphFeed { return &graphFeed{poll: readGraph} }

func (f *graphFeed) read(ctx context.Context) (pwGraph, error) {
	f.mutex.Lock()
	graph, hasGraph := f.graph, f.hasGraph
	f.mutex.Unlock()
	if hasGraph {
		return graph, nil
	}
	return f.poll(ctx)
}

// changes returns a channel that closes when the next graph arrives, or
// when the monitor ends and the next read runs a plain pw-dump. A
// caller that waits for a change gets the channel before it reads the
// graph, as codecSwitch.await does, so a batch that arrives after the
// read still closes the caller's channel.
func (f *graphFeed) changes() <-chan struct{} {
	f.mutex.Lock()
	defer f.mutex.Unlock()
	if f.nextChange == nil {
		f.nextChange = make(chan struct{})
	}
	return f.nextChange
}

// The caller must hold f.mutex.
func (f *graphFeed) closeChanged() {
	if f.nextChange != nil {
		close(f.nextChange)
		f.nextChange = nil
	}
}

func (f *graphFeed) deliver(graph pwGraph) {
	f.mutex.Lock()
	defer f.mutex.Unlock()
	f.graph, f.hasGraph = graph, true
	f.closeChanged()
}

// monitorEnded discards the monitor's graph, because it stopped
// updating. It also closes the channel from changes, so each waiter
// runs a plain pw-dump once and does not wait until the monitor
// restarts.
func (f *graphFeed) monitorEnded() {
	f.mutex.Lock()
	defer f.mutex.Unlock()
	f.hasGraph = false
	f.closeChanged()
}

// follow runs pw-dump -m until ctx ends, starts it again each time it
// exits, and calls wake after every batch.
func (f *graphFeed) follow(ctx context.Context, wake func()) {
	for ctx.Err() == nil {
		monitor, err := watchGraph(ctx)
		if err == nil {
			for graph := range monitor.Graphs() {
				f.deliver(graph)
				wake()
			}
			err = monitor.Err()
		}
		f.monitorEnded()
		if ctx.Err() != nil {
			return
		}
		fmt.Fprintf(os.Stderr, "the graph feed stopped, and starts again in %s: %v\n",
			graphRetryDelay, err)
		select {
		case <-ctx.Done():
			return
		case <-time.After(graphRetryDelay):
		}
	}
}

// pipewireReadyTimeout bounds the startup wait for a graph read to
// succeed. When it passes, the operator publishes every output with a
// taint and exits, so the ResourceSlice shows that nothing can play.
// The kubelet's restart of the container is the retry, and a PipeWire
// that stays down leaves this pod in a crash loop.
const pipewireReadyTimeout = 60 * time.Second

// pipewireReadyInterval is how often the startup waits read the graph
// again. They poll because PipeWire raises no event when it is ready,
// and the graph feed has not started yet.
const pipewireReadyInterval = time.Second

// nodeReadyTimeout bounds the startup wait for the declared nodes.
// PipeWire creates them while it loads its configuration, before it
// serves its socket, so they are normally in the graph at the first
// successful read.
const nodeReadyTimeout = 15 * time.Second

// waitForPipeWire returns an error that wraps the last failed read when
// the timeout passes.
func waitForPipeWire(ctx context.Context, read func(context.Context) (pwGraph, error), timeout time.Duration) error {
	deadline := time.After(timeout)
	tick := time.NewTicker(pipewireReadyInterval)
	defer tick.Stop()
	var last error
	for {
		_, err := read(ctx)
		if err == nil {
			return nil
		}
		last = err
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline:
			return fmt.Errorf("PipeWire did not respond within %s: %w", timeout, last)
		case <-tick.C:
		}
	}
}

// waitForNodes reads the graph until every endpoint has a node or the
// timeout passes. On a timeout it logs what is missing and returns, and
// each endpoint with no node is published with the no-sink taint. An
// error would restart the pod over one PCM device that cannot open, and
// take the card's working endpoints down with it on every attempt.
func waitForNodes(ctx context.Context, read func(context.Context) (pwGraph, error), outputs []alsaEndpoint, timeout time.Duration) {
	deadline := time.After(timeout)
	tick := time.NewTicker(pipewireReadyInterval)
	defer tick.Stop()
	var report string
	for {
		graph, err := read(ctx)
		if err != nil {
			report = fmt.Sprintf("reading PipeWire's graph: %v", err)
		} else {
			missing := missingNodes(outputs, graph)
			if len(missing) == 0 {
				return
			}
			report = "PipeWire has no node for " + strings.Join(missing, ", ")
		}
		select {
		case <-ctx.Done():
			return
		case <-deadline:
			fmt.Fprintf(os.Stderr, "waited %s for the declared nodes, so each endpoint with no node publishes with the no-sink taint: %s\n",
				timeout, report)
			return
		case <-tick.C:
		}
	}
}

func missingNodes(outputs []alsaEndpoint, graph pwGraph) []string {
	var missing []string
	for _, output := range outputs {
		if _, has := graph.Nodes[output.graphAddress()]; !has {
			missing = append(missing, output.Name())
		}
	}
	slices.Sort(missing)
	return missing
}
