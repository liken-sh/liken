package main

// Connecting the bonded speakers.
//
// A trusted bond lets a device connect with no agent, but it does not
// make the device connect. An A2DP speaker stays paired, trusted, and
// disconnected after a power cycle until something pages it, and
// until then its Sink carries the bluetooth.liken.sh/disconnected
// taint and a claim on it parks. So every pass calls Device1.Connect
// for each bonded, trusted audio sink that is not connected.
//
// Audio sinks only. A game controller sleeps until its own button
// pages the radio, so a page at a controller never succeeds and holds
// the radio while it fails.
//
// The call runs off the loop. Device1.Connect returns only when the
// link is up or the page failed, which takes several seconds for a
// device that is switched off, and the reconcile loop is one
// goroutine that also publishes the slice and writes the bonds.
//
// A failure reaches the log and not the API. A Pairing status field
// for the last connect error is the natural next step.

import (
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/liken-sh/bluetooth-operator/bonds"
)

const (
	// A speaker that is switched off or out of range fails every
	// attempt, and each attempt holds the radio for the page timeout.
	// The first retry waits 10 seconds, the wait doubles on each
	// failure, and the ceiling bounds what a device that never answers
	// costs.
	firstConnectRetry = 10 * time.Second
	maxConnectRetry   = 2 * time.Minute
)

// audioSinkProfile is the flag classify.go decodes from the A2DP sink
// UUID. A device that carries it is one this operator pages.
const audioSinkProfile = "audioSink"

// connector holds the only state kept between passes about
// connecting: which device has a call in flight, and how long the next
// attempt on a device waits. The loop reads both and the goroutines
// that run the calls write them, so a mutex covers them.
type connector struct {
	radio radio

	// now is the clock, a field so a test runs a backoff out without
	// waiting for it.
	now func() time.Time

	// wake asks the loop for another pass. A connect that finishes
	// changes state the pass reads from bluetoothd, and the goroutine
	// that finishes it is not the loop.
	wake func()

	mu       sync.Mutex
	inFlight map[bonds.Address]bool
	backoff  map[bonds.Address]time.Duration
	nextTry  map[bonds.Address]time.Time

	// attempts counts the calls in flight, so a test waits for them
	// instead of polling.
	attempts sync.WaitGroup
}

func newConnector(radio radio, now func() time.Time) *connector {
	return &connector{
		radio:    radio,
		now:      now,
		wake:     func() {},
		inFlight: map[bonds.Address]bool{},
		backoff:  map[bonds.Address]time.Duration{},
		nextTry:  map[bonds.Address]time.Time{},
	}
}

// reconcile decides from the snapshot alone, in one pass over the
// devices. A connected device costs a map read and nothing else.
func (c *connector) reconcile(snapshot radioSnapshot, pass *inventoryPass) {
	for _, device := range snapshot.Devices {
		if !device.Paired || !device.Trusted || !isAudioSink(device) {
			continue
		}
		if device.Connected {
			// The device answered, whatever paged it, so its backoff
			// goes.
			c.forget(device.Address)
			continue
		}
		if pass.unpairing[device.Address] {
			// The teardown disconnects the device and removes the
			// bond, so a page would work against it.
			continue
		}
		c.attempt(device, pass)
	}
}

// attempt starts one call, behind two guards: one call at a time for
// a device, and no call before its backoff runs out. A pass inside
// the backoff window asks for the wake that ends it, so the retry is
// one more pass and not a second loop.
func (c *connector) attempt(device deviceState, pass *inventoryPass) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.inFlight[device.Address] {
		return
	}
	if wait := c.nextTry[device.Address].Sub(c.now()); wait > 0 {
		pass.runAgainIn(wait)
		return
	}
	c.inFlight[device.Address] = true
	fmt.Printf("pairing: connecting %s (%s)\n", deviceDisplayName(device), device.Address)
	c.attempts.Add(1)
	go c.connect(device)
}

// connect runs the one blocking call and then wakes the loop, so the
// next pass reads the state bluetoothd holds now and not the state
// this goroutine believes.
func (c *connector) connect(device deviceState) {
	defer c.attempts.Done()
	err := c.radio.Connect(device.Address)
	if err == nil {
		c.succeeded(device.Address)
		fmt.Printf("pairing: %s (%s) is connected\n", deviceDisplayName(device), device.Address)
		c.wake()
		return
	}
	next := c.failed(device.Address)
	fmt.Printf("pairing: %s (%s) did not connect: %v; next try in %s\n",
		deviceDisplayName(device), device.Address, err, next)
	c.wake()
}

// forget drops the wait of a device bluetoothd reports connected. The
// in-flight mark stays: only the call that set it knows when it is
// over, and clearing it here would let a second call start beside the
// first.
func (c *connector) forget(device bonds.Address) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.clearWait(device)
}

// succeeded records that the call is over and the device answered, so
// both the in-flight mark and the wait go.
func (c *connector) succeeded(device bonds.Address) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.inFlight, device)
	c.clearWait(device)
}

// clearWait drops a device's backoff. The caller holds c.mu.
func (c *connector) clearWait(device bonds.Address) {
	delete(c.backoff, device)
	delete(c.nextTry, device)
}

// failed doubles this device's wait, caps it, and returns the wait it
// wrote so the caller names it in the log.
func (c *connector) failed(device bonds.Address) time.Duration {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.inFlight, device)
	next := c.backoff[device] * 2
	if next < firstConnectRetry {
		next = firstConnectRetry
	}
	if next > maxConnectRetry {
		next = maxConnectRetry
	}
	c.backoff[device] = next
	c.nextTry[device] = c.now().Add(next)
	return next
}

// isAudioSink reports whether the device plays audio, which the A2DP
// sink profile says. classify.go's decode is the one place a UUID
// becomes a name.
func isAudioSink(device deviceState) bool {
	return slices.Contains(profileFlags(device.UUIDs), audioSinkProfile)
}
