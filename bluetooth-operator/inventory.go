package main

// The pass that makes the operator's objects agree with the radio.
//
// It runs beside the slice publish and the bond persist, on the same
// wakes and through the same settle window, and it takes one read of
// bluetoothd's object tree for the whole pass. The three parts run in
// order, because each one depends on what the one before it wrote: the
// Adapter is the owner every Peripheral needs, a Peripheral is the
// owner every bond Secret needs, and a PairingRequest that pairs a
// device creates a Peripheral under the same Adapter.
//
// Every pass reads the Adapter, lists the Adapters, the Peripherals for
// this radio, and the PairingRequests, and acts on what it read. The
// reads come from the watches' stores (objectcache.go), which hold what
// the API server sent last, and the pass keeps no copy of its own. A
// copy older than the pass's own last write, a store that has not
// finished its first read, and a Peripheral whose unpair runs are read
// from the API server instead. The
// one piece of state that does survive a pass is which devices the
// publisher has already dropped from the slice, because that is a fact
// about a write this program made and cannot read back from the
// objects.

import (
	"errors"
	"fmt"
	"os"
	"time"
	"unicode/utf8"

	"github.com/liken-sh/bluetooth-operator/bonds"
	"github.com/liken-sh/liken/kubernetes/apiclient"
	"github.com/liken-sh/liken/kubernetes/events"
)

// followUpDelay is how soon the loop runs again while the pass has
// work in flight: a window that is open, or a teardown between two of
// its steps. It is short because each of those is a person waiting,
// and it is bounded because the pass asks for exactly one follow-up.
const followUpDelay = 2 * time.Second

// inventory reconciles the Adapter, its Peripherals, and the
// PairingRequests aimed at it.
type inventory struct {
	client    *apiclient.Client
	radio     radio
	nodeName  string
	namespace string

	// cache is the watches' stores, which the pass reads in place of
	// the API server (objectcache.go). The zero value reads the API
	// server.
	cache objectCache

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

	// connects pages the bonded audio devices that are not connected,
	// and holds the backoff each device carries between passes.
	connects *connector

	// inputs reconnects a controller whose link is up with no HID device
	// behind it (inputrecovery.go).
	inputs *inputRecovery

	// relays holds each bonded controller's virtual input devices. A
	// teardown stops one, in the step that takes the device out of the
	// published inventory, and each status write arms or disarms the
	// press that wakes the room (wake.go).
	relays *relays

	// ends holds why each link ended, from BlueZ's signal until a status
	// write carries it (linkend.go).
	ends *linkEnds

	// windowOpen records that this operator has the radio discoverable
	// and pairable for a request. The radio's own report of that state
	// lags a pass, because the pass reads the tree before it opens
	// anything, so the pass that closes a window reads this instead.
	windowOpen bool

	// follow reports the radio this pod holds to the edit watcher
	// (editwatch.go), which watches that radio's Peripherals. It does
	// nothing until main sets it.
	follow func(adapterKey string)

	// metrics is this operator's Prometheus registry. A nil value
	// records nothing, which is what every test that has no reason to
	// check a metric gets by leaving the field unset.
	metrics *metrics

	// recorder posts the Events (events.go). A nil recorder posts
	// nothing, which is what a test that checks no Event gets.
	recorder *events.Recorder

	// node is the Node this pod runs on. The radio's arrival and
	// departure are posted on it, because the Adapter can be gone by
	// the time the radio is.
	node events.ObjectReference

	// radioHeld is the radio the last valid read of bluetoothd
	// reported, and the zero address when it reported none. A change
	// of it posts RadioClaimed or RadioLost.
	radioHeld bonds.Address

	// settings is the pod's settings volume. Its privacy file holds
	// the value bluetoothd started with (privacy.go), and its btmon
	// file holds the trace setting (btmon.go).
	settings string

	// btmon is the value in the btmon file after this pass, which the
	// Adapter's status.btmon reports. It is nil when the pass could not
	// read the file, and the status then keeps the value it has.
	btmon *bool

	// privacy is the value this pass reports in the Adapter's
	// status.privacy. It is empty when the pass could not read the
	// settings file, and the status then keeps the value it has.
	privacy string
}

func newInventory(client *apiclient.Client, radio radio, held *relays, nodeName, namespace string, readings *metrics) *inventory {
	i := &inventory{
		client:    client,
		radio:     radio,
		relays:    held,
		nodeName:  nodeName,
		namespace: namespace,
		now:       time.Now,
		retired:   map[bonds.Address]bool{},
		retiring:  map[bonds.Address]bool{},
		ends:      newLinkEnds(),
		follow:    func(string) {},
		metrics:   readings,
		settings:  settingsRoot,
	}
	// The connector reads the clock through a closure, because the
	// inventory's clock is a field a test replaces after construction,
	// and both must run on the same time.
	i.connects = newConnector(radio, func() time.Time { return i.now() })
	i.inputs = newInputRecovery(radio, held, func() time.Time { return i.now() })
	return i
}

// inventoryPass holds one pass's results for the rest of the loop.
type inventoryPass struct {
	// keepOut names the controllers the slice must not publish, keyed
	// the way the paired set is keyed. A device under teardown leaves
	// the inventory before its bond is removed, so no new claim can be
	// allocated to it in the moment between the two.
	keepOut map[string]bool

	// owners names each bond's Peripheral, which is the owner reference on
	// its Secret. A bond with no Peripheral yet gets no Secret this
	// pass, and the next pass writes it.
	owners map[bonds.Address]OwnerReference

	// unpairing names the bonds a teardown is working through. Their
	// Secrets are not rewritten, because the teardown removes each bond
	// and garbage collection takes each Secret with its Peripheral.
	unpairing map[bonds.Address]bool

	// adapter is the Adapter this pass reconciled, and nil when the
	// pass did not read one. It owns the identity Secret, and its
	// spec.privacy decides whether the pod restarts.
	adapter *Adapter

	// privacy is the value in the settings file, which bluetoothd
	// started with. It is empty when the pod has no settings file or
	// the pass could not read it, and the pod then never restarts for
	// privacy.
	privacy string

	// again is how long until the loop must run this pass again, and
	// zero means no follow-up is needed.
	again time.Duration

	// ok reports whether the pass finished everything it set out to do.
	ok bool
}

// noteRadio posts the change of radio that one valid read of
// bluetoothd shows. A read that reports no radio before any radio was
// reported is the ordinary start of a pod, before bluetoothd publishes
// its tree, and posts nothing.
func (i *inventory) noteRadio(address bonds.Address) {
	held := i.radioHeld
	if address == held {
		return
	}
	i.radioHeld = address
	if held != (bonds.Address{}) {
		i.recorder.Warning(i.node, reasonRadioLost, fmt.Sprintf("the radio at %s is gone from bluetoothd", held))
	}
	if address != (bonds.Address{}) {
		i.recorder.Normal(i.node, reasonRadioClaimed, fmt.Sprintf("bluetoothd holds the radio at %s", address))
	}
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
	// The read either answers or it does not, whatever it answers with.
	// A missing adapter is still bluetoothd's own report, over a call
	// that worked, so it is a valid observation like any other; only an
	// error from the call itself, such as the bus going away, is not.
	i.metrics.recordObservation(sourceBlueZ, err == nil || errors.Is(err, ErrNoAdapter), i.now())
	if errors.Is(err, ErrNoAdapter) {
		// bluetoothd has not published its tree yet, or the radio has
		// departed. Either way this operator holds no radio right now,
		// and the one thing it can still do is release an Adapter for a
		// radio that this machine no longer has.
		i.metrics.setAdapterPresent(false)
		i.noteRadio(bonds.Address{})
		i.releaseDepartedAdapters(bonds.Address{})
		pass.ok = false
		return pass
	}
	if err != nil {
		// The read itself failed, so nothing here says whether the
		// adapter is still there. The gauge keeps the value the last
		// valid read left it at, the rule the hardware triple states
		// for every reading behind an observation_valid gauge.
		fmt.Fprintf(os.Stderr, "reading the radio: %v\n", err)
		pass.ok = false
		return pass
	}
	i.metrics.setAdapterPresent(true)
	i.noteRadio(snapshot.Adapter.Address)

	i.releaseDepartedAdapters(snapshot.Adapter.Address)
	i.follow(snapshot.Adapter.Address.Key())

	// A radio that is not connectable answers no bonded device: page
	// scan is off, so a controller's reconnect button reaches nothing,
	// and no error appears anywhere. The outbound direction is this
	// operator's own: connect.go pages a bonded speaker instead of
	// waiting for one to page the radio. A fresh bluetoothd starts the
	// adapter with page scan off, so every pass
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

	// The settings file is read on each pass, before the Adapter's
	// status is written. A missing file is a pod whose bluetoothd
	// started with privacy off.
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
		// The pass could not read or write the btmon file. The retry
		// pass tries again.
		pass.ok = false
	}

	// One walk of sysfs answers the battery of every device this pass
	// writes, before any status write, so each Peripheral reads the level
	// the kernel reports now. The same walk says which connected
	// controllers have a HID device. claimedDevices answers from the same
	// CDI read the unpair teardown already makes, so the Peripheral phase
	// costs no second read of its own for the claimed gauge.
	peripheralStart, wasOK := i.now(), pass.ok
	kernel := discoverHIDDevices(draSysfsRoot, snapshot.Adapter.Address)
	i.reconcilePeripherals(adapter, snapshot, kernelBatteries(kernel), claimedDevices(), &pass)
	// The connects and the reconnects run after the Peripherals, because
	// the Peripheral pass marks the bonds a teardown works through, and a
	// device under teardown must not be paged. All three act on
	// Peripherals, so all three fall under one phase.
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

// truncateRunes cuts a free-text value to a byte limit, moving the cut
// back to a rune boundary.
//
// A person can name a controller in any script, the limits here count
// bytes, and a cut through the middle of a multi-byte rune produces a
// string the API server rejects as invalid UTF-8. That would fail the
// whole status write, not only the one name.
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
