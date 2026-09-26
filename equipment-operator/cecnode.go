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
	"os"
	"os/signal"
	"path/filepath"
	"reflect"
	"slices"
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

// How often an adapter in Control scans the bus. A scan polls fourteen
// addresses and asks each device five questions, and one CEC message
// takes tens of milliseconds, so a scan takes a few seconds of the
// wire. A minute keeps the list current as devices wake and sleep
// without filling the wire.
var cecScanInterval = time.Minute

// How often the node workload writes its entry when nothing changed.
// The time of each write is the entry's reportedAt, and the Deployment
// treats an entry as stale once it is three intervals old, so a pod
// that dies without a word stops counting within a few minutes.
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
	client    *Client
	machine   string
	device    *cec.Device
	caps      cec.Caps
	directory *cec.Directory
	now       func() time.Time
	wake      chan struct{}
	dirty     chan struct{}
	failed    chan error

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
	// stopMode ends the scan loop of the mode the adapter runs, and
	// returns once the loop has stopped.
	stopMode func()
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
		wake:      make(chan struct{}, 1),
		dirty:     make(chan struct{}, 1),
		failed:    make(chan error, 1),
		stopMode:  func() {},
	}, nil
}

// run reads the adapter and follows the CECBus collection until ctx
// ends or a call on the adapter fails. Either way it takes the adapter
// off the bus and writes a Stopped entry before it returns.
func (n *cecNode) run(ctx context.Context) error {
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

// loop is run's body: the first list, the watch, and the passes.
func (n *cecNode) loop(ctx context.Context, started *sync.WaitGroup) error {
	list, err := ListCECBuses(n.client)
	if err != nil {
		return fmt.Errorf("listing CECBuses: %w", err)
	}
	started.Go(func() {
		watchCECBuses(ctx, n.client, list.Metadata.ResourceVersion, n.wake, func() {})
	})
	backstop := time.NewTicker(backstopInterval)
	defer backstop.Stop()
	heartbeat := time.NewTicker(cecReportInterval)
	defer heartbeat.Stop()
	force := true
	for {
		if err := n.pass(ctx); err != nil {
			return err
		}
		n.report(force)
		force = false
		select {
		case <-ctx.Done():
			return nil
		case err := <-n.failed:
			return err
		case <-n.wake:
		case <-n.dirty:
		case <-backstop.C:
		case <-heartbeat.C:
			force = true
		}
	}
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
	if note != "" {
		entry.Message = joinMessages(note, entry.Message)
	}
	if written != nil && reflect.DeepEqual(*written, entry) && !force {
		return
	}
	stamped := entry
	stamped.ReportedAt = timestamp(n.now())
	if err := ApplyCECAdapterStatus(n.client, bus, n.machine, &stamped); err != nil {
		fmt.Fprintf(os.Stderr, "writing machine %s's entry in CECBus %s: %v\n", n.machine, bus, err)
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
