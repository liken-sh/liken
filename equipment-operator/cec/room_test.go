package cec_test

// The fixtures the tests of the device, the scan, and the read loop
// share: a bus in memory with a TV and a receiver on it.

import (
	"testing"

	"github.com/liken-sh/equipment-operator/cec"
	"github.com/liken-sh/equipment-operator/cec/cectest"
)

// The two peers of the room. The values are invented; the vendor is
// an OUI shape, not a real company's.
var (
	television = cectest.Peer{
		Logical: 0, Physical: 0x0000, PrimaryType: 0, OSDName: "TV",
		Vendor: 0x00e091, Version: cec.Version14, Power: cec.PowerStandby,
	}
	receiver = cectest.Peer{
		Logical: 5, Physical: 0x1000, PrimaryType: 5, OSDName: "AVR",
		Vendor: 0x0005cd, Version: cec.Version14, Power: cec.PowerOn,
	}
)

// room is a bus with the TV and the receiver on it.
func room() *cectest.Bus {
	bus := cectest.NewBus()
	bus.Add(television)
	bus.Add(receiver)
	return bus
}

// joined is a USB adapter on the room's bus that took the machine's
// address and claimed a playback address as a follower.
func joined(t *testing.T, bus *cectest.Bus) (*cectest.Adapter, *cec.Device) {
	t.Helper()
	adapter, device := bus.Adapter(cectest.Options{Physical: cec.InvalidPhysicalAddress})
	mustSucceed(t, device.Follow())
	mustSucceed(t, device.SetPhysicalAddress(0x1300))
	mustSucceed(t, device.Claim(cec.Claim{OSDName: "node-1", Passthrough: true}))
	return adapter, device
}

func mustSucceed(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
