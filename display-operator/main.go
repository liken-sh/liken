// display-operator publishes each of a graphics card's monitor
// outputs as its own DRA device. A pod claims one screen by its
// connector name or by what the monitor is, and receives the
// Wayland socket and the app-id that put its window on that screen.
//
// It is an instance of liken's device operator pattern. The operator
// claims the card's display device through an ordinary liken.sh claim
// and publishes what the compositor drives under its own driver name,
// display.liken.sh.
//
// Weston with the kiosk shell runs in a container of its own in the
// same pod. The kubelet starts it, restarts it when it dies, and
// stops it, so no process in this pod supervises another.
//
// The operator uses no private interface into liken. The raw claim,
// the slices it writes, and the CDI files it leaves for the runtime
// are the public contracts that any DRA driver on any cluster gets.
//
// The claim does two jobs that a person would otherwise write down. It
// places the pod, because only a machine that has a graphics card
// publishes a display device, so no node selector names the machine
// with the monitors. And it arbitrates, because liken publishes the
// card node as an exclusive device, so the claim holder is the only
// program setting a mode on that card.
//
// The published outputs then arbitrate for every consumer: the
// scheduler allocates a screen once, and a client cannot take the
// same screen by repeating its name from a config.
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
	// event before it writes. One monitor plugged in produces a burst
	// of uevents, and one write must cover the whole burst.
	//
	// Every ResourceSlice write wakes every DRA-pending pod in the
	// cluster, because the scheduler event that a slice change raises
	// includes no queueing hint. A cable that flaps must not turn into
	// a cluster-wide scheduling storm.
	settleWindow = 1500 * time.Millisecond

	// settleLimit bounds the wait. A monitor that wakes and sleeps in
	// a loop restarts the quiet window forever, and the state it
	// settles on may never arrive, so the loop publishes what it
	// reads at this interval regardless.
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

// westonConfigPath is where the declare container writes the
// compositor's config and where the compositor's container reads it.
//
// The volume the two containers share is the pod's own: the file
// describes the monitors this pod found, so no deployment supplies
// it, and no edit to it survives the pod. It is a variable so the
// tests can point it at a directory they control.
var westonConfigPath = "/etc/weston/weston.ini"

// ModeRecordPath is where the operator records the mode each
// claim asked for, in the same volume as the config.
//
// The record is the operator's own file, and weston.ini is
// derived from it and the connector walk on every write. The record is
// what the declare container reads to build the config the compositor
// starts from, so a compositor that restarts comes back at the modes
// the held claims stated. The volume dies with the pod, so a machine
// with no consumer left comes up at every monitor's preferred mode.
var modeRecordPath = "/etc/weston/modes.json"

// ProcRoot is the process tree this operator reads to find the
// compositor. The pod shares one process namespace, so the
// compositor's process is in this one. It is a variable so the tests
// can point it at a directory they control.
var procRoot = "/proc"

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

// main selects the role from the command line.
//
// The pod runs this one image three times. The declare init
// container and the compositor's container each name their role in
// an argument, and the operator container runs with none.
func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case declareMode:
			declare()
			return
		case compositorMode:
			compose()
			return
		}
	}
	operate()
}

// claimedCard names the one card node the pod's claim delivered.
//
// All three roles read the same directory rather than taking a
// device path from anywhere, because the kernel renumbers cards
// across a reboot and no manifest can name one.
func claimedCard() string {
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
	return cards[0]
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

	card := claimedCard()
	socketPath := socketDir + "/" + socketName

	// The plugin registers whether or not a compositor serves. A
	// prepare call that arrives while the socket is gone must be
	// refused with a reason, and an unregistered driver answers with
	// nothing at all.
	plugin := newDRAPlugin(client, card, socketDir)

	uevents, err := listenForUevents(ctx)
	if err != nil {
		fatal("watching for kernel events: %v", err)
	}

	// The Display controller runs beside the slice publisher and
	// writes the panels' own resources. It reads the same connectors
	// and shares the probe cache, so the two never ask one panel twice.
	// Its own loop is what keeps an override off the slice publisher's
	// settle window.
	panels := newDisplayControl(client, nodeName, plugin.controls, func() []Output {
		return screens(card, plugin.currentModes, plugin.connectorModes)
	})
	// The mode seams are the prepare path's own, so a resting
	// mode and a claim's mode take one road to the compositor and hold
	// one lock between them. The heal's restart is that road with no
	// config change.
	panels.setMode = func(ctx context.Context, output Output, mode string) error {
		return plugin.applyMode(ctx, output, mode)
	}
	panels.restart = plugin.restartCompositor
	// The compositor's own registry reports when an output was
	// destroyed and re-created. The watch holds one standing
	// connection to the compositor's socket, and every restart the
	// operator makes ends that connection, so the operator's own
	// restarts report nothing.
	watch := newOutputWatch(socketPath, panels.outputsMoved)
	// The same connection answers the mode readback: a switch
	// waits for the compositor that started after its restart to
	// report the mode the claim stated.
	plugin.served = watch.served
	// The same connection fills the second half of the mode the
	// Display reports: status.mode.kernel is what the card is synced
	// to, and status.mode.weston is what the compositor serves
	// canvases at.
	panels.served = watch.served
	go watch.run(ctx)
	go panels.run(ctx)
	go watchDisplays(ctx, client, panels.wake)

	// A write that failed schedules one more pass through the same
	// channel every other source uses. The retry costs the loop no
	// time and takes the same settle window.
	retries := make(chan struct{}, 1)
	publish := func() {
		if err := reconcile(client, nodeName, owner, card, socketPath, plugin.currentModes, plugin.controls); err != nil {
			fmt.Fprintf(os.Stderr, "publishing the slice: %v; retrying in %s\n", err, writeRetryDelay)
			time.AfterFunc(writeRetryDelay, func() {
				select {
				case retries <- struct{}{}:
				default:
				}
			})
		}
		// Hardware that moved reaches the Display controller
		// through the same settled pass, so one burst of uevents costs
		// one pass over the resources.
		panels.wake()
	}

	// A prepare republishes through the same pass every event takes,
	// so the mode list it read reaches the slice without waiting for
	// a wake. The seam is assigned before the plugin serves, because
	// the kubelet calls into the plugin from its own goroutine and a
	// later write would race it.
	plugin.republish = publish
	go func() {
		if err := serveDRAPlugin(ctx, plugin); err != nil {
			fatal("the DRA plugin is not serving: %v", err)
		}
	}()
	settled := settle(ctx, wakes(ctx, uevents, retries, watchSocket(ctx, socketPath)), settleWindow, settleLimit)

	// The first pass runs before any event. It replaces the slice the
	// previous pod left and states whether a compositor serves right
	// now, tainted if the socket is not up yet.
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

// One walk of the card for the Display controller: the
// connectors from sysfs, the mode each output drives, and the modes
// each connector offers. It is the same pair of reads the slice pass
// makes, through the same seams, so the resource and the slice report
// one card and cannot disagree about it. A read that fails costs the
// field it fills and nothing else.
func screens(card string,
	currentModes func() (map[string]string, error),
	connectorModes func() (map[string][]drmMode, error),
) []Output {
	outputs := discoverOutputs(sysRoot, card)
	current, err := currentModes()
	if err != nil {
		fmt.Fprintf(os.Stderr, "reading the mode each output runs: %v\n", err)
	}
	offered, err := connectorModes()
	if err != nil {
		fmt.Fprintf(os.Stderr, "reading the modes each connector offers: %v\n", err)
	}
	return withOfferedModes(withCurrentModes(outputs, current), offered)
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
// the card's connectors right now, and with whether a compositor
// serves them. The caller schedules another pass when this one returns
// an error.
//
// A pass that finds no connector writes nothing. The card registers
// its connectors when the driver binds and keeps them until the card
// leaves, so an empty answer means the card is going away or the walk
// read the wrong path, and publishing it would delete every device a
// consumer holds.
//
// Every pass reads the connectors again, so the pass that follows a
// compositor's return carries the mode list the kernel re-probed on
// the way up. A list read too early stays stale only until the next
// wake.
//
// CurrentModes is the card's own answer about what each output
// runs right now. The pass publishes it as an attribute, so the slice
// always says what a claim's mode did and what a mode a claim left
// behind is still doing. It is the same read the prepare path makes,
// so the slice and a delivery never disagree.
//
// A read that fails costs the attribute and nothing else. The
// rest of the slice is what sysfs says, and a card that cannot answer
// the ioctl still has connectors, monitors, and a compositor.
func reconcile(client *Client, nodeName string, owner OwnerReference, card, socketPath string,
	currentModes func() (map[string]string, error), controls *panelControls) error {
	outputs := discoverOutputs(sysRoot, card)
	if len(outputs) == 0 {
		return fmt.Errorf("%s registers no connectors, so the published slice stays as it is", card)
	}
	modes, err := currentModes()
	if err != nil {
		fmt.Fprintf(os.Stderr, "reading the mode each output runs: %v\n", err)
	}
	// The pass asks each panel what controls it carries. The answer
	// is cached against the monitor's EDID, so a pass over unchanged
	// hardware sends nothing on any i2c wire, and a panel that refuses
	// DDC/CI publishes no control attribute and no control device.
	devices := sliceDevices(withControls(withCurrentModes(outputs, modes), controls))
	if !compositorServing(socketPath) {
		// No compositor holds the screens, so every output says it
		// serves nobody, and the NoExecute taint is what ends the
		// clients whose connections died with the socket.
		devices = compositorDown(devices)
	}
	return EnsureResourceSlice(client, nodeName, owner, devices)
}

// watchSocket wakes the loop when a compositor starts answering on the
// socket and when it stops.
//
// A compositor that comes or goes raises no event a program can
// wait on, so the watch connects on a tick, and the change from one
// reading to the next is the whole signal. The pass it wakes is what
// taints or frees the screens.
func watchSocket(ctx context.Context, socketPath string) <-chan struct{} {
	out := make(chan struct{}, 1)
	// The first reading is taken before the ticker starts, so it is
	// the same state the caller's first pass publishes, and no change
	// falls between the two.
	serving := compositorServing(socketPath)
	go func() {
		defer close(out)
		tick := time.NewTicker(socketWatchInterval)
		defer tick.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
				now := compositorServing(socketPath)
				if now == serving {
					continue
				}
				serving = now
				fmt.Printf("the compositor's socket at %s: serving=%v\n", socketPath, serving)
				select {
				case out <- struct{}{}:
				default:
				}
			}
		}
	}()
	return out
}

// wakes turns the kernel's drm events, the write retries, and the
// compositor's socket into one channel of wakes, with a backstop tick
// in it. Nothing on any of them holds state that the loop uses: each
// wake means look again, and the look is a fresh read of sysfs.
func wakes(ctx context.Context, uevents <-chan drmEvent, retries, sockets <-chan struct{}) <-chan struct{} {
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
			case _, ok := <-sockets:
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
