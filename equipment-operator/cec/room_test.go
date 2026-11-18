package cec_test

import (
	"testing"

	"github.com/liken-sh/liken/equipment-operator/cec"
	"github.com/liken-sh/liken/equipment-operator/cec/cectest"
)

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

func room() *cectest.Bus {
	bus := cectest.NewBus()
	bus.Add(television)
	bus.Add(receiver)
	return bus
}

// joined returns an adapter on bus that is a follower and has claimed
// a playback address.
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
		t.Fatalf("setting up the room: %v", err)
	}
}
