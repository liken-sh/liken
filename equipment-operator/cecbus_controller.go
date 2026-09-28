package main

// The Deployment's loop over CECBus and Television objects. It has
// the Receiver loop's shape: level-triggered, and woken by a watch on
// each kind a pass reads: the CECBuses, the Televisions, the Displays,
// and the Receivers' specs. Each pass derives every bus's device list
// and conditions from the adapters' reports, then every Television's
// status from those buses, and writes only what changed. A clock also
// runs a pass, because an adapter's report goes stale with age and no
// event says so.

import (
	"context"
	"fmt"
	"io"
	"os"
	"reflect"
	"sync"
	"time"
)

// cecBusClock is how often the loop runs a pass with no event. It is a
// clock and not a backstop for a watch. An adapter's entry goes stale
// staleAfter after its reportedAt, and the pod of a node workload that
// dies writes nothing that wakes the loop, so only a clock finds the
// stale entry. The same tick tries again a status write the API server
// refused. Every object a pass reads has a watch, so no change to one
// waits for the tick.
var cecBusClock = 30 * time.Second

// cecBusRetry is how long the loop waits before it lists again after
// a failed list, such as on a cluster where the CECBus definition is
// not installed yet.
var cecBusRetry = 30 * time.Second

type cecBusController struct {
	client *Client
	now    func() time.Time
	wake   chan struct{}
	// log takes a line for each Television discovery creates or
	// deletes.
	log io.Writer
	// The stores of the watches each pass reads (objectcache.go).
	buses, televisions, displays, receivers *watchStore
	// sharedReceivers says another watch feeds receivers and wakes this
	// loop for a Receiver's spec, so run starts no Receiver watch.
	sharedReceivers bool
}

func newCECBusController(client *Client) *cecBusController {
	return &cecBusController{
		client: client, now: time.Now, wake: make(chan struct{}, 1), log: os.Stderr,
		buses: &watchStore{}, televisions: &watchStore{}, displays: &watchStore{}, receivers: &watchStore{},
	}
}

// pass derives and writes every bus, and then every Television. A
// write that fails is logged, and the next pass tries it again.
//
// The pass compares each bus's derived status with the stored one. The
// store can hold the copy from before the pass's last write until the
// write's own event arrives, and a pass that compared with it would
// write the status again with a new lastTransitionTime, so the read
// replaces such a copy with the API server's (objectcache.go).
func (c *cecBusController) pass() error {
	list, err := readCECBuses(c.client, c.buses)
	if err != nil {
		return err
	}
	for index := range list.Items {
		bus := &list.Items[index]
		devices, conditions := deriveCECBus(bus, c.now())
		changed := !reflect.DeepEqual(devices, bus.Status.Devices) || !reflect.DeepEqual(conditions, bus.Status.Conditions)
		// The Television pass reads what this pass derived, so a TV's
		// power reaches its Television in the same pass that merged it.
		bus.Status.Devices, bus.Status.Conditions = devices, conditions
		if !changed {
			continue
		}
		if err := ApplyCECBusDerived(c.client, bus.Metadata.Name, devices, conditions); err != nil {
			fmt.Fprintf(os.Stderr, "writing the status of CECBus %s: %v\n", bus.Metadata.Name, err)
		}
	}
	c.passTelevisions(list.Items)
	return nil
}

// run lists until the CECBus collection answers, starts the watches,
// and then passes on every wake and every tick of cecBusClock until
// ctx ends.
func (c *cecBusController) run(ctx context.Context, readings *metrics) {
	var list *CECBusList
	for ctx.Err() == nil {
		var err error
		err = retryThrottled(ctx, func() error {
			var err error
			list, err = ListCECBuses(c.client)
			if err != nil {
				return fmt.Errorf("listing CECBuses: %w", err)
			}
			return nil
		})
		if err == nil {
			break
		}
		list = nil
		fmt.Fprintln(os.Stderr, err)
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
	// In the Deployment, the Receiver loop's watch also feeds this loop
	// (watchReceivers), so the process holds each Receiver once. A loop
	// that runs alone watches the Receivers' specs itself.
	if !c.sharedReceivers {
		watching.Go(func() { watchReceiverSpecs(ctx, c.client, c.wake, readings.watchRestarted, c.receivers) })
	}
	ticker := time.NewTicker(cecBusClock)
	defer ticker.Stop()
	for {
		if err := c.pass(); err != nil {
			fmt.Fprintf(os.Stderr, "listing CECBuses: %v\n", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-c.wake:
		case <-ticker.C:
		}
	}
}
