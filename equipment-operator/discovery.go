package main

// The network discovery loop: it finds the WiiM amps on the local
// network, resolves an identity to a current address, and creates the
// Receivers this operator owns for amps no Receiver claims. A person's
// Receiver that names a discovered identity always wins: the operator
// creates nothing beside it and prunes its own copy.

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/liken-sh/equipment-operator/wiim"
)

// discover is wiim.Discover. It is a variable so a test replaces the
// search with a fixed result.
var discover = wiim.Discover

// The window a run searches for, and the wait between runs. Multicast
// misses a device, so a run repeats its search across the window; an
// amp does not move, so runs are far apart. Both are variables so a
// test holds them still.
var (
	discoveryWindow   = 4 * time.Second
	discoveryInterval = 30 * time.Second
)

// discovery holds the amps the operator last found and reconciles the
// Receivers it owns for them.
type discovery struct {
	client *Client
	wake   func()

	mutex   sync.Mutex
	devices map[string]wiim.Device
}

func newDiscovery(client *Client, wake func()) *discovery {
	return &discovery{client: client, wake: wake, devices: map[string]wiim.Device{}}
}

// address answers the address the operator last found for one identity,
// and an empty string when it has not found it yet. The identity is
// normalized, so every spelling of one amp finds the same entry.
func (d *discovery) address(uuid string) string {
	d.mutex.Lock()
	defer d.mutex.Unlock()
	return d.devices[wiim.NormalizeUUID(uuid)].Address
}

// run discovers until ctx ends.
func (d *discovery) run(ctx context.Context) {
	for ctx.Err() == nil {
		d.once(ctx)
		select {
		case <-ctx.Done():
			return
		case <-time.After(discoveryInterval):
		}
	}
}

// once is one search window and the Receiver reconcile that follows.
func (d *discovery) once(ctx context.Context) {
	found := discover(ctx, discoveryWindow)
	d.store(found)
	if err := d.reconcile(found); err != nil {
		fmt.Fprintf(os.Stderr, "reconciling discovered receivers: %v\n", err)
	}
	d.wake()
}

// store replaces the found set, so an amp that stopped answering drops
// out of the next address lookup.
func (d *discovery) store(found []wiim.Device) {
	devices := make(map[string]wiim.Device, len(found))
	for _, device := range found {
		devices[device.UUID] = device
	}
	d.mutex.Lock()
	d.devices = devices
	d.mutex.Unlock()
}

// discoveredName is the object name the operator gives an amp it found:
// its own identity, lowercased, because a friendly name moves and a
// UUID does not.
func discoveredName(uuid string) string {
	return strings.ToLower(uuid)
}

// reconcile creates a Receiver for every discovered amp no Receiver
// claims, and prunes the Receivers the operator owns whose amp is gone
// or whose identity a person's Receiver now claims.
func (d *discovery) reconcile(found []wiim.Device) error {
	list, err := ListReceivers(d.client)
	if err != nil {
		return err
	}
	claimants := map[string][]string{}
	owned := map[string]*Receiver{}
	for index := range list.Items {
		receiver := &list.Items[index]
		if receiver.Spec.Wiim == nil || receiver.Spec.Wiim.UUID == "" {
			continue
		}
		uuid := wiim.NormalizeUUID(receiver.Spec.Wiim.UUID)
		claimants[uuid] = append(claimants[uuid], receiver.Metadata.Name)
		if receiver.Metadata.Labels[discoveredLabel] != "" {
			owned[uuid] = receiver
		}
	}

	foundSet := map[string]bool{}
	for _, device := range found {
		foundSet[device.UUID] = true
		if len(claimants[device.UUID]) > 0 {
			// A Receiver already names this amp, so discovery creates
			// nothing and defers to what stands.
			continue
		}
		if _, err := ApplyDiscoveredReceiver(d.client, discoveredName(device.UUID), device.UUID); err != nil {
			fmt.Fprintf(os.Stderr, "creating receiver %s: %v\n", discoveredName(device.UUID), err)
		}
	}

	for uuid, receiver := range owned {
		if foundSet[uuid] && !claimedByOther(claimants[uuid], receiver.Metadata.Name) {
			continue
		}
		if err := DeleteReceiver(d.client, receiver.Metadata.Name); err != nil {
			fmt.Fprintf(os.Stderr, "pruning receiver %s: %v\n", receiver.Metadata.Name, err)
		}
	}
	return nil
}

// claimedByOther answers whether a Receiver other than the named one
// claims the identity, which means a person took the amp over and the
// operator's copy steps aside.
func claimedByOther(claimants []string, name string) bool {
	for _, claimant := range claimants {
		if claimant != name {
			return true
		}
	}
	return false
}

// resolvedAddress is the address a Receiver's protocol block declares,
// or the one discovery found for its identity when it declares none. A
// discovered address that moves is a different wiring too, so the unit
// is replaced and redialled.
func (c *controller) resolvedAddress(spec *ReceiverSpec) string {
	if address := protocolAddress(spec); address != "" {
		return address
	}
	if spec.Wiim != nil {
		return c.discovery.address(spec.Wiim.UUID)
	}
	return ""
}
