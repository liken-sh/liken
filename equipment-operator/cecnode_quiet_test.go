package main

// In Control mode the node workload sends nothing on a timer, because a
// TV can switch its input when it receives a request. It scans the bus
// once when it joins, requests only what a device's announcement left
// out, and reads the TV's power once for each powerAsk.

import (
	"testing"
	"testing/synctest"
	"time"

	"github.com/liken-sh/liken/equipment-operator/cec"
	"github.com/liken-sh/liken/equipment-operator/cec/cectest"
)

// sentTo returns the messages that the adapters sent to address.
func sentTo(wire *cectest.Bus, address cec.LogicalAddress) []cec.Message {
	var sent []cec.Message
	for _, message := range wire.Sent() {
		if message.To == address {
			sent = append(sent, message)
		}
	}
	return sent
}

// After the join scan, the adapter sends nothing on a bus where nothing
// happens, however many minutes pass and however many reconcile passes
// API events trigger.
func TestAnIdleBusGetsNoMessagesFromTheAdapterAfterTheJoinScan(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		wire := busWithTV(televisionTV(cec.PowerOn))
		api := controlling(t, wire, lounge(""))
		api.scanned(t, "node-1")
		scanned := len(wire.Sent())

		for range 20 {
			api.nudge()
			time.Sleep(30 * time.Second)
		}

		if sent := wire.Sent()[scanned:]; len(sent) != 0 {
			t.Errorf("the adapter sent %v after its scan, want nothing", sent)
		}
	})
}

// A device that joins after the scan announces itself with Report
// Physical Address. The adapter requests from it what the announcement
// left out, and sends nothing when the device announces itself again.
func TestADeviceThatAnnouncesItselfIsAskedOnce(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		wire := busWithTV(televisionTV(cec.PowerOn))
		api := controlling(t, wire, lounge(""))
		api.scanned(t, "node-1")
		wire.Add(cectest.Peer{Logical: 8, Physical: 0x1500, PrimaryType: 4, OSDName: "Streamer", Vendor: 0x001a11, Version: cec.Version14, Power: cec.PowerOn})
		polled := len(sentTo(wire, 8))
		others := len(wire.Sent()) - polled

		wire.Send(cec.ReportPhysicalAddress(8, 0x1500, 4))

		entry := api.waitForEntry(t, "den", "node-1", func(entry CECAdapterStatus) bool {
			return len(entry.Devices) == 3 && entry.Devices[2].OSDName == "Streamer" && entry.Devices[2].Power == "On"
		})
		mustDeepEqual(t, entry.Devices[2], CECDevice{PhysicalAddress: "1.5.0.0", LogicalAddress: 8, Type: string(cec.TypePlayback),
			OSDName: "Streamer", Vendor: "001a11", CECVersion: "1.4", Power: "On"})
		asked := len(sentTo(wire, 8))
		mustMatch(t, asked-polled, 4)

		wire.Send(cec.ReportPhysicalAddress(8, 0x1500, 4))
		wire.Send(cec.ActiveSource(8, 0x1500))
		api.waitForEntry(t, "den", "node-1", func(entry CECAdapterStatus) bool { return entry.ActiveSource == "1.5.0.0" })

		mustMatch(t, len(sentTo(wire, 8)), asked)
		mustMatch(t, len(wire.Sent())-asked, others)
	})
}

// A TV that reported no power at the join scan, such as one in deep
// standby, announces itself when it wakes. The adapter requests its
// power then, so that it does not stay unknown until a powerAsk. An
// announcement from a TV whose power is known gets no request.
func TestATVWithNoKnownPowerIsAskedWhenItAnnouncesItself(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		wire := busWithTV(televisionTV(cec.PowerUnknown))
		api := controlling(t, wire, lounge(""))
		api.scanned(t, "node-1")
		wire.Add(televisionTV(cec.PowerOn))
		scanned := len(sentTo(wire, 0))

		wire.Send(cec.ReportPhysicalAddress(0, 0x0000, 0))

		api.waitForEntry(t, "den", "node-1", func(entry CECAdapterStatus) bool {
			return len(entry.Devices) > 0 && entry.Devices[0].LogicalAddress == 0 && entry.Devices[0].Power == "On"
		})
		asked := len(sentTo(wire, 0))
		mustMatch(t, asked-scanned, 1)

		wire.Send(cec.ReportPhysicalAddress(0, 0x0000, 0))
		wire.Send(cec.ActiveSource(0, 0x0000))
		api.waitForEntry(t, "den", "node-1", func(entry CECAdapterStatus) bool { return entry.ActiveSource == "0.0.0.0" })

		mustMatch(t, len(sentTo(wire, 0)), asked)
	})
}

// A device whose power stays unknown, such as a TV that refuses Give
// Device Power Status, can announce itself again and again. The adapter
// then requests the power only from the TV, and once for each physical
// address. A player whose power is unknown gets no request.
func TestAnAnnouncingDeviceWithNoKnownPowerIsAskedAtMostOnce(t *testing.T) {
	cases := []struct {
		name     string
		device   cectest.Peer
		physical cec.PhysicalAddress
		asked    int
	}{
		{"the TV", televisionTV(cec.PowerUnknown), 0x0000, 1},
		{"a player", cectest.Peer{Logical: 8, Physical: 0x1500, PrimaryType: 4, OSDName: "Streamer", Vendor: 0x001a11, Version: cec.Version14, Power: cec.PowerUnknown}, 0x1500, 0},
	}
	t.Parallel()
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				wire := busWithTV(televisionTV(cec.PowerUnknown))
				wire.Add(c.device)
				api := controlling(t, wire, lounge(""))
				api.scanned(t, "node-1")
				scanned := len(sentTo(wire, c.device.Logical))

				for range 5 {
					wire.Send(cec.ReportPhysicalAddress(c.device.Logical, c.physical, c.device.PrimaryType))
					wire.Send(cec.DeviceVendorID(c.device.Logical, c.device.Vendor))
				}
				// No message shows that the adapter has handled the
				// announcements, and an Active Source message would record
				// the TV as on. So the test waits a fixed time.
				time.Sleep(200 * time.Millisecond)

				mustMatch(t, len(sentTo(wire, c.device.Logical))-scanned, c.asked)
			})
		})
	}
}

// askPowerRead writes status.session.powerReadAt of the lounge
// Television, as the operator's Deployment does for a powerAsk.
func askPowerRead(t *testing.T, api *cecAPI, at string) {
	t.Helper()
	mustSucceed(t, ApplyTelevisionSession(api.client, "lounge", &TelevisionSession{Player: "media/den", Display: "acm-0001-receiver", PowerReadAt: at}))
}

// For a powerAsk, the adapter sends one Give Device Power Status to
// the TV and writes the reply to status.powerRead, even when
// status.power says otherwise. A TV that a person turned off with its
// own remote can send the adapter no message, so status.power can be
// out of date.
func TestTheAdapterReadsTheTVsPowerForAPress(t *testing.T) {
	cases := []struct {
		name  string
		tv    cectest.Peer
		power string
	}{
		{"a TV turned off by its own remote", televisionTV(cec.PowerStandby), "Standby"},
		{"a TV turned on by its own remote", televisionTV(cec.PowerOn), "On"},
		{"a TV that does not answer", cectest.Peer{Logical: 0, Mute: true}, ""},
	}
	t.Parallel()
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				wire := busWithTV(televisionTV(cec.PowerOn))
				api := controlling(t, wire, lounge(""))
				api.scanned(t, "node-1")
				wire.Add(c.tv)
				scanned := len(sentTo(wire, 0))

				askPowerRead(t, api, "2026-09-27T18:04:05.123Z")

				television := api.waitForTelevision(t, "lounge", func(television Television) bool { return television.Status.PowerRead != nil })
				mustDeepEqual(t, *television.Status.PowerRead, TelevisionPowerRead{At: "2026-09-27T18:04:05.123Z", Power: c.power})
				api.nudge()
				time.Sleep(20 * time.Millisecond)
				mustMatch(t, len(sentTo(wire, 0)), scanned+1)
			})
		})
	}
}

// A powerReadAt that is already in the status when the node workload
// starts is older than the press's wait, so the adapter sends nothing
// for it.
func TestAPowerReadFoundAtStartSendsNothing(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		api := startCECAPI(t)
		wire := busWithTV(televisionTV(cec.PowerOn))
		_, device := usbAdapter(wire)
		api.putDisplay("acm-0001-receiver", "node-1", "1.3.0.0")
		api.putBus(controlBus("den", CECBusAdapter{Machine: "node-1", Display: "acm-0001-receiver"}))
		waiting := lounge("")
		waiting.Status.Session = &TelevisionSession{Player: "media/den", Display: "acm-0001-receiver", PowerReadAt: "2026-09-27T18:04:05.123Z"}
		api.putTelevision(waiting)

		startNode(t, api, "node-1", device)
		api.scanned(t, "node-1")
		scanned := len(sentTo(wire, 0))
		api.nudge()
		time.Sleep(20 * time.Millisecond)

		television, _ := api.television("lounge")
		if television.Status.PowerRead != nil {
			t.Errorf("status.powerRead is %+v, want none", *television.Status.PowerRead)
		}
		mustMatch(t, len(sentTo(wire, 0)), scanned)
	})
}
