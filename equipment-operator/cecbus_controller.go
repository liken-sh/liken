package main

// The Deployment's loop over CECBus and Television objects. Each pass
// reads the whole current state. Watches on CECBuses, Televisions,
// Displays, Receiver specs, and the node workload's pods wake the loop.
// A pass derives each bus's devices and conditions from the entries that
// the node workloads wrote and from the pods that wrote them. It then
// derives each Television's status from the buses, and writes only what
// changed.
//
// No timer polls for changes. When a node workload pod stops being
// Ready, the pod watch wakes the loop (cecpods.go). The timers only
// measure time: the retry after a failed read or write, and the end of
// a starting pod's grace period (cecStartupGrace), after which its
// entry is stale although no object changed.

import (
	"context"
	"fmt"
	"io"
	"os"
	"reflect"
	"sync"
	"time"

	"github.com/liken-sh/liken/kubernetes/events"
)

// cecBusRetry is how long the loop waits before it tries again after a
// failed read or write, such as on a cluster where the CECBus
// definition is not installed yet. No watch event follows such a
// failure, so this timer is the only trigger for the next try.
const cecBusRetry = 30 * time.Second

type cecBusController struct {
	client *Client
	// recorder posts the Events for the conditions of each bus and each
	// Television, and for each Television that discovery creates or
	// deletes. When nil, no Events are posted.
	recorder *events.Recorder
	now      func() time.Time
	wake     chan struct{}
	// log receives a line for each Television that discovery creates
	// or deletes.
	log io.Writer
	// namespace is where the node workload's pods run. It is the
	// operator's own namespace, where deploy/ installs both workloads.
	namespace string
	// The stores of the watches that each pass reads (objectcache.go).
	buses, televisions, displays, receivers, pods *watchStore
	// sharedReceivers is true when another loop's watch fills receivers
	// and wakes this loop on a Receiver spec change, so run does not
	// start its own Receiver watch.
	sharedReceivers bool
	// failed is true when a read or write in the last pass failed, so
	// run passes again after cecBusRetry. recheck is when the earliest
	// grace period of a starting pod's report ends, and run passes again
	// then. It is zero when no report is in a grace period. Only the
	// goroutine that runs the passes reads or writes either field.
	failed  bool
	recheck time.Time
}

func newCECBusController(client *Client, namespace string) *cecBusController {
	return &cecBusController{
		client: client, now: time.Now, wake: make(chan struct{}, 1), log: os.Stderr, namespace: namespace,
		buses: &watchStore{}, televisions: &watchStore{}, displays: &watchStore{}, receivers: &watchStore{}, pods: &watchStore{},
	}
}

func (c *cecBusController) retryLater() {
	c.failed = true
}

// pass derives and writes the status of every bus, and then of every
// Television. A failed write is logged, and the pass after cecBusRetry
// tries it again.
//
// Until the watch delivers the previous pass's write, the store still
// has the copy from before it. Comparing with that copy would write the
// status again with a new lastTransitionTime, so readCECBuses replaces
// such a copy with the API server's (objectcache.go).
//
// When the pass cannot read the node workload's pods, it writes
// nothing, because without the pods it cannot tell a current entry
// from a stale one.
func (c *cecBusController) pass() error {
	list, err := readCECBuses(c.client, c.buses)
	if err != nil {
		c.retryLater()
		return fmt.Errorf("listing CECBuses: %w", err)
	}
	live, err := readCECPods(c.client, c.pods, c.namespace, c.now())
	if err != nil {
		c.retryLater()
		return fmt.Errorf("listing the equipment-operator-cec pods in %s: %w", c.namespace, err)
	}
	c.recheck = time.Time{}
	for index := range list.Items {
		bus := &list.Items[index]
		c.recheck = earliest(c.recheck, reportedEntries(bus, live).recheck)
		devices, conditions := deriveCECBus(bus, live, c.now())
		changed := !reflect.DeepEqual(devices, bus.Status.Devices) || !reflect.DeepEqual(conditions, bus.Status.Conditions)
		stored := bus.Status.Conditions
		// Store the derived status in the object that passTelevisions
		// reads, so a TV's power reaches its Television in the same pass.
		bus.Status.Devices, bus.Status.Conditions = devices, conditions
		if !changed {
			continue
		}
		if err := ApplyCECBusDerived(c.client, bus.Metadata.Name, devices, conditions); err != nil {
			fmt.Fprintf(os.Stderr, "writing the status of CECBus %s: %v\n", bus.Metadata.Name, err)
			c.retryLater()
			continue
		}
		postTransitions(c.recorder, reference("CECBus", bus.Metadata), stored, conditions)
	}
	c.passTelevisions(list.Items, live)
	return nil
}

// run lists CECBuses until the list succeeds, starts the watches, and
// then runs a pass on every wake until ctx ends. It also runs a pass
// cecBusRetry after a failed read or write, and at the end of each
// starting pod's grace period.
func (c *cecBusController) run(ctx context.Context, readings *metrics) {
	var list *CECBusList
	for ctx.Err() == nil {
		var err error
		list, err = ListCECBuses(c.client.withContext(ctx))
		if err == nil {
			break
		}
		list = nil
		fmt.Fprintf(os.Stderr, "listing CECBuses: %v\n", err)
		select {
		case <-ctx.Done():
			return
		case <-time.After(cecBusRetry):
		}
	}
	if list == nil {
		return
	}
	// Each watch wakes the loop when its first list is done, so a change
	// made during the first pass, before that watch opened, still starts
	// another pass. On a cluster without the Television or Display
	// definition, that watch keeps an empty store until the definition
	// appears (watchCollection).
	var watching sync.WaitGroup
	defer watching.Wait()
	watching.Go(func() { watchCECBuses(ctx, c.client, c.wake, readings.cecBusWatchRestarted, c.buses) })
	watching.Go(func() { watchTelevisions(ctx, c.client, c.wake, readings.televisionWatchRestarted, c.televisions) })
	watching.Go(func() { watchDisplays(ctx, c.client, c.wake, readings.displayWatchRestarted, c.displays) })
	watching.Go(func() { cecPods(c.namespace)(ctx, c.client, c.wake, readings.podWatchRestarted, c.pods) })
	// In the Deployment, the Receiver loop's watch also fills
	// c.receivers (watchReceivers), so the process keeps one copy of
	// each Receiver.
	if !c.sharedReceivers {
		watching.Go(func() { watchReceiverSpecs(ctx, c.client, c.wake, readings.watchRestarted, c.receivers) })
	}
	for {
		c.failed = false
		if err := c.pass(); err != nil {
			fmt.Fprintln(os.Stderr, err)
		}
		var retry, recheck <-chan time.Time
		if c.failed {
			retry = time.After(cecBusRetry)
		}
		if !c.recheck.IsZero() {
			recheck = time.After(c.recheck.Sub(c.now()))
		}
		select {
		case <-ctx.Done():
			return
		case <-c.wake:
		case <-retry:
		case <-recheck:
		}
	}
}
