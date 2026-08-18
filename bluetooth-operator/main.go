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
// holds, a Pairing for each bond, and the PairingRequests a person
// creates to open a pairing window. Those objects are the reason a
// person never needs a shell in this pod.
//
// Five sources drive the loop, and each one only says that something
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
)

const (
	// settleWindow is how long the loop waits for quiet after the last
	// event before it writes. A controller that connects produces a
	// burst of uevents and a burst of D-Bus signals, and one write
	// covers the whole burst.
	//
	// Every ResourceSlice write wakes every DRA-pending pod in the
	// cluster, because the scheduler event that a slice change raises
	// includes no queueing hint. Hardware that flaps must not turn into
	// a cluster-wide scheduling storm.
	settleWindow = 1500 * time.Millisecond

	// settleLimit bounds the wait. A controller that reconnects in a
	// loop restarts the quiet window forever, and the state it settles
	// on may never arrive, so the loop publishes what it reads at
	// this interval regardless.
	settleLimit = 10 * time.Second

	// backstopInterval is how often the loop reconciles with no event
	// to prompt it. The kernel drops uevent datagrams when its socket
	// buffer fills, and a dropped datagram costs one edge, so this
	// tick recovers the state after one.
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

	// requestPoll is how often the operator asks the API server whether
	// a PairingRequest needs attention. A request is a small object that
	// a person creates and edits by hand, and neither the kernel nor
	// bluetoothd raises an event when that happens, so this is the one
	// place the loop polls. Five seconds is the delay a person accepts
	// between creating a request and reading the window it opened.
	requestPoll = 5 * time.Second
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
	// The bonds are stored in one Secret for each bond, in this pod's
	// own namespace, and the downward API is where a pod reads which
	// namespace that is. The same variable names the same Secrets for
	// bondfetch, which restored them before bluetoothd started.
	namespace := os.Getenv(namespaceVar)
	if namespace == "" {
		fatal("%s is unset; the pod spec must supply it from metadata.namespace", namespaceVar)
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

	// retries carries the passes the loop asks itself for: the one that
	// follows a failed write, and the follow-up a pairing window or an
	// unpair asks for while it is between two of its steps. Both are
	// sources of wakes like the kernel and the bus are, so they go
	// through the same settle window as everything else.
	retries := make(chan struct{}, 1)
	requests := watchPairingRequests(ctx, client, requestPoll, time.Now)
	settled := settle(ctx, wakes(ctx, uevents, blueZChanges, retries, requests), settleWindow, settleLimit)

	// The first pass runs before any event, because the operator
	// starts with controllers already paired and possibly already
	// connected, and a restart must republish what the previous pod
	// published.
	publish := &publisher{client: client, nodeName: nodeName, owner: owner}
	keep := &bondStore{client: client, namespace: namespace, root: bondsRoot()}
	objects := newInventory(client, newBlueZRadio(conn), nodeName, namespace)
	readPairedSet := func() (map[string]controller, error) { return pairedControllers(conn) }
	readAdapter := func() (bonds.Address, error) { return adapterAddress(conn) }
	wakeSoon := func(after time.Duration) {
		time.AfterFunc(after, func() {
			select {
			case retries <- struct{}{}:
			default:
			}
		})
	}
	retryScheduled := false
	pass := func() {
		// The three parts of a pass run in order and all of them run. The
		// object reconcile runs first, because a bond's Secret is owned by
		// that bond's Pairing and a device under teardown must leave the
		// slice before its bond is removed. A pairing that the slice
		// write failed to publish is still a key that must reach the
		// API, and a slice that the bonds could not be written for is
		// still the truth about this node's hardware.
		state := objects.reconcile()
		published := publish.reconcile(readPairedSet, readAdapter, state.keepOut)
		if published {
			objects.published()
		}
		persisted := keep.persist(readAdapter, state.owners, state.unpairing)
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
	pass()

	for {
		select {
		case <-ctx.Done():
			// The slice stays. The operator's pod restarts for ordinary
			// reasons while a consumer holds a prepared claim, and the
			// Node's ownership of the slice retracts it when this node
			// really leaves.
			return
		case <-blueZGone:
			// bluetoothd owns the HID sessions, and its death
			// disconnects every controller at once. An operator that
			// kept publishing would advertise controllers it can no
			// longer deliver. The published devices keep their taints
			// from the last pass until the replacement pod corrects
			// them.
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

// wakes merges the kernel's HID events, bluetoothd's signals, the
// PairingRequests that need a pass, and the loop's own retries into
// one channel. None of them holds state that the loop uses, so the
// merge loses nothing: each wake means look again.
func wakes(ctx context.Context, uevents <-chan hidEvent, blueZChanges, retries, requests <-chan struct{}) <-chan struct{} {
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
			case _, ok := <-requests:
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
