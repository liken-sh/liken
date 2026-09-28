package main

// The network discovery loop: it finds the WiiM amps on the local
// network, resolves an identity to a current address, and creates the
// Receivers this operator owns for amps no Receiver claims. A person's
// Receiver that names a discovered identity always wins: the operator
// creates nothing beside it and prunes its own copy.

import (
	"context"
	"fmt"
	"io"
	"os"
	"slices"
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
//
// The wait is a backstop, and it is the only evidence discovery has.
// A search finds an amp that joined and an address that moved, and a
// run of searches that miss an amp is the proof that it left. An amp
// that loses power or its network sends no goodbye. A passive capture
// of both groups heard no SSDP NOTIFY and no unsolicited mDNS record
// from the amps, only their answers to a query. So a
// listener alone would never learn that an amp left, and would learn a
// new address only if the amp announced it. One run sends two mDNS
// queries and two SSDP searches over the window. The amps answer the
// mDNS query on the group, and every UPnP media renderer on the LAN
// answers each search.
var (
	discoveryWindow   = 4 * time.Second
	discoveryInterval = 30 * time.Second
)

// discovery holds the amps the operator last found and reconciles the
// Receivers it owns for them. It deletes a Receiver it owns only on
// evidence that the amp left: discoveryMisses full searches in a row
// that missed it. Multicast drops a device for one search, and an
// operator that just started has heard from no amp yet, so neither is
// evidence. The count starts with the operator: an amp no search has
// found since the start has missed every search since the start.
type discovery struct {
	client *Client
	wake   func()
	// receivers holds the Receiver watch's store, which reconcile reads.
	receivers *watchStore
	// log takes a line for each Receiver discovery creates or deletes.
	log io.Writer
	// now stamps each search, so a delete states how long the amp was
	// missed. It is a field so a test holds the clock.
	now func() time.Time

	mutex   sync.Mutex
	devices map[string]wiim.Device
	// searches counts the full searches since the start, and
	// firstSearch and lastSearch stamp the first and the latest.
	searches    int
	firstSearch time.Time
	lastSearch  time.Time
	// foundIn is the number of the search that last found each amp, and
	// missedSince stamps the first search that missed it after that.
	foundIn     map[string]int
	missedSince map[string]time.Time
}

// discoveryMisses is how many full searches in a row must miss an amp
// before the operator forgets its address and deletes the Receiver it
// made for it. One missed search is multicast, not an amp that left.
const discoveryMisses = 3

func newDiscovery(client *Client, wake func()) *discovery {
	return &discovery{
		client:      client,
		wake:        wake,
		log:         os.Stderr,
		now:         time.Now,
		devices:     map[string]wiim.Device{},
		foundIn:     map[string]int{},
		missedSince: map[string]time.Time{},
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

// once is one search window and the Receiver reconcile that follows. A
// search that ctx cut short did not run its full window, so it is no
// evidence that an amp it missed left, and the operator does not count
// it. The Receiver loop reads only the addresses from discovery, so a
// search wakes it only when an address appeared, moved, or went. A
// Receiver that the reconcile creates or deletes wakes the loop through
// its watch.
func (d *discovery) once(ctx context.Context) {
	found := discover(ctx, discoveryWindow)
	if ctx.Err() != nil {
		return
	}
	moved := d.store(found)
	if err := d.reconcile(); err != nil {
		fmt.Fprintf(os.Stderr, "reconciling discovered receivers: %v\n", err)
	}
	if moved {
		d.wake()
	}
}

// store folds one full search into the devices the operator holds. An
// amp the search found takes its address and clears its misses. An amp
// the search missed keeps the address it had, and is forgotten once it
// has missed discoveryMisses searches. It answers whether an address
// appeared, moved, or went.
func (d *discovery) store(found []wiim.Device) bool {
	d.mutex.Lock()
	defer d.mutex.Unlock()
	now := d.now()
	d.searches++
	if d.searches == 1 {
		d.firstSearch = now
	}
	d.lastSearch = now
	moved := false
	for _, device := range found {
		if d.devices[device.UUID].Address != device.Address {
			moved = true
		}
		d.devices[device.UUID] = device
		d.foundIn[device.UUID] = d.searches
		delete(d.missedSince, device.UUID)
	}
	for uuid := range d.devices {
		if d.foundIn[uuid] == d.searches {
			continue
		}
		if _, ok := d.missedSince[uuid]; !ok {
			d.missedSince[uuid] = now
		}
		if d.searches-d.foundIn[uuid] >= discoveryMisses {
			delete(d.devices, uuid)
			moved = true
		}
	}
	return moved
}

// missed answers how many full searches in a row have missed one amp,
// and the time from the first of them to the latest. An amp no search
// has found since the start has missed every search since the start.
func (d *discovery) missed(uuid string) (int, time.Duration) {
	d.mutex.Lock()
	defer d.mutex.Unlock()
	misses := d.searches - d.foundIn[uuid]
	if misses == 0 {
		return 0, 0
	}
	since, ok := d.missedSince[uuid]
	if !ok {
		since = d.firstSearch
	}
	return misses, d.lastSearch.Sub(since)
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
// claims, and prunes the Receivers the operator owns whose amp full
// searches have missed discoveryMisses times in a row, or whose
// identity a person's Receiver now claims.
func (d *discovery) reconcile() error {
	// Discovery creates and deletes Receivers, and the next search reads
	// its own writes. The read answers a Receiver it created that the
	// store has not received yet, so discovery does not create it again
	// (objectcache.go).
	list, err := readReceivers(d.client, d.receivers)
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

	// The creates follow what the operator holds, not the last search
	// alone, so an amp one search missed keeps the Receiver the operator
	// made for it.
	for _, device := range d.held() {
		if len(claimants[device.UUID]) > 0 {
			// A Receiver already names this amp, so discovery creates
			// nothing and defers to what stands.
			continue
		}
		if _, err := ApplyDiscoveredReceiver(d.client, discoveredName(device.UUID), device.UUID); err != nil {
			fmt.Fprintf(os.Stderr, "creating receiver %s: %v\n", discoveredName(device.UUID), err)
			continue
		}
		fmt.Fprintf(d.log, "discovery found the WiiM %s at %s, which no Receiver names; created Receiver %s\n",
			device.UUID, device.Address, discoveredName(device.UUID))
	}

	for uuid, receiver := range owned {
		name := receiver.Metadata.Name
		reason := d.pruneReason(uuid, name, claimants[uuid])
		if reason == "" {
			continue
		}
		if err := DeleteReceiver(d.client, name); err != nil {
			fmt.Fprintf(os.Stderr, "pruning receiver %s: %v\n", name, err)
			continue
		}
		fmt.Fprintf(d.log, "deleted the discovered Receiver %s: %s\n", name, reason)
	}
	return nil
}

// pruneReason answers why the operator deletes the Receiver it owns for
// one amp, and an empty string when the Receiver stands. A person's
// Receiver that names the amp replaces the operator's copy at once. An
// amp that full searches missed discoveryMisses times in a row is gone.
// An amp with fewer misses keeps its Receiver, which reports the amp
// unreachable while discovery holds no address for it.
func (d *discovery) pruneReason(uuid, name string, claimants []string) string {
	if others := slices.DeleteFunc(slices.Clone(claimants), func(other string) bool { return other == name }); len(others) > 0 {
		return fmt.Sprintf("Receiver %s names the WiiM %s", strings.Join(others, ", "), uuid)
	}
	misses, span := d.missed(uuid)
	if misses < discoveryMisses {
		return ""
	}
	return fmt.Sprintf("the WiiM %s missed %d searches in a row over %d s", uuid, misses, int(span.Seconds()))
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
