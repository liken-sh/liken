package main

// The reason a link ended tells a remote that went to sleep apart from
// a link that this side ended. A sleeping remote stops answering, and
// the radio reports a supervision timeout. When the radio powers off,
// at a restart of bluetoothd or in the power cycle of the input
// recovery, every link ends from this side. From version 5.83, BlueZ
// emits org.bluez.Device1.Disconnected for each link that ends, with
// the reason, before it sets Connected to false.
//
// BlueZ has no property for the last reason, so the operator must
// record it from the signal. The signal reader (bluez.go) stores the
// newest reason for each device in linkEnds, and the next pass writes
// it into the Peripheral's Connected condition (connected.go). The pass
// forgets the reason after its write succeeds, and when it finds the
// device connected again. linkEnds is in memory only, so after the
// operator restarts, the condition reports NotConnected. NotConnected
// never makes the relay write the KEY_UNKNOWN press of wake.go.

import (
	"strings"
	"sync"

	"github.com/godbus/dbus/v5"

	"github.com/liken-sh/liken/bluetooth-operator/bonds"
)

// The reasons in the Disconnected signal (doc/org.bluez.Device.rst in
// BlueZ). Suspend and Unknown have no constant here, because the
// condition treats them as no reason.
const (
	blueZReasonTimeout        = "org.bluez.Reason.Timeout"
	blueZReasonLocal          = "org.bluez.Reason.Local"
	blueZReasonRemote         = "org.bluez.Reason.Remote"
	blueZReasonAuthentication = "org.bluez.Reason.Authentication"
)

// disconnectedMember is the signal's full name, as godbus sets it in
// dbus.Signal.Name.
const disconnectedMember = deviceInterface + ".Disconnected"

// linkEnds maps each device whose link ended to the newest reason BlueZ
// gave. The signal reader writes it from its own goroutine and the pass
// reads it from the loop's, so mu guards the map.
type linkEnds struct {
	mu      sync.Mutex
	reasons map[bonds.Address]string
}

func newLinkEnds() *linkEnds {
	return &linkEnds{reasons: map[bonds.Address]string{}}
}

// record stores the reason one link ended. A newer reason replaces an
// older one, because the condition reports only the last link.
func (e *linkEnds) record(device bonds.Address, reason string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.reasons[device] = reason
}

// reason returns the stored reason for one device, or the empty string
// when no signal has arrived since a pass last forgot it.
func (e *linkEnds) reason(device bonds.Address) string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.reasons[device]
}

// forget deletes the reason a pass read, after the pass wrote it or
// found the device connected. When the stored reason differs from the
// one the pass read, it stays, because it belongs to a link that ended
// during the pass and the next pass writes it.
func (e *linkEnds) forget(device bonds.Address, read string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.reasons[device] == read {
		delete(e.reasons, device)
	}
}

// disconnectedFrom returns the device and the reason from one
// Disconnected signal. It returns false for any other signal, and for a
// signal whose path has no device address or whose reason is empty.
//
// The signal has the device's object path and no address argument.
// BlueZ builds each device's object path from its address, as in
// /org/bluez/hci0/dev_C2_11_22_33_44_55, so the address comes from the
// path without a call to bluetoothd.
func disconnectedFrom(signal *dbus.Signal) (bonds.Address, string, bool) {
	if signal.Name != disconnectedMember || len(signal.Body) == 0 {
		return bonds.Address{}, "", false
	}
	reason, ok := signal.Body[0].(string)
	if !ok || reason == "" {
		return bonds.Address{}, "", false
	}
	path := string(signal.Path)
	element, ok := strings.CutPrefix(path[strings.LastIndex(path, "/")+1:], "dev_")
	if !ok {
		return bonds.Address{}, "", false
	}
	device, err := bonds.ParseAddress(strings.ReplaceAll(element, "_", ":"))
	if err != nil {
		return bonds.Address{}, "", false
	}
	return device, reason, true
}
