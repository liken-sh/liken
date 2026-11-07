package main

// The Deployment's loop over CECBus and Television objects. It has
// the Receiver loop's shape: level-triggered, and woken by a watch on
// each kind a pass reads: the CECBuses, the Televisions, the Displays,
// the Receivers' specs, and the pods of the node workload. Each pass
// derives every bus's device list and conditions from the adapters'
// reports and the pods that wrote them, then every Television's status
// from those buses, and writes only what changed. No clock runs a pass
// to find a change: a pod of the node workload that stops being Ready
// is a write to the pod, and the pod watch wakes the loop for it
// (cecpods.go). The two timers are clocks: the retry of a read or a
// write that failed, and the end of a starting pod's grace, after
// which its entry is stale with no event (cecStartupGrace).

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
// read or a write the API server did not answer, such as on a cluster
// where the CECBus definition is not installed yet. No event follows
// such a failure, so only this timer runs the next try. It is armed
// only after a failure.
const cecBusRetry = 30 * time.Second

type cecBusController struct {
	client *Client
	// recorder posts the Events of each bus's and each Television's
	// conditions, and of each Television discovery creates or deletes.
	// Nil posts none.
	recorder *events.Recorder
	now      func() time.Time
	wake     chan struct{}
	// log takes a line for each Television discovery creates or
	// deletes.
	log io.Writer
	// namespace holds the pods of the node workload: the operator's own
	// namespace, where deploy/ puts both workloads.
	namespace string
	// The stores of the watches each pass reads (objectcache.go).
	buses, televisions, displays, receivers, pods *watchStore
	// sharedReceivers says another watch feeds receivers and wakes this
	// loop for a Receiver's spec, so run starts no Receiver watch.
	sharedReceivers bool
	// failed says a read or a write of the last pass failed, so run
	// tries the pass again after cecBusRetry. recheck is the end of the
	// earliest grace of an entry whose pod is starting, when run passes
	// again, and zero when no entry is in a grace. Only the goroutine
	// that runs the passes reads or writes either.
	failed  bool
	recheck time.Time
}

func newCECBusController(client *Client, namespace string) *cecBusController {
	return &cecBusController{
		client: client, now: time.Now, wake: make(chan struct{}, 1), log: os.Stderr, namespace: namespace,
		buses: &watchStore{}, televisions: &watchStore{}, displays: &watchStore{}, receivers: &watchStore{}, pods: &watchStore{},
	}
}

// retryLater records a read or a write that failed, so the loop runs
// the pass again after cecBusRetry.
func (c *cecBusController) retryLater() {
	c.failed = true
}

// pass derives and writes every bus, and then every Television. A
// write that fails is logged, and the pass after cecBusRetry tries it
// again.
//
// The pass compares each bus's derived status with the stored one. The
// store can hold the copy from before the pass's last write until the
// write's own event arrives, and a pass that compared with it would
// write the status again with a new lastTransitionTime, so the read
// replaces such a copy with the API server's (objectcache.go).
//
// A pass that cannot read the pods of the node workload writes
// nothing, because without them it cannot tell a current entry from
// a stale one.
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
		// The Television pass reads what this pass derived, so a TV's
		// power reaches its Television in the same pass that merged it.
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

// run lists until the CECBus collection answers, starts the watches,
// and then passes on every wake until ctx ends. After a pass whose read
// or write failed, it also passes after cecBusRetry, and after a pass
// that found an entry in a starting pod's grace, at the end of it.
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
	// run returns only after every watch stops, so nothing it started
	// outlives it. Each watch opens before the first pass reads its
	// kind, so a change made during the pass wakes the next one. The
	// Television and Display watches hold an empty store on a cluster
	// without their definitions, and find the definition when it
	// arrives (watchCollection).
	var watching sync.WaitGroup
	defer watching.Wait()
	watching.Go(func() { watchCECBuses(ctx, c.client, c.wake, readings.cecBusWatchRestarted, c.buses) })
	watching.Go(func() { watchTelevisions(ctx, c.client, c.wake, readings.televisionWatchRestarted, c.televisions) })
	watching.Go(func() { watchDisplays(ctx, c.client, c.wake, readings.displayWatchRestarted, c.displays) })
	watching.Go(func() { cecPods(c.namespace)(ctx, c.client, c.wake, readings.podWatchRestarted, c.pods) })
	// In the Deployment, the Receiver loop's watch also feeds this loop
	// (watchReceivers), so the process holds each Receiver once. A loop
	// that runs alone watches the Receivers' specs itself.
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
