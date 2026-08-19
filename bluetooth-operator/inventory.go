package main

// The pass that makes the pairing objects agree with the radio.
//
// It runs beside the slice publish and the bond persist, on the same
// wakes and through the same settle window, and it takes one read of
// bluetoothd's object tree for the whole pass. The three parts run in
// order, because each one depends on what the one before it wrote: the
// Adapter is the owner every Pairing needs, a Pairing is the owner
// every bond Secret needs, and a PairingRequest that pairs a device
// creates a Pairing under the same Adapter.
//
// Nothing here holds a cache of the objects between passes. Every pass
// reads the Adapter, lists the Pairings for this radio, and lists the
// PairingRequests, and acts on what it read. The one piece of state
// that does survive a pass is which devices the publisher has already
// dropped from the slice, because that is a fact about a write this
// program made and cannot read back from the objects.

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/liken-sh/bluetooth-operator/bonds"
)

// followUpDelay is how soon the loop runs again while the pass has
// work in flight: a window that is open, a teardown between two of its
// steps, or a finished request whose TTL is close. It is short because
// each of those is a person waiting, and it is bounded because the
// pass asks for exactly one follow-up.
const followUpDelay = 2 * time.Second

// inventory reconciles the Adapter, its Pairings, and the
// PairingRequests aimed at it.
type inventory struct {
	client    *Client
	radio     radio
	nodeName  string
	namespace string

	// now is the clock. It is a field so that a test can run a window
	// out without waiting for it.
	now func() time.Time

	// retired names the devices the publisher has already left out of
	// the slice. A teardown removes a bond from bluetoothd only after
	// the device is out of the published inventory, and the slice
	// holds no record of a device that is not in it, so this is the
	// only account of that step.
	retired map[bonds.Address]bool

	// retiring names the devices this pass asked the publisher to drop.
	// They become retired when the publisher reports that it wrote the
	// slice.
	retiring map[bonds.Address]bool

	// windowOpen records that this operator has the radio discoverable
	// and pairable for a request. The radio's own report of that state
	// lags a pass, because the pass reads the tree before it opens
	// anything, so the pass that closes a window reads this instead.
	windowOpen bool
}

func newInventory(client *Client, radio radio, nodeName, namespace string) *inventory {
	return &inventory{
		client:    client,
		radio:     radio,
		nodeName:  nodeName,
		namespace: namespace,
		now:       time.Now,
		retired:   map[bonds.Address]bool{},
		retiring:  map[bonds.Address]bool{},
	}
}

// inventoryPass holds one pass's results for the rest of the loop.
type inventoryPass struct {
	// keepOut names the controllers the slice must not publish, keyed
	// the way the paired set is keyed. A device under teardown leaves
	// the inventory before its bond is removed, so no new claim can be
	// allocated to it in the moment between the two.
	keepOut map[string]bool

	// owners names each bond's Pairing, which is the owner reference on
	// its Secret. A bond with no Pairing yet gets no Secret this
	// pass, and the next pass writes it.
	owners map[bonds.Address]OwnerReference

	// unpairing names the bonds a teardown is working through. Their
	// Secrets are not rewritten, because the teardown removes each bond
	// and garbage collection takes each Secret with its Pairing.
	unpairing map[bonds.Address]bool

	// again is how long until the loop must run this pass again, and
	// zero means no follow-up is needed.
	again time.Duration

	// ok reports whether the pass finished everything it set out to do.
	ok bool
}

// reconcile runs one pass over the three objects.
func (i *inventory) reconcile() inventoryPass {
	pass := inventoryPass{
		keepOut:   map[string]bool{},
		owners:    map[bonds.Address]OwnerReference{},
		unpairing: map[bonds.Address]bool{},
		ok:        true,
	}

	snapshot, err := i.radio.Snapshot()
	if errors.Is(err, ErrNoAdapter) {
		// bluetoothd has not published its tree yet, or the radio has
		// departed. Either way this operator holds no radio right now,
		// and the one thing it can still do is release an Adapter for a
		// radio that this machine no longer has.
		i.releaseDepartedAdapters(bonds.Address{})
		pass.ok = false
		return pass
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "reading the radio: %v\n", err)
		pass.ok = false
		return pass
	}

	i.releaseDepartedAdapters(snapshot.Adapter.Address)

	// A radio that is not connectable answers no bonded device: page
	// scan is off, so a controller's reconnect button and a speaker's
	// own connect loop reach nothing, and no error appears anywhere. A
	// fresh bluetoothd starts the adapter that way, so every pass
	// asserts the setting instead of trusting the startup state, the
	// same write-on-divergence rule the rest of the reconcile follows.
	// It runs before the API-server writes below, because the radio's
	// health must not wait on them.
	if snapshot.Adapter.Powered && !snapshot.Adapter.Connectable {
		if err := i.radio.SetAdapterConnectable(true); err != nil {
			fmt.Fprintf(os.Stderr, "making the adapter connectable: %v\n", err)
			pass.ok = false
		}
	}

	adapter, err := i.ensureAdapter(snapshot.Adapter)
	if err != nil {
		fmt.Fprintf(os.Stderr, "reconciling the Adapter for %s: %v\n", snapshot.Adapter.Address, err)
		pass.ok = false
		return pass
	}

	i.reconcilePairings(adapter, snapshot, &pass)
	i.reconcileRequests(adapter, snapshot, &pass)
	return pass
}

// published moves the devices this pass asked to drop into the set the
// teardown treats as retired. The caller calls it after the publisher
// reports that the slice was written, because a write that failed
// leaves the device in the inventory and the bond must stay until the
// device is really out of the slice.
func (i *inventory) published() {
	for device := range i.retiring {
		i.retired[device] = true
	}
	i.retiring = map[bonds.Address]bool{}
}

// retire asks the publisher to leave a device out of the slice on this
// pass.
func (i *inventory) retire(device bonds.Address, pass *inventoryPass) {
	i.retiring[device] = true
	pass.keepOut[macFromDeviceName(device.Key())] = true
}

// runAgainIn records how soon the loop must run this pass again, and
// keeps the soonest value when several parts of the pass supply one.
func (pass *inventoryPass) runAgainIn(after time.Duration) {
	if after <= 0 {
		return
	}
	if pass.again == 0 || after < pass.again {
		pass.again = after
	}
}

// claimedDevices names the devices the prepared claims on this node
// hold right now, by their published device names. The media bus can
// be among them. The one caller looks up controllers by their own
// names, and a controller's name never equals the bus's.
//
// The record is the CDI spec files this driver wrote. The kubelet
// prepares a claim before the consumer's container starts and
// unprepares it after the container is gone. Prepare creates those
// files and unprepare removes them, so a file that names a device is a
// claim that still holds it. The alternative is to list
// every ResourceClaim in the cluster and read its allocation, which
// widens this operator's grant to every workload's claims for a fact
// that is already on this node's disk.
//
// A claim that the scheduler allocated but the kubelet has not
// prepared yet is not in the result. That claim's pod is not running,
// so nothing is using the controller, and the device leaves the slice
// before the bond is removed, which keeps the allocation from
// reaching prepare afterwards.
func claimedDevices() map[string]bool {
	held := map[string]bool{}
	entries, err := os.ReadDir(cdiDir)
	if err != nil {
		// No directory means no claim has been prepared on this boot.
		return held
	}
	for _, entry := range entries {
		claimUID, ok := claimUIDFromSpecName(entry.Name())
		if !ok {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(cdiDir, entry.Name()))
		if err != nil {
			continue
		}
		var spec cdiSpec
		if err := json.Unmarshal(raw, &spec); err != nil {
			continue
		}
		for _, device := range spec.Devices {
			// prepare names each CDI device for the claim and the
			// allocated device together, so the allocated name is in the
			// file and this needs no call to the API server.
			allocated, ok := strings.CutPrefix(device.Name, claimUID+"-")
			if !ok {
				continue
			}
			held[allocated] = true
		}
	}
	return held
}

// truncateRunes cuts a free-text value to a byte limit, moving the cut
// back to a rune boundary.
//
// A person can name a controller in any script, the limits here count
// bytes, and a cut through the middle of a multi-byte rune produces a
// string the API server rejects as invalid UTF-8. That would fail the
// whole status write, not just the one name.
func truncateRunes(s string, limit int) string {
	if len(s) <= limit {
		return s
	}
	cut := limit
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut]
}
