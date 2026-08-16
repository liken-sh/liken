// bluetooth-operator publishes each paired Bluetooth controller as
// its own DRA device, so that a pod claims one controller by its MAC
// address and receives that controller's evdev nodes and nothing
// else.
//
// It is an instance of liken's device operator pattern. The operator
// claims the Bluetooth adapter through an ordinary liken.sh claim,
// runs bluetoothd beside itself in the same pod, and publishes what
// bluetoothd holds under its own driver name, bluetooth.liken.sh. The
// operator uses no private interface into liken: the raw claim, the
// slices it writes, and the CDI files it leaves for the runtime are
// the public contracts that any DRA driver on any Kubernetes cluster
// gets.
//
// The claim does two jobs that a person would otherwise write down.
// It places the pod, because only a machine that has an adapter
// publishes one, so no node selector names the machine with the
// radio. And it arbitrates, because liken publishes an adapter as an
// exclusive device, so the claim holder is the only Bluetooth stack
// on that radio.
//
// Four sources drive the loop, and each one only says that something
// changed. Every pass re-reads bluetoothd's whole object tree and
// re-walks sysfs, because a mirror built from event payloads drifts
// out of step with the daemon and a re-read cannot.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/godbus/dbus/v5"
)

const (
	// settleWindow is how long the loop waits for quiet after the last
	// event before it writes. A controller that connects produces a
	// burst of uevents and a burst of D-Bus signals, and the whole
	// burst deserves one write.
	//
	// Every ResourceSlice write wakes every DRA-pending pod in the
	// cluster, because the scheduler event that a slice change raises
	// carries no queueing hint. Hardware that flaps must not turn into
	// a cluster-wide scheduling storm.
	settleWindow = 1500 * time.Millisecond

	// settleLimit bounds the wait. A controller that reconnects in a
	// loop restarts the quiet window forever, and the state it settles
	// on may never arrive, so the loop publishes what it can see at
	// this interval regardless.
	settleLimit = 10 * time.Second

	// backstopInterval is how often the loop reconciles with no event
	// to prompt it. The kernel drops uevent datagrams when its socket
	// buffer fills, and a dropped datagram costs one edge, so this
	// tick is what recovers the state after one.
	backstopInterval = 60 * time.Second

	// retryDelay is how long the loop waits before it runs a failed
	// pass again. One retry follows each failure, and
	// a retry that fails schedules nothing more, so a failure that
	// persists falls back to the backstop tick instead of turning into
	// a five-second poll.
	retryDelay = 5 * time.Second

	// blueZTimeout bounds the wait for bluetoothd to claim its bus
	// name at startup. A daemon that never claims it is a failure to
	// report, and the pod's restart is the retry.
	blueZTimeout = 30 * time.Second
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// The pod spec gives the pod its node's name through the downward
	// API. A ResourceSlice names the node whose hardware it
	// describes, and a pod cannot read that from anywhere else without
	// asking the API server which node it is on.
	nodeName := os.Getenv("NODE_NAME")
	if nodeName == "" {
		fatal("NODE_NAME is unset; the pod spec must supply it from spec.nodeName")
	}
	fmt.Printf("%s: operating the Bluetooth adapter on %s\n", DriverName, nodeName)

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

	// The bus is the one in this pod, started by the entrypoint for
	// these two processes alone. Nothing outside the pod reaches it.
	conn, err := dbus.SystemBus()
	if err != nil {
		fatal("connecting to the D-Bus system bus: %v", err)
	}
	if err := waitForBlueZ(ctx, conn, blueZTimeout); err != nil {
		fatal("waiting for bluetoothd: %v", err)
	}

	// The plugin registers with the kubelet only after bluetoothd is
	// up, so the driver appears when it can actually answer a prepare
	// call.
	go func() {
		if err := serveDRAPlugin(ctx, client); err != nil {
			fatal("the DRA plugin is not serving: %v", err)
		}
	}()

	uevents, err := listenForUevents(ctx)
	if err != nil {
		fatal("watching for kernel events: %v", err)
	}
	blueZChanges, err := watchBlueZ(ctx, conn)
	if err != nil {
		fatal("watching bluetoothd: %v", err)
	}
	blueZGone, err := watchBlueZExit(ctx, conn)
	if err != nil {
		fatal("watching bluetoothd's bus name: %v", err)
	}

	// retries carries the one extra pass that follows a failed write.
	// It is a source of wakes like the kernel and the bus are, so a
	// retry goes through the same settle window as everything else.
	retries := make(chan struct{}, 1)
	settled := settle(ctx, wakes(ctx, uevents, blueZChanges, retries), settleWindow, settleLimit)

	// The first pass runs before any event, because the operator
	// starts with controllers already paired and possibly already
	// connected, and a restart must republish what the previous pod
	// published.
	publish := &publisher{client: client, nodeName: nodeName, owner: owner}
	readPairedSet := func() (map[string]controller, error) { return pairedControllers(conn) }
	retryScheduled := false
	pass := func() {
		if publish.reconcile(readPairedSet) {
			retryScheduled = false
			return
		}
		if retryScheduled {
			return
		}
		retryScheduled = true
		time.AfterFunc(retryDelay, func() {
			select {
			case retries <- struct{}{}:
			default:
			}
		})
	}
	pass()

	for {
		select {
		case <-ctx.Done():
			// The slice stays. The operator's pod restarts for ordinary
			// reasons while a consumer holds a prepared claim, and the
			// Node's ownership of the slice is what retracts it when
			// this node really leaves.
			return
		case <-blueZGone:
			// bluetoothd owns the HID sessions, and its death
			// disconnects every controller at once, so an operator that
			// kept publishing would offer devices no pod can use. The
			// published devices keep their taints from the last pass
			// until the replacement pod corrects them.
			fatal("bluetoothd left the bus")
		case _, ok := <-settled:
			switch err := exitReason(ctx, ok); {
			case err == nil:
				pass()
			case errors.Is(err, errShutdown):
				return
			default:
				fatal("%v", err)
			}
		}
	}
}

// The two ways the loop's event sources can end.
var (
	// errShutdown is the ordinary one. The context was cancelled, so
	// the closed channels are the shutdown's own doing and the
	// operator ends with a zero exit.
	errShutdown = errors.New("shutting down")

	// errSourcesClosed is the other one. godbus closes every
	// registered signal channel when the connection to the bus is
	// lost, so channels that close while the operator is still meant
	// to be running mean it can no longer read the paired set.
	errSourcesClosed = errors.New("the event sources closed while running; the D-Bus connection is gone")
)

// exitReason says whether the loop must end, and why. A nil answer
// means the loop goes on, which is every receive that delivered a
// value.
func exitReason(ctx context.Context, ok bool) error {
	if ok {
		return nil
	}
	if ctx.Err() != nil {
		return errShutdown
	}
	return errSourcesClosed
}

// pairedSetReader answers with the controllers bluetoothd holds.
// reconcile takes one as a parameter rather than calling bluetoothd
// itself, so that a test can supply an answer, including ErrNoAdapter,
// without a bus.
type pairedSetReader func() (map[string]controller, error)

// publisher writes one node's slice. It holds the last paired set
// that bluetoothd answered with, because an adapter that departs
// takes every device object with it, and that record is then the only
// account of which controllers the slice is offering.
type publisher struct {
	client   *Client
	nodeName string
	owner    OwnerReference
	known    map[string]controller
}

// reconcile makes the published slice and every prepared CDI spec
// agree with what bluetoothd and sysfs say right now. It reports
// whether the pass left the node's state correct, so that the caller
// can run a failure again shortly.
//
// The order matters. The CDI refresh runs first, so that a controller
// which came back on a different evdev node has a correct spec before
// the slice says the controller is usable again.
func (p *publisher) reconcile(readPairedSet pairedSetReader) bool {
	nodes := nodesByMAC(discoverHIDDevices(draSysfsRoot))
	refreshCDISpecs(nodes)

	controllers, err := readPairedSet()
	switch {
	case errors.Is(err, ErrNoAdapter) && len(p.known) == 0:
		// The startup window. bluetoothd has not published its object
		// tree yet, so there is no last-known set to taint and nothing
		// true to say about this node.
		fmt.Fprintf(os.Stderr, "waiting for bluetoothd to publish an adapter\n")
		return false

	case errors.Is(err, ErrNoAdapter):
		// The adapter departed, by an unplug or a USB reset. Every
		// controller it held is out of reach, and the slice has to say
		// so rather than say nothing: the devices stay, so no
		// allocation is stranded, and both taints go on, so the
		// eviction controller ends the sessions that are already
		// running and the next claim parks instead of failing in
		// prepare. Passing no nodes is what derives both taints, which
		// is the same rule a single controller going quiet takes.
		fmt.Fprintf(os.Stderr, "the adapter is gone; taints all %d published controllers\n", len(p.known))
		controllers, nodes = unreachable(p.known), nil

	case err != nil:
		fmt.Fprintf(os.Stderr, "reading the paired set: %v\n", err)
		return false

	default:
		p.known = controllers
	}

	devices := sliceDevices(controllers, nodes)
	if len(devices) > maxSliceDevices {
		fmt.Fprintf(os.Stderr, "%d paired controllers exceed one slice's capacity of %d; dropping the overflow\n",
			len(devices), maxSliceDevices)
		devices = devices[:maxSliceDevices]
	}
	if err := EnsureResourceSlice(p.client, p.nodeName, p.owner, devices); err != nil {
		fmt.Fprintf(os.Stderr, "publishing the slice: %v\n", err)
		return false
	}
	// A tainted slice is a correct answer and a degraded one. Reporting
	// it as unfinished buys one quick retry, which is what catches an
	// adapter that comes back from a USB reset a second later.
	return !errors.Is(err, ErrNoAdapter)
}

// unreachable copies the last-known controllers with every one of
// them marked disconnected. The connected attribute must agree with
// the taints: an adapter that is gone holds no connection, whatever
// bluetoothd last reported.
func unreachable(known map[string]controller) map[string]controller {
	out := make(map[string]controller, len(known))
	for mac, c := range known {
		c.Connected = false
		out[mac] = c
	}
	return out
}

// wakes merges the kernel's HID events, bluetoothd's signals, and the
// loop's own retries into one channel. None of them carries state that
// the loop uses, so the merge loses nothing: they all say to look
// again.
func wakes(ctx context.Context, uevents <-chan hidEvent, blueZChanges, retries <-chan struct{}) <-chan struct{} {
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
			case event, ok := <-uevents:
				if !ok {
					return
				}
				fmt.Printf("controller %s: hid %s\n", publishedMAC(event.MAC), event.Action)
				wake()
			case _, ok := <-blueZChanges:
				if !ok {
					return
				}
				wake()
			case _, ok := <-retries:
				if !ok {
					return
				}
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
// The limit is what keeps a flapping controller publishing. Without
// it, hardware that reconnects faster than the quiet window would
// restart the wait on every event and the loop would never write.
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
