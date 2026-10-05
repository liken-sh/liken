package main

// Reconnecting a controller whose link came up with no input.
//
// bluetoothd's Connected property says the radio link is up. It does
// not say that the input profile attached. A HID controller delivers
// presses only when bluetoothd also creates its HID device: through
// /dev/uhid for HID over GATT, or through the kernel's hidp for a
// classic controller. When bluetoothd connects the link and never
// creates the HID device, the device is Connected, the slice drops the
// disconnected taint, and every press goes nowhere. Nothing in the
// operator's events reports it, because the event that is missing is
// the kernel's HID add.
//
// The pass reads that state directly. A controller is stuck when
// bluetoothd reports it Connected, the relay holds virtual devices for
// it, and sysfs holds no Bluetooth HID device with its address. The
// relay's virtual devices are the proof that the controller registers
// input when it works: a speaker that lists a HID profile and never
// opens it has none, so it is never reconnected.
//
// The pass reads sysfs on every wake and on the backstop tick, so an
// input node that exists is always found. A stuck controller is one
// whose HID device does not exist at all, and only bluetoothd can
// create it. So the repair is a new connection: Device1.Disconnect,
// then Device1.Connect, which runs the input profile again. The
// Connect is not optional. BlueZ turns off the passive-scan reconnect
// of a Low Energy device on a Disconnect call until a Connect call
// turns it on again, so a Disconnect alone would leave a remote that
// cannot reconnect until bluetoothd restarts.
//
// Two clocks bound the repair. The grace gives an ordinary connect the
// time to create its HID device, which takes about a second. The wait
// after a reconnect that did not help doubles to a ceiling, so a
// controller that no reconnect repairs costs one reconnect each quarter
// hour and not one each pass. Both are deadlines, and the pass asks the
// loop to run again when the nearer one ends.

import (
	"fmt"
	"sync"
	"time"

	"github.com/liken-sh/bluetooth-operator/bonds"
)

const (
	// recoveryGrace is how long a controller may hold a link with no HID
	// device before the pass reconnects it. An ordinary connect creates
	// the HID device about a second after the link comes up, and a
	// classic controller opens its interrupt channel in the same time,
	// so the grace covers both with a wide margin.
	recoveryGrace = 15 * time.Second

	// A reconnect that does not bring the HID device back is not
	// repeated at once. The first wait is a minute, and it doubles on
	// each reconnect that does not help, up to the ceiling.
	firstRecoveryRetry = time.Minute
	maxRecoveryRetry   = 15 * time.Minute
)

// inputRecovery holds what the passes know about stuck controllers:
// when each one was first seen stuck, and when its last reconnect ran
// and how long the next one waits. The loop reads these and the
// goroutine that runs a reconnect writes them, so a mutex covers
// them.
type inputRecovery struct {
	radio  radio
	relays *relays

	// now is the clock, a field so a test runs the grace and the wait
	// out without waiting for them.
	now func() time.Time

	// wake asks the loop for another pass. A reconnect that finishes
	// changes state the pass reads from bluetoothd and from sysfs, and
	// the goroutine that finishes it is not the loop.
	wake func()

	mu sync.Mutex

	// stuckSince is when each controller was first seen Connected with
	// no HID device. It goes when the controller has its HID device or
	// is not connected.
	stuckSince map[bonds.Address]time.Time

	// lastTry and backoff are when the last reconnect of each controller
	// ran and how long the next one waits after it. They go only when
	// the HID device appears, because the reconnect itself takes the
	// link down, and a pass that reads that moment must not start the
	// controller over at the grace.
	lastTry map[bonds.Address]time.Time
	backoff map[bonds.Address]time.Duration

	inFlight map[bonds.Address]bool

	// attempts counts the reconnects in flight, so a test waits for them
	// instead of polling.
	attempts sync.WaitGroup
}

func newInputRecovery(radio radio, held *relays, now func() time.Time) *inputRecovery {
	return &inputRecovery{
		radio:      radio,
		relays:     held,
		now:        now,
		wake:       func() {},
		stuckSince: map[bonds.Address]time.Time{},
		lastTry:    map[bonds.Address]time.Time{},
		backoff:    map[bonds.Address]time.Duration{},
		inFlight:   map[bonds.Address]bool{},
	}
}

// reconcile decides from the snapshot and one walk of sysfs, in one
// pass over the devices. kernel is that walk: the Bluetooth HID
// devices on this operator's adapter.
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
			// The controller has never registered input, so a missing
			// HID device is its ordinary state.
		default:
			r.stuck(device, pass)
		}
	}
}

// stuck acts on one controller that is Connected with no HID device:
// it records when that began, and it reconnects the controller once
// the grace and the wait after the last reconnect have both run out.
// The caller holds r.mu.
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
	fmt.Printf("controller %s: connected for %s with no HID device; reconnecting it through bluetoothd\n",
		publishedMAC(device.Address.Directory()), now.Sub(since).Round(time.Second))
	r.attempts.Add(1)
	go r.reconnect(device)
}

// reconnect runs the two blocking calls and then wakes the loop, so
// the next pass reads the HID device the new connection made, or
// reads that there is still none.
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
		fmt.Printf("controller %s: the disconnect before the reconnect failed: %v\n", name, err)
		return
	}
	if err := r.radio.Connect(device.Address); err != nil {
		// A remote that went back to sleep between the two calls does not
		// answer the Connect. The call still turns the passive-scan
		// reconnect back on, so the next press connects it.
		fmt.Printf("controller %s: the reconnect failed: %v\n", name, err)
		return
	}
	fmt.Printf("controller %s: reconnected\n", name)
}

// forget drops everything held about a controller that has its HID
// device, so a later stuck link starts from the grace again. The
// caller holds r.mu.
func (r *inputRecovery) forget(device bonds.Address) {
	delete(r.stuckSince, device)
	delete(r.lastTry, device)
	delete(r.backoff, device)
}
