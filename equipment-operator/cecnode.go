package main

// The node workload, `equipment-operator cec`: one pod on each machine
// that carries a CEC adapter. The pod holds the DRA claim on the
// adapter's -cec device, finds the CECBus that names its machine, runs
// the adapter in the bus's mode, and writes its own entry under that
// bus's status.adapters. It needs no host network, no broker, and no
// receiver credentials. plans/09-cec.md gives the design.

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/liken-sh/equipment-operator/cec"
)

// The node workload's environment.
const (
	// NODE_NAME is the node the pod runs on. liken names each node after
	// its Machine, so it is also the machine a CECBus names.
	nodeNameVariable = "NODE_NAME"
	// CEC_DEVICE names the adapter node, for a run by hand. The pod
	// leaves it unset and finds the one node its claim delivered.
	cecDeviceVariable = "CEC_DEVICE"
)

// How often the node workload writes its entry when nothing changed.
// The time of each write is the entry's reportedAt, and the Deployment
// treats an entry as stale once it is three intervals old, so a pod
// that dies without a word stops counting within a few minutes. The
// write goes to the API server and sends nothing on the CEC wire.
var cecReportInterval = 30 * time.Second

// findAdapter answers the one CEC node the pod's claim delivered. The
// claim is exclusive and names one device, so the container's /dev
// holds one /dev/cecN.
func findAdapter(devices string) (string, error) {
	if path := os.Getenv(cecDeviceVariable); path != "" {
		return path, nil
	}
	found, _ := filepath.Glob(filepath.Join(devices, "cec*"))
	if len(found) == 0 {
		return "", fmt.Errorf("no CEC adapter in %s: the pod's claim delivered no cec node", devices)
	}
	slices.Sort(found)
	return found[0], nil
}

// runCEC is the node workload's main. Every failure ends the process
// with the error as its last log line. The kubelet restarts the
// container, and a new container receives the adapter's current node,
// which is how the pod finds an adapter again after an unplug.
func runCEC() {
	// The kubelet stops a pod with SIGTERM. The context ends on it, so the
	// node workload releases the adapter before the process exits.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, os.Interrupt)
	defer stop()
	if err := serveCEC(ctx, "/dev"); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// serveCEC opens the adapter the claim delivered under devices and
// runs the node workload until ctx ends or the adapter fails.
func serveCEC(ctx context.Context, devices string) error {
	machine := os.Getenv(nodeNameVariable)
	if machine == "" {
		return fmt.Errorf("%s is unset; the DaemonSet must state the node", nodeNameVariable)
	}
	path, err := findAdapter(devices)
	if err != nil {
		return err
	}
	device, err := cec.Open(path)
	if err != nil {
		return err
	}
	defer device.Close()
	client, err := InClusterClient()
	if err != nil {
		return fmt.Errorf("in-cluster config: %w", err)
	}
	node, err := newCECNode(client, machine, device)
	if err == nil {
		err = node.run(ctx)
	}
	if err != nil {
		return fmt.Errorf("the CEC adapter on %s: %w", machine, err)
	}
	return nil
}

// cecNode is one adapter on one machine.
type cecNode struct {
	client *Client
	// displays holds the Display watch's store, which each pass reads.
	// The node workload writes no Display.
	displays *watchStore

	machine   string
	device    *cec.Device
	caps      cec.Caps
	directory *cec.Directory
	now       func() time.Time
	// log takes the lines a person reads to follow the adapter: what it
	// held when the pod opened it, and each change of the entry's state.
	log    io.Writer
	wake   chan struct{}
	dirty  chan struct{}
	failed chan error
	// retryAsk asks the loop for a pass after cecAPIRetry.
	retryAsk chan struct{}

	mutex sync.Mutex
	// bus is the CECBus this adapter reports to, and applied is the
	// configuration the adapter runs.
	bus     string
	applied adapterConfig
	// entry is the report as it stands now, and written is the last
	// report the API server accepted, with no reportedAt.
	entry   CECAdapterStatus
	written *CECAdapterStatus
	// resync asks the next pass to read the adapter's addresses from
	// the kernel, because the kernel reported a state change.
	resync bool
	// display is the last good read of the Display the adapter speaks
	// for, and displayNote is why the latest read failed, if it did.
	display     adapterDisplay
	displayNote string
	// retryAt is when a join that failed is tried again, and retryWait
	// is the wait that set it.
	retryAt   time.Time
	retryWait time.Duration
	// logged is the state and the message the log last stated.
	logged CECAdapterStatus
	// declared is the set of a person's buses that last named the
	// machine, as the log last stated it.
	declared string
	// stopMode ends the work of the mode the adapter runs, the scan,
	// the introductions, and a power application, and returns once all
	// of it has stopped. modeContext ends with the mode, and modeWork
	// counts the work, so a power application that starts later in the
	// mode stops with it. Only the loop's goroutine starts or stops the
	// mode's work.
	stopMode    func()
	modeContext context.Context
	modeWork    *sync.WaitGroup
	// powered is what the node workload holds about its applications of
	// a Television's spec.power, woken what it holds about its wakes of
	// a Television's session, and standby what it holds about the
	// standbys a power press asked of the session.
	powered powerMemory
	woken   wakeMemory
	standby standbyMemory
	// powerRead is what the node workload holds about the power reads a
	// power press asked for.
	powerRead powerReadMemory
	// arrivals takes each device the adapter heard arrive in Control to
	// the mode's work, which asks it for its facts. introduced maps each
	// device the adapter found or introduced to the physical address it
	// held then. Both belong to the mode, and startMode makes them anew.
	arrivals   chan cec.LogicalAddress
	introduced map[cec.LogicalAddress]cec.PhysicalAddress
	// tvPowerAsk takes one request to ask the TV for its power after it
	// announced itself with no known power, and holds at most one, so a
	// burst of announcements queues one question. tvPowerAskedAt is the
	// physical address the TV announced for the last such question; the
	// TV is not asked again at that address while its power stays
	// unknown. Both belong to the mode, and startMode makes them anew.
	tvPowerAsk     chan struct{}
	tvPowerAskedAt *cec.PhysicalAddress
	// tvPowerRead is the read of the TV's power in flight, which every
	// reader shares; askPower holds the rule.
	tvPowerRead tvPowerFlight
	// lastCommand is when the TV was last sent a power command: by this
	// adapter, or a Standby by another device that the adapter heard,
	// such as a receiver that turns the TV off with itself. A TV answers
	// its old state for a while after either.
	lastCommand time.Time
	// source is the physical address of the last Active Source the
	// adapter heard or sent, and sources wakes a wake that watches for
	// another source's claim.
	source  cec.PhysicalAddress
	sources chan struct{}
}

// newCECNode reads the adapter's capabilities, which every later
// choice depends on.
func newCECNode(client *Client, machine string, device *cec.Device) (*cecNode, error) {
	caps, err := device.Caps()
	if err != nil {
		return nil, err
	}
	return &cecNode{
		client:    client,
		machine:   machine,
		device:    device,
		caps:      caps,
		directory: cec.NewDirectory(),
		now:       time.Now,
		log:       os.Stderr,
		wake:      make(chan struct{}, 1),
		dirty:     make(chan struct{}, 1),
		retryAsk:  make(chan struct{}, 1),
		failed:    make(chan error, 1),
		stopMode:  func() {},
		displays:  &watchStore{},
		source:    cec.InvalidPhysicalAddress,
		sources:   make(chan struct{}, 1),
	}, nil
}

// run reads the adapter and follows the CECBus collection until ctx
// ends or a call on the adapter fails. Either way it takes the adapter
// off the bus and writes a Stopped entry before it returns.
func (n *cecNode) run(ctx context.Context) error {
	n.logOpened()
	loop, cancel := context.WithCancel(ctx)
	var started sync.WaitGroup
	started.Go(func() {
		if err := cec.Read(loop, n.device, n.heard, n.adapterChanged); err != nil {
			n.fail(err)
		}
	})
	err := n.loop(loop, &started)
	// Nothing the loop started may read the adapter or the API once
	// run takes the adapter off the bus.
	cancel()
	n.stopMode()
	started.Wait()
	cause := fmt.Sprintf("the node workload stopped: %v", context.Cause(ctx))
	if err != nil {
		cause = fmt.Sprintf("the adapter failed: %v", err)
	}
	n.stop(cause)
	return err
}

// logOpened states the logical addresses the kernel holds for the
// adapter when the pod opens it, before the pod clears or claims
// anything. A previous pod that stopped cleanly released the adapter,
// so this line shows whether it did.
func (n *cecNode) logOpened() {
	held, err := n.device.Addresses()
	switch {
	case err != nil:
		fmt.Fprintf(n.log, "the adapter on %s (%s) did not report its logical addresses when the pod opened it: %v\n", n.machine, n.caps.Driver, err)
	case len(held.Logical) == 0:
		fmt.Fprintf(n.log, "the adapter on %s (%s) holds no logical address when the pod opens it\n", n.machine, n.caps.Driver)
	default:
		addresses := make([]string, 0, len(held.Logical))
		for _, address := range held.Logical {
			addresses = append(addresses, fmt.Sprint(uint8(address)))
		}
		noun := "address"
		if len(addresses) > 1 {
			noun = "addresses"
		}
		fmt.Fprintf(n.log, "the adapter on %s (%s) holds logical %s %s as %q when the pod opens it\n",
			n.machine, n.caps.Driver, noun, strings.Join(addresses, ", "), held.OSDName)
	}
}

// logState states a change of the entry's state or message. A report
// that changes only the devices or the time logs nothing, so the log
// stays quiet while the adapter learns the bus and reports.
func (n *cecNode) logState(bus string, entry CECAdapterStatus) {
	if entry.State == n.logged.State && entry.Message == n.logged.Message {
		return
	}
	from := string(n.logged.State)
	if from == "" {
		from = "none"
	}
	line := fmt.Sprintf("CECBus %s: the adapter on %s went from %s to %s", bus, n.machine, from, entry.State)
	if entry.Message != "" {
		line += ": " + entry.Message
	}
	fmt.Fprintln(n.log, line)
	n.logged = entry
}

// loop is run's body: the first lists, the watches, and the passes.
//
// The node workload follows one rule for every state it keeps current,
// on the wire and in the API. It subscribes first, then it reads the
// state once as a baseline, and after that only the subscription's
// events change what it holds. It subscribes first so that a change
// during the read is not lost. When a subscription fails, it subscribes
// again and reads again. On the wire, the subscription is the follower
// or monitor mode that makes the kernel pass the adapter what it hears,
// which the handle takes before it claims an address, and the baseline
// is the scan when the adapter joins; startMode states the rest. In the
// API, the subscriptions are the watches of CECBuses, Televisions, and
// Displays, and the baseline is each pass's read: a list of the
// CECBuses and the Televisions, which the node workload writes, and
// the Display watch's store (watchcache.go). client-go's
// reflector runs each watch: it resumes a watch that drops from its
// last version and reads again after a 410, and the watch wakes the
// loop when its own first read is done (watch.go). No timer re-reads
// a state. The timers here are clocks: the heartbeat that keeps
// reportedAt current, and the retry of a join or an API call that
// failed.
func (n *cecNode) loop(ctx context.Context, started *sync.WaitGroup) error {
	err := retryThrottled(ctx, func() error {
		_, err := ListCECBuses(n.client)
		return err
	})
	if ctx.Err() != nil {
		return nil
	}
	if err != nil {
		return fmt.Errorf("listing CECBuses: %w", err)
	}
	started.Go(func() { watchCECBuses(ctx, n.client, n.wake, nil, nil) })
	// A change of a Television's spec.power or status.session wakes the
	// loop, because the pass is where the adapter acts on it. A change of
	// a Display's physical address wakes it too, because the adapter
	// announces that address. A cluster without a definition lists no
	// version and gets no watch yet; watchLater starts it once a pass
	// lists a version.
	watches := []*lateWatch{
		{path: televisionsPath, watch: watchTelevisions, list: func() (string, error) {
			listed, err := ListTelevisions(n.client)
			if err != nil {
				return "", err
			}
			return listed.Metadata.ResourceVersion, nil
		}},
		{path: displaysPath, watch: watchAllDisplays, held: n.displays, list: func() (string, error) {
			listed, err := ListDisplays(n.client)
			if err != nil {
				return "", err
			}
			return listed.Metadata.ResourceVersion, nil
		}},
	}
	if err := n.watchLater(ctx, started, watches, true); err != nil {
		return err
	}
	heartbeat := time.NewTicker(cecReportInterval)
	defer heartbeat.Stop()
	var retry <-chan time.Time
	force := true
	for {
		if err := n.pass(ctx); err != nil {
			return err
		}
		n.report(force)
		force = false
		_ = n.watchLater(ctx, started, watches, false)
		retry = nil
		if wait, due := n.retryIn(); due {
			retry = time.After(wait)
		}
		if done, err := n.await(ctx, heartbeat.C, retry); done {
			return err
		}
	}
}

// await waits for the loop's next pass: an event, or the retry clock. The
// heartbeat and a request for a retry are handled here and run no
// pass. done says the node workload ends, with err as its cause.
func (n *cecNode) await(ctx context.Context, heartbeat <-chan time.Time, retry <-chan time.Time) (bool, error) {
	for {
		select {
		case <-ctx.Done():
			return true, nil
		case err := <-n.failed:
			return true, err
		case <-n.wake:
			return false, nil
		case <-n.dirty:
			return false, nil
		case <-retry:
			return false, nil
		case <-n.retryAsk:
			if retry == nil {
				retry = time.After(cecAPIRetry)
			}
		case <-heartbeat:
			// The heartbeat is a clock and reads nothing: it writes the
			// entry again so its reportedAt stays current, and runs no pass.
			n.report(true)
		}
	}
}

// lateWatch is one watch the loop starts once its collection lists a
// version: at once for a cluster with the definition, and at a later
// pass for a definition installed after the node workload started.
type lateWatch struct {
	path    string
	watch   func(context.Context, *Client, chan<- struct{}, func(), *watchStore)
	held    *watchStore
	list    func() (string, error)
	running bool
}

// watchLater starts each watch that is not running and whose collection
// now lists a version. first says the loop is starting, when a list the
// API server refuses ends the node workload, as the first CECBus list
// does. A later list that fails is tried again at the next pass.
func (n *cecNode) watchLater(ctx context.Context, started *sync.WaitGroup, watches []*lateWatch, first bool) error {
	for _, watch := range watches {
		if watch.running {
			continue
		}
		var version string
		err := retryThrottled(ctx, func() error {
			var err error
			version, err = watch.list()
			return err
		})
		if ctx.Err() != nil {
			return nil
		}
		if err != nil && first {
			return fmt.Errorf("listing %s: %w", watch.path, err)
		}
		if err != nil || version == "" {
			continue
		}
		watch.running = true
		start, held := watch.watch, watch.held
		started.Go(func() { start(ctx, n.client, n.wake, nil, held) })
	}
	return nil
}

// cecAPIRetry is how long the loop waits before it runs a pass again
// after an API call failed with no event to follow, such as a list the
// API server refused or a result it did not accept.
var cecAPIRetry = 10 * time.Second

// retryLater asks the loop for a pass after cecAPIRetry, because an API
// call failed and no event will follow it. A pass that runs sooner for
// an event also tries again.
func (n *cecNode) retryLater() {
	poke(n.retryAsk)
}

// retryIn answers how long the loop waits before its next pass when no
// event arrives, and false when it waits for an event alone: a join
// that failed is tried again at its retryAt, and a result the API
// server has not accepted after cecAPIRetry.
func (n *cecNode) retryIn() (time.Duration, bool) {
	n.mutex.Lock()
	defer n.mutex.Unlock()
	unwritten := n.powered.unwritten != nil || n.woken.unwritten != nil || n.standby.unwritten != nil
	joining := (n.entry.State == AdapterJoining || n.entry.State == AdapterRefused) && n.applied.problem == ""
	switch {
	case joining && n.entry.Machine != "":
		wait := max(n.retryAt.Sub(n.now()), 0)
		if unwritten {
			wait = min(wait, cecAPIRetry)
		}
		return wait, true
	case unwritten:
		return cecAPIRetry, true
	}
	return 0, false
}

// stop takes the adapter off the bus and writes the entry's last
// state. The kernel keeps a claim after its handle closes, and a
// Pulse-Eight answers the TV by itself while it holds one, so a pod
// that stopped without a release would leave the adapter on the bus
// for a machine no pod serves. An adapter that left fails every call,
// and the entry still says why the pod stopped.
func (n *cecNode) stop(cause string) {
	n.release()
	n.mutex.Lock()
	bus := n.bus
	entry := CECAdapterStatus{Machine: n.machine, Mode: n.applied.mode, State: AdapterStopped, Driver: n.caps.Driver, Message: cause}
	n.mutex.Unlock()
	if bus == "" {
		return
	}
	entry.ReportedAt = timestamp(n.now())
	n.logState(bus, entry)
	if err := ApplyCECAdapterStatus(n.client, bus, n.machine, &entry); err != nil {
		fmt.Fprintf(os.Stderr, "writing machine %s's last entry in CECBus %s: %v\n", n.machine, bus, err)
	}
}

// release clears the adapter's logical addresses and leaves the
// follower or monitor mode. Clearing needs an initiator, so the handle
// takes that mode first. It answers the first error.
func (n *cecNode) release() error {
	for _, call := range []func() error{n.device.Initiate, n.device.Release, n.device.Leave} {
		if err := call(); err != nil {
			return err
		}
	}
	return nil
}

// report writes this adapter's entry when it changed since the last
// write the API server accepted, or when force asks for the steady
// write that keeps reportedAt current.
func (n *cecNode) report(force bool) {
	n.mutex.Lock()
	bus := n.bus
	entry := n.entry
	written := n.written
	note := n.displayNote
	n.mutex.Unlock()
	if bus == "" || entry.Machine == "" {
		return
	}
	entry.Devices = devicesOf(n.directory.Peers())
	n.mutex.Lock()
	if n.source != cec.InvalidPhysicalAddress {
		entry.ActiveSource = n.source.String()
	}
	n.mutex.Unlock()
	if note != "" {
		entry.Message = joinMessages(note, entry.Message)
	}
	if written != nil && reflect.DeepEqual(*written, entry) && !force {
		return
	}
	n.logState(bus, entry)
	stamped := entry
	stamped.ReportedAt = timestamp(n.now())
	if err := ApplyCECAdapterStatus(n.client, bus, n.machine, &stamped); err != nil {
		fmt.Fprintf(os.Stderr, "writing machine %s's entry in CECBus %s: %v\n", n.machine, bus, err)
		n.retryLater()
		return
	}
	n.mutex.Lock()
	n.written = &entry
	n.mutex.Unlock()
}

// joinMessages puts two messages in one, and leaves out an empty one.
func joinMessages(first, second string) string {
	if second == "" {
		return first
	}
	return first + "; " + second
}

// fail hands the loop the error that ends the node workload. The read
// loop, a scan, and an answer can each find the adapter gone at once;
// the first error ends the loop, and the others are dropped.
func (n *cecNode) fail(err error) {
	select {
	case n.failed <- err:
	default:
	}
}

// markDirty asks the loop to write the entry again.
func (n *cecNode) markDirty() {
	poke(n.dirty)
}
