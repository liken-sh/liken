package main

// Why each link ended.
//
// The reason a link ended separates a remote that went to sleep from a
// link that the cluster ended. A sleeping remote stops answering, and
// the radio reports a supervision timeout. A radio that powers off, at
// a restart of bluetoothd or at the power cycle of the input recovery,
// ends each link from this side. From 5.83, BlueZ emits
// org.bluez.Device1.Disconnected once for each link that ends, with the
// reason, before it sets Connected to false.
//
// BlueZ keeps no property with the last reason, so the signal is the
// only source. The signal reader (bluez.go) records the newest reason
// for each device here, and the next pass writes it into the
// Peripheral's Connected condition (connected.go). The pass forgets the
// reason after its write lands, and when it finds the device connected
// again. So this record holds nothing that the API server can supply.
// A reason that a restart of this operator loses gives the condition
// NotConnected, and NotConnected never arms the press that wakes the
// room (wake.go).

import (
	"strings"
	"sync"

	"github.com/godbus/dbus/v5"

	"github.com/liken-sh/bluetooth-operator/bonds"
)

// The reasons Disconnected carries (doc/org.bluez.Device.rst in BlueZ).
// Suspend and Unknown are not named here, because the condition reads
// them the same as no reason at all.
const (
	blueZReasonTimeout        = "org.bluez.Reason.Timeout"
	blueZReasonLocal          = "org.bluez.Reason.Local"
	blueZReasonRemote         = "org.bluez.Reason.Remote"
	blueZReasonAuthentication = "org.bluez.Reason.Authentication"
)

// disconnectedMember is the full name of the signal, as godbus reports
// it in dbus.Signal.Name.
const disconnectedMember = deviceInterface + ".Disconnected"

// linkEnds holds the newest reason BlueZ gave for each device whose
// link ended. The signal reader writes it from its own goroutine, and
// the pass reads it on the loop's, so a lock covers the map.
type linkEnds struct {
	mu   sync.Mutex
	held map[bonds.Address]string
}

func newLinkEnds() *linkEnds {
	return &linkEnds{held: map[bonds.Address]string{}}
}

// record holds the reason one link ended. A newer reason replaces an
// older one, because the condition reports only the last link.
func (e *linkEnds) record(device bonds.Address, reason string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.held[device] = reason
}

// reason answers the reason held for one device, and the empty string
// when no signal arrived since the last pass wrote one.
func (e *linkEnds) reason(device bonds.Address) string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.held[device]
}

// forget removes the reason a pass read, after the pass wrote it or
// found the device connected. A reason that changed since the read
// stays, because it belongs to a link that ended during the pass, and
// the next pass writes it.
func (e *linkEnds) forget(device bonds.Address, read string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.held[device] == read {
		delete(e.held, device)
	}
}

// disconnectedFrom reads the device and the reason out of one
// Disconnected signal. It answers false for every other signal, and for
// a signal that does not name a device or a reason.
//
// The signal carries the device's object path and not its address.
// BlueZ names each device object after the address, as
// /org/bluez/hci0/dev_C2_11_22_33_44_55, so the address is read from
// the path with no call to bluetoothd.
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
