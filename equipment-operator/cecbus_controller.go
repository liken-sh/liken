package main

// The Deployment's loop over CECBus objects. It has the Receiver
// loop's shape: level-triggered, woken by a watch, with a ticker as
// the backstop. Each pass derives every bus's device list and
// conditions from the adapters' reports and writes only what changed.

import (
	"context"
	"fmt"
	"os"
	"reflect"
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
}

func newCECBusController(client *Client) *cecBusController {
	return &cecBusController{client: client, now: time.Now, wake: make(chan struct{}, 1)}
}

// pass derives and writes every bus. A write that fails is logged,
// and the next pass tries it again.
func (c *cecBusController) pass() error {
	list, err := ListCECBuses(c.client)
	if err != nil {
		return err
	}
	for index := range list.Items {
		bus := &list.Items[index]
		devices, conditions := deriveCECBus(bus, c.now())
		if reflect.DeepEqual(devices, bus.Status.Devices) && reflect.DeepEqual(conditions, bus.Status.Conditions) {
			continue
		}
		if err := ApplyCECBusDerived(c.client, bus.Metadata.Name, devices, conditions); err != nil {
			fmt.Fprintf(os.Stderr, "writing the status of CECBus %s: %v\n", bus.Metadata.Name, err)
		}
	}
	return nil
}

// run lists until the collection answers, starts the watch from that
// list's version, and then passes on every wake and every backstop
// tick until ctx ends.
func (c *cecBusController) run(ctx context.Context, readings *metrics) {
	var list *CECBusList
	for ctx.Err() == nil {
		var err error
		if list, err = ListCECBuses(c.client); err == nil {
			break
		}
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
	// run returns only after the watch stops, so nothing it started
	// outlives it.
	watching := make(chan struct{})
	go func() {
		defer close(watching)
		watchCECBuses(ctx, c.client, list.Metadata.ResourceVersion, c.wake, readings.cecBusWatchRestarted)
	}()
	defer func() { <-watching }()
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
