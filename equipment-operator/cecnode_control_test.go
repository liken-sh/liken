package main

// The node workload in Control: the join, the scan, the answers a
// follower owes, and what the entry says when the adapter cannot join.
// cecnode_test.go holds the fixtures.

import (
	"slices"
	"testing"

	"github.com/liken-sh/equipment-operator/cec"
	"github.com/liken-sh/equipment-operator/cec/cectest"
)

func TestAControllingAdapterJoinsAndScans(t *testing.T) {
	api := startCECAPI(t)
	wire := cecRoom()
	adapter, device := usbAdapter(wire)
	api.putDisplay("acm-0001-receiver", "node-1", "1.3.0.0")
	api.putBus(controlBus("den", CECBusAdapter{Machine: "node-1", Display: "acm-0001-receiver"}))

	startNode(t, api, "node-1", device)

	entry := api.waitForEntry(t, "den", "node-1", func(entry CECAdapterStatus) bool { return entry.State == AdapterScanned })
	mustMatch(t, entry.PhysicalAddress, "1.3.0.0")
	mustMatch(t, *entry.LogicalAddress, 4)
	mustMatch(t, entry.OSDName, "node-1")
	mustMatch(t, entry.Message, "")
	mustDeepEqual(t, entry.Devices, []CECDevice{
		{LogicalAddress: 0, PhysicalAddress: "0.0.0.0", Type: "TV", OSDName: "TV", Vendor: "00e091", CECVersion: "1.4", Power: "Standby"},
		{LogicalAddress: 5, PhysicalAddress: "1.0.0.0", Type: "AudioSystem", OSDName: "AVR", Vendor: "0005cd", CECVersion: "1.4", Power: "On"},
	})
	mustMatch(t, adapter.Physical(), cec.PhysicalAddress(0x1300))
}

func TestAControllingAdapterAnswersTheTV(t *testing.T) {
	api := startCECAPI(t)
	wire := cecRoom()
	_, device := usbAdapter(wire)
	api.putDisplay("acm-0001-receiver", "node-1", "1.3.0.0")
	api.putBus(controlBus("den", CECBusAdapter{Machine: "node-1", Display: "acm-0001-receiver"}))
	startNode(t, api, "node-1", device)
	api.waitForEntry(t, "den", "node-1", func(entry CECAdapterStatus) bool { return entry.State == AdapterScanned })

	wire.Send(cec.GiveDevicePowerStatus(0, 4))
	wire.Send(cec.NewMessage(0, 4, cec.OpMenuRequest, 0x02))

	want := []string{
		cec.ReportPowerStatus(4, 0, cec.PowerOn).String(),
		cec.FeatureAbort(4, 0, cec.OpMenuRequest, cec.AbortUnrecognizedOpcode).String(),
	}
	api.waitUntil(t, "the answers to the TV", func() bool {
		var sent []string
		for _, message := range wire.Sent() {
			sent = append(sent, message.String())
		}
		return slices.Contains(sent, want[0]) && slices.Contains(sent, want[1])
	})
}

func TestAControllingAdapterWaitsForItsDisplaysAddress(t *testing.T) {
	cases := []struct {
		name    string
		display func(*cecAPI)
		message string
	}{
		{"no Display", func(*cecAPI) {}, "reading Display acm-0001-receiver: not found"},
		{"a Display with no address", func(api *cecAPI) { api.putDisplay("acm-0001-receiver", "node-1", "") },
			"Display acm-0001-receiver has no status.physicalAddress"},
		{"a Display with the invalid address", func(api *cecAPI) { api.putDisplay("acm-0001-receiver", "node-1", "f.f.f.f") },
			`Display acm-0001-receiver: physical address "f.f.f.f" is the invalid address`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			api := startCECAPI(t)
			wire := cecRoom()
			_, device := usbAdapter(wire)
			c.display(api)
			api.putBus(controlBus("den", CECBusAdapter{Machine: "node-1", Display: "acm-0001-receiver"}))

			startNode(t, api, "node-1", device)

			entry := api.waitForEntry(t, "den", "node-1", func(entry CECAdapterStatus) bool { return entry.State == AdapterJoining })
			mustMatch(t, entry.Message, c.message)
			if entry.LogicalAddress != nil || len(wire.Sent()) != 0 {
				t.Errorf("an adapter with no address joined: %+v, sent %v", entry, wire.Sent())
			}
		})
	}
}

// An adapter on a video port keeps its own port's address, and the
// entry says that it differs from the Display's.
func TestAnAdapterThatKeepsItsOwnAddressSaysSo(t *testing.T) {
	api := startCECAPI(t)
	_, device := cecRoom().Adapter(cectest.Options{Physical: 0x2000, Capabilities: cec.CapLogAddrs | cec.CapTransmit})
	api.putDisplay("acm-0001-receiver", "node-1", "1.3.0.0")
	api.putBus(controlBus("den", CECBusAdapter{Machine: "node-1", Display: "acm-0001-receiver"}))

	startNode(t, api, "node-1", device)

	entry := api.waitForEntry(t, "den", "node-1", func(entry CECAdapterStatus) bool { return entry.State == AdapterScanned })
	mustMatch(t, entry.PhysicalAddress, "2.0.0.0")
	mustMatch(t, entry.Message, "the adapter's driver sets its own physical address 2.0.0.0 and does not take 1.3.0.0 from Display acm-0001-receiver")
}

func TestAnAdapterWithNoFreePlaybackAddressSaysSo(t *testing.T) {
	api := startCECAPI(t)
	wire := cecRoom()
	for _, address := range []cec.LogicalAddress{4, 8, 11} {
		wire.Add(cectest.Peer{Logical: address, Physical: 0x2000, PrimaryType: 4})
	}
	_, device := usbAdapter(wire)
	api.putDisplay("acm-0001-receiver", "node-1", "1.3.0.0")
	api.putBus(controlBus("den", CECBusAdapter{Machine: "node-1", Display: "acm-0001-receiver"}))

	startNode(t, api, "node-1", device)

	entry := api.waitForEntry(t, "den", "node-1", func(entry CECAdapterStatus) bool { return entry.State == AdapterJoining })
	mustMatch(t, entry.Message, "the kernel claimed no playback logical address: a device holds each of 4, 8, and 11")
}

func TestAModeChangeReconfiguresTheAdapter(t *testing.T) {
	api := startCECAPI(t)
	adapter, device := usbAdapter(cecRoom())
	api.putDisplay("acm-0001-receiver", "node-1", "1.3.0.0")
	api.putBus(controlBus("den", CECBusAdapter{Machine: "node-1", Display: "acm-0001-receiver"}))
	startNode(t, api, "node-1", device)
	api.waitForEntry(t, "den", "node-1", func(entry CECAdapterStatus) bool { return entry.State == AdapterScanned })

	api.putBus(CECBus{Metadata: ObjectMeta{Name: "den"}, Spec: CECBusSpec{Mode: CECListen, Adapters: []CECBusAdapter{{Machine: "node-1"}}}})

	api.waitForEntry(t, "den", "node-1", func(entry CECAdapterStatus) bool { return entry.State == AdapterListening })
	if held := adapter.Logical(); len(held) != 0 {
		t.Errorf("a listening adapter still holds %v", held)
	}
}

// When the kernel takes the adapter's addresses away, the node
// workload configures the adapter again and joins once more.
func TestAnAdapterThatLosesItsAddressJoinsAgain(t *testing.T) {
	api := startCECAPI(t)
	adapter, device := usbAdapter(cecRoom())
	api.putDisplay("acm-0001-receiver", "node-1", "1.3.0.0")
	api.putBus(controlBus("den", CECBusAdapter{Machine: "node-1", Display: "acm-0001-receiver"}))
	startNode(t, api, "node-1", device)
	api.waitForEntry(t, "den", "node-1", func(entry CECAdapterStatus) bool { return entry.State == AdapterScanned })

	adapter.Disconnect()

	api.waitUntil(t, "the adapter to join again", func() bool {
		held := adapter.Logical()
		return len(held) == 1 && held[0] == 4 && adapter.Physical() == 0x1300
	})
	api.waitForEntry(t, "den", "node-1", func(entry CECAdapterStatus) bool { return entry.State == AdapterScanned })
}

func TestADisplayNameIsWhatControlNeedsFirst(t *testing.T) {
	_, device := usbAdapter(cecRoom())
	node, err := newCECNode(startCECAPI(t).client, "node-1", device)
	mustSucceed(t, err)

	want := node.desired(CECControl, "")

	mustMatch(t, want.problem, "the CECBus names no display for machine node-1")
}
