// audio-operator publishes each physical audio output as its own DRA
// device, so that a pod claims one monitor's speakers or the analog
// jack and receives the PipeWire socket and the name of the sink its
// streams must reach.
//
// It is an instance of liken's device operator pattern. The operator
// claims the machine's audio controller through an ordinary liken.sh
// claim, reads the PipeWire that runs beside it in the same pod, and
// publishes what PipeWire holds under its own driver name,
// audio.liken.sh. The operator uses no private interface into liken:
// the raw claim, the slices it writes, and the CDI files it leaves
// for the runtime are the public contracts that any DRA driver on any
// Kubernetes cluster gets.
//
// The claim does two jobs that a person would otherwise write down.
// It places the pod, because only a machine that has an audio
// controller publishes one, so no node selector names the machine
// with the speakers. And it arbitrates, because liken publishes a
// controller as an exclusive device, so the claim holder is the only
// sound server on that card.
//
// Which PCM device plays into which monitor is the fact this operator
// exists to publish. It comes from the ELD block that the graphics
// driver writes into the audio driver, so only a running driver makes
// it true, and it changes whenever somebody moves a cable.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"
)

const (
	// settleWindow is how long the loop waits for quiet after the last
	// event before it writes. A monitor that a person plugs in
	// produces a burst of jack events, and PipeWire needs a moment
	// after them to build the sink, so the whole burst deserves one
	// write.
	//
	// Every ResourceSlice write wakes every DRA-pending pod in the
	// cluster, because the scheduler event that a slice change raises
	// carries no queueing hint. Hardware that flaps must not turn into
	// a cluster-wide scheduling storm.
	settleWindow = 1500 * time.Millisecond

	// settleLimit bounds the wait. A cable that somebody wiggles
	// restarts the quiet window forever, and the state it settles on
	// may never arrive, so the loop publishes what it can see at this
	// interval regardless.
	settleLimit = 10 * time.Second

	// backstopInterval is how often the loop reconciles with no event
	// to prompt it. A jack that changes while its node is closed for a
	// rescan costs one edge, and WirePlumber can rename a sink with no
	// jack event at all, so this tick is what recovers the state after
	// either one.
	backstopInterval = 60 * time.Second

	// maxSinkFailures is how many graph reads may fail in a row before
	// the operator gives up and exits.
	//
	// One failure skips the write, because a slice that tainted every
	// output would evict every consumer for a pw-dump that timed out
	// once. A run of them is a different state: the published slice
	// still says every output has a sink and carries no taint, while
	// every prepare call fails because the driver cannot read a sink
	// name, and nothing in the cluster bounds that. Three in a row make
	// it a restart, which is the one repair that reaches a PipeWire
	// that has stopped answering.
	maxSinkFailures = 3

	// writeRetryDelay is how long the loop waits before it repeats a
	// failed slice write. One retry covers the conflict that a
	// concurrent writer causes and the moment an API server takes to
	// come back. A failure that outlives it waits for the next event
	// or the backstop tick.
	writeRetryDelay = 2 * time.Second
)

// main selects the mode from the command line.
//
// The pod runs this one image four times. The declare init container
// passes the declare argument and writes PipeWire's node
// declarations, the PipeWire and WirePlumber containers name their
// own binary, and the operator container runs with no argument.
func main() {
	if len(os.Args) > 1 && os.Args[1] == declareMode {
		declare()
		return
	}
	operate()
}

func operate() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// The DaemonSet gives the pod its node's name through the downward
	// API. A ResourceSlice names the node whose hardware it describes,
	// and a pod cannot read that from anywhere else without asking the
	// API server which node it is on.
	nodeName := os.Getenv("NODE_NAME")
	if nodeName == "" {
		fatal("NODE_NAME is unset; the DaemonSet must supply it from spec.nodeName")
	}
	fmt.Printf("%s: operating the audio controller on %s\n", DriverName, nodeName)

	// Failures during setup end the process deliberately. This code
	// has no retry logic of its own, because the kubelet already
	// provides it: a pod that exits nonzero restarts with backoff, and
	// the failure shows in kubectl instead of hiding in a log.
	client, err := InClusterClient()
	if err != nil {
		fatal("in-cluster config: %v", err)
	}
	owner, err := NodeOwner(client, nodeName)
	if err != nil {
		fatal("reading node %s: %v", nodeName, err)
	}

	// The declaration the init container wrote, which is what PipeWire
	// built its graph from. See nodes.go.
	//
	// The operator cannot regenerate the file, because PipeWire has
	// already read it. A missing file is an init container that did
	// not run, so the process ends.
	declared, err := readNodeConfig()
	if err != nil {
		fatal("reading the declaration PipeWire loaded: %v", err)
	}

	operator := &reconciler{
		client:   client,
		nodeName: nodeName,
		owner:    owner,
		sinks:    readSinks,
		declared: declared,
	}

	if err := operator.awaitPipeWire(ctx, pipewireReadyTimeout); err != nil {
		fatal("waiting for PipeWire: %v", err)
	}

	outputs, err := readOutputs()
	if err != nil {
		fatal("reading the card's outputs: %v", err)
	}
	// PipeWire creates the declared nodes while it loads its
	// configuration, so they are there as soon as it answers. The wait
	// costs one graph read in that case, and it covers the case where
	// they are not: without it the first pass would taint every output,
	// and a NoExecute taint ends the pods that the previous operator's
	// prepared claims left running.
	waitForNodes(ctx, readSinks, outputs, nodeReadyTimeout)

	// The plugin registers with the kubelet only after PipeWire
	// answers, so the driver appears when it can actually answer a
	// prepare call.
	go func() {
		if err := serveDRAPlugin(ctx, client); err != nil {
			fatal("the DRA plugin is not serving: %v", err)
		}
	}()

	jacks, err := watchJacks(ctx)
	if err != nil {
		fatal("watching the jack nodes: %v", err)
	}

	settled := settle(ctx, wakes(ctx, jacks), settleWindow, settleLimit)

	// The first pass runs before any event, because the operator
	// starts with monitors already connected, and a restart must
	// republish what the previous pod published.
	if err := operator.pass(ctx); err != nil {
		fatal("%v", err)
	}
	if err := run(ctx, operator, settled); err != nil {
		fatal("%v", err)
	}
}

// run is the operator's loop. It returns nil when the process is
// shutting down, and an error when the operator must stop, which the
// caller turns into a nonzero exit for the kubelet to restart.
func run(ctx context.Context, operator *reconciler, settled <-chan struct{}) error {
	for {
		select {
		case <-ctx.Done():
			// The published slice stays. A consumer that already runs
			// keeps its socket mount, the next pod's prepare call reads
			// an allocation that still names these devices, and the Node
			// that owns the slice is what retracts it when the machine
			// leaves the cluster.
			return nil
		case _, ok := <-settled:
			if !ok {
				if ctx.Err() != nil {
					return nil
				}
				// The jack nodes and the backstop tick are the only
				// sources. A closed channel while the context is live
				// leaves the operator running with no way to notice a
				// monitor again, so it exits and the kubelet
				// restarts it.
				return errors.New("the event sources closed while running")
			}
			if err := operator.pass(ctx); err != nil {
				return err
			}
		}
	}
}

// reconciler holds what every pass needs, and the one piece of state
// that outlives a pass: how many graph reads have failed in a row.
//
// The graph read is a field rather than a call to readSinks, so a
// test drives the failure paths without a PipeWire to break.
type reconciler struct {
	client   *Client
	nodeName string
	owner    OwnerReference
	sinks    func(context.Context) (map[pcmAddress]string, error)

	// declared is the drop-in the init container wrote before PipeWire
	// started. PipeWire reads its configuration once, so this is what
	// the running graph was built from, and a pass that would generate
	// something else has found a card that no longer matches its own
	// sound server.
	declared string

	sinkFailures int

	// driftReported keeps the divergence report to one line. The
	// backstop tick runs every minute and the card does not change back.
	driftReported bool
}

// pass runs one reconcile and taints every output when the operator
// has to stop.
//
// This is the coupling contract. The daemons run in their own
// containers and the operator cannot end them, so when it loses the
// graph it publishes the fact instead: every output tainted, then a
// nonzero exit for the kubelet to restart. The slice must never say
// an output plays while nothing plays.
func (r *reconciler) pass(ctx context.Context) error {
	if err := r.reconcile(ctx); err != nil {
		r.taintEverything()
		return err
	}
	return nil
}

// awaitPipeWire waits for the container beside this one to serve its
// socket, and taints every output when it never does.
//
// A PipeWire that never answers leaves the previous pod's slice
// published, and that slice says every output plays. The taint is
// what ends the sessions of the consumers that slice still holds.
func (r *reconciler) awaitPipeWire(ctx context.Context, timeout time.Duration) error {
	if err := waitForPipeWire(ctx, r.sinks, timeout); err != nil {
		r.taintEverything()
		return err
	}
	return nil
}

// reconcile makes the published slice and every prepared CDI spec
// agree with what the card and PipeWire say right now. It returns an
// error only when the operator must stop, which is a PipeWire that
// has stopped answering.
//
// The order matters. The CDI refresh runs first, so that an output
// whose sink came back under a different name has a correct spec
// before the slice says the output is usable again.
//
// Two failures skip the write rather than publish what they found.
// An enumeration that returns no output is not a machine with no
// speakers, because this pod holds an exclusive claim on a card, and
// a graph read that fails is not a card with no sinks. Publishing
// either one would taint every output, evict every consumer, and in
// the empty case retract devices that prepared claims still name.
func (r *reconciler) reconcile(ctx context.Context) error {
	outputs, err := readOutputs()
	if err != nil {
		fmt.Fprintf(os.Stderr, "reading the card's outputs: %v\n", err)
		return nil
	}
	if len(outputs) == 0 {
		fmt.Fprintf(os.Stderr, "the claimed card has no playback PCM device; publishing nothing\n")
		return nil
	}
	// A PCM device that appeared or left since PipeWire started has no
	// node in the running graph, and PipeWire reads context.objects
	// only while it loads its configuration, so nothing this operator
	// does gives that output a sink. Only the init container writes
	// the declaration, and an init container runs once per pod, so an
	// operator restart would re-read the same file and find the same
	// divergence. The report goes out once, the pass continues, and
	// every output with no node publishes with the no-sink taint.
	// Deleting the pod is what declares the new set.
	if current := nodeConfig(outputs); current != r.declared && !r.driftReported {
		r.driftReported = true
		fmt.Fprintf(os.Stderr, "the card's playback PCM devices have changed since PipeWire started; "+
			"the outputs with no declared node publish with the no-sink taint until this pod is replaced\n")
	}
	sinks, err := r.sinks(ctx)
	if err != nil {
		r.sinkFailures++
		fmt.Fprintf(os.Stderr, "reading PipeWire's graph, %d in a row: %v\n", r.sinkFailures, err)
		if r.sinkFailures >= maxSinkFailures {
			return fmt.Errorf("PipeWire has not answered %d graph reads in a row: %w", r.sinkFailures, err)
		}
		return nil
	}
	r.sinkFailures = 0
	refreshCDISpecs(sinks)

	r.publish(ctx, sliceDevices(outputs, sinks))
	return nil
}

// taintEverything publishes the card's outputs with every one of them
// tainted.
//
// The operator publishes this form at the two moments when it holds
// no connection to PipeWire: at startup, when the socket never
// answers, and on the way out, when a run of graph reads has failed.
//
// It is best effort. The process is ending either way, so a failure
// here is reported and not retried, and the next pod republishes the
// truth when it starts.
func (r *reconciler) taintEverything() {
	outputs, err := readOutputs()
	if err != nil || len(outputs) == 0 {
		fmt.Fprintf(os.Stderr, "tainting the outputs on the way out: no outputs to taint: %v\n", err)
		return
	}
	// An empty set of sinks is what makes sliceDevices taint every
	// device, and an empty set is also the truth: the sound server that
	// held them is gone.
	if err := EnsureResourceSlice(r.client, r.nodeName, r.owner, sliceDevices(outputs, nil)); err != nil {
		fmt.Fprintf(os.Stderr, "tainting the outputs on the way out: %v\n", err)
	}
}

// publish writes the slice, with one retry.
func (r *reconciler) publish(ctx context.Context, devices []SliceDevice) {
	if len(devices) > maxSliceDevices {
		fmt.Fprintf(os.Stderr, "%d outputs exceed one slice's capacity of %d; dropping the overflow\n",
			len(devices), maxSliceDevices)
		devices = devices[:maxSliceDevices]
	}
	if err := EnsureResourceSlice(r.client, r.nodeName, r.owner, devices); err != nil {
		fmt.Fprintf(os.Stderr, "publishing the slice: %v\n", err)
		select {
		case <-ctx.Done():
			return
		case <-time.After(writeRetryDelay):
		}
		if err := EnsureResourceSlice(r.client, r.nodeName, r.owner, devices); err != nil {
			fmt.Fprintf(os.Stderr, "publishing the slice, second try: %v\n", err)
		}
	}
}

// wakes turns the jack events and the backstop tick into one channel.
// Neither carries state that the loop uses, so the merge loses
// nothing: both say to look again.
func wakes(ctx context.Context, jacks <-chan jackEvent) <-chan struct{} {
	out := make(chan struct{}, 1)
	wake := func() {
		select {
		case out <- struct{}{}:
		default:
		}
	}
	go func() {
		defer close(out)
		tick := time.NewTicker(backstopInterval)
		defer tick.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case event, ok := <-jacks:
				if !ok {
					return
				}
				fmt.Printf("jack: %s\n", event)
				wake()
			case <-tick.C:
				wake()
			}
		}
	}()
	return out
}

// settle collapses a burst of events into one wake. It emits after
// the input has been quiet for window, or after limit has passed
// since the first event of the burst, whichever comes first.
//
// The limit is what keeps a flapping cable publishing. Without it,
// hardware that changes faster than the quiet window would restart
// the wait on every event and the loop would never write.
func settle(ctx context.Context, in <-chan struct{}, window, limit time.Duration) <-chan struct{} {
	out := make(chan struct{}, 1)
	go func() {
		defer close(out)

		var quiet, deadline *time.Timer
		var quietC, deadlineC <-chan time.Time
		emit := func() {
			quiet.Stop()
			deadline.Stop()
			quiet, deadline = nil, nil
			quietC, deadlineC = nil, nil
			select {
			case out <- struct{}{}:
			default:
			}
		}
		for {
			select {
			case <-ctx.Done():
				return
			case _, ok := <-in:
				if !ok {
					return
				}
				if quiet == nil {
					quiet = time.NewTimer(window)
					deadline = time.NewTimer(limit)
					quietC, deadlineC = quiet.C, deadline.C
					continue
				}
				quiet.Stop()
				quiet.Reset(window)
			case <-quietC:
				emit()
			case <-deadlineC:
				emit()
			}
		}
	}()
	return out
}

func fatal(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}
