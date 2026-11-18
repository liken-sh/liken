package main

// Discovery finds the WiiM amps on the local network with mDNS and SSDP,
// and records the current address of each amp by its UUID. It creates a
// Receiver for each amp that no Receiver names. When a person declares a
// Receiver for an amp, discovery deletes the Receiver it created for it.

import (
	"context"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/liken-sh/liken/equipment-operator/upnp"
	"github.com/liken-sh/liken/equipment-operator/wiim"
)

// discover is a variable so that a test can replace the search.
var discover = wiim.Discover

// discoveryWindow is how long one search listens. Multicast loses
// packets, so wiim.Discover repeats its queries during the window.
//
// Discovery searches on a timer because the amps announce nothing that
// a listener could follow. An amp that loses power sends no goodbye, and
// a capture of the mDNS and SSDP groups showed the amps only replying to
// queries, with no SSDP NOTIFY and no unsolicited mDNS record. So
// discovery learns that an amp left only when several searches in a row
// miss it. discoveryInterval is long because every UPnP media renderer
// on the network replies to each SSDP search.
const (
	discoveryWindow   = 4 * time.Second
	discoveryInterval = 30 * time.Second
)

// discovery records the amps that recent searches found and reconciles
// the Receivers it created for them. Multicast drops packets, so one
// missed search does not delete a Receiver. Only discoveryMisses full
// searches in a row that miss the amp do. The count starts when the
// operator starts, so an amp that no search has found yet has missed
// every search so far.
type discovery struct {
	client *Client
	wake   func()
	// dial opens the connection that reads a device's UPnP description.
	dial dialFunc
	// receivers is the Receiver watch's store, which reconcile reads.
	receivers *watchStore
	// log receives the lines about devices and Receivers. A failed API
	// call is written to os.Stderr.
	log io.Writer
	// now returns the time of each search, for the span in the reason
	// of a delete. A test replaces it.
	now func() time.Time

	// reconciling serializes reconcile, which a search and skip call
	// from different goroutines.
	reconciling sync.Mutex

	mutex sync.Mutex
	// devices maps each UUID to the device that the latest search to
	// find it returned.
	devices map[string]wiim.Device
	// foreign maps the UUID of each device whose getStatusEx reply
	// reported a project other than a WiiM to that project.
	foreign map[string]string
	// descriptions caches each device's UPnP description for the life
	// of the process. unread marks the devices whose last read failed,
	// so that judge logs the failure once.
	descriptions map[string]upnp.Description
	unread       map[string]bool
	// searches counts the full searches since the operator started.
	// firstSearch and lastSearch are the times of the first and the
	// latest.
	searches    int
	firstSearch time.Time
	lastSearch  time.Time
	// foundIn maps each amp to the number of the latest search that
	// found it. missedSince maps it to the time of the first search that
	// missed it after that.
	foundIn     map[string]int
	missedSince map[string]time.Time
}

// discoveryMisses is how many full searches in a row must miss an amp
// before the operator forgets its address and deletes the Receiver it
// created for it.
const discoveryMisses = 3

func newDiscovery(client *Client, wake func()) *discovery {
	return &discovery{
		client:       client,
		wake:         wake,
		dial:         dialTCP,
		log:          os.Stderr,
		now:          time.Now,
		devices:      map[string]wiim.Device{},
		foreign:      map[string]string{},
		descriptions: map[string]upnp.Description{},
		unread:       map[string]bool{},
		foundIn:      map[string]int{},
		missedSince:  map[string]time.Time{},
	}
}

// address returns the address that discovery last found for a UUID in
// any spelling, or an empty string when it has found none.
func (d *discovery) address(uuid string) string {
	d.mutex.Lock()
	defer d.mutex.Unlock()
	return d.devices[wiim.NormalizeUUID(uuid)].Address
}

// skip records that a device's getStatusEx reply reported a project
// other than a WiiM, and reconciles at once, so the Receiver that
// discovery created for the device is deleted before the next search.
// The driver passes the project from the reply it already reads, so the
// check sends no extra request. skip logs only when the project of the
// device changes.
func (d *discovery) skip(uuid, project, address string) {
	uuid = wiim.NormalizeUUID(uuid)
	d.mutex.Lock()
	changed := d.foreign[uuid] != project
	d.foreign[uuid] = project
	d.mutex.Unlock()
	if !changed {
		return
	}
	fmt.Fprintf(d.log, "discovery skipped the LinkPlay device %s at %s: its project is %s, which is not a WiiM\n", uuid, address, project)
	if err := d.reconcile(); err != nil {
		fmt.Fprintf(os.Stderr, "reconciling the discovered Receivers: %v\n", err)
	}
}

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

// once runs one search, reads the UPnP description of each device that
// has none cached, and reconciles the Receivers. A search that ctx cut
// short did not listen for its whole window, so once discards it and it
// does not count as a miss. once calls wake only when an address was
// added, changed, or removed, because addresses are all that the
// Receiver loop reads from discovery. A Receiver that reconcile creates
// or deletes starts a pass through the Receiver watch.
func (d *discovery) once(ctx context.Context) {
	found := discover(ctx, discoveryWindow)
	if ctx.Err() != nil {
		return
	}
	moved := d.store(found)
	d.judge(ctx, found)
	if err := d.reconcile(); err != nil {
		fmt.Fprintf(os.Stderr, "reconciling the discovered Receivers: %v\n", err)
	}
	if moved {
		d.wake()
	}
}

// store records the result of one full search, and returns true when
// an address was added, changed, or removed. An amp that the search
// missed keeps its address until discoveryMisses searches in a row have
// missed it.
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

// missed returns how many full searches in a row have missed an amp,
// and the time from the first of those searches to the latest.
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

// knownDevices returns a copy of the devices that discovery has an
// address for. That includes an amp that the latest searches missed
// fewer than discoveryMisses times.
func (d *discovery) knownDevices() []wiim.Device {
	d.mutex.Lock()
	defer d.mutex.Unlock()
	devices := make([]wiim.Device, 0, len(d.devices))
	for _, device := range d.devices {
		devices = append(devices, device)
	}
	return devices
}

// discoveredName returns the name of the Receiver that discovery
// creates for an amp. It uses the UUID because an amp's friendly name
// can change, and lowercase because a Kubernetes name must be.
func discoveredName(uuid string) string {
	return strings.ToLower(uuid)
}

// reconcile creates a Receiver for each known WiiM that no Receiver
// names, and deletes each Receiver that discovery created for which
// pruneReason returns a reason.
func (d *discovery) reconcile() error {
	d.reconciling.Lock()
	defer d.reconciling.Unlock()
	// readReceivers includes a Receiver that discovery just created but
	// the watch has not delivered yet (objectcache.go). Without it, the
	// next reconcile would create the same Receiver again.
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

	for _, device := range d.knownDevices() {
		if d.notWiiM(device.UUID) != "" {
			// Another brand built on the LinkPlay platform.
			continue
		}
		if len(claimants[device.UUID]) > 0 {
			continue
		}
		if _, err := ApplyDiscoveredReceiver(d.client, discoveredName(device.UUID), device.UUID); err != nil {
			fmt.Fprintf(os.Stderr, "creating Receiver %s: %v\n", discoveredName(device.UUID), err)
			continue
		}
		fmt.Fprintf(d.log, "discovery found the WiiM %s at %s, and no Receiver declares it, so discovery created Receiver %s\n",
			device.UUID, device.Address, discoveredName(device.UUID))
	}

	for uuid, receiver := range owned {
		name := receiver.Metadata.Name
		reason := d.pruneReason(uuid, name, claimants[uuid])
		if reason == "" {
			continue
		}
		if err := DeleteReceiver(d.client, name); err != nil {
			fmt.Fprintf(os.Stderr, "deleting Receiver %s: %v\n", name, err)
			continue
		}
		fmt.Fprintf(d.log, "deleted the discovered Receiver %s: %s\n", name, reason)
	}
	return nil
}

// pruneReason returns why discovery should delete the Receiver it
// created for an amp, or an empty string to keep it. An amp that has
// missed fewer than discoveryMisses searches keeps its Receiver, which
// reports the amp unreachable while discovery has no address for it.
func (d *discovery) pruneReason(uuid, name string, claimants []string) string {
	if others := slices.DeleteFunc(slices.Clone(claimants), func(other string) bool { return other == name }); len(others) > 0 {
		return fmt.Sprintf("Receiver %s names the WiiM %s", strings.Join(others, ", "), uuid)
	}
	if reason := d.notWiiM(uuid); reason != "" {
		return reason
	}
	misses, span := d.missed(uuid)
	if misses < discoveryMisses {
		return ""
	}
	return fmt.Sprintf("the WiiM %s missed %d searches in a row over %d s", uuid, misses, int(span.Seconds()))
}

// resolvedAddress returns the address in a Receiver's protocol block,
// or, for a WiiM block with no address, the address that discovery
// found for its UUID. The controller replaces a unit whose address
// differs from this one, so the unit reconnects when an amp moves.
func (c *controller) resolvedAddress(spec *ReceiverSpec) string {
	if address := protocolAddress(spec); address != "" {
		return address
	}
	if spec.Wiim != nil {
		return c.discovery.address(spec.Wiim.UUID)
	}
	return ""
}
