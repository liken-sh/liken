// display-operator publishes each of a graphics card's monitor
// outputs as its own DRA device, so that a pod claims one screen by
// its connector name or by what the monitor is, and receives the
// Wayland socket and the app-id that put its window on that screen.
//
// It is an instance of liken's device operator pattern. The operator
// claims the card's display device through an ordinary liken.sh claim,
// runs Weston with the kiosk shell beside itself in the same pod, and
// publishes what the compositor drives under its own driver name,
// display.liken.sh. The operator uses no private interface into liken:
// the raw claim, the slices it writes, and the CDI files it leaves for
// the runtime are the public contracts that any DRA driver on any
// Kubernetes cluster gets.
//
// The claim does two jobs that a person would otherwise write down. It
// places the pod, because only a machine that has a graphics card
// publishes a display device, so no node selector names the machine
// with the monitors. And it arbitrates, because liken publishes the
// card node as an exclusive device, so the claim holder is the only
// program setting a mode on that card.
//
// The published outputs then arbitrate for every consumer: a screen
// is a resource the scheduler allocates once, not a name that any
// client can repeat from its config to take the same screen.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

const (
	// settleWindow is how long the loop waits for quiet after the last
	// event before it writes. One monitor plugged in produces a burst
	// of uevents, and the whole burst deserves one write.
	//
	// Every ResourceSlice write wakes every DRA-pending pod in the
	// cluster, because the scheduler event that a slice change raises
	// carries no queueing hint. A cable that flaps must not turn into
	// a cluster-wide scheduling storm.
	settleWindow = 1500 * time.Millisecond

	// settleLimit bounds the wait. A monitor that wakes and sleeps in
	// a loop restarts the quiet window forever, and the state it
	// settles on may never arrive, so the loop publishes what it can
	// see at this interval regardless.
	settleLimit = 10 * time.Second

	// backstopInterval is how often the loop reconciles with no event
	// to prompt it. The kernel drops uevent datagrams when its socket
	// buffer fills, and a dropped datagram costs one edge, so this
	// tick is what recovers the state after one.
	backstopInterval = 60 * time.Second

	// writeRetryDelay is how long the loop waits before it writes a
	// second time. One retry covers the conflict that a concurrent
	// writer causes and the API server that was restarting. Anything
	// past that is the next pass's work, and the backstop tick
	// guarantees there is one.
	//
	// The wait is a timer that wakes the loop, never a sleep inside
	// it. The same loop watches the compositor and the pod's own
	// shutdown, and a sleep there would leave a dead compositor
	// unreported for as long as it lasted.
	writeRetryDelay = 2 * time.Second
)

// westonConfigPath is where the operator writes the compositor's
// config. It is the pod's own filesystem, not a mount: the file
// describes the monitors this pod found, so no deployment supplies it
// and no edit to it survives a restart.
const westonConfigPath = "/run/weston/weston.ini"

// defaultSocketDir is where the compositor listens and where a
// consumer's container mounts. The path is the same on the host, in
// this pod, and in the consumer's container, because the CDI mount
// names one path for both ends.
const defaultSocketDir = "/var/run/display.liken.sh"

// socketName is the Wayland socket's name inside that directory, and
// the value of WAYLAND_DISPLAY that a consumer receives.
//
// It is a constant, not a setting. The CDI spec promises this name to
// every consumer, so an operator pod that inherited a WAYLAND_DISPLAY
// of its own would rename the socket its own devices promise, and
// every client would fail to connect with nothing to read that said
// why.
const socketName = "wayland-0"

// sysRoot is the sysfs mount this operator reads. It is a variable so
// the tests can point it at a directory they control.
var sysRoot = "/sys"

// driRoot is where the claim delivers the card node. The operator
// reads the name of its own card from here rather than naming one,
// because the kernel renumbers cards across a reboot.
var driRoot = "/dev/dri"

func main() {
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
	socketDir := envOr("SOCKET_DIR", defaultSocketDir)
	fmt.Printf("%s: operating the monitors on %s\n", DriverName, nodeName)

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

	cards, err := cardNode(driRoot)
	if err != nil {
		fatal("reading %s: %v", driRoot, err)
	}
	switch len(cards) {
	case 1:
	case 0:
		fatal("no card node in %s; does this pod claim a display device?", driRoot)
	default:
		fatal("this pod holds %d card nodes (%v); one compositor drives one card", len(cards), cards)
	}
	card := cards[0]

	// The outputs are enumerated once, here, and the config the
	// compositor reads is written from that enumeration. Every
	// connector gets an [output] section, dark or lit. The compositor
	// parses the file once, so the section for a connector must exist
	// before a monitor arrives on it.
	outputs := discoverOutputs(sysRoot, card)
	if len(outputs) == 0 {
		fatal("%s registers no connectors under %s/class/drm", card, sysRoot)
	}
	live := connected(outputs)
	if len(live) == 0 {
		// Every connector still publishes, tainted, so a person can
		// claim a screen that is cabled and asleep and the pod parks
		// until somebody wakes it. Whether the compositor tolerates
		// starting with no output is the compositor's answer, and it
		// gives it by exiting.
		fmt.Fprintf(os.Stderr, "%s has no monitor on any of its %d connectors\n", card, len(outputs))
	}
	for _, output := range live {
		monitor := monitorID(output.Monitor)
		if monitor == "" {
			monitor = "a monitor with no readable EDID"
		}
		fmt.Printf("%s: %s carries %s, app-id %s\n",
			DriverName, output.Connector, monitor, appID(output.Connector))
	}
	if err := writeWestonConfig(westonConfigPath, outputs); err != nil {
		fatal("writing %s: %v", westonConfigPath, err)
	}

	// The inventory publishes before the compositor runs, with every
	// device tainted, because no compositor serves any screen yet.
	// sysfs and the EDID say what is plugged in without a compositor,
	// so the attributes are complete. This write replaces the slice
	// the previous pod left. The first reconcile after the socket
	// appears removes the taint from every screen that has a monitor.
	// If the compositor never starts, the taint stays, and a claim
	// parks instead of taking a screen that no compositor drives.
	//
	// A client that was drawing before a restart keeps its screen.
	// The taint clears about three seconds after this write, and the
	// manual recommends a tolerationSeconds of 30, so the restart
	// never evicts the client.
	if err := EnsureResourceSlice(client, nodeName, owner, compositorDown(sliceDevices(outputs))); err != nil {
		fmt.Fprintf(os.Stderr, "publishing the outputs before the compositor starts: %v\n", err)
	}

	// libwayland creates the socket with the process umask and never
	// chmods it. A umask of 022 leaves the socket 0755, and connect()
	// needs write permission, so a client running under another uid is
	// refused. The compositor inherits the umask at start, and the
	// operator restores its own directly after, so nothing else this
	// process creates is world-writable.
	unix.Umask(0)
	westonExit, err := startWeston(ctx, card, westonConfigPath, socketDir, socketName)
	unix.Umask(0o022)
	if err != nil {
		fatal("starting the compositor: %v", err)
	}
	if err := waitForSocket(ctx, socketDir+"/"+socketName, socketWaitTimeout, westonExit); err != nil {
		fatal("%v", err)
	}

	// The plugin registers with the kubelet only after the socket
	// exists, so the driver appears when it can actually answer a
	// prepare call. What a prepared claim delivers is that socket.
	plugin := &draPlugin{
		client:    client,
		sysRoot:   sysRoot,
		card:      card,
		socketDir: socketDir,
	}
	go func() {
		if err := serveDRAPlugin(ctx, plugin); err != nil {
			fatal("the DRA plugin is not serving: %v", err)
		}
	}()

	uevents, err := listenForUevents(ctx)
	if err != nil {
		fatal("watching for kernel events: %v", err)
	}

	// A write that failed asks for one more pass through the same
	// channel every other source uses, so the retry costs the loop no
	// time and takes the same settle window.
	retries := make(chan struct{}, 1)
	publish := func() {
		if err := reconcile(client, nodeName, owner, card); err != nil {
			fmt.Fprintf(os.Stderr, "publishing the slice: %v; retrying in %s\n", err, writeRetryDelay)
			time.AfterFunc(writeRetryDelay, func() {
				select {
				case retries <- struct{}{}:
				default:
				}
			})
		}
	}
	settled := settle(ctx, wakes(ctx, uevents, retries), settleWindow, settleLimit)

	// The first pass runs before any event. It removes the taint that
	// the startup publish put on every screen that has a monitor. It
	// also catches a monitor that arrived while the compositor was
	// starting, before this process listened on the kernel's socket.
	publish()

	for {
		select {
		case <-ctx.Done():
			// Nothing is retracted. The slice outlives the pod on
			// purpose: a consumer's allocation names a device, and a
			// device that leaves the inventory strands the kubelet's
			// prepare call with no bound on its retry. The Node owns
			// the slice, so a node that leaves the cluster is what
			// takes it away.
			return
		case err := <-westonExit:
			if ctx.Err() != nil {
				// The compositor ended because this process is ending.
				return
			}
			// The compositor holds the screens and the socket, so its
			// death ends every client's Wayland connection at once.
			// Taint every output before this process ends, because
			// that write is the only thing that evicts those clients:
			// the replacement pod publishes the same devices again,
			// and a slice that does not change raises no scheduler
			// event.
			if writeErr := EnsureResourceSlice(client, nodeName, owner,
				compositorDown(sliceDevices(discoverOutputs(sysRoot, card)))); writeErr != nil {
				fmt.Fprintf(os.Stderr, "tainting the outputs after the compositor exited: %v\n", writeErr)
			}
			fatal("the compositor exited: %v", err)
		case _, ok := <-settled:
			if !ok {
				if err := eventsEnded(ctx); err != nil {
					fatal("%v", err)
				}
				return
			}
			publish()
		}
	}
}

// eventsEnded says what a closed wake channel means.
//
// It means nothing while the process is stopping, which is how every
// shutdown ends. Any other time it means the kernel's uevent socket
// closed under a running operator, and an operator that kept going
// would publish only on the backstop tick, minutes after a monitor
// moved. The pod's restart is the repair.
func eventsEnded(ctx context.Context) error {
	if ctx.Err() != nil {
		return nil
	}
	return errors.New("the kernel event stream ended while the operator was running")
}

// reconcile makes the published slice agree with what sysfs says about
// the card's connectors right now. The caller schedules another pass
// when this one returns an error.
//
// A pass that finds no connector writes nothing. The card registers
// its connectors when the driver binds and keeps them until the card
// leaves, so an empty answer means the card is going away or the walk
// read the wrong path, and publishing it would delete every device a
// consumer holds.
func reconcile(client *Client, nodeName string, owner OwnerReference, card string) error {
	outputs := discoverOutputs(sysRoot, card)
	if len(outputs) == 0 {
		return fmt.Errorf("%s registers no connectors, so the published slice stays as it is", card)
	}
	return EnsureResourceSlice(client, nodeName, owner, sliceDevices(outputs))
}

// wakes turns the kernel's drm events and the write retries into one
// channel of wakes, with a backstop tick in it. Nothing on any of them
// carries state that the loop uses: they say to look again, and the
// look is a fresh read of sysfs.
func wakes(ctx context.Context, uevents <-chan drmEvent, retries <-chan struct{}) <-chan struct{} {
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
				fmt.Printf("drm %s: %s\n", event.Action, event.DevPath)
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

// settle collapses a burst of events into one wake. It emits after the
// input has been quiet for window, or after limit has passed since the
// first event of the burst, whichever comes first.
//
// The limit is what keeps a flapping cable publishing. Without it,
// hardware that changes faster than the quiet window would restart the
// wait on every event and the loop would never write.
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

// envOr reads one setting from the pod's environment, with the value
// the deployment usually leaves alone as the fallback.
func envOr(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}

func fatal(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}
