package main

// The reconcile pass.
//
// One pass reads the card, the paired speakers, and PipeWire's
// graph, and writes the slice that publishes what it found. Two
// failures skip the write rather than publish it: a card that would
// not read, and a graph that would not read for fewer passes than
// the coupling contract allows. Past that count the pass publishes
// every endpoint with the no-sink taint, because a PipeWire this
// operator cannot read is a PipeWire no consumer should be sent to.
// The endpoints' own resources are reconciled after the slice, from
// the same reads, so the two never disagree about a node's name.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"time"
)

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
				// The wake channel merges the jack nodes, bluetoothd,
				// the cards' own control devices, the graph feed, and
				// the backstop tick. A closed channel while the context
				// is live leaves the operator running with no way to
				// notice a change again, so it exits and the kubelet
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
// The graph read is a field rather than a call to readGraph, so a
// test drives the failure paths without a PipeWire to break.
type reconciler struct {
	client   *Client
	nodeName string
	owner    OwnerReference
	graph    func(context.Context) (pwGraph, error)

	// speakers reads bluetoothd's paired set. It is nil on a pod
	// whose claim delivered no media bus.
	speakers func() (map[string]speaker, error)

	// lastSpeakers is the newest paired set that read returned. A
	// bluetoothd that stops answering costs the speakers their sink
	// and their attributes, never their place in the slice:
	// membership is the paired set, and a read that fails says
	// nothing about who is paired.
	lastSpeakers map[string]speaker

	// speakerFailure keeps the report of a bluetoothd that does not
	// answer to one line for each run of failures.
	speakerFailure bool

	// declared is the drop-in the init container wrote before PipeWire
	// started. PipeWire reads its configuration once, so this is what
	// the running graph was built from, and a pass that would generate
	// something else has found a card that no longer matches its own
	// sound server.
	declared string

	// endpoints is what the last pass read from the hardware, keyed by
	// device name. The DRA plugin resolves a prepare call's device
	// name against it.
	endpoints *endpointInventory

	// cards holds one control-event reader for each card the inventory
	// holds, and every pass tells it which cards those are. It is nil
	// in a test, which drives a pass with no event source behind it.
	cards *cardWatchers

	// control reconciles the Sink and the Source of every endpoint.
	// It is nil in a test that reads the slice alone.
	control *endpointControl

	sinkFailures int

	// refusalReported keeps the report of an endpoint this operator
	// cannot name to one line for each run of passes, the way
	// driftReported does. A card whose identity sysfs does not carry
	// does not start carrying one.
	refusalReported bool

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
	if err := waitForPipeWire(ctx, r.graph, timeout); err != nil {
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
	outputs, err := readEndpoints()
	if err != nil {
		fmt.Fprintf(os.Stderr, "reading the card's outputs: %v\n", err)
		return nil
	}
	if len(outputs) == 0 {
		fmt.Fprintf(os.Stderr, "the claimed card has no playback PCM device; publishing nothing\n")
		return nil
	}
	// A PCM device that appeared or left since PipeWire started has no
	// node in the running graph. PipeWire reads context.objects only
	// while it loads its configuration, so nothing this operator does
	// gives that output a sink. Only the init container writes the
	// declaration, and an init container runs once per pod, so an
	// operator restart would re-read the same file and find the same
	// divergence. The report goes out once, the pass continues, and
	// every output with no node publishes with the no-sink taint.
	// Deleting the pod is what declares the new set.
	if current := nodeConfig(outputs); current != r.declared && !r.driftReported {
		r.driftReported = true
		fmt.Fprintf(os.Stderr, "the card's playback PCM devices have changed since PipeWire started; "+
			"the outputs with no declared node publish with the no-sink taint until this pod is replaced\n")
	}
	graph, err := r.graph(ctx)
	if err != nil {
		r.sinkFailures++
		fmt.Fprintf(os.Stderr, "reading PipeWire's graph, %d in a row: %v\n", r.sinkFailures, err)
		if r.sinkFailures >= maxSinkFailures {
			return fmt.Errorf("PipeWire has not answered %d graph reads in a row: %w", r.sinkFailures, err)
		}
		return nil
	}
	r.sinkFailures = 0

	// The device name is built from the hardware's identity, so an
	// endpoint whose identity this operator cannot read reaches no
	// slice and no claim. The inventory holds what was named, because
	// that is the set a prepare call can name.
	endpoints, refused := nameEndpoints(r.nodeName, outputs)
	r.reportRefusals(refused)
	r.endpoints.publish(endpoints)
	refreshCDISpecs(r.endpoints, graph)
	// The event readers follow the inventory, so a card that arrived
	// this pass reports its own changes from here on.
	r.cards.follow(cardNumbers(endpoints))

	speakers := r.pairedSpeakers()
	r.publish(ctx, sliceDevices(endpoints, speakers, graph))

	// The resources come after the slice, because a claim is what
	// places a workload and the resource is what a person reads. A
	// failure here is reported and the pass stands: an API server that
	// refuses a status write says nothing about whether the card
	// plays.
	if r.control != nil {
		if err := r.control.pass(ctx, endpoints, speakers, graph); err != nil {
			fmt.Fprintf(os.Stderr, "reconciling the endpoints' resources: %v\n", err)
		}
	}
	return nil
}

// cardNumbers names the cards an inventory holds, in order and once
// each.
func cardNumbers(endpoints []alsaEndpoint) []int {
	cards := make([]int, 0, len(endpoints))
	for _, endpoint := range endpoints {
		if !slices.Contains(cards, endpoint.Card) {
			cards = append(cards, endpoint.Card)
		}
	}
	slices.Sort(cards)
	return cards
}

// reportRefusals names the endpoints this pass could not name, once
// for each run of passes that finds them.
func (r *reconciler) reportRefusals(refused []string) {
	if len(refused) == 0 {
		r.refusalReported = false
		return
	}
	if r.refusalReported {
		return
	}
	r.refusalReported = true
	fmt.Fprintf(os.Stderr, "%d endpoint(s) publish nothing, because this operator cannot name them: %s\n",
		len(refused), strings.Join(refused, "; "))
}

// pairedSpeakers reads bluetoothd's paired set, and gives back the
// newest set it read when bluetoothd does not answer.
//
// A failed read must not shrink the slice. Unpairing is the one
// thing that removes a speaker, and a bus that went away is not an
// unpairing. The speakers keep their place, their sink is gone from
// the graph, and the taints say they cannot play.
func (r *reconciler) pairedSpeakers() map[string]speaker {
	if r.speakers == nil {
		return nil
	}
	speakers, err := r.speakers()
	if err != nil {
		if !r.speakerFailure {
			r.speakerFailure = true
			fmt.Fprintf(os.Stderr, "reading bluetoothd's paired set: %v; "+
				"the %d speaker(s) it last reported publish tainted\n", err, len(r.lastSpeakers))
		}
		return r.lastSpeakers
	}
	if r.speakerFailure {
		r.speakerFailure = false
		fmt.Fprintf(os.Stderr, "bluetoothd answers again with %d speaker(s)\n", len(speakers))
	}
	r.lastSpeakers = speakers
	return speakers
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
	outputs, err := readEndpoints()
	if err != nil || len(outputs) == 0 {
		fmt.Fprintf(os.Stderr, "tainting the outputs on the way out: no outputs to taint: %v\n", err)
		return
	}
	// An empty set of sinks is what makes sliceDevices taint every
	// device, and an empty set is also the truth: the sound server that
	// held them is gone.
	//
	// The paired speakers stay in the list, tainted with the rest,
	// because the sound server is what ended and the pairings did
	// not.
	endpoints, _ := nameEndpoints(r.nodeName, outputs)
	devices := sliceDevices(endpoints, r.pairedSpeakers(), pwGraph{})
	if err := EnsureResourceSlice(r.client, r.nodeName, r.owner, devices); err != nil {
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
