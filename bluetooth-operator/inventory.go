package main

// The inventory pass makes the Adapter, Peripheral, and
// PairingRequest objects match the radio. It reads bluetoothd's object
// tree once, then reconciles the Adapter, the Peripherals, and the
// PairingRequests in that order. The Adapter comes first because it
// owns every Peripheral, and a PairingRequest that pairs a device
// creates the device's Peripheral under the Adapter.
//
// The pass reads the objects from the watches' stores (objectcache.go).
// main.go runs it before the slice publisher and the bond store.

import (
	"errors"
	"fmt"
	"os"
	"time"
	"unicode/utf8"

	"github.com/liken-sh/liken/bluetooth-operator/bonds"
	"github.com/liken-sh/liken/kubernetes/apiclient"
	"github.com/liken-sh/liken/kubernetes/events"
)

// followUpDelay is how long the loop waits before the next pass while
// a pairing window is open or an unpair is between two of its steps.
// A person is waiting in both cases, so the delay is much shorter than
// backstopInterval (main.go).
const followUpDelay = 2 * time.Second

// inventory reconciles the Adapter, its Peripherals, and the
// PairingRequests for this node's radio.
type inventory struct {
	client    *apiclient.Client
	radio     radio
	nodeName  string
	namespace string

	// cache holds the watches' stores (objectcache.go). The zero value
	// reads the API server every time.
	cache objectCache

	// now is the clock. A test replaces it to run a pairing window out
	// without waiting.
	now func() time.Time

	// removed is the set of devices that the publisher has removed from
	// the slice for an unpair. The unpair removes the bond from
	// bluetoothd only after that. The slice has no entry that shows a
	// device was removed, so this set is the only record of it.
	removed map[bonds.Address]bool

	// removing is the set of devices this pass asked the publisher to
	// remove. They move to removed when the publisher reports that it
	// wrote the slice.
	removing map[bonds.Address]bool

	// connects connects each bonded audio device that is not connected,
	// and keeps each device's retry backoff between passes.
	connects *connector

	// inputs reconnects a controller whose link is up but that has no
	// HID device (inputrecovery.go).
	inputs *inputRecovery

	// relays holds each bonded controller's virtual input devices. An
	// unpair stops the controller's relay when it removes the device
	// from the slice. Each Peripheral status write passes the Connected
	// condition to the relay, which then sends a key press for a remote
	// that went to sleep (wake.go).
	relays *relays

	// ends records the reason BlueZ gave when each link ended, until a
	// status write reports it (linkend.go).
	ends *linkEnds

	// windowOpen is true while this operator has made the radio
	// discoverable and pairable for a request. The pass reads the
	// object tree before it opens a window, so the tree does not show
	// the window until the next pass. The pass that closes the window
	// checks this field instead.
	windowOpen bool

	// follow tells the edit watcher (editwatch.go) which radio to watch
	// Peripherals for. It does nothing until main sets it.
	follow func(adapterKey string)

	// metrics is this operator's Prometheus registry. A nil value
	// records nothing, so a test that checks no metric leaves it unset.
	metrics *metrics

	// recorder posts the Events (events.go). A nil recorder posts
	// nothing, so a test that checks no Event leaves it unset.
	recorder *events.Recorder

	// node is the Node this pod runs on. RadioClaimed and RadioLost are
	// posted on the Node, because the Adapter may already be deleted
	// when the radio goes away.
	node events.ObjectReference

	// lastRadio is the address of the radio in the last valid read of
	// bluetoothd, or the zero address when that read had none.
	lastRadio bonds.Address

	// settings is the directory of the pod's settings volume. It has
	// a privacy file with the value bluetoothd started with
	// (privacy.go) and a btmon file with the trace setting (btmon.go).
	settings string

	// btmon is the value in the btmon file after this pass, which the
	// pass writes to the Adapter's status.btmon. It is nil when the pass could
	// not read the file, and status.btmon then keeps its current value.
	btmon *bool

	// privacy is the value this pass writes to the Adapter's
	// status.privacy. It is empty when the pass could not read the
	// settings file, and status.privacy then keeps its current value.
	privacy string
}

func newInventory(client *apiclient.Client, radio radio, inputRelays *relays, nodeName, namespace string, readings *metrics) *inventory {
	i := &inventory{
		client:    client,
		radio:     radio,
		relays:    inputRelays,
		nodeName:  nodeName,
		namespace: namespace,
		now:       time.Now,
		removed:   map[bonds.Address]bool{},
		removing:  map[bonds.Address]bool{},
		ends:      newLinkEnds(),
		follow:    func(string) {},
		metrics:   readings,
		settings:  settingsRoot,
	}
	// The closures read i.now when they are called, so a test that
	// replaces the field after construction moves the clocks of the
	// connector and the input recovery too.
	i.connects = newConnector(radio, func() time.Time { return i.now() })
	i.inputs = newInputRecovery(radio, inputRelays, func() time.Time { return i.now() })
	return i
}

// inventoryPass is the result of one pass, which the loop in main.go
// passes to the slice publisher and the bond store.
type inventoryPass struct {
	// excluded is the set of controllers the slice must not publish,
	// keyed by MAC address as the paired set is. An unpair removes the
	// device from the slice before it removes the bond, so that the
	// scheduler cannot allocate a new claim to the device in between.
	excluded map[string]bool

	// owners maps each bond to the owner reference of its Peripheral,
	// which goes on the bond's Secret. A bond with no Peripheral yet
	// gets no Secret on this pass, and the next pass writes it.
	owners map[bonds.Address]OwnerReference

	// unpairing is the set of bonds that an unpair is removing. Their
	// Secrets are not rewritten, because the unpair removes the bond
	// and garbage collection deletes the Secret with its Peripheral.
	unpairing map[bonds.Address]bool

	// adapter is the Adapter this pass reconciled, or nil when the pass
	// failed before it read or created one. It owns the identity
	// Secret, and a change to its spec.privacy restarts the pod.
	adapter *Adapter

	// privacy is the value in the settings file, which is the value
	// bluetoothd started with. It is empty when the pod has no settings
	// file or the pass could not read it, and the pod then does not
	// restart for a privacy change.
	privacy string

	// again is how long the loop waits before it runs another pass.
	// Zero means no follow-up pass is needed.
	again time.Duration

	// ok is false when any part of the pass failed, and the loop then
	// schedules a retry.
	ok bool
}

// noteRadio posts an Event when the radio in a valid read of
// bluetoothd differs from the radio in the last one. When the pod
// starts, bluetoothd has not published its tree yet, so a first read
// with no radio is not a change and posts nothing.
func (i *inventory) noteRadio(address bonds.Address) {
	previous := i.lastRadio
	if address == previous {
		return
	}
	i.lastRadio = address
	if previous != (bonds.Address{}) {
		i.recorder.Warning(i.node, reasonRadioLost, fmt.Sprintf("bluetoothd no longer reports the radio at %s", previous))
	}
	if address != (bonds.Address{}) {
		i.recorder.Normal(i.node, reasonRadioClaimed, fmt.Sprintf("bluetoothd reports the radio at %s", address))
	}
}

// reconcile runs one pass over the Adapter, the Peripherals, and the
// PairingRequests.
func (i *inventory) reconcile() inventoryPass {
	pass := inventoryPass{
		excluded:  map[string]bool{},
		owners:    map[bonds.Address]OwnerReference{},
		unpairing: map[bonds.Address]bool{},
		ok:        true,
	}

	snapshot, err := i.radio.Snapshot()
	// A reply that reports no adapter is still a successful read
	// of bluetoothd, so it counts as a valid observation. Only a failed
	// call, such as the D-Bus connection going away, does not.
	i.metrics.recordObservation(sourceBlueZ, err == nil || errors.Is(err, ErrNoAdapter), i.now())
	if errors.Is(err, ErrNoAdapter) {
		// Either bluetoothd has not published its tree yet or the radio
		// is gone. With no radio, the only work left is to release an
		// Adapter object for a radio that this machine no longer has.
		i.metrics.setAdapterPresent(false)
		i.noteRadio(bonds.Address{})
		i.releaseDepartedAdapters(bonds.Address{})
		pass.ok = false
		return pass
	}
	if err != nil {
		// The call failed, so the read does not show whether the adapter
		// is still there. The adapter-present gauge keeps its value from
		// the last valid read, and bluetooth_observation_valid reports
		// that this reading is stale.
		fmt.Fprintf(os.Stderr, "reading the radio: %v\n", err)
		pass.ok = false
		return pass
	}
	i.metrics.setAdapterPresent(true)
	i.noteRadio(snapshot.Adapter.Address)

	i.releaseDepartedAdapters(snapshot.Adapter.Address)
	i.follow(snapshot.Adapter.Address.Key())

	// The adapter's Connectable property turns page scan on. With page
	// scan off, the radio ignores a bonded controller that tries to
	// reconnect, and nothing reports an error. A new bluetoothd starts
	// the adapter with page scan off, so each pass sets Connectable.
	// This runs before the API server writes, because a failed write
	// returns from the pass. connect.go handles connections in the
	// other direction, from this radio to a bonded speaker.
	if snapshot.Adapter.Powered && !snapshot.Adapter.Connectable {
		if err := i.radio.SetAdapterConnectable(true); err != nil {
			fmt.Fprintf(os.Stderr, "making the adapter connectable: %v\n", err)
			pass.ok = false
		}
	}

	// ensureAdapter writes i.privacy to status.privacy, so read the
	// settings file first. A pod with no privacy file started
	// bluetoothd with privacy off.
	started, err := readStartedPrivacy(i.settings)
	switch {
	case err != nil:
		fmt.Fprintf(os.Stderr, "reading the privacy that bluetoothd started with: %v\n", err)
		i.privacy = ""
		pass.ok = false
	case started == "":
		i.privacy = privacyOff
	default:
		i.privacy = started
	}
	pass.privacy = started

	adapterStart := i.now()
	adapter, err := i.ensureAdapter(snapshot.Adapter)
	i.metrics.timeReconcile(adapterKind, adapterStart, i.now())
	if err != nil {
		fmt.Fprintf(os.Stderr, "reconciling the Adapter for %s: %v\n", snapshot.Adapter.Address, err)
		i.metrics.countReconcileError(adapterKind)
		pass.ok = false
		return pass
	}
	pass.adapter = adapter
	if i.btmon == nil || *i.btmon != adapter.Spec.Btmon {
		// The btmon file could not be read or written, so spec.btmon is
		// not applied yet and the pass must run again.
		pass.ok = false
	}

	// Walk sysfs once for the whole pass. reconcilePeripherals writes
	// each controller's battery level from it, and the input recovery
	// reads which connected controllers have a HID device.
	// claimedDevices reads the CDI specs once for all Peripherals too.
	peripheralStart, wasOK := i.now(), pass.ok
	kernel := discoverHIDDevices(draSysfsRoot, snapshot.Adapter.Address)
	i.reconcilePeripherals(adapter, snapshot, kernelBatteries(kernel), claimedDevices(), &pass)
	// Connecting and input recovery run after reconcilePeripherals
	// because it fills pass.unpairing, and a device that is being
	// unpaired must not be connected. They are timed and counted
	// together with the Peripheral part.
	i.connects.reconcile(snapshot, &pass)
	i.inputs.reconcile(snapshot, kernel, &pass)
	i.metrics.timeReconcile(peripheralKind, peripheralStart, i.now())
	if wasOK && !pass.ok {
		i.metrics.countReconcileError(peripheralKind)
	}

	requestStart, wasOK := i.now(), pass.ok
	i.reconcileRequests(adapter, snapshot, &pass)
	i.metrics.timeReconcile(pairingRequestKind, requestStart, i.now())
	if wasOK && !pass.ok {
		i.metrics.countReconcileError(pairingRequestKind)
	}
	return pass
}

// published moves the devices in removing to removed. The
// caller calls it only after the publisher reports that it wrote the
// slice. When the write fails, the device is still in the slice and
// the bond must stay.
func (i *inventory) published() {
	for device := range i.removing {
		i.removed[device] = true
	}
	i.removing = map[bonds.Address]bool{}
}

// removeFromSlice tells the publisher to leave a device out of the
// slice on this pass.
func (i *inventory) removeFromSlice(device bonds.Address, pass *inventoryPass) {
	i.removing[device] = true
	pass.excluded[macFromDeviceName(device.Key())] = true
}

// runAgainIn asks for another pass after the given delay. When several
// parts of the pass ask, the shortest delay wins.
func (pass *inventoryPass) runAgainIn(after time.Duration) {
	if after <= 0 {
		return
	}
	if pass.again == 0 || after < pass.again {
		pass.again = after
	}
}

// truncateToBytes cuts s to at most limit bytes, backing up to a rune
// boundary. A name can be in any script, and the API server rejects a
// string that ends in the middle of a multi-byte rune as invalid
// UTF-8, which would fail the whole write and not just the name.
func truncateToBytes(s string, limit int) string {
	if len(s) <= limit {
		return s
	}
	cut := limit
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut]
}
