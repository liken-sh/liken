package main

// A trusted bond lets a device connect without a pairing agent, but it
// does not make the device connect. After a power cycle, an A2DP speaker
// stays paired and trusted but disconnected until something connects to
// it. Until then its Sink has the bluetooth.liken.sh/disconnected taint,
// and a claim on it stays Pending. So the operator calls Device1.Connect
// for each bonded, trusted audio sink that is not connected, with the
// backoff below.
//
// The operator does not connect a game controller. A controller stays
// asleep until its own button wakes it and it connects to the radio, so
// an attempt to connect it never succeeds and occupies the radio until it
// times out.
//
// Device1.Connect returns only when the link is up or the attempt has
// failed, which takes several seconds for a device that is switched off.
// The reconcile loop also publishes the ResourceSlice and writes the
// bonds, so each call runs in its own goroutine. A failed attempt is
// written to the log and not to the API.

import (
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/liken-sh/liken/bluetooth-operator/bonds"
)

const (
	// After a failed attempt the operator waits firstConnectRetry, and
	// it doubles the wait on each further failure up to maxConnectRetry.
	// The limit bounds how much radio time a speaker that is switched off
	// or out of range can use.
	firstConnectRetry = 10 * time.Second
	maxConnectRetry   = 2 * time.Minute
)

// audioSinkProfile is the flag that profileFlags returns for the A2DP
// sink UUID.
const audioSinkProfile = "audioSink"

// connector starts Device1.Connect calls for bonded audio sinks, and
// keeps the backoff of each device between passes. mu guards the maps,
// because the reconcile loop and the goroutines that make the calls both
// write them.
type connector struct {
	radio radio

	now func() time.Time

	// wake asks the reconcile loop to run another pass, so that the
	// loop reads the device's state after a call returns.
	wake func()

	mu       sync.Mutex
	inFlight map[bonds.Address]bool
	backoff  map[bonds.Address]time.Duration
	nextTry  map[bonds.Address]time.Time

	// attempts lets a test wait for the calls in flight to return.
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

// reconcile starts a connection attempt for each bonded, trusted audio
// sink in the snapshot that is not connected and not being unpaired.
func (c *connector) reconcile(snapshot radioSnapshot, pass *inventoryPass) {
	for _, device := range snapshot.Devices {
		if !device.Paired || !device.Trusted || !isAudioSink(device) {
			continue
		}
		if device.Connected {
			// The device is connected, by this operator or by itself, so
			// its backoff no longer applies.
			c.forget(device.Address)
			continue
		}
		if pass.unpairing[device.Address] {
			// The unpair disconnects the device and removes the bond,
			// so connecting it now would undo that.
			continue
		}
		c.attempt(device, pass)
	}
}

// attempt starts a Connect call in a goroutine, unless the device
// already has a call in flight or its backoff has not ended. During the
// backoff it asks for another pass when the backoff ends, so the retry
// needs no timer of its own.
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
	fmt.Printf("peripheral: connecting %s (%s)\n", deviceDisplayName(device), device.Address)
	c.attempts.Add(1)
	go c.connect(device)
}

// connect makes the blocking Device1.Connect call, records the result,
// and wakes the reconcile loop.
func (c *connector) connect(device deviceState) {
	defer c.attempts.Done()
	err := c.radio.Connect(device.Address)
	if err == nil {
		c.succeeded(device.Address)
		fmt.Printf("peripheral: %s (%s) is connected\n", deviceDisplayName(device), device.Address)
		c.wake()
		return
	}
	next := c.failed(device.Address)
	fmt.Printf("peripheral: %s (%s) did not connect: %v, next try in %s\n",
		deviceDisplayName(device), device.Address, err, next)
	c.wake()
}

// forget clears the backoff of a device that bluetoothd reports as
// connected. It does not clear inFlight. The goroutine that makes the
// call clears it when the call returns, and clearing it earlier would let
// the next pass start a second call while the first runs.
func (c *connector) forget(device bonds.Address) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.clearWait(device)
}

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

// failed doubles the device's backoff up to maxConnectRetry and returns
// the new backoff.
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

func isAudioSink(device deviceState) bool {
	return slices.Contains(profileFlags(device.UUIDs), audioSinkProfile)
}
