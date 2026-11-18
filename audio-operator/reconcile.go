package main

// A reconcile pass reads the card's PCM devices, bluetoothd's paired
// speakers, and PipeWire's graph, and writes this machine's
// ResourceSlice from them. Then it writes the status of each Sink and
// Source from the same reads, so the slice and the status name the same
// PipeWire node for each endpoint.

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/liken-sh/liken/kubernetes/apiclient"
)

// run starts a pass on each wake from settled, and applies each new
// status.session.volumeAsk as it arrives. It returns nil when ctx ends,
// and an error when the operator must exit so that the kubelet restarts
// its container.
//
// In main.go, wakes merges the event sources into one channel, and
// settle turns each burst of events on it into one wake on settled.
func run(ctx context.Context, operator *reconciler, settled <-chan struct{}) error {
	for {
		select {
		case <-ctx.Done():
			// The ResourceSlice is not deleted at shutdown, so running
			// pods keep their PipeWire socket mounts, and the next
			// operator's prepare calls find the devices that their
			// allocations name. The slice has an owner reference to the
			// Node, so the garbage collector deletes it with the Node.
			return nil
		case _, ok := <-settled:
			if !ok {
				if ctx.Err() != nil {
					return nil
				}
				// settled closes when the bluetoothd event channel or the
				// card control-event channel closes. While ctx is live,
				// no pass would run again, so the operator exits and the
				// kubelet restarts it. A restart of the Bluetooth
				// operator's pod does not close the bluetoothd channel,
				// because followBus connects to the new D-Bus socket.
				return errors.New("the event sources closed while running")
			}
			if err := operator.pass(ctx); err != nil {
				return err
			}
		case <-operator.volumeAsks:
			// applyAsks writes each new level at once, without waiting
			// for settled, because a level write does not change the
			// ResourceSlice. requestPass then queues a pass, which
			// writes the level from PipeWire's graph into the Sink's
			// status. A PipeWire node that is not playing reports no
			// change in the graph, so without requestPass that pass
			// would wait for an unrelated event.
			if operator.control.applyAsks(ctx) && operator.requestPass != nil {
				operator.requestPass()
			}
		}
	}
}

type reconciler struct {
	client   *apiclient.Client
	nodeName string
	owner    OwnerReference

	// graph is the graph feed's read, or a test's fake of it.
	graph func(context.Context) (pwGraph, error)

	// speakers reads bluetoothd's paired speakers over D-Bus. It is nil
	// when the pod's claim includes no Bluetooth media bus.
	speakers     func() (map[string]speaker, error)
	lastSpeakers map[string]speaker

	// speakerFailure is true while reads of bluetoothd fail, so that
	// each run of failures is logged and posted once.
	speakerFailure bool

	// declared is the text of the PipeWire drop-in that declares a
	// PipeWire node for each PCM device. The init container writes it
	// before PipeWire starts, and the operator rewrites it for a layout
	// change.
	declared string

	// declarationStale reports whether the drop-in was written after
	// the running PipeWire started, which is true between a layout write
	// and the PipeWire restart that applies it. It is nil in a test.
	declarationStale func() bool

	// restartRequested is when the operator last wrote a layout, or the
	// zero time when it has written none. reconcile does not count a
	// graph read that fails within layoutRestartGrace of it, because
	// PipeWire restarts for the new layout.
	restartRequested time.Time

	// layoutReport is the last line that the layout step logged, so the
	// same line is not logged again on every pass.
	layoutReport string

	// layoutWriteFailure is the last error from a write of the drop-in,
	// so that each new error is logged once, and posted once on each
	// Sink whose layout waits for the write.
	layoutWriteFailure string

	// The DRA plugin looks up the device name of each prepare call in
	// endpoints, which the last pass filled.
	endpoints *endpointInventory

	// cards runs a control-event reader for each card. It is nil in a
	// test.
	cards *cardWatchers

	// control writes the status of each endpoint's Sink and Source. It
	// is nil in a test that checks only the slice.
	control *endpointControl

	// readShares reads the share count that each endpoint's Sink or
	// Source declares, and lastShares is the result of the last read
	// that succeeded. readShares is nil in a test with no shares.
	readShares func() (map[string]int, error)
	lastShares map[string]int

	// volumeAsks receives a wake when a Sink of this machine has a new
	// status.session.volumeAsk, and requestPass queues a pass through
	// settled. Both are nil in a test that sends no volume request.
	volumeAsks  <-chan struct{}
	requestPass func()

	// readings is the registry that the metrics listener serves. A nil
	// registry discards every call.
	readings *metrics

	// failedGraphReads counts the graph reads that failed in a row.
	failedGraphReads int

	// namingFailureReported is true while nameEndpoints cannot name an
	// endpoint, so the failure is logged once for each run of passes. A
	// card with no identity in sysfs does not get one later, so a line
	// on every pass would repeat the same failure.
	namingFailureReported bool

	// driftReported is true after the mismatch between the card's PCM
	// devices and the drop-in has been logged. The mismatch lasts until
	// the pod is replaced, so without the flag every pass would log it
	// again.
	driftReported bool

	// monitors hides short hotplug drops on HDMI and DisplayPort
	// outputs. When monitors is nil, apply returns the reads unchanged.
	monitors *monitorHold

	// cardRead replaces readEndpoints in a test, so that the test can
	// give an endpoint an ELD. The operator reads an ELD with an ioctl
	// on the card's control device, which a test cannot create.
	cardRead func() ([]alsaEndpoint, error)
}

func (r *reconciler) readCard() ([]alsaEndpoint, error) {
	if r.cardRead != nil {
		return r.cardRead()
	}
	return readEndpoints()
}

// pass runs reconcile. When reconcile returns an error, which happens
// after maxFailedGraphReads failed graph reads in a row, pass taints
// every device and returns the error, and main exits. PipeWire runs in
// its own container, which the operator cannot stop, so the taints keep
// the slice from listing an output as playable while PipeWire does not
// respond.
func (r *reconciler) pass(ctx context.Context) error {
	if err := r.reconcile(ctx); err != nil {
		r.taintEverything()
		return err
	}
	return nil
}

// awaitPipeWire waits for the PipeWire container to serve its socket,
// and taints every device when it does not within timeout. Until this
// operator writes, the slice is the one that the previous operator
// wrote, and it lists every output as playable. The NoExecute taint
// evicts the pods whose claims still name those devices.
func (r *reconciler) awaitPipeWire(ctx context.Context, timeout time.Duration) error {
	if err := waitForPipeWire(ctx, r.graph, timeout); err != nil {
		r.taintEverything()
		return err
	}
	return nil
}

// reconcile writes the ResourceSlice, and refreshes every prepared CDI
// spec, from the current reads of the card and PipeWire. It returns an
// error only when maxFailedGraphReads graph reads have failed in a row.
//
// The CDI specs are refreshed before the slice is written. So when a
// PipeWire sink comes back under a different node name, the spec names
// the new node before the slice removes the output's taint.
//
// A pass writes no slice when the card read fails or returns no PCM
// device, or when the graph read fails. This pod runs only with an
// exclusive claim on the card, so an empty card read is a failed read.
// A slice written from it would delete every device, including the
// devices that prepared claims still name. A failed graph read does not
// show that PipeWire has lost its sinks, and a slice written after one
// would taint every device and evict every pod that uses one.
func (r *reconciler) reconcile(ctx context.Context) error {
	// The control-event readers start before the card read, so a change
	// during the read wakes another pass. follow also starts a reader
	// for a card that appeared, and for a card whose reader stopped.
	r.cards.follow(controlCards())
	outputs, err := r.readCard()
	if err != nil {
		fmt.Fprintf(os.Stderr, "reading the card's outputs: %v\n", err)
		return nil
	}
	if len(outputs) == 0 {
		fmt.Fprintf(os.Stderr, "the claimed cards have no PCM device, so the pass publishes nothing\n")
		return nil
	}
	// PipeWire reads context.objects only when it loads its
	// configuration, so its nodes do not follow a PCM device that was
	// added or removed since then. Only the init container of a new pod
	// writes a new set of PCM devices into the drop-in. So the pass logs
	// the mismatch once, and each output with no PipeWire node is
	// published with the no-sink taint.
	if nodes, err := parseDeclaration(r.declared); (err != nil || !samePCMDevices(nodes, outputs)) && !r.driftReported {
		r.driftReported = true
		fmt.Fprintf(os.Stderr, "the card's PCM devices have changed since PipeWire started, so "+
			"each endpoint with no declared node publishes with the no-sink taint until this pod is replaced\n")
	}
	graph, err := r.graph(ctx)
	if err != nil && r.awaitingRestart() {
		// The PipeWire container is restarting for a layout that this
		// operator wrote, so the failure does not count toward
		// maxFailedGraphReads. The pass writes nothing, so no output is
		// tainted during the restart.
		fmt.Fprintf(os.Stderr, "reading PipeWire's graph while it restarts for a layout change: %v\n", err)
		return nil
	}
	if err != nil {
		r.failedGraphReads++
		r.readings.observationFailed(sourcePipeWire)
		fmt.Fprintf(os.Stderr, "reading PipeWire's graph, %d in a row: %v\n", r.failedGraphReads, err)
		// The Warning is posted on the first failure of a run, because
		// the last failure ends the process, and an Event that is still
		// queued when the process exits is lost.
		if r.failedGraphReads == 1 {
			r.control.warnSinks(r.sinkNames(), reasonPipeWireLost, fmt.Sprintf(
				"PipeWire does not respond to a graph read, and after %d failed reads in a row the operator taints every output and restarts: %v",
				maxFailedGraphReads, err))
		}
		if r.failedGraphReads >= maxFailedGraphReads {
			return fmt.Errorf("PipeWire did not respond to %d graph reads in a row: %w", r.failedGraphReads, err)
		}
		return nil
	}
	if r.failedGraphReads > 0 {
		r.control.noteSinks(r.sinkNames(), reasonPipeWireRecovered, fmt.Sprintf(
			"PipeWire responds to a graph read again, after %d failed reads", r.failedGraphReads))
	}
	r.failedGraphReads = 0
	r.readings.observationSucceeded(sourcePipeWire)

	// Each device name comes from the hardware's identity, so an
	// endpoint whose identity cannot be read is left out of the slice
	// and of the inventory. monitors.apply runs on the named endpoints,
	// because the ELDs that it reads from the previous operator's slice
	// are keyed by device name.
	endpoints, unnamed := nameEndpoints(r.nodeName, outputs)
	endpoints = r.monitors.apply(endpoints)
	r.reportNamingFailures(unnamed)
	r.endpoints.publish(endpoints)
	refreshCDISpecs(r.endpoints, graph)

	speakers := r.pairedSpeakers()
	r.publish(ctx, shareDevices(sliceDevices(endpoints, speakers, graph), r.shares()))

	// A layout change restarts PipeWire, so it runs after the slice
	// write, and the slice describes the graph that this pass read.
	layouts := r.reconcileLayouts(endpoints, graph)

	// The Sink and Source status is written after the slice, because
	// the scheduler places workloads from the slice, and the status is
	// for people to read. A failed status write is only logged, because
	// the failure comes from the API server and not from PipeWire, and
	// an error here would taint every device and make the operator exit.
	if r.control != nil {
		if err := r.control.pass(ctx, endpoints, speakers, graph, layouts); err != nil {
			fmt.Fprintf(os.Stderr, "reconciling the endpoints' resources: %v\n", err)
		}
	}
	return nil
}

func (r *reconciler) reportNamingFailures(unnamed []string) {
	if len(unnamed) == 0 {
		r.namingFailureReported = false
		return
	}
	if r.namingFailureReported {
		return
	}
	r.namingFailureReported = true
	fmt.Fprintf(os.Stderr, "%d endpoint(s) publish nothing, because this operator cannot name them: %s\n",
		len(unnamed), strings.Join(unnamed, "; "))
}

// pairedSpeakers returns bluetoothd's paired speakers, or lastSpeakers
// when the read fails. Only an unpairing removes a speaker from the
// slice, and a lost connection to bluetoothd is not an unpairing. The
// speakers stay in the slice, and sliceDevices taints each one that has
// no sink in PipeWire's graph.
func (r *reconciler) pairedSpeakers() map[string]speaker {
	if r.speakers == nil {
		return nil
	}
	speakers, err := r.speakers()
	if err != nil {
		r.readings.observationFailed(sourceBlueZ)
		if !r.speakerFailure {
			r.speakerFailure = true
			fmt.Fprintf(os.Stderr, "bluetoothd's paired set is unreadable, so "+
				"the %d speaker(s) it last reported publish tainted: %v\n", len(r.lastSpeakers), err)
			r.control.warnSinks(speakerSinks(r.lastSpeakers), reasonBluetoothUnavailable, fmt.Sprintf(
				"bluetoothd does not respond to a read of the paired speakers, so the speaker publishes tainted until it responds: %v", err))
		}
		return r.lastSpeakers
	}
	r.readings.observationSucceeded(sourceBlueZ)
	if r.speakerFailure {
		r.speakerFailure = false
		fmt.Fprintf(os.Stderr, "bluetoothd responds again, with %d speaker(s)\n", len(speakers))
		r.control.noteSinks(speakerSinks(speakers), reasonBluetoothAvailable,
			"bluetoothd responds to a read of the paired speakers again")
	}
	r.lastSpeakers = speakers
	return speakers
}

func (r *reconciler) sinkNames() []string {
	names := r.endpoints.sinkNames()
	return append(names, speakerSinks(r.lastSpeakers)...)
}

func speakerSinks(speakers map[string]speaker) []string {
	names := make([]string, 0, len(speakers))
	for _, address := range slices.Sorted(maps.Keys(speakers)) {
		names = append(names, speakerName(address))
	}
	return names
}

// taintEverything publishes every endpoint of the card and every
// paired speaker with the disconnected and no-sink taints, before the
// operator exits because PipeWire does not respond. The NoExecute
// taint evicts the pods whose claims name these devices. A failed
// write is logged and not retried, because the process exits, and the
// next operator process writes the slice when it starts.
func (r *reconciler) taintEverything() {
	outputs, err := readEndpoints()
	if err != nil || len(outputs) == 0 {
		fmt.Fprintf(os.Stderr, "tainting the endpoints before the operator exits: no endpoints to taint: %v\n", err)
		return
	}
	// An empty graph has no PipeWire nodes, so sliceDevices taints every
	// device. The paired speakers and the last share counts stay in the
	// slice, because only PipeWire failed.
	endpoints, _ := nameEndpoints(r.nodeName, outputs)
	devices := shareDevices(sliceDevices(endpoints, r.pairedSpeakers(), pwGraph{}), r.lastShares)
	if err := EnsureResourceSlice(r.client, r.nodeName, r.owner, devices); err != nil {
		fmt.Fprintf(os.Stderr, "tainting the endpoints before the operator exits: %v\n", err)
	}
}

func (r *reconciler) publish(ctx context.Context, devices []SliceDevice) {
	if len(devices) > maxSliceDevices {
		fmt.Fprintf(os.Stderr, "%d outputs exceed one slice's capacity of %d, so the slice leaves out the overflow\n",
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
