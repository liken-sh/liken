package main

// The graph feed: one pw-dump -m for the life of the pod, in place of
// one pw-dump per pass.
//
// pw-dump -m prints the whole graph once, then one JSON array for
// every batch of changes, each element the whole changed object in
// the shape a plain pw-dump prints. A removed global prints as an
// object whose info is null. The stream carries only what changed, so
// the operator keeps its own copy of every object the stream has
// named and builds the graph again from all of them after each batch.
//
// That makes a steady state cost no process at all, and it makes
// every change in the graph a wake: a node that appears, a speaker
// whose own volume moved, a client that changed a gain. The one place
// the operator still polls is the startup wait at the bottom of this
// file, because a graph that is not there yet raises no event.

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

// pwMonitor is one running pw-dump -m. A graph arrives on the channel
// after every batch, and the channel closes when the stream ends,
// which is how the reconciler learns that the process is gone.
type pwMonitor struct {
	graphs chan pwGraph
	mu     sync.Mutex
	err    error
}

// Graphs is the stream. A receive is both the current graph and the
// wake, so the reconciler needs no second signal.
func (m *pwMonitor) Graphs() <-chan pwGraph {
	return m.graphs
}

// Err says why the stream ended. It answers once Graphs has closed,
// and it is what the reconciler logs before it starts the monitor
// again.
func (m *pwMonitor) Err() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.err
}

// stop records the reason before it closes the channel, so a reader
// that saw the close always finds the reason waiting.
func (m *pwMonitor) stop(err error) {
	m.mu.Lock()
	m.err = err
	m.mu.Unlock()
	close(m.graphs)
}

// watchGraph starts one pw-dump -m under the context. The context is
// what ends it on the way down. A pw-dump that ends on its own, which
// happens when PipeWire goes away, closes the channel with the reason
// in Err, and the reconciler starts another after a short wait.
func watchGraph(ctx context.Context) (*pwMonitor, error) {
	command := exec.CommandContext(ctx, "pw-dump", "-m")
	// The context's kill bounds the process, and WaitDelay bounds the
	// wait after the kill, for the reason readGraph gives.
	command.WaitDelay = time.Second
	stream, err := command.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("reading pw-dump -m: %w", err)
	}
	var complaints bytes.Buffer
	command.Stderr = &complaints
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
		monitor.stop(monitorError(decoded, command.Wait(), complaints.String()))
	}()
	return monitor, nil
}

// monitorError picks the reason the reconciler is told. A decode
// error names what the stream did wrong. Otherwise the exit and
// whatever pw-dump said on stderr are the reason, and pw-dump -m ends
// only when PipeWire goes away or the context does.
func monitorError(decoded, exit error, complaints string) error {
	if decoded != nil {
		return decoded
	}
	said := strings.TrimSpace(complaints)
	switch {
	case exit != nil && said != "":
		return fmt.Errorf("pw-dump -m stopped: %w: %s", exit, said)
	case exit != nil:
		return fmt.Errorf("pw-dump -m stopped: %w", exit)
	case said != "":
		return fmt.Errorf("pw-dump -m stopped: %s", said)
	}
	return errors.New("pw-dump -m stopped")
}

// decodeGraphs reads the stream: one JSON array for each batch of
// changes, the first being the whole graph, one after another until
// the process ends. A decoder on the stream reads them as they come,
// because the arrays are separated by newlines and not wrapped in an
// outer document.
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

// pwView is every object the stream has named, by id. A batch carries
// only what changed, and the graph is built from all of them.
type pwView struct {
	objects map[int]pwObject
}

// apply folds one batch into the view. An element with an info block
// replaces the object of that id, and an element whose info is null
// removes it, which is how pw-dump prints a global that went away. A
// global that never carried an info block holds nothing this graph
// reads, so dropping it is the same answer.
func (v *pwView) apply(batch []pwObject) {
	for _, object := range batch {
		if object.Info == nil {
			delete(v.objects, object.ID)
			continue
		}
		v.objects[object.ID] = object
	}
}

// graph builds the graph again from the whole view. buildGraph takes
// the objects in any order, because a map has none.
func (v *pwView) graph() pwGraph {
	objects := make([]pwObject, 0, len(v.objects))
	for _, object := range v.objects {
		objects = append(objects, object)
	}
	return buildGraph(objects)
}

// graphRetryDelay is how long the operator waits before it starts the
// graph feed again. A pw-dump -m ends when PipeWire goes away, and
// the container beside this one is what comes back, so the retry is
// short and has no ceiling.
const graphRetryDelay = 2 * time.Second

// graphFeed is the graph the operator holds now.
//
// It has two states. While the monitor runs, every pass reads the
// graph the last batch produced, at the cost of no process. While it
// does not, a pass runs a plain pw-dump instead, which is what the
// first pass does before the monitor has printed anything and what
// every pass does while the monitor is starting again. A pw-dump that
// fails is the failure the reconcile pass counts, so the monitor's
// absence changes nothing about how a dead PipeWire is reported.
type graphFeed struct {
	mutex sync.Mutex
	graph pwGraph
	held  bool

	// poll is the plain read the feed falls back to. It is a field so
	// that a test drives the feed with no PipeWire behind it.
	poll func(context.Context) (pwGraph, error)
}

func newGraphFeed() *graphFeed { return &graphFeed{poll: readGraph} }

// read answers with the newest graph the monitor delivered.
func (f *graphFeed) read(ctx context.Context) (pwGraph, error) {
	f.mutex.Lock()
	graph, held := f.graph, f.held
	f.mutex.Unlock()
	if held {
		return graph, nil
	}
	return f.poll(ctx)
}

func (f *graphFeed) deliver(graph pwGraph) {
	f.mutex.Lock()
	defer f.mutex.Unlock()
	f.graph, f.held = graph, true
}

// lost says the monitor ended, so the next pass reads PipeWire itself
// rather than answering from a graph that stopped moving.
func (f *graphFeed) lost() {
	f.mutex.Lock()
	defer f.mutex.Unlock()
	f.held = false
}

// follow keeps one pw-dump -m running for the life of the operator,
// and wakes the reconcile loop after every batch of changes.
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
		f.lost()
		if ctx.Err() != nil {
			return
		}
		fmt.Fprintf(os.Stderr, "the graph feed stopped: %v; starting it again in %s\n",
			err, graphRetryDelay)
		select {
		case <-ctx.Done():
			return
		case <-time.After(graphRetryDelay):
		}
	}
}

// pipewireReadyTimeout bounds the wait for PipeWire to answer at
// startup.
//
// A PipeWire that never answers within this window is a container
// crashlooping beside this one. The operator publishes every output
// tainted and exits, so the slice says nothing plays, and the
// kubelet's restart of this container is the retry.
const pipewireReadyTimeout = 60 * time.Second

// pipewireReadyInterval is how often the startup wait asks again.
// PipeWire raises no event that says it is ready, and the operator
// has no connection to it until it is, so this one wait polls. Every
// later read is driven by an event.
const pipewireReadyInterval = time.Second

// nodeReadyTimeout bounds the wait for the declared nodes to appear
// in the graph. PipeWire creates the objects its configuration
// declares while it loads that configuration, which is before it
// serves the socket, so the nodes are there on the first read or they
// are not coming.
const nodeReadyTimeout = 15 * time.Second

// waitForPipeWire blocks until PipeWire answers a graph read, or
// until the timeout passes.
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
			return fmt.Errorf("PipeWire did not answer within %s: %w", timeout, last)
		case <-tick.C:
		}
	}
}

// waitForNodes blocks until every endpoint has a node in the graph,
// or until the timeout passes. It reports what it found and never
// fails.
//
// An endpoint whose node PipeWire could not create is a fact the
// slice reports as the no-sink taint, so the operator publishes it
// rather than refusing to start. Failing here instead would restart
// the pod over one PCM device that cannot open, and take the card's
// working endpoints down with it on every attempt.
func waitForNodes(ctx context.Context, read func(context.Context) (pwGraph, error), outputs []alsaEndpoint, timeout time.Duration) {
	deadline := time.After(timeout)
	tick := time.NewTicker(pipewireReadyInterval)
	defer tick.Stop()
	var report string
	for {
		graph, err := read(ctx)
		if err != nil {
			report = fmt.Sprintf("PipeWire's graph did not read: %v", err)
		} else {
			missing := missingNodes(outputs, graph)
			if len(missing) == 0 {
				return
			}
			report = "PipeWire holds no node for " + strings.Join(missing, ", ")
		}
		select {
		case <-ctx.Done():
			return
		case <-deadline:
			fmt.Fprintf(os.Stderr, "%s after %s; those endpoints publish with the no-sink taint\n",
				report, timeout)
			return
		case <-tick.C:
		}
	}
}

// missingNodes names the endpoints that have no node, sorted by name.
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
