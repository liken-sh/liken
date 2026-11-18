package main

// Tests of the controller's choice of protocol: which spec block
// selects the driver, and which protocol's snapshot the status gets.

import (
	"testing"

	"github.com/liken-sh/liken/equipment-operator/denon"
	"github.com/liken-sh/liken/equipment-operator/equipment"
	"github.com/liken-sh/liken/equipment-operator/wiim"
)

func TestProtocolAddressReadsEitherBlock(t *testing.T) {
	t.Parallel()
	mustMatch(t, protocolAddress(&ReceiverSpec{Denon: &DenonProtocol{Address: "denon.example"}}), "denon.example")
	mustMatch(t, protocolAddress(&ReceiverSpec{Wiim: &WiimProtocol{UUID: "id", Address: "wiim.example"}}), "wiim.example")
	mustMatch(t, protocolAddress(&ReceiverSpec{}), "")
}

func TestStartDriverBuildsTheDeclaredProtocol(t *testing.T) {
	t.Parallel()
	unit := &receiverUnit{}
	unit.startDriver(&Receiver{Spec: ReceiverSpec{Wiim: &WiimProtocol{UUID: "FF98F2F7AABBCCDDEEFF0011", Address: "192.0.2.1"}}}, "192.0.2.1", func(string) {})
	if unit.wiimClient == nil || unit.driver == nil || unit.denonClient != nil {
		t.Fatal("a WiiM receiver did not build a WiiM driver")
	}
	mustMatch(t, unit.wiimClient.UUID, "FF98F2F7AABBCCDDEEFF0011")

	other := &receiverUnit{}
	other.startDriver(&Receiver{Spec: ReceiverSpec{Denon: &DenonProtocol{Address: "192.0.2.2"}}}, "192.0.2.2", func(string) {})
	if other.denonClient == nil || other.driver == nil || other.wiimClient != nil {
		t.Fatal("a Denon receiver did not build a Denon driver")
	}
}

// Each protocol reports only its own snapshot, so a status never has
// the other protocol's block.
func TestProtocolSnapshotsAreNilForTheOtherProtocol(t *testing.T) {
	t.Parallel()
	wiimUnit := &receiverUnit{wiimClient: wiim.NewClient("192.0.2.1", nil)}
	mustMatch(t, wiimUnit.denonSettings(), (*denon.Settings)(nil))
	if wiimUnit.wiimStatus() == nil {
		t.Fatal("a WiiM unit did not report its status")
	}

	denonUnit := &receiverUnit{denonClient: denon.NewClient("192.0.2.2", nil)}
	if denonUnit.denonSettings() == nil {
		t.Fatal("a Denon unit did not report its settings")
	}
	mustMatch(t, denonUnit.wiimStatus(), (*wiim.Status)(nil))
}

// Settings apply only to a Denon receiver, so for a WiiM receiver
// setSettings must do nothing, and not panic on the nil Denon client.
func TestSetSettingsIsANoOpForAWiimReceiver(t *testing.T) {
	t.Parallel()
	unit := &receiverUnit{}
	unit.setSettings(denon.Settings{})
}

// A WiiM receiver's address is the one in its spec when it sets one.
// Otherwise it is the address of the discovered device with the same
// UUID, compared without regard to case or dashes.
func TestResolvedAddressPrefersTheDeclaredOne(t *testing.T) {
	t.Parallel()
	c := &controller{discovery: newDiscovery(nil, func() {})}
	c.discovery.store([]wiim.Device{{UUID: "FF98F2F78136CE45A780D8A1", Address: "192.0.2.9"}})

	mustMatch(t, c.resolvedAddress(&ReceiverSpec{Wiim: &WiimProtocol{UUID: "ff98f2f7-8136-ce45-a780-d8a1ff98f2f7"}}), "192.0.2.9")
	mustMatch(t, c.resolvedAddress(&ReceiverSpec{Wiim: &WiimProtocol{UUID: "FF98F2F78136CE45A780D8A1", Address: "192.0.2.1"}}), "192.0.2.1")
	mustMatch(t, c.resolvedAddress(&ReceiverSpec{}), "")
}

// A WiiM receiver's snapshot is written to status.wiim, and a change to
// it makes the next status write differ.
func TestWiimStatusTravelsIntoTheStatus(t *testing.T) {
	t.Parallel()
	unit := &receiverUnit{wiimClient: wiim.NewClient("192.0.2.1", nil)}
	state := equipment.State{
		Reachable: equipment.ConditionTrue,
		Zones:     map[string]equipment.ZoneState{equipment.MainZone: {Power: equipment.PowerOn}},
	}
	withWiim := buildReceiverStatus(state, nil, unit.wiimStatus(), "", 1, 1, nil, statusNow)
	if withWiim.Wiim == nil {
		t.Fatal("the WiiM snapshot did not reach the status")
	}
	mustMatch(t, withWiim.Driver, "wiim")
	without := buildReceiverStatus(state, nil, nil, "", 1, 1, nil, statusNow)
	if sameStatus(withWiim, without) {
		t.Fatal("a status with a WiiM snapshot matched one without")
	}
}
