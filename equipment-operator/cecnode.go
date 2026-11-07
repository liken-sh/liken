package main

// The node workload, `equipment-operator-node cec`: one pod on each machine
// that carries a CEC adapter. The pod holds the DRA claim on the
// adapter's -cec device, finds the CECBus that names its machine, runs
// the adapter in the bus's mode, and writes its own entry under that
// bus's status.adapters. It needs no host network, no message bus, and
// no receiver credentials. plans/completed/09-cec.md gives the design.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/liken-sh/equipment-operator/cec"
	"github.com/liken-sh/liken/kubernetes/events"
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

// cecStopWrite bounds the write of the Stopped entry after the node
// workload's context ends. The kubelet gives a pod 30 seconds after
// SIGTERM, and a write that waits out a 429 would otherwise take up to
// the shared client's limit of ten seconds.
const cecStopWrite = 5 * time.Second

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
	// The pod's namespace holds the pods of every node workload, which
	// the pass reads to tell whether another adapter's entry is current
	// (cecpods.go).
	namespace := os.Getenv(podNamespaceVariable)
	if namespace == "" {
		return fmt.Errorf("%s is unset; the DaemonSet must state the pod's namespace", podNamespaceVariable)
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
	client, err := InClusterClient(0)
	if err != nil {
		return fmt.Errorf("in-cluster config: %w", err)
	}
	node, err := newCECNode(client, namespace, machine, device)
	if err == nil {
		_, err = node.serveProbe(ctx, os.Getenv(cecProbeVariable))
	}
	if err == nil {
		node.recorder = events.New(ctx, client.Client, "equipment-operator-cec", events.Options{})
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
	// recorder posts the Events of the conditions the node workload
	// writes on a Television. Nil posts none.
	recorder *events.Recorder
	// The stores of the watches each pass reads (objectcache.go). The
	// Display store holds the Displays of this machine; the node
	// workload writes no Display. The pod store holds the node
	// workload's pods in namespace (cecpods.go).
	buses, televisions, displays, pods *watchStore
	namespace                          string

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
	// probes takes each probe's question to the loop, which closes the
	// channel it receives to answer (cecnode_probe.go).
	probes chan chan struct{}

	mutex sync.Mutex
	// bus is the CECBus this adapter reports to, and applied is the
	// configuration the adapter runs.
	bus     string
	applied adapterConfig
	// entry is the report as it stands now, and stored is this
	// machine's entry in the bus as the last pass read it, or nil when
	// the bus holds none.
	entry  CECAdapterStatus
	stored *CECAdapterStatus
	// recheck is the end of the earliest grace of another adapter's
	// entry whose pod is starting, when the loop passes again, and zero
	// when no entry is in a grace (cecpods.go).
	recheck time.Time
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
	// shown is what the node workload holds about the home presses it
	// answered.
	shown showMemory
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
	// route is where the bus's routing messages last sent the TV's
	// picture (cecnode_answer.go).
	route routeState
	// room is the room a Player's session holds awake through this
	// adapter, and nil when none does. The adapter answers Request
	// Active Source for it. asleep is the room of a session that stands
	// on this adapter's Display and sleeps.
	room   *roomHold
	asleep *roomHold
	// picked is the TV's Set Stream Path for a sleeping session's
	// Display, until the wake it asked for claims the input, and
	// screenAsk is an ask of the Player's screen the API server has not
	// accepted yet (cecnode_screen.go).
	picked    *streamPick
	screenAsk *screenAskRecord
	// audioAnswers takes the receiver's answers to a System Audio Mode
	// Request while one waits, and is nil otherwise
	// (cecnode_systemaudio.go).
	audioAnswers chan cec.Message
}

// newCECNode reads the adapter's capabilities, which every later
// choice depends on.
func newCECNode(client *Client, namespace, machine string, device *cec.Device) (*cecNode, error) {
	caps, err := device.Caps()
	if err != nil {
		return nil, err
	}
	return &cecNode{
		client:      client,
		namespace:   namespace,
		machine:     machine,
		device:      device,
		caps:        caps,
		directory:   cec.NewDirectory(),
		now:         time.Now,
		log:         os.Stderr,
		wake:        make(chan struct{}, 1),
		dirty:       make(chan struct{}, 1),
		retryAsk:    make(chan struct{}, 1),
		probes:      make(chan chan struct{}),
		failed:      make(chan error, 1),
		stopMode:    func() {},
		buses:       &watchStore{},
		televisions: &watchStore{},
		displays:    &watchStore{},
		pods:        &watchStore{},
		source:      cec.InvalidPhysicalAddress,
		sources:     make(chan struct{}, 1),
	}, nil
}

// run reads the adapter and follows the CECBus collection until ctx
// ends or a call on the adapter fails. Either way it takes the adapter
// off the bus and writes a Stopped entry before it returns.
func (n *cecNode) run(ctx context.Context) error {
	n.logOpened()
	loop, cancel := context.WithCancel(ctx)
	// The client's wait after a 429 ends when the loop does, so an
	// entry write that meets an API server that is not ready holds a
	// SIGTERM for no more than the request itself. A request already sent
	// runs to its answer: an entry write cut at the SIGTERM could still
	// land after the Stopped entry, and a verdict write cut then would be
	// lost and its CEC command sent again. The client is bound before anything
	// the loop starts reads it.
	n.client = n.client.withWaits(loop)
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
	} else {
		n.withdrawOnStop()
	}
	n.stop(ctx, cause)
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
// Displays, and the baseline is each pass's read of the watches' stores
// (objectcache.go). The watch of the node workload's pods tells the
// pass which other adapters' entries are current (cecpods.go).
// client-go's reflector runs each watch: it resumes a watch that drops
// from its last version and reads again after a 410, and the watch
// wakes the loop when its own first read is done (watch.go). No timer
// re-reads a state, and the entry is written only when it changes. The
// timers here are clocks: the retry of a join or an API call that
// failed, and the end of a starting pod's grace (cecpods.go).
func (n *cecNode) loop(ctx context.Context, started *sync.WaitGroup) error {
	err := untilAnswered(ctx, func() error {
		_, err := ListCECBuses(n.client)
		return err
	})
	if ctx.Err() != nil {
		return nil
	}
	if err != nil {
		return fmt.Errorf("listing CECBuses: %w", err)
	}
	started.Go(func() { watchCECBuses(ctx, n.client, n.wake, nil, n.buses) })
	// A change of a Television's spec.power or status.session wakes the
	// loop, because the pass is where the adapter acts on it. A change of
	// a Display's physical address wakes it too, because the adapter
	// announces that address. Each watch holds an empty store on a
	// cluster without its definition, and finds the definition when it
	// arrives (watchCollection).
	//
	// The node workload lists the Televisions once before it watches
	// them, so an API server that refuses the list, such as for a
	// missing grant, ends the node workload at its start, as the
	// CECBus list does. A cluster without the definition answers not
	// found, which ListTelevisions reads as no Television.
	err = untilAnswered(ctx, func() error {
		_, err := ListTelevisions(n.client)
		return err
	})
	if ctx.Err() != nil {
		return nil
	}
	if err != nil {
		return fmt.Errorf("listing %s: %w", televisionsPath, err)
	}
	started.Go(func() { watchTelevisions(ctx, n.client, n.wake, nil, n.televisions) })
	started.Go(func() { nodeDisplays(n.machine)(ctx, n.client, n.wake, nil, n.displays) })
	// The pods are listed once before the watch for the same reason as
	// the Televisions: a missing grant ends the node workload at its
	// start, where a person sees it.
	err = untilAnswered(ctx, func() error {
		_, err := readCECPods(n.client, nil, n.namespace, n.now())
		return err
	})
	if ctx.Err() != nil {
		return nil
	}
	if err != nil {
		return fmt.Errorf("listing the equipment-operator-cec pods in %s: %w", n.namespace, err)
	}
	started.Go(func() { cecPods(n.namespace)(ctx, n.client, n.wake, nil, n.pods) })
	var retry <-chan time.Time
	for {
		if err := n.pass(ctx); err != nil {
			return err
		}
		n.report()
		retry = nil
		if wait, due := n.retryIn(); due {
			retry = time.After(wait)
		}
		if done, err := n.await(ctx, retry); done {
			return err
		}
	}
}

// await waits for the loop's next pass: an event, or the retry clock. A
// probe's question and a request for a retry are handled here and run
// no pass. done says the node workload ends, with err as its cause.
func (n *cecNode) await(ctx context.Context, retry <-chan time.Time) (bool, error) {
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
		case answer := <-n.probes:
			// The probe asks only whether the loop reaches this select,
			// so the answer reads nothing and runs no pass.
			close(answer)
		}
	}
}

// cecAPIRetry is how long the loop waits before it runs a pass again
// after an API call failed with no event to follow, such as a list the
// API server refused or a result it did not accept. It is a variable
// because the tests on the kernel's vivid driver run on the real
// clock, and shorten it.
var cecAPIRetry = 10 * time.Second

// retryLater asks the loop for a pass after cecAPIRetry, because an API
// call failed and no event will follow it. A pass that runs sooner for
// an event also tries again.
func (n *cecNode) retryLater() {
	poke(n.retryAsk)
}

// retryIn answers how long the loop waits before its next pass when no
// event arrives, and false when it waits for an event alone: a join
// that failed is tried again at its retryAt, a result the API server
// has not accepted after cecAPIRetry, and another adapter's entry in a
// starting pod's grace at the end of the grace, when that adapter may
// lose its turn.
func (n *cecNode) retryIn() (time.Duration, bool) {
	wait, due := n.clockRetry()
	n.mutex.Lock()
	recheck := n.recheck
	n.mutex.Unlock()
	if recheck.IsZero() {
		return wait, due
	}
	until := max(recheck.Sub(n.now()), 0)
	if due {
		until = min(until, wait)
	}
	return until, true
}

// clockRetry answers the retries of a join and of an API result.
func (n *cecNode) clockRetry() (time.Duration, bool) {
	n.mutex.Lock()
	defer n.mutex.Unlock()
	unwritten := n.powered.unwritten != nil || n.woken.unwritten != nil || n.standby.unwritten != nil || n.screenAsk != nil
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
// and the entry still says why the pod stopped. The loop's context has
// ended by then, so the last write has a context of its own, which
// cecStopWrite bounds.
func (n *cecNode) stop(ctx context.Context, cause string) {
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
	writing, done := context.WithTimeout(context.WithoutCancel(ctx), cecStopWrite)
	defer done()
	if err := ApplyCECAdapterStatus(n.client.withContext(writing), bus, n.machine, &entry); err != nil {
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

// report writes this adapter's entry when it differs from the entry
// the bus stores, or the bus stores none. The comparison is with the
// API server's copy and not with the last write, so an entry that a
// restore or another writer removed or changed is written again. An
// entry that did not change is not written again: the Deployment reads
// whether the entry is current from the node workload's pod
// (cecpods.go), so reportedAt is the time of the last change.
func (n *cecNode) report() {
	n.mutex.Lock()
	bus := n.bus
	entry := n.entry
	stored := n.stored
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
	if sameEntry(stored, entry) {
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
	n.stored = &stamped
	n.mutex.Unlock()
}

// sameEntry answers whether the stored entry holds the report, apart
// from reportedAt. The two are compared as the API server stores them,
// in JSON, where an empty list and no list are the same.
func sameEntry(stored *CECAdapterStatus, entry CECAdapterStatus) bool {
	if stored == nil {
		return false
	}
	held := *stored
	held.ReportedAt, entry.ReportedAt = "", ""
	first, _ := json.Marshal(held)
	second, _ := json.Marshal(entry)
	return string(first) == string(second)
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

// liveness answers what the node workload's pods show (cecpods.go). A
// read that fails answers no pods, which judges no other adapter's
// entry stale, so the adapter can defer to an adapter that is gone
// until the read works again. The log line says so, and the loop tries
// again.
func (n *cecNode) liveness() podLiveness {
	live, err := readCECPods(n.client, n.pods, n.namespace, n.now())
	if err != nil {
		fmt.Fprintf(os.Stderr, "listing the equipment-operator-cec pods in %s: %v; until the pods can be read, the adapter on %s counts every other adapter's entry as current\n", n.namespace, err, n.machine)
		n.retryLater()
		return podLiveness{}
	}
	return live
}

// markDirty asks the loop to write the entry again.
func (n *cecNode) markDirty() {
	poke(n.dirty)
}
