// bluetooth-operator publishes each paired Bluetooth controller as a
// DRA device. A pod claims one controller by its MAC address and gets
// that controller's evdev nodes and no others.
//
// The operator runs in the same pod as bluetoothd. The pod claims the
// node's Bluetooth adapter, a liken.sh DRA device, so it schedules
// only onto a machine that has an adapter and needs no node selector.
// liken publishes an adapter as an exclusive device, so this pod runs
// the only Bluetooth stack on the radio. The operator publishes the
// controllers that bluetoothd has paired under the driver name
// bluetooth.liken.sh, through the ResourceSlices and CDI files that
// any DRA driver uses. It uses no interface that is private to liken.
//
// The operator keeps an Adapter for its radio and a Peripheral for
// each bond. A person pairs a controller by creating a PairingRequest,
// which opens a pairing window, with no shell in the pod.
//
// The loop runs a pass when the kernel's uevents, bluetoothd's D-Bus
// signals, the watches of the objects above, a retry, or the backstop
// timer wake it. A wake carries no data. Every pass reads bluetoothd's
// whole object tree and sysfs again, because a cache built from event
// payloads can drift from the daemon's real state. The one exception
// is the reason a link ended, which a pass takes from a signal because
// bluetoothd does not store it (linkend.go).
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/liken-sh/liken/bluetooth-operator/bonds"
	"github.com/liken-sh/liken/kubernetes/apiclient"
	"github.com/liken-sh/liken/kubernetes/events"
	"github.com/liken-sh/liken/kubernetes/informer"
)

const (
	// settleWindow is how long the loop waits for quiet after the last
	// event before it runs a pass. A controller that connects causes a
	// burst of uevents and a burst of D-Bus signals, and one pass
	// handles the whole burst.
	//
	// The scheduler has no queueing hint for a ResourceSlice change, so
	// every slice write makes it retry every pod that waits on a DRA
	// claim. A controller that connects and disconnects repeatedly must
	// not cause a write for each change.
	//
	// The attach for a uevent does not wait for this window. An evdev
	// reader receives only the events after it opens the node, and a
	// Bluetooth LE remote that wakes on a key press sends that press
	// about one second after its HID device appears. So each uevent
	// opens the new nodes at once (publisher.attach), and the pass at
	// the end of the window publishes them.
	settleWindow = 1500 * time.Millisecond

	// settleLimit is the longest the loop waits after the first event of
	// a burst. A controller that reconnects in a loop keeps restarting
	// the quiet window, so without a limit the loop would never run a
	// pass.
	settleLimit = 10 * time.Second

	// backstopInterval is how often the loop runs a pass when no event
	// arrives. It covers the failures that no event reports:
	//
	//   - bluetoothd wrote a bond file without a D-Bus signal, such as
	//     the cache entry it writes after it resolves a device's name
	//     and browses its services (bondstore.go). The next pass copies
	//     that file into the bond's Secret.
	//   - A pass failed, and its retry failed too (retryDelay).
	//
	// A dropped uevent datagram needs no backstop, because the next
	// receive fails with ENOBUFS and the reader wakes the loop
	// (uevents.go).
	backstopInterval = 60 * time.Second

	// retryDelay is how long the loop waits before it retries a failed
	// pass. The loop schedules one retry and no more until a pass
	// succeeds, so a failure that persists is retried at
	// backstopInterval.
	retryDelay = 5 * time.Second

	// blueZTimeout is how long startup waits for bluetoothd to claim
	// its bus name. When it never does, the operator exits with an
	// error, and the kubelet restarts the pod.
	blueZTimeout = 30 * time.Second

	// busTimeout is how long startup waits for dbus-daemon in the
	// bluetoothd container to bind the bus socket (waitForBus).
	busTimeout = 30 * time.Second

	// busRetryDelay is how often startup tries to connect to the socket
	// again while it waits.
	busRetryDelay = 500 * time.Millisecond
)

// metricsAddressVar is the environment variable that holds the
// address the /metrics listener binds, in the form net.Listen takes,
// such as ":9250" or "127.0.0.1:9250". When it is empty or unset, the
// operator does not serve metrics.
const metricsAddressVar = "BLUETOOTH_METRICS_ADDRESS"

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// The ResourceSlice names this node. The pod spec supplies the name
	// through the downward API.
	nodeName := os.Getenv("NODE_NAME")
	if nodeName == "" {
		fatal("NODE_NAME is not set. Set it from spec.nodeName in the pod spec")
	}
	// Each bond is stored in its own Secret in this pod's namespace.
	// bondfetch reads the same variable to find the Secrets that it
	// restores before bluetoothd starts.
	namespace := os.Getenv(namespaceVar)
	if namespace == "" {
		fatal("%s is not set. Set it from metadata.namespace in the pod spec", namespaceVar)
	}
	// The pod deletes itself by this name to apply a new privacy
	// setting (privacy.go).
	podName := os.Getenv(podNameVar)
	if podName == "" {
		fatal("%s is not set. Set it from metadata.name in the pod spec", podNameVar)
	}
	fmt.Printf("%s: operating the Bluetooth adapter on %s\n", DriverName, nodeName)

	// A setup failure exits the process with an error and has no retry
	// here. The kubelet restarts a pod that exits nonzero, with
	// backoff, and the failure shows in kubectl.
	client, err := apiclient.InCluster(apiclient.InClusterOptions{})
	if err != nil {
		fatal("creating the Kubernetes API client: %v", err)
	}
	watcher, err := informer.InCluster()
	if err != nil {
		fatal("creating the Kubernetes watch client: %v", err)
	}
	owner, err := NodeOwner(client, nodeName)
	if err != nil {
		fatal("reading Node %s: %v", nodeName, err)
	}

	// The D-Bus system bus is private to the pod. The bluetoothd
	// container runs its dbus-daemon.
	conn, err := waitForBus(ctx, busTimeout)
	if err != nil {
		fatal("connecting to the D-Bus system bus: %v", err)
	}
	if err := waitForBlueZ(ctx, conn, blueZTimeout); err != nil {
		fatal("waiting for bluetoothd: %v", err)
	}

	uevents, err := listenForUevents(ctx)
	if err != nil {
		fatal("watching for kernel events: %v", err)
	}
	// ends holds the reason each link ended, from when the signal
	// reader receives it until a pass writes it (linkend.go).
	ends := newLinkEnds()
	blueZChanges, err := watchBlueZ(ctx, conn, ends)
	if err != nil {
		fatal("watching bluetoothd signals: %v", err)
	}
	blueZGone, err := watchBlueZExit(ctx, conn)
	if err != nil {
		fatal("watching bluetoothd's bus name: %v", err)
	}

	// The operator wakes itself through retries: the retry after a
	// failed pass, the next pass that a pairing window or an unpair
	// needs between two of its steps, and the end of a connect or an
	// input recovery. These wakes go through the same settle window as
	// the others.
	retries := make(chan struct{}, 1)
	requests, requestStore := watchPairingRequests(ctx, watcher, time.Now)
	edits := watchEdits(ctx, watcher, nodeName)
	woken, attaches := wakes(ctx, uevents, blueZChanges, retries, requests, edits.wakes())
	settled := settle(ctx, woken, settleWindow, settleLimit)

	// readings is this operator's Prometheus registry. Its methods
	// accept a nil receiver and record nothing, so the wiring is the
	// same when the listener is off.
	readings := newMetrics()
	if address := os.Getenv(metricsAddressVar); address != "" {
		listener, err := readings.listen(address)
		if err != nil {
			fatal("listening for metrics on %s: %v", address, err)
		}
		fmt.Printf("%s: serving metrics on %s\n", DriverName, address)
		go serveMetrics(ctx, listener, readings)
	}

	// recorder posts the Events (events.go) from its own goroutine,
	// which ends with ctx, so a pass never waits on it.
	recorder := events.New(ctx, client, component, events.Options{})
	inputRelays := newRelays(linuxInput{})
	inputRelays.metrics = readings
	inputRelays.recorder = recorder
	publish := &publisher{client: client, nodeName: nodeName, owner: owner, relays: inputRelays}
	keep := &bondStore{client: client, namespace: namespace, root: bondsRoot(), relays: inputRelays,
		watchSecrets: func(adapter bonds.Address) informer.View { return watchBondSecrets(ctx, watcher, namespace, adapter) }}
	restart := &privacyRestart{client: client, namespace: namespace, pod: podName, recorder: recorder}
	objects := newInventory(client, newBlueZRadio(conn), inputRelays, nodeName, namespace, readings)
	objects.recorder = recorder
	objects.ends = ends
	objects.inputs.recorder = recorder
	objects.node = nodeReference(owner)
	// The pass reads the Kubernetes objects from the watches' stores
	// (objectcache.go).
	objects.cache = edits.cache(requestStore)
	readPairedSet := func() (map[string]controller, error) { return pairedControllers(conn) }
	readAdapter := func() (bonds.Address, error) { return adapterAddress(conn) }
	wake := func() {
		select {
		case retries <- struct{}{}:
		default:
		}
	}
	wakeSoon := func(after time.Duration) { time.AfterFunc(after, wake) }
	// The connector, which pages a device, and the input recovery, which
	// reconnects one, run in their own goroutines. When one finishes, it
	// wakes the loop, so that the next pass reads the result.
	objects.connects.wake = wake
	objects.inputs.wake = wake
	// The edit watcher needs the radio's address to select the
	// Peripherals of that radio.
	objects.follow = edits.follow
	retryScheduled := false
	pass := func() {
		// Every step runs even when an earlier step fails. A new pairing
		// whose slice write failed still has a key that must be stored, and
		// a bond that could not be stored does not change the slice.
		//
		// The object reconcile runs first, because it returns the
		// Peripherals that own the bond Secrets, and the devices being
		// unpaired, which must leave the slice before their bonds are
		// removed.
		state := objects.reconcile()
		published := publish.reconcile(readPairedSet, readAdapter, state.excluded)
		if published {
			objects.published()
		}
		bonded := keep.persist(readAdapter, state.owners, state.unpairing)
		identified := keep.persistIdentity(state.adapter)
		persisted := bonded && identified
		// The privacy restart runs last. It deletes this pod, so it waits
		// until a pass has stored every bond and the identity key.
		restart.apply(state.adapter, state.privacy, persisted)
		if state.again > 0 {
			// An open pairing window, or an unpair between two of its steps,
			// needs the next pass sooner than the backstop.
			wakeSoon(state.again)
		}
		if published && persisted && state.ok {
			retryScheduled = false
			return
		}
		if retryScheduled {
			return
		}
		retryScheduled = true
		wakeSoon(retryDelay)
	}
	// Create each bonded controller's virtual input device from its
	// stored snapshot before the first pass publishes the slice, so that
	// a claim on a controller that is not connected can be prepared.
	keep.restore(readAdapter)
	// Containers that started before this operator still have prepared
	// claims, and the settings of those claims decide which events each
	// relay copies from a real evdev node. Rebuild the settings before
	// the first pass opens a real node.
	inputRelays.restorePrepared()

	// The plugin registers with the kubelet only after bluetoothd is up
	// and the relays are restored, so that the kubelet sends no prepare
	// call that the driver cannot handle yet.
	go func() {
		if err := serveDRAPlugin(ctx, client, inputRelays); err != nil {
			fatal("serving the DRA plugin: %v", err)
		}
	}()

	pass()

	switch err := loop(ctx, settled, attaches, blueZGone, pass, publish.attach); {
	case errors.Is(err, errShutdown):
		// Leave the slice in place, because the pod also restarts for
		// ordinary reasons while a consumer holds a prepared claim. The
		// slice's owner reference is the Node, so the garbage collector
		// deletes the slice with the Node.
		return
	default:
		fatal("%v", err)
	}
}

// loop runs a pass for each settled wake and an attach for each
// uevent, and returns the reason it stopped. Both run on this
// goroutine, so only one goroutine uses the publisher.
func loop(ctx context.Context, settled, attaches, blueZGone <-chan struct{}, pass, attach func()) error {
	for {
		select {
		case <-ctx.Done():
			return errShutdown
		case <-blueZGone:
			// bluetoothd owns the HID sessions, so its exit disconnects every
			// controller, and an operator that kept running would publish
			// controllers that no pod can use. The published devices keep the
			// taints of the last pass until the operator starts again.
			return errBlueZGone
		case _, ok := <-settled:
			if err := exitReason(ctx, ok); err != nil {
				return err
			}
			pass()
		case <-attaches:
			attach()
		}
	}
}

// The reasons the loop ends.
var (
	// errShutdown means the context was cancelled, so the operator exits
	// with status zero.
	errShutdown = errors.New("the operator received a shutdown signal")

	// errSourcesClosed means a source of wakes closed while the context
	// was live. godbus closes every registered signal channel when the
	// connection to the bus is lost, and a pass cannot read bluetoothd
	// without that connection.
	errSourcesClosed = errors.New("the sources of wakes closed because the D-Bus connection is gone")

	// errBlueZGone is returned when bluetoothd leaves the bus.
	errBlueZGone = errors.New("bluetoothd left the bus")
)

// exitReason returns the error that ends the loop, or nil when the
// loop goes on. ok is the second result of the receive from settled.
func exitReason(ctx context.Context, ok bool) error {
	if ok {
		return nil
	}
	if ctx.Err() != nil {
		return errShutdown
	}
	return errSourcesClosed
}

func fatal(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}
