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
// Receivers it owns for them. An amp a search missed keeps its last
// address and its Receiver until it has missed discoveryMisses
// searches, because multicast drops a device for one search and the
// amp has not moved.
type discovery struct {
	client *Client
	wake   func()

	mutex   sync.Mutex
	devices map[string]wiim.Device
	misses  map[string]int
}

// discoveryMisses is how many searches in a row may miss an amp before
// the operator forgets it. One missed search is multicast, not an amp
// that left.
const discoveryMisses = 3

func newDiscovery(client *Client, wake func()) *discovery {
	return &discovery{
		client:  client,
		wake:    wake,
		devices: map[string]wiim.Device{},
		misses:  map[string]int{},
	}
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
	if err := d.reconcile(); err != nil {
		fmt.Fprintf(os.Stderr, "reconciling discovered receivers: %v\n", err)
	}
	d.wake()
}

// store folds one search into the devices the operator holds. An amp
// the search found takes its address and clears its miss count. An amp
// the search missed keeps the address it had, and is forgotten once it
// has missed discoveryMisses searches.
func (d *discovery) store(found []wiim.Device) {
	seen := make(map[string]bool, len(found))
	d.mutex.Lock()
	defer d.mutex.Unlock()
	for _, device := range found {
		seen[device.UUID] = true
		d.devices[device.UUID] = device
		d.misses[device.UUID] = 0
	}
	for uuid := range d.devices {
		if seen[uuid] {
			continue
		}
		d.misses[uuid]++
		if d.misses[uuid] >= discoveryMisses {
			delete(d.devices, uuid)
			delete(d.misses, uuid)
		}
	}
}

// held copies the devices the operator holds, which keep an amp through
// a search that missed it.
func (d *discovery) held() []wiim.Device {
	d.mutex.Lock()
	defer d.mutex.Unlock()
	devices := make([]wiim.Device, 0, len(d.devices))
	for _, device := range d.devices {
		devices = append(devices, device)
	}
	return devices
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
func (d *discovery) reconcile() error {
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

	// The reconcile follows what the operator holds, not the last search
	// alone, so an amp one search missed keeps the Receiver the operator
	// made for it.
	held := d.held()
	heldSet := map[string]bool{}
	for _, device := range held {
		heldSet[device.UUID] = true
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
		if heldSet[uuid] && !claimedByOther(claimants[uuid], receiver.Metadata.Name) {
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
