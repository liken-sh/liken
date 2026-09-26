package main

// The Deployment's loop over CECBus and Television objects. It has
// the Receiver loop's shape: level-triggered, woken by a watch on each
// kind, with a ticker as the backstop. Each pass derives every bus's
// device list and conditions from the adapters' reports, then every
// Television's status from those buses, and writes only what changed.
// A change to a Display or a Receiver sends no event to this loop, so
// the backstop tick carries it to a Television's status.

import (
	"context"
	"fmt"
	"io"
	"os"
	"reflect"
	"sync"
	"time"
)

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
}

func newCECBusController(client *Client) *cecBusController {
	return &cecBusController{client: client, now: time.Now, wake: make(chan struct{}, 1), log: os.Stderr}
}

// pass derives and writes every bus, and then every Television. A
// write that fails is logged, and the next pass tries it again.
func (c *cecBusController) pass() error {
	list, err := ListCECBuses(c.client)
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

// run lists until the collection answers, starts the watch from that
// list's version, and then passes on every wake and every backstop
// tick until ctx ends.
func (c *cecBusController) run(ctx context.Context, readings *metrics) {
	var list *CECBusList
	var televisions *TelevisionList
	for ctx.Err() == nil {
		var err error
		err = retryThrottled(ctx, func() error {
			var err error
			list, err = ListCECBuses(c.client)
			if err != nil {
				return fmt.Errorf("listing CECBuses: %w", err)
			}
			televisions, err = ListTelevisions(c.client)
			if err != nil {
				return fmt.Errorf("listing Televisions: %w", err)
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
	// run returns only after both watches stop, so nothing it started
	// outlives it.
	var watching sync.WaitGroup
	watching.Go(func() {
		watchCECBuses(ctx, c.client, list.Metadata.ResourceVersion, c.wake, readings.cecBusWatchRestarted)
	})
	// A cluster without the Television definition lists no version and
	// gets no Television watch; the backstop tick finds a definition
	// installed later.
	if televisions.Metadata.ResourceVersion != "" {
		watching.Go(func() {
			watchTelevisions(ctx, c.client, televisions.Metadata.ResourceVersion, c.wake, readings.televisionWatchRestarted)
		})
	}
	defer watching.Wait()
	ticker := time.NewTicker(backstopInterval)
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
