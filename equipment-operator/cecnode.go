package main

// The node workload (`equipment-operator-node cec`) runs as one pod on
// each machine that has a CEC adapter. The pod holds the DRA claim on
// the adapter's cec device, finds the CECBus that lists its machine,
// runs the adapter in that bus's mode, and writes its own entry under
// the bus's status.adapters. It needs no host network, no message bus,
// and no receiver credentials.

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

	"github.com/liken-sh/liken/equipment-operator/cec"
	"github.com/liken-sh/liken/kubernetes/events"
)

// The node workload's environment.
const (
	// NODE_NAME is the node the pod runs on. liken names each node after
	// its Machine, so it is also the machine name that a CECBus lists.
	nodeNameVariable = "NODE_NAME"
	// CEC_DEVICE is the path of the adapter's device node, for running
	// the workload by hand. In the pod it is unset, and findAdapter
	// searches /dev.
	cecDeviceVariable = "CEC_DEVICE"
)

// cecStopWrite limits the write of the Stopped entry, which happens
// after the node workload's context has ended. The kubelet kills a pod
// 30 seconds after SIGTERM, and a write that waits out a 429 could
// otherwise take up to the shared client's limit of ten seconds.
const cecStopWrite = 5 * time.Second

// findAdapter returns CEC_DEVICE when it is set, and otherwise the
// first cec* file in devices. The pod's claim covers one adapter, so
// the container's /dev has one cec device.
func findAdapter(devices string) (string, error) {
	if path := os.Getenv(cecDeviceVariable); path != "" {
		return path, nil
	}
	found, _ := filepath.Glob(filepath.Join(devices, "cec*"))
	if len(found) == 0 {
		return "", fmt.Errorf("no cec* device in %s and %s is unset", devices, cecDeviceVariable)
	}
	slices.Sort(found)
	return found[0], nil
}

// runCEC is the node workload's main. Any failure exits the process
// with the error as the last log line. The kubelet then restarts the
// container, and the new container gets the adapter's current device
// node, so the pod finds an adapter again after it was unplugged.
func runCEC() {
	// The kubelet stops a pod with SIGTERM. Cancelling the context lets
	// the node workload release the adapter before the process exits.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, os.Interrupt)
	defer stop()
	if err := serveCEC(ctx, "/dev"); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// serveCEC opens the claim's adapter under devices and runs the node
// workload until ctx ends or the adapter fails.
func serveCEC(ctx context.Context, devices string) error {
	machine := os.Getenv(nodeNameVariable)
	if machine == "" {
		return fmt.Errorf("%s is unset, and the DaemonSet must set it to the node name", nodeNameVariable)
	}
	// The pass reads the node workload's pods in this namespace to tell
	// whether another adapter's entry is current (cecpods.go).
	namespace := os.Getenv(podNamespaceVariable)
	if namespace == "" {
		return fmt.Errorf("%s is unset, and the DaemonSet must set it to the pod's namespace", podNamespaceVariable)
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
		return fmt.Errorf("reading the in-cluster configuration: %w", err)
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
		return fmt.Errorf("running the CEC adapter on %s: %w", machine, err)
	}
	return nil
}

// cecNode is the state of one adapter on one machine. The loop's
// goroutine runs the passes. mutex guards the fields that other
// goroutines also read or write.
type cecNode struct {
	client *Client
	// recorder posts the Events for the conditions that the node
	// workload writes on a Television. When nil, it posts none.
	recorder *events.Recorder
	// Each pass reads the caches of these watches (objectcache.go).
	// displays has only this machine's Displays, and pods has the node
	// workload's pods in namespace.
	buses, televisions, displays, pods *watchStore
	namespace                          string

	machine   string
	device    *cec.Device
	caps      cec.Caps
	directory *cec.Directory
	now       func() time.Time
	// log receives the lines that a person reads to follow the adapter,
	// such as each change of the entry's state. Errors go to stderr.
	log io.Writer
	// A watch sends on wake when an object changes, and markDirty sends
	// on dirty when the entry must be written again. Either starts a
	// pass at once. retryLater sends on retryRequests, which starts a
	// pass after cecAPIRetry. Each channel buffers one signal, so
	// signals that arrive before the loop reads them start one pass.
	wake          chan struct{}
	dirty         chan struct{}
	retryRequests chan struct{}
	// failed receives the first error that ends the node workload.
	failed chan error
	// The readiness probe sends a channel on probes, and the loop closes
	// it to show that it still runs (cecnode_probe.go).
	probes chan chan struct{}

	mutex sync.Mutex
	// bus is the name of the CECBus that this adapter reports to, and
	// applied is the configuration that the adapter runs.
	bus     string
	applied adapterConfig
	// entry is the adapter's current report. stored is this machine's
	// entry in the bus as the last pass read it, or nil when the bus has
	// none.
	entry  CECAdapterStatus
	stored *CECAdapterStatus
	// recheck is the earliest end of a grace period of another adapter
	// whose pod is still starting. The entry of that adapter becomes
	// stale then with no event to start a pass, so the loop runs one at
	// recheck. It is zero when no adapter is in a grace period
	// (cecpods.go).
	recheck time.Time
	// resync is set when the kernel reports a state change of the
	// adapter, and makes the next pass read the adapter's addresses
	// again.
	resync bool
	// display is the last successful read of this adapter's Display,
	// and displayNote is the error of the latest read when it failed.
	display     adapterDisplay
	displayNote string
	// After a failed join, retryAt is when the adapter tries the join
	// again, and retryWait is the wait that set retryAt.
	retryAt   time.Time
	retryWait time.Duration
	// logged is the entry that logState last wrote to the log.
	logged CECAdapterStatus
	// declared is the list of CECBuses, created by a person, that list
	// this machine, as the log last reported it.
	declared string
	// stopMode stops the goroutines of the adapter's current mode, such
	// as the scan, the queries of new devices, and an application of
	// spec.power, and returns when all of them have stopped. A goroutine
	// that the mode starts later runs under modeContext and is counted in
	// modeWork, so stopMode stops it too. Only the loop's goroutine
	// starts or stops a mode.
	stopMode    func()
	modeContext context.Context
	modeWork    *sync.WaitGroup
	// These record which requests the node workload has already acted
	// on, so a pass acts on each request once, and retries a status
	// write that the API server refused. powered is for a Television's
	// spec.power, woken for the session's wake, standby for a powerAsk
	// that turns the room off, powerRead for a powerAsk's read of the
	// TV's power, and shown for a home press.
	powered   powerState
	woken     wakeState
	standby   standbyState
	powerRead powerReadState
	shown     showState
	// In Control mode, arrived sends on arrivals the logical address of
	// each device that is new or announced a new physical address, and
	// the mode's goroutine queries that device with cec.Introduce.
	// introduced maps each device that the scan found or the goroutine
	// queried to its physical address at that time. startMode creates
	// both again for each mode.
	arrivals   chan cec.LogicalAddress
	introduced map[cec.LogicalAddress]cec.PhysicalAddress
	// When the TV announces itself and its power is unknown, the adapter
	// queues a read of the TV's power on tvPowerReadQueue. The channel buffers
	// one request, so a burst of announcements queues one read.
	// tvPowerQueuedAddress is the physical address of the TV's last such
	// announcement, and the adapter does not queue another read for the
	// same address while the power stays unknown. startMode creates both
	// again for each mode.
	tvPowerReadQueue     chan struct{}
	tvPowerQueuedAddress *cec.PhysicalAddress
	// tvPowerRead is the read of the TV's power that is in progress,
	// which every caller of readTVPower shares.
	tvPowerRead tvPowerReadCall
	// lastCommand is when this adapter last sent the TV a power command,
	// or heard another device, such as a receiver, send the TV Standby.
	// For a while after a power command, a TV reports its old power
	// state.
	lastCommand time.Time
	// source is the physical address of the last Active Source that the
	// adapter heard or sent. Each change sends on sources, which the
	// wake's guard waits on (cecnode_source.go).
	source  cec.PhysicalAddress
	sources chan struct{}
	// route is where the bus's last routing message, such as a Routing
	// Change or a Set Stream Path, switched the TV's input, and whether
	// a person chose it (cecnode_answer.go).
	route routeState
	// awake is set while a Player's session keeps the TV awake through
	// this adapter, and the adapter then answers Request Active Source.
	// asleep is set while the session of this adapter's Display sleeps.
	// Both are nil otherwise.
	awake  *displaySession
	asleep *displaySession
	// streamPath is the Set Stream Path that the TV sent for the Display of
	// a sleeping session, kept until the wake that it requested claims
	// the input. screenAsk is a request for the Player's screen that the
	// API server has not accepted yet (cecnode_screen.go).
	streamPath *streamPathRequest
	screenAsk  *unwrittenScreenAsk
	// While a caller waits for the receiver's reply to a System Audio
	// Mode Request, audioAnswers receives the replies. It is nil
	// otherwise (cecnode_systemaudio.go).
	audioAnswers chan cec.Message
}

// newCECNode reads the capabilities of an open adapter, which the mode
// and the join depend on, and returns its node.
func newCECNode(client *Client, namespace, machine string, device *cec.Device) (*cecNode, error) {
	caps, err := device.Caps()
	if err != nil {
		return nil, err
	}
	return &cecNode{
		client:        client,
		namespace:     namespace,
		machine:       machine,
		device:        device,
		caps:          caps,
		directory:     cec.NewDirectory(),
		now:           time.Now,
		log:           os.Stderr,
		wake:          make(chan struct{}, 1),
		dirty:         make(chan struct{}, 1),
		retryRequests: make(chan struct{}, 1),
		probes:        make(chan chan struct{}),
		failed:        make(chan error, 1),
		stopMode:      func() {},
		buses:         &watchStore{},
		televisions:   &watchStore{},
		displays:      &watchStore{},
		pods:          &watchStore{},
		source:        cec.InvalidPhysicalAddress,
		sources:       make(chan struct{}, 1),
	}, nil
}

// run serves the adapter until ctx ends or a call on the adapter
// fails. Before it returns, it releases the adapter's logical addresses
// and writes a Stopped entry.
func (n *cecNode) run(ctx context.Context) error {
	n.logOpened()
	loop, cancel := context.WithCancel(ctx)
	// When the loop ends, the client stops waiting out a 429, so a
	// throttled write delays shutdown only by the request itself. A
	// request already sent is not cancelled. A cancelled entry write
	// could still reach the API server after the Stopped entry, and a
	// cancelled write of a result would be lost, so the adapter would
	// send the CEC command again. This must run before the loop starts
	// anything that uses the client.
	n.client = n.client.withWaits(loop)
	var started sync.WaitGroup
	started.Go(func() {
		if err := cec.Read(loop, n.device, n.heard, n.adapterChanged); err != nil {
			n.fail(err)
		}
	})
	err := n.loop(loop, &started)
	// Wait for every goroutine that the loop started, so none uses the
	// adapter or the API after stop releases the adapter.
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

// logOpened logs the logical addresses that the adapter has when the
// pod opens it. A previous pod that stopped cleanly released them all,
// so the line shows whether it did.
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

// logState logs a change of the entry's state or message. A change of
// only the devices or the time is not logged, because the device list
// changes often while the adapter scans the bus.
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

// loop starts the watches and then runs a pass for each event until
// ctx ends or the adapter fails.
//
// The node workload subscribes to each source of state before it reads
// the whole state once, so a change during the read is not lost. After
// the read it acts only on events. On the CEC bus, the subscription is
// the follower or monitor mode, in which the kernel passes the handle
// every message that the adapter hears. The handle enters that mode
// before it claims an address, and the read is the scan when the
// adapter joins (see startMode). In the API, the subscriptions are the
// watches of CECBuses, Televisions, Displays, and the node workload's
// pods. client-go's reflector runs each watch (watch.go).
//
// No timer reads state again. Each timer is a deadline: the retry of a
// failed join or a failed API call, and the end of a starting pod's
// grace period (cecpods.go).
func (n *cecNode) loop(ctx context.Context, started *sync.WaitGroup) error {
	err := retryWhileThrottled(ctx, func() error {
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
	// The list before each watch makes a refused list, such as one with
	// a missing RBAC permission, end the node workload at start, where a
	// person sees it, because the watch retries a refused list. A missing
	// Television CRD is not a failure: ListTelevisions returns no
	// Television, and the watch checks for the CRD again every
	// optionalRecheck (watchCollection).
	err = retryWhileThrottled(ctx, func() error {
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
	err = retryWhileThrottled(ctx, func() error {
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

// await blocks until an event or the retry deadline calls for another
// pass. A probe or a retry request does not start a pass: await
// answers the probe, starts the retry timer when none runs, and keeps
// waiting. When done is true, the node workload ends, with err as the
// cause.
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
		case <-n.retryRequests:
			if retry == nil {
				retry = time.After(cecAPIRetry)
			}
		case answer := <-n.probes:
			// The probe checks only that the loop reaches this select.
			close(answer)
		}
	}
}

// cecAPIRetry is how long the loop waits before it runs another pass
// after an API call failed with no event to follow, such as a write
// that the API server refused. It is a variable so that the tests on
// the kernel's vivid driver, which run on the real clock, can shorten
// it.
var cecAPIRetry = 10 * time.Second

// retryLater requests a pass after cecAPIRetry. A pass that an event
// starts sooner also retries the failed call.
func (n *cecNode) retryLater() {
	sendWake(n.retryRequests)
}

// retryIn returns how long the loop waits for its next pass when no
// event arrives, or false when only an event can start it. The wait
// ends at the earliest of: retryAt after a failed join, cecAPIRetry
// after a status write that the API server refused, and recheck, when
// a starting pod's entry can become stale.
func (n *cecNode) retryIn() (time.Duration, bool) {
	wait, due := n.joinOrWriteRetry()
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

// joinOrWriteRetry returns the wait for the retry of a failed join or
// of a refused status write, or false when neither is pending.
func (n *cecNode) joinOrWriteRetry() (time.Duration, bool) {
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

// stop releases the adapter's logical addresses and writes a Stopped
// entry. The kernel keeps the logical addresses after the handle
// closes, and a Pulse-Eight adapter's firmware answers the TV by itself
// while it has one (cec.Device.Release). Without the release, the
// adapter stays on the bus with no pod to serve it. When the adapter is
// unplugged, the release fails, and stop still writes the entry with
// the cause. ctx has already ended, so the write uses its own context,
// which cecStopWrite limits.
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
		fmt.Fprintf(os.Stderr, "writing the last entry of machine %s in CECBus %s: %v\n", n.machine, bus, err)
	}
}

// release clears the adapter's logical addresses and leaves the
// follower or monitor mode. Clearing the addresses needs the initiator
// mode, so release enters it first.
func (n *cecNode) release() error {
	for _, call := range []func() error{n.device.Initiate, n.device.Release, n.device.Leave} {
		if err := call(); err != nil {
			return err
		}
	}
	return nil
}

// report writes this adapter's entry when the bus has none or a
// different one. It compares with the bus as the last pass read it, not
// with its own last write, so it writes again an entry that another
// writer or a restore of the cluster changed or removed. An unchanged
// entry is not written, so reportedAt is the time of the last change.
// Whether an entry is current is read from the node workload's pods,
// not from reportedAt (cecpods.go).
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
		fmt.Fprintf(os.Stderr, "writing the entry of machine %s in CECBus %s: %v\n", n.machine, bus, err)
		n.retryLater()
		return
	}
	n.mutex.Lock()
	n.stored = &stamped
	n.mutex.Unlock()
}

// sameEntry reports whether stored equals entry apart from reportedAt.
// It compares the JSON encodings, which is how the API server stores
// them, and in which an empty list and a missing list are the same.
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

func joinMessages(first, second string) string {
	if second == "" {
		return first
	}
	return first + "; " + second
}

// fail sends the loop the error that ends the node workload. The read
// goroutine, a scan, and a reply to a device can each find the adapter
// unplugged at the same time, so fail keeps the first error and drops
// the rest.
func (n *cecNode) fail(err error) {
	select {
	case n.failed <- err:
	default:
	}
}

// liveness reads which of the node workload's pods are Ready
// (cecpods.go). When the read fails, it returns an empty podLiveness,
// which treats every other adapter's entry as current, so this adapter
// can defer to an adapter that is gone until a later pass reads the
// pods.
func (n *cecNode) liveness() podLiveness {
	live, err := readCECPods(n.client, n.pods, n.namespace, n.now())
	if err != nil {
		fmt.Fprintf(os.Stderr, "listing the equipment-operator-cec pods in %s: %v. Until the pods can be read, the adapter on %s counts every other adapter's entry as current\n", n.namespace, err, n.machine)
		n.retryLater()
		return podLiveness{}
	}
	return live
}

func (n *cecNode) markDirty() {
	sendWake(n.dirty)
}
