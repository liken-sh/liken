// audio-operator publishes each physical audio output of a machine as
// a DRA device under the driver name audio.liken.sh. A pod that claims
// one receives the PipeWire socket and the name of the sink that its
// streams play to.
//
// The operator's own pod claims the machine's audio controller through
// a liken.sh claim, runs PipeWire beside the operator, and publishes
// PipeWire's sinks and sources in a ResourceSlice. Only a machine with
// an audio controller publishes one, so the claim places the pod with
// no node selector. liken publishes a controller as an exclusive
// device, so no other sound server can run on that card. The operator
// uses only the interfaces that any DRA driver uses: the claim, the
// slice, and CDI files.
//
// The operator publishes which PCM device plays into which monitor.
// That mapping comes from the ELD that the graphics driver writes into
// the HDMI audio driver, so it is valid only while the driver runs, and
// it changes when somebody moves a cable.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/liken-sh/liken/kubernetes/apiclient"
	"github.com/liken-sh/liken/kubernetes/events"
	"github.com/liken-sh/liken/kubernetes/informer"
)

// version is set through -ldflags in the Dockerfile, and is dev in any
// other build. liken_build_info exports it.
var version = "dev"

const (
	// settleWindow is the quiet time after the last event before the
	// loop writes. Plugging in a monitor produces a burst of control
	// events, and PipeWire builds the sink some time after them, so one
	// write must cover the whole burst. Each ResourceSlice write also
	// wakes every pod that waits on a DRA claim, because kube-scheduler
	// has no queueing hint for a slice change, so flapping hardware
	// would requeue those pods again and again.
	settleWindow = 1500 * time.Millisecond

	// settleLimit is the longest the loop waits after the first event of
	// a burst. Without it, a loose cable would restart the quiet window
	// forever and the loop would never write.
	settleLimit = 10 * time.Second

	// backstopInterval is how often the loop runs a pass when nothing
	// wakes it. The tick covers a card whose control events could not
	// be subscribed, and a write of the slice or a status that failed,
	// because no event repeats either. Jack, ELD, and control changes
	// arrive as control events, and the graph feed delivers every change
	// in PipeWire's graph, so neither needs the tick.
	backstopInterval = 60 * time.Second

	// maxFailedGraphReads is the number of PipeWire graph reads in a row
	// that can fail before the operator taints every output and exits.
	// A single failure only skips the pass, because tainting every
	// output would evict every consumer over one pw-dump that timed out.
	// During a longer run of failures the slice still reports every
	// output as ready while every prepare call fails, because the driver
	// cannot read a sink name. Only a restart of the pod repairs a
	// PipeWire that has stopped responding.
	maxFailedGraphReads = 3

	// writeRetryDelay is the wait before the single retry of a failed
	// slice write. The retry covers a conflict with another writer and a
	// short API server outage. When the retry fails too, the next event
	// or the backstop tick writes the slice.
	writeRetryDelay = 2 * time.Second
)

// main selects the program from its first argument. The operator's pod
// and the audio-api Deployment run this one image, so every container
// runs the same version:
//
//   - declare is the init container that writes the PipeWire drop-in
//     that declares the card's nodes.
//   - pipewire and wireplumber start the sound daemons. The remaining
//     arguments of wireplumber go to WirePlumber.
//   - endpoints-registered and graph-answers are exec probes of those
//     containers. They run this binary, so the image needs no shell.
//   - capture serves the taps that record a Sink or a Source.
//   - api is the audio-api Deployment.
//   - With no argument, it is the operator.
func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case declareMode:
			declare()
			return
		case endpointsMode:
			endpointsRegistered()
			return
		case graphProbeMode:
			graphProbe()
			return
		case pipewireMode:
			runPipewire()
			return
		case wireplumberMode:
			runWireplumber(os.Args[2:])
			return
		case captureMode:
			capture()
			return
		case apiMode:
			serveAPI()
			return
		}
	}
	operate()
}

func operate() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// The DaemonSet sets NODE_NAME from spec.nodeName through the
	// downward API, because a ResourceSlice must name its node.
	nodeName := os.Getenv("NODE_NAME")
	if nodeName == "" {
		fatal("NODE_NAME is unset, and the DaemonSet must set it from spec.nodeName")
	}
	fmt.Printf("%s: operating the audio controller on %s\n", DriverName, nodeName)

	// An empty METRICS_ADDRESS serves no metrics.
	readings := newMetrics(version)
	metricsListener, err := readings.listen(os.Getenv("METRICS_ADDRESS"))
	if err != nil {
		fatal("listening for metrics: %v", err)
	}
	if metricsListener != nil {
		fmt.Printf("%s: serving metrics on %s\n", DriverName, metricsListener.Addr())
		go serveMetrics(ctx, metricsListener, readings)
	}

	// A setup failure exits the process instead of retrying, so the
	// kubelet restarts the container with backoff, and kubectl shows the
	// restarts.
	client, err := apiclient.InCluster(apiclient.InClusterOptions{})
	if err != nil {
		fatal("reading the in-cluster config: %v", err)
	}
	owner, err := NodeOwner(client, nodeName)
	if err != nil {
		fatal("reading the Node %s: %v", nodeName, err)
	}

	// declared is the text of the drop-in that the running PipeWire
	// loaded: the file that the init container wrote, or the file that
	// the operator rewrote for a layout change before PipeWire's last
	// restart. A missing file means that the init container did not run.
	declared, err := readNodeConfig()
	if err != nil {
		fatal("reading the declaration PipeWire loaded: %v", err)
	}

	// When the claim includes a Bluetooth media bus, the operator also
	// publishes the speakers that bluetoothd has paired. A bus that does
	// not respond at startup exits the process, because the pod could
	// not have prepared its claim without bluetooth-operator either
	// (plans/completed/60-bluetooth-audio.md). After startup, followBus
	// connects again when the bus closes, so a restart of
	// bluetooth-operator's pod does not restart this one.
	var speakers func() (map[string]speaker, error)
	var bluez <-chan struct{}
	if bluetoothEnabled() {
		if _, err := waitForBus(ctx, busReadyTimeout); err != nil {
			fatal("connecting to the Bluetooth media bus: %v", err)
		}
		bus := &mediaBus{}
		speakers = bus.speakers
		bluez = followBus(ctx, bus)
		fmt.Printf("%s: the claim includes a Bluetooth media bus at %s\n",
			DriverName, os.Getenv(busAddressVariable))
	}

	// wake queues a pass. The channel holds one request, so requests
	// that arrive before the loop reads it merge into one.
	wakeRequests := make(chan struct{}, 1)
	wake := func() {
		select {
		case wakeRequests <- struct{}{}:
		default:
		}
	}

	// The graph feed runs one pw-dump -m for the life of the pod, and a
	// pass reads the last graph that it delivered. Before the first
	// graph, and while pw-dump restarts, a pass reads PipeWire directly.
	feed := newGraphFeed()

	// claims records which claim holds which endpoint. The DRA plugin
	// writes it, and the status of the Sinks and Sources reports it.
	claims := &preparedClaims{}

	// The watches on the Sinks and Sources start before the first pass,
	// so a change that arrives during that pass still wakes the loop.
	watcher, err := informer.InCluster()
	if err != nil {
		fatal("reading the in-cluster config for the watches: %v", err)
	}
	// A new status.session.volumeAsk on a Sink signals volumeAsks
	// instead of waking a pass, so the loop applies it without the
	// settle delay. One buffered signal covers a burst, because the loop
	// reads the newest request of each Sink.
	volumeAsks := make(chan struct{}, 1)
	askArrived := func() {
		select {
		case volumeAsks <- struct{}{}:
		default:
		}
	}
	resources := watchEndpoints(ctx, watcher, nodeName, wake, askArrived, readings)

	operator := &reconciler{
		client:   client,
		nodeName: nodeName,
		owner:    owner,
		graph:    feed.read,
		speakers: speakers,
		declared: declared,
		// The PipeWire container's first process makes the same check
		// before it starts PipeWire. The operator uses it to report a
		// layout change that waits for a PipeWire restart.
		declarationStale: runningStale,
		readings:         readings,
		// The reconcile pass writes the inventory and the DRA plugin
		// reads it.
		endpoints:   &endpointInventory{},
		cards:       watchCards(ctx, wake),
		control:     newEndpointControl(client, resources, nodeName, claims, feed, readings),
		volumeAsks:  volumeAsks,
		requestPass: wake,
		// The hold wakes a pass when its wait for an empty ELD ends, so
		// a monitor that stayed disconnected is published then.
		monitors: newMonitorHold(wake),
	}
	// A monitor whose hot plug detect is down when the operator starts
	// has no earlier reading in this process, so the hold uses the ELDs
	// that the previous operator published in the slice.
	operator.monitors.setPublishedELDs(readPublishedELDs(client, nodeName))
	operator.control.recorder = events.New(ctx, client, operatorComponent, events.Options{})
	// The slice reads the share counts from spec.sharedClaims in the
	// same stores that the control reads.
	operator.readShares = operator.control.sharedClaims

	if err := operator.awaitPipeWire(ctx, pipewireReadyTimeout); err != nil {
		fatal("waiting for PipeWire: %v", err)
	}

	outputs, err := readEndpoints()
	if err != nil {
		fatal("reading the card's outputs: %v", err)
	}
	// PipeWire creates the declared nodes while it loads its
	// configuration, so they usually exist when it first responds, and
	// waitForNodes costs one graph read. Without the wait, a first pass
	// that ran before the nodes existed would taint every output, and a
	// NoExecute taint evicts the pods on claims that the previous
	// operator prepared.
	//
	// The wait covers only the playback endpoints. A capture node that
	// PipeWire could not create affects a recorder only when it claims
	// the source, and no running pod holds one at startup.
	named, _ := nameEndpoints(nodeName, outputs)
	// The inventory is filled before the plugin serves, so a prepare
	// call that the kubelet sends before the first pass finds its device.
	operator.endpoints.publish(named)
	waitForNodes(ctx, operator.graph, sinkEndpoints(named), nodeReadyTimeout)

	// The feed starts before the plugin serves, so a codec switch in
	// the first prepare call can wait on the feed's graphs. It also
	// starts before the first pass, so a graph change during that pass
	// wakes the loop.
	go feed.follow(ctx, wake)

	// The plugin registers with the kubelet only after PipeWire
	// responds, so the kubelet sends no prepare call before the driver
	// can serve it.
	go func() {
		if err := serveDRAPlugin(ctx, client, operator.endpoints, claims, feed); err != nil {
			fatal("serving the DRA plugin: %v", err)
		}
	}()

	settled := settle(ctx, wakes(ctx, bluez, operator.cards.Events(), wakeRequests, operator.monitors.countELDChange),
		settleWindow, settleLimit)

	// The first pass runs without waiting for an event, because the
	// monitors are already connected and a restarted operator must
	// publish the slice again.
	if err := operator.pass(ctx); err != nil {
		fatal("%v", err)
	}
	if err := run(ctx, operator, settled); err != nil {
		fatal("%v", err)
	}
}

// wakes merges the event sources and the backstop tick into one
// channel. A wake carries no state, because the pass that follows reads
// everything again.
//
// cards carries each change on a card's control device: a control set
// on the card or by another process, a jack change, and an ELD that a
// monitor rewrote. bluez is nil when the claim includes no media bus,
// and a receive from a nil channel blocks forever. A closed bluez or
// cards channel ends the merge. bluez closes only when ctx ends,
// because followBus connects again when the bus closes.
func wakes(ctx context.Context, bluez <-chan struct{}, cards <-chan controlEvent,
	wakeRequests <-chan struct{}, countELDChange func(controlEvent)) <-chan struct{} {
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
				// The hold counts the event before the wake, so the
				// pass that the wake starts finds the ELD change
				// counted.
				if countELDChange != nil {
					countELDChange(event)
				}
				wake()
			case <-wakeRequests:
				wake()
			case <-tick.C:
				wake()
			}
		}
	}()
	return out
}

// settle turns a burst of values on in into one value on out. It sends
// when in has been quiet for window, or when limit has passed since the
// first value of the burst, whichever comes first. Without limit,
// hardware that changes faster than window would keep the loop from
// ever writing.
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
