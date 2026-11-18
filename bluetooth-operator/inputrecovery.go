package main

// Reconnecting a controller whose link is up but whose input profile
// never attached.
//
// bluetoothd's Connected property reports only that the radio link is up.
// A HID controller delivers presses only after bluetoothd also creates
// its HID device, through /dev/uhid for HID over GATT or through the
// kernel's hidp for a classic controller. When bluetoothd connects the
// link and never creates that device, the Peripheral shows Connected,
// the ResourceSlice drops the disconnected taint, and no program
// receives the presses. No event reports this, because the event that
// is missing is the kernel's HID add.
//
// So a pass treats a controller as stuck when bluetoothd reports it
// Connected, the relays have virtual input devices for it, and sysfs
// shows no Bluetooth HID device with its address. The relays create
// virtual devices only for a device that has had input nodes, so a
// speaker that lists a HID profile and never opens it is never
// reconnected. Every pass walks sysfs again, so a HID device that
// appears is found on the next pass.
//
// Only bluetoothd can create the missing HID device, so the repair is a
// new connection: Device1.Disconnect, then Device1.Connect, which runs
// the input profile again if the controller responds. The operator
// trusts every device it pairs, and BlueZ keeps the passive-scan
// reconnect of a trusted Low Energy device enabled after a Disconnect
// call. A remote that sleeps through the Connect therefore connects on
// its next press.
//
// The Disconnect can fail when the link exists only in bluetoothd.
// bluetoothd can report a device Connected after the kernel dropped the
// link, and the kernel then replies to the disconnect with "no link".
// bluetoothd/rescan/main.go describes a kernel path that drops a link
// this way and also removes the device from the kernel's reconnect
// list. The rescan container adds the device back to that list, so the
// controller can still reconnect on its own after that path. BlueZ
// 5.82 sends no reply to the D-Bus call in this case, so the call
// waits out its timeout. BlueZ 5.87 treats the same kernel reply as a
// completed disconnect and replies. A Connect after a failed Disconnect
// does not help, because BlueZ returns success for a Connect on a Low
// Energy device it still considers connected, and does nothing.
//
// So when the Disconnect fails, for any reason, the operator powers the
// adapter off and on through bluetoothd. The power-off makes bluetoothd
// drop every connection it holds, real or stale, and the power-on makes
// the kernel restore the passive-scan reconnect of each bonded Low
// Energy device.
//
// The power cycle also disconnects every other controller and speaker
// on the adapter. The operator accepts that because the recovery is
// rare, because the stuck controller sends no input until its link is
// gone, and because each bonded device connects again: a controller on
// its next press, and a speaker when the connector pages it
// (connect.go).

import (
	"fmt"
	"sync"
	"time"

	"github.com/liken-sh/liken/kubernetes/events"

	"github.com/liken-sh/liken/bluetooth-operator/bonds"
)

const (
	// recoveryGrace is how long a controller may be connected with no
	// HID device before the pass reconnects it. An ordinary connect
	// creates the HID device a short time after the link comes up, so
	// this leaves a wide margin.
	recoveryGrace = 15 * time.Second

	// After a reconnect that does not bring the HID device back, the
	// pass waits firstRecoveryRetry before the next one. The wait
	// doubles after each further failure, up to maxRecoveryRetry.
	firstRecoveryRetry = time.Minute
	maxRecoveryRetry   = 15 * time.Minute
)

// inputRecovery tracks the controllers that are connected with no HID
// device: when each was first seen that way, when its last reconnect
// ran, and how long to wait before the next one. The loop and the
// reconnect goroutines both access these maps, so mu guards them.
type inputRecovery struct {
	radio  radio
	relays *relays

	// now is the clock. A test replaces it to move past the grace and
	// the wait without sleeping.
	now func() time.Time

	// wake asks the loop to run another pass. A reconnect runs on its
	// own goroutine, and the loop must check its result.
	wake func()

	// recorder posts the Events of a power cycle (events.go).
	recorder *events.Recorder

	mu sync.Mutex

	// stuckSince is when each controller was first seen Connected with
	// no HID device. An entry is deleted when the controller gets its
	// HID device or is no longer connected.
	stuckSince map[bonds.Address]time.Time

	// lastTry and backoff are when each controller's last reconnect ran
	// and how long to wait after it. Only the appearance of the HID
	// device deletes them. The reconnect itself disconnects the link,
	// and if a pass that saw the link down deleted them, the next stuck
	// link would wait only recoveryGrace.
	lastTry map[bonds.Address]time.Time
	backoff map[bonds.Address]time.Duration

	// inFlight marks controllers with a reconnect goroutine running.
	inFlight map[bonds.Address]bool

	// attempts counts running reconnects, so a test can wait for them.
	attempts sync.WaitGroup
}

func newInputRecovery(radio radio, inputRelays *relays, now func() time.Time) *inputRecovery {
	return &inputRecovery{
		radio:      radio,
		relays:     inputRelays,
		now:        now,
		wake:       func() {},
		stuckSince: map[bonds.Address]time.Time{},
		lastTry:    map[bonds.Address]time.Time{},
		backoff:    map[bonds.Address]time.Duration{},
		inFlight:   map[bonds.Address]bool{},
	}
}

// reconcile starts a reconnect for each stuck controller whose grace
// and wait have ended. kernel lists the Bluetooth HID devices that
// sysfs shows on this operator's adapter.
func (r *inputRecovery) reconcile(snapshot radioSnapshot, kernel []hidDevice, pass *inventoryPass) {
	attached := map[string]bool{}
	for _, device := range kernel {
		attached[device.MAC] = true
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, device := range snapshot.Devices {
		mac := macFromDeviceName(device.Address.Key())
		switch {
		case attached[mac]:
			r.forget(device.Address)
		case !device.Paired || !device.Connected || pass.unpairing[device.Address]:
			delete(r.stuckSince, device.Address)
		case len(r.relays.virtualNodes(mac)) == 0:
			// A device that never had input nodes, such as a speaker.
		default:
			r.stuck(device, pass)
		}
	}
}

// stuck handles one controller that is Connected with no HID device.
// It starts a reconnect once the grace and the wait after the last
// reconnect have both ended. Until then it asks for another pass when
// the later of the two ends. The caller holds r.mu.
func (r *inputRecovery) stuck(device deviceState, pass *inventoryPass) {
	if r.inFlight[device.Address] {
		return
	}
	now := r.now()
	since, seen := r.stuckSince[device.Address]
	if !seen {
		since = now
		r.stuckSince[device.Address] = since
	}
	due := since.Add(recoveryGrace)
	if last, tried := r.lastTry[device.Address]; tried {
		if retry := last.Add(r.backoff[device.Address]); retry.After(due) {
			due = retry
		}
	}
	if wait := due.Sub(now); wait > 0 {
		pass.runAgainIn(wait)
		return
	}

	next := r.backoff[device.Address] * 2
	if next < firstRecoveryRetry {
		next = firstRecoveryRetry
	}
	if next > maxRecoveryRetry {
		next = maxRecoveryRetry
	}
	r.backoff[device.Address] = next
	r.lastTry[device.Address] = now
	r.inFlight[device.Address] = true
	fmt.Printf("controller %s: connected for %s with no HID device, so the operator is reconnecting it through bluetoothd\n",
		publishedMAC(device.Address.Directory()), now.Sub(since).Round(time.Second))
	r.attempts.Add(1)
	go r.reconnect(device)
}

// reconnect runs the blocking bluetoothd calls on its own goroutine,
// then wakes the loop so the next pass checks whether the HID device
// appeared.
func (r *inputRecovery) reconnect(device deviceState) {
	defer r.attempts.Done()
	defer r.wake()
	defer func() {
		r.mu.Lock()
		delete(r.inFlight, device.Address)
		r.mu.Unlock()
	}()
	name := publishedMAC(device.Address.Directory())
	if err := r.radio.Disconnect(device.Address); err != nil {
		fmt.Printf("controller %s: bluetoothd did not disconnect the controller: %v, so the operator is powering the adapter off and on\n", name, err)
		r.powerCycle(device, err)
		return
	}
	if err := r.radio.Connect(device.Address); err != nil {
		// A remote that went back to sleep between the two calls does not
		// respond to the Connect. It is trusted, so its next press connects
		// it.
		fmt.Printf("controller %s: bluetoothd did not connect the controller again: %v\n", name, err)
		return
	}
	fmt.Printf("controller %s: bluetoothd disconnected and connected it again\n", name)
}

// powerCycle powers the adapter off and on after bluetoothd failed to
// disconnect a controller. It runs inside a reconnect, so the wait
// between reconnects also limits how often it runs.
//
// A failed power-off changes nothing, so it posts no Event and the next
// reconnect tries again. A failed power-on leaves the adapter off, and
// the operator does not retry it. bluetoothd powers the adapter on when
// it starts, so the Warning tells a person to delete the pod.
func (r *inputRecovery) powerCycle(device deviceState, cause error) {
	name := publishedMAC(device.Address.Directory())
	about := controllerReference(macFromDeviceName(device.Address.Key()))
	if err := r.radio.SetAdapterPowered(false); err != nil {
		fmt.Printf("controller %s: bluetoothd did not power the adapter off: %v\n", name, err)
		return
	}
	if err := r.radio.SetAdapterPowered(true); err != nil {
		fmt.Printf("controller %s: the adapter is off and bluetoothd did not power it on: %v\n", name, err)
		r.recorder.Warning(about, reasonAdapterPowerOnFailed, fmt.Sprintf(
			"The operator powered the adapter off to recover the controller, and bluetoothd did not power it on again: %v. Delete the operator's pod on this node to power the adapter on", err))
		return
	}
	fmt.Printf("controller %s: powered the adapter off and on, which dropped every link on it\n", name)
	r.recorder.Normal(about, reasonAdapterPowerCycled, fmt.Sprintf(
		"bluetoothd did not disconnect the controller (%v), so the operator powered the adapter off and on. Every link on the adapter dropped, and each bonded device can connect again", cause))
}

// forget clears all state for a controller that has its HID device, so
// a later stuck link starts again at the grace period. The caller holds
// r.mu.
func (r *inputRecovery) forget(device bonds.Address) {
	delete(r.stuckSince, device)
	delete(r.lastTry, device)
	delete(r.backoff, device)
}
