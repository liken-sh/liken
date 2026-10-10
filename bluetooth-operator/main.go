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
// The claim does two jobs. It places the pod, because only a machine
// that has an adapter publishes one, so no node selector names the
// machine with the radio. It also arbitrates, because liken publishes
// an adapter as an exclusive device, so the claim holder is the only
// Bluetooth stack on that radio.
//
// The operator also keeps the pairing API: an Adapter for the radio it
// holds, a Peripheral for each bond, and the PairingRequests a person
// creates to open a pairing window. Those objects are the reason a
// person never needs a shell in this pod.
//
// Six sources drive the loop, and each one only says that something
// changed. Every pass re-reads bluetoothd's whole object tree and
// re-walks sysfs. A cache built from event payloads can fall out of
// step with the daemon; a full re-read stays correct.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/liken-sh/bluetooth-operator/bonds"
	"github.com/liken-sh/liken/kubernetes/apiclient"
	"github.com/liken-sh/liken/kubernetes/events"
	"github.com/liken-sh/liken/kubernetes/informer"
)

const (
	// settleWindow is how long the loop waits for quiet after the last
	// event before it runs a pass. A controller that connects produces a
	// burst of uevents and a burst of D-Bus signals, and one write
	// covers the whole burst.
	//
	// Every ResourceSlice write wakes every DRA-pending pod in the
	// cluster, because the scheduler event that a slice change raises
	// includes no queueing hint. Hardware that flaps must not turn into
	// a cluster-wide scheduling storm.
	//
	// The relay does not wait for the window. The kernel keeps no
	// backlog for a reader that opens an evdev node late, so each press
	// before the open is lost. A Bluetooth LE remote that wakes on a
	// press sends that press about one second after its HID device
	// appears. So each uevent opens the new nodes at once
	// (publisher.attach), and the pass at the end of the window
	// publishes them.
	settleWindow = 1500 * time.Millisecond

	// settleLimit bounds the wait. A controller that reconnects in a
	// loop restarts the quiet window forever, and the state it settles
	// on may never arrive, so the loop publishes what it reads at
	// this interval regardless.
	settleLimit = 10 * time.Second

	// backstopInterval is how often the loop reconciles with no event
	// to prompt it. The tick is a backstop, and it covers two failures
	// that no event reports:
	//
	//   - A bond file that bluetoothd wrote with no D-Bus signal to
	//     announce it, such as the cache entry it writes when it
	//     resolves a device's name and browses its services
	//     (bondstore.go). The Secret that holds the bond gets that file
	//     on the next pass, which is at most this long.
	//   - A pass that failed, and whose one retry failed too. A pass
	//     fails when a read fails, such as bluetoothd's object tree or a
	//     list from the API server, or when a write fails. The loop
	//     schedules no more retries (retryDelay), so the tick runs the
	//     pass again.
	//
	// The other sources report their own changes. A uevent datagram
	// that the kernel dropped makes the next receive fail with ENOBUFS,
	// and the reader wakes the loop for it (uevents.go). The watches of
	// the PairingRequests, the Adapters, and the Peripherals wake the
	// loop for an edit (requestwatch.go, editwatch.go).
	backstopInterval = 60 * time.Second

	// retryDelay is how long the loop waits before it runs a failed
	// pass again. One retry follows each failure, and a retry that
	// fails schedules nothing more, so a failure that persists falls
	// back to the backstop tick instead of turning into a five-second
	// poll.
	retryDelay = 5 * time.Second

	// blueZTimeout bounds the wait for bluetoothd to claim its bus
	// name at startup. A daemon that never claims it is a failure to
	// report, and the pod's restart is the retry.
	blueZTimeout = 30 * time.Second

	// busTimeout bounds the wait for the bus socket itself. The bus
	// runs in the pod's other container, so its socket appears when
	// that container's dbus-daemon binds it, which is after this
	// process starts. The kubelet starts the bluetoothd container
	// first, because it is a sidecar, and this wait covers the gap
	// between "started" and "listening".
	busTimeout = 30 * time.Second

	// busRetryDelay is how often the wait tries the socket again. The
	// socket is a file on a volume shared with the other container,
	// and there is no event to wait on.
	busRetryDelay = 500 * time.Millisecond
)

// metricsAddressVar names the address the /metrics listener binds, in
// the form net.Listen takes: ":9250" or "127.0.0.1:9250". An empty
// value, which is the zero value of an environment variable the pod
// spec does not set, turns the listener off.
const metricsAddressVar = "BLUETOOTH_METRICS_ADDRESS"

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
	// The bonds are stored in one Secret for each bond, in this pod's
	// own namespace, and the downward API is where a pod reads which
	// namespace that is. The same variable names the same Secrets for
	// bondfetch, which restored them before bluetoothd started.
	namespace := os.Getenv(namespaceVar)
	if namespace == "" {
		fatal("%s is unset; the pod spec must supply it from metadata.namespace", namespaceVar)
	}
	// The pod deletes itself by this name to apply a new privacy
	// setting (privacy.go), and the downward API is where a pod reads
	// its own name.
	podName := os.Getenv(podNameVar)
	if podName == "" {
		fatal("%s is unset; the pod spec must supply it from metadata.name", podNameVar)
	}
	fmt.Printf("%s: operating the Bluetooth adapter on %s\n", DriverName, nodeName)

	// Failures during setup end the process deliberately. This code
	// has no retry logic of its own, because the kubelet already
	// provides it: a pod that exits nonzero restarts with backoff, and
	// the failure shows in kubectl instead of hiding in a log.
	client, err := apiclient.InCluster(apiclient.InClusterOptions{})
	if err != nil {
		fatal("in-cluster config: %v", err)
	}
	watcher, err := informer.InCluster()
	if err != nil {
		fatal("in-cluster config for the watches: %v", err)
	}
	owner, err := NodeOwner(client, nodeName)
	if err != nil {
		fatal("reading node %s: %v", nodeName, err)
	}

	// The bus is the one in this pod, started by the bluetoothd
	// container for these two containers alone. Nothing outside the pod
	// reaches it.
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
	blueZChanges, err := watchBlueZ(ctx, conn)
	if err != nil {
		fatal("watching bluetoothd: %v", err)
	}
	blueZGone, err := watchBlueZExit(ctx, conn)
	if err != nil {
		fatal("watching bluetoothd's bus name: %v", err)
	}

	// retries carries the passes the loop asks itself for: the one that
	// follows a failed write, and the follow-up a pairing window or an
	// unpair asks for while it is between two of its steps. Both are
	// sources of wakes like the kernel and the bus are, so they go
	// through the same settle window as everything else.
	retries := make(chan struct{}, 1)
	requests, requestStore := watchPairingRequests(ctx, watcher, time.Now)
	edits := watchEdits(ctx, watcher, nodeName)
	woken, attaches := wakes(ctx, uevents, blueZChanges, retries, requests, edits.wakes())
	settled := settle(ctx, woken, settleWindow, settleLimit)

	// readings is this operator's Prometheus registry. Every method on
	// it accepts a nil receiver and records nothing, so wiring it in
	// unconditionally costs nothing when the listener is off.
	readings := newMetrics()
	if address := os.Getenv(metricsAddressVar); address != "" {
		listener, err := readings.listen(address)
		if err != nil {
			fatal("listening for metrics on %s: %v", address, err)
		}
		fmt.Printf("%s: serving metrics on %s\n", DriverName, address)
		go serveMetrics(ctx, listener, readings)
	}

	// The first pass runs before any event, because the operator
	// starts with controllers already paired and possibly already
	// connected, and a restart must republish what the previous pod
	// published.
	// recorder posts the Events (events.go). It writes from its own
	// goroutine, which ends with ctx, so a pass never waits on it.
	recorder := events.New(ctx, client, component, events.Options{})
	held := newRelays(linuxInput{})
	held.metrics = readings
	held.recorder = recorder
	publish := &publisher{client: client, nodeName: nodeName, owner: owner, relays: held}
	keep := &bondStore{client: client, namespace: namespace, root: bondsRoot(), relays: held,
		watchSecrets: func(adapter bonds.Address) informer.View { return watchBondSecrets(ctx, watcher, namespace, adapter) }}
	restart := &privacyRestart{client: client, namespace: namespace, pod: podName, recorder: recorder}
	objects := newInventory(client, newBlueZRadio(conn), held, nodeName, namespace, readings)
	objects.recorder = recorder
	objects.inputs.recorder = recorder
	objects.node = nodeReference(owner)
	// The pass reads the objects the watches hold from their stores
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
	// The connector pages a device and the input recovery reconnects
	// one from their own goroutines, and the pass that reads the result
	// is the loop's, so a finished call is a wake like the kernel's and
	// the bus's.
	objects.connects.wake = wake
	objects.inputs.wake = wake
	// The pass reads the radio's address, and the edit watcher needs
	// it to select the Peripherals of that radio.
	objects.follow = edits.follow
	retryScheduled := false
	pass := func() {
		// The three parts of a pass run in order and all of them run.
		// The bond store also writes the radio's identity key
		// (identity.go). The
		// object reconcile runs first, because a bond's Secret is owned by
		// that bond's Peripheral and a device under teardown must leave the
		// slice before its bond is removed. A pairing that the slice
		// write failed to publish is still a key that must reach the
		// API, and a slice that the bonds could not be written for is
		// still the truth about this node's hardware.
		state := objects.reconcile()
		published := publish.reconcile(readPairedSet, readAdapter, state.keepOut)
		if published {
			objects.published()
		}
		bonded := keep.persist(readAdapter, state.owners, state.unpairing)
		identified := keep.persistIdentity(state.adapter)
		persisted := bonded && identified
		// The restart for a new privacy setting runs last, because it
		// deletes this pod, and it waits for a pass that stored every
		// bond and the identity key.
		restart.apply(state.adapter, state.privacy, persisted)
		if state.again > 0 {
			// A window that is open or a teardown between two of its steps
			// needs the next pass sooner than the backstop tick.
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
	// Each bonded controller's virtual input devices come back before
	// anything else runs, so a claim on a controller that is asleep is
	// prepared with the node the previous pod published.
	keep.restore(readAdapter)
	// Running containers still hold the claims the kubelet prepared
	// before this operator started, and their demand decides what
	// each pump reads, so it is restored before the first pass opens a
	// real node.
	held.restorePrepared()

	// The plugin registers with the kubelet only after bluetoothd is up
	// and the relays are restored, so the driver appears when it can
	// actually answer a prepare call.
	go func() {
		if err := serveDRAPlugin(ctx, client, held); err != nil {
			fatal("the DRA plugin is not serving: %v", err)
		}
	}()

	pass()

	switch err := loop(ctx, settled, attaches, blueZGone, pass, publish.attach); {
	case errors.Is(err, errShutdown):
		// The slice stays. The operator's pod restarts for ordinary
		// reasons while a consumer holds a prepared claim, and the
		// Node's ownership of the slice retracts it when this node
		// really leaves.
		return
	default:
		fatal("%v", err)
	}
}

// loop runs a pass for each settled wake and an attach for each
// uevent, until the operator must stop, and answers why it stopped.
// Both run on this goroutine, so the publisher has one owner.
func loop(ctx context.Context, settled, attaches, blueZGone <-chan struct{}, pass, attach func()) error {
	for {
		select {
		case <-ctx.Done():
			return errShutdown
		case <-blueZGone:
			// bluetoothd owns the HID sessions, and its death
			// disconnects every controller at once. An operator that
			// kept publishing would advertise controllers it can no
			// longer deliver. The published devices keep their taints
			// from the last pass until the replacement pod corrects
			// them.
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

// The ways the loop can end.
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

	// errBlueZGone ends the operator when bluetoothd leaves the bus.
	errBlueZGone = errors.New("bluetoothd left the bus")
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

// wakes merges the kernel's uevents, bluetoothd's signals, the
// PairingRequests that need a pass, the edits to Adapters and
// Peripherals, and the loop's own retries into one channel. None of
// them holds state that the loop uses, so the merge loses nothing:
// each wake means look again.
//
// Each uevent also goes to the second channel, which the loop reads
// without the settle window, so the relay opens a new evdev node at
// once. Every uevent goes there, not only an input add, because the
// uevent channel drops an event when it is full, and the dropped event
// can be the input add.
func wakes(ctx context.Context, uevents <-chan kernelEvent, blueZChanges, retries, requests, edits <-chan struct{}) (<-chan struct{}, <-chan struct{}) {
	out := make(chan struct{}, 1)
	attaches := make(chan struct{}, 1)
	send := func(to chan<- struct{}) {
		select {
		case to <- struct{}{}:
		default:
		}
	}
	wake := func() { send(out) }
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
				fmt.Println(kernelEventLine(event))
				send(attaches)
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
			case _, ok := <-requests:
				if !ok {
					return
				}
				wake()
			case _, ok := <-edits:
				if !ok {
					return
				}
				wake()
			case <-tick.C:
				wake()
			}
		}
	}()
	return out, attaches
}

// kernelEventLine says what one uevent reported. A HID event names its
// controller, and a power supply change names none, because the
// datagram carries no address. A lost datagram says so.
func kernelEventLine(event kernelEvent) string {
	if event.Lost {
		return "kernel: uevents were lost; reading the whole state"
	}
	if event.MAC == "" {
		return fmt.Sprintf("kernel: %s %s", event.Subsystem, event.Action)
	}
	return fmt.Sprintf("controller %s: %s %s", publishedMAC(event.MAC), event.Subsystem, event.Action)
}

// settle collapses a burst of events into one wake. It emits after
// the input has been quiet for window, or after limit has passed
// since the first event of the burst, whichever comes first.
//
// The limit keeps the loop publishing under a flapping controller.
// Without it, hardware that reconnects faster than the quiet window
// would restart the wait on every event and the loop would never
// write.
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
