// audio-operator publishes each physical audio output as its own DRA
// device, so that a pod claims one monitor's speakers or the analog
// jack and receives the PipeWire socket and the name of the sink its
// streams must reach.
//
// It is an instance of liken's device operator pattern. The operator
// claims the machine's audio controller through an ordinary liken.sh
// claim, reads the PipeWire that runs beside it in the same pod, and
// publishes what PipeWire holds under its own driver name,
// audio.liken.sh. The operator uses no private interface into liken.
// The raw claim, the slices it writes, and the CDI files it leaves
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
// it true. It changes whenever somebody moves a cable.
package main

import (
	"context"
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
	// after them to build the sink, so one write must cover the whole
	// burst.
	//
	// Every ResourceSlice write wakes every DRA-pending pod in the
	// cluster, because the scheduler event that a slice change raises
	// includes no queueing hint. Hardware that flaps must not turn into
	// a cluster-wide scheduling storm.
	settleWindow = 1500 * time.Millisecond

	// settleLimit bounds the wait. A cable that somebody wiggles
	// restarts the quiet window forever, and the state it settles on
	// may never arrive, so the loop publishes what it reads at this
	// interval regardless.
	settleLimit = 10 * time.Second

	// backstopInterval is how often the loop reconciles with no event
	// to prompt it. A jack that changes while its node is closed for a
	// rescan costs one edge, and WirePlumber can rename a sink with no
	// jack event at all, so this tick is what recovers the state after
	// either one.
	backstopInterval = 60 * time.Second

	// maxSinkFailures is how many graph reads may fail in a row before
	// the operator exits.
	//
	// One failure skips the write, because a slice that tainted every
	// output would evict every consumer for a pw-dump that timed out
	// once. A run of them is a different state. The published slice
	// still says every output has a sink and no taint, while every
	// prepare call fails because the driver cannot read a sink name,
	// and nothing in the cluster bounds that. Three in a row make it a
	// restart, which is the one repair that reaches a PipeWire that
	// has stopped answering.
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
//
// The WirePlumber container runs the image a fifth way, as its own
// probe: the same binary is already there, so the endpoints check
// needs no second image and no shell.
func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case declareMode:
			declare()
			return
		case endpointsMode:
			endpointsRegistered()
			return
		}
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

	// When the claim delivered a Bluetooth media bus, the operator
	// reads bluetoothd's paired set over it. A pod whose claim
	// delivered none never opens a bus and publishes the card's
	// outputs alone.
	//
	// A bus that never answers ends the process, like every other
	// setup failure here: the kubelet's restart with backoff is this
	// operator's only retry, and the failure shows in kubectl instead
	// of hiding in a log. Plan 60 accepts the coupling this creates,
	// because the pod could not prepare its claim without the
	// Bluetooth operator either.
	var speakers func() (map[string]speaker, error)
	var bluez <-chan struct{}
	if bluetoothEnabled() {
		conn, err := waitForBus(ctx, busReadyTimeout)
		if err != nil {
			fatal("connecting to the delivered Bluetooth media bus: %v", err)
		}
		speakers = func() (map[string]speaker, error) { return pairedSpeakers(conn) }
		bluez, err = watchBlueZ(ctx, conn)
		if err != nil {
			fatal("watching bluetoothd: %v", err)
		}
		fmt.Printf("%s: the claim delivered a Bluetooth media bus at %s\n",
			DriverName, os.Getenv(busAddressVariable))
	}

	// The one wake every watcher that carries no event of its own
	// pokes: the graph feed and the watch on the two collections.
	pokes := make(chan struct{}, 1)
	wake := func() {
		select {
		case pokes <- struct{}{}:
		default:
		}
	}

	// The graph feed is one pw-dump -m for the life of the pod. Every
	// pass reads the graph it last delivered, and a pass that runs
	// before the first batch, or while the feed is starting again,
	// reads PipeWire itself.
	feed := newGraphFeed()

	// The record of which claim holds which endpoint. The DRA plugin
	// writes it and the endpoints' own resources report it, so the two
	// hold one object between them, the way they hold the inventory.
	claims := &preparedClaims{}

	operator := &reconciler{
		client:   client,
		nodeName: nodeName,
		owner:    owner,
		graph:    feed.read,
		speakers: speakers,
		declared: declared,
		// The reconcile pass fills the inventory and the DRA plugin
		// reads it, so the two hold one object between them.
		endpoints: &endpointInventory{},
		cards:     watchCards(ctx),
		control:   newEndpointControl(client, nodeName, claims, feed.read),
	}

	if err := operator.awaitPipeWire(ctx, pipewireReadyTimeout); err != nil {
		fatal("waiting for PipeWire: %v", err)
	}

	outputs, err := readEndpoints()
	if err != nil {
		fatal("reading the card's outputs: %v", err)
	}
	// PipeWire creates the declared nodes while it loads its
	// configuration, so they are there as soon as it answers. The wait
	// costs one graph read in that case, and it covers the case where
	// they are not. Without it the first pass would taint every output,
	// and a NoExecute taint ends the pods that the previous operator's
	// prepared claims left running.
	//
	// The wait watches the playback endpoints alone. A capture node
	// that PipeWire could not create costs a recorder nothing until
	// it claims the source, and no running pod holds one at startup.
	named, _ := nameEndpoints(nodeName, outputs)
	// The inventory is filled before the plugin serves, so that a
	// prepare call the kubelet makes ahead of the first reconcile pass
	// resolves its device rather than reporting a name this driver
	// does publish as one it does not.
	operator.endpoints.publish(named)
	waitForNodes(ctx, operator.graph, sinkEndpoints(named), nodeReadyTimeout)

	// The plugin registers with the kubelet only after PipeWire
	// answers, so the driver appears when it can answer a prepare
	// call.
	go func() {
		if err := serveDRAPlugin(ctx, client, operator.endpoints, claims); err != nil {
			fatal("the DRA plugin is not serving: %v", err)
		}
	}()

	jacks, err := watchJacks(ctx)
	if err != nil {
		fatal("watching the jack nodes: %v", err)
	}

	// The graph feed and the watch on the two collections start
	// before the first pass, so that a change either one carries wakes
	// the loop from the moment it runs.
	go feed.follow(ctx, wake)
	watchEndpoints(ctx, client, wake)

	settled := settle(ctx, wakes(ctx, jacks, bluez, operator.cards.Events(), pokes),
		settleWindow, settleLimit)

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

// wakes turns every event source and the backstop tick into one
// channel. None of them holds state that the loop uses, so the merge
// loses nothing: all of them say to look again.
//
// cards carries every control a person moved, on a knob or from
// another process, and pokes is where the graph feed and the watch
// on the two collections arrive, because neither one carries an
// event the pass reads: the pass reads everything again.
//
// bluez is nil on a pod whose claim delivered no media bus. A
// receive on a nil channel blocks forever, so the merge needs no
// branch for that pod.
//
// A closed bluez channel ends the merge, the same way a closed jack
// channel does. The relay closes it when the connection to the bus
// is lost, and a lost bus is a bluetoothd this operator can no
// longer read, so the loop stops and the kubelet restarts the pod.
func wakes(ctx context.Context, jacks <-chan jackEvent, bluez <-chan struct{},
	cards <-chan controlEvent, pokes <-chan struct{}) <-chan struct{} {
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
			case _, ok := <-bluez:
				if !ok {
					return
				}
				wake()
			case event, ok := <-cards:
				if !ok {
					return
				}
				fmt.Printf("control: %s\n", event)
				wake()
			case <-pokes:
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
// The limit keeps the loop publishing under a flapping cable. Without
// it, hardware that changes faster than the quiet window would restart
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
