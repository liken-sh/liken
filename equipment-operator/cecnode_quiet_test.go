package main

// The node workload in Control sends the bus nothing on a timer: it
// scans once when it joins, asks a device that announces itself only
// for the facts its announcement left out, and reads the TV's power
// once when a power press asks for it.

import (
	"testing"
	"time"

	"github.com/liken-sh/equipment-operator/cec"
	"github.com/liken-sh/equipment-operator/cec/cectest"
)

// fastClocks shortens the node workload's clocks, the heartbeat and the
// retry, so a test sees many of them in a short window.
func fastClocks(t *testing.T) {
	t.Helper()
	report, retry := cecReportInterval, cecAPIRetry
	cecReportInterval, cecAPIRetry = 5*time.Millisecond, 5*time.Millisecond
	t.Cleanup(func() { cecReportInterval, cecAPIRetry = report, retry })
}

// sentTo lists the messages the adapters sent to one logical address.
func sentTo(wire *cectest.Bus, address cec.LogicalAddress) []cec.Message {
	var sent []cec.Message
	for _, message := range wire.Sent() {
		if message.To == address {
			sent = append(sent, message)
		}
	}
	return sent
}

// After the scan, a bus where nothing happens hears nothing from the
// adapter: not over many heartbeats, and not over passes that API
// events run.
func TestAnIdleBusHearsNothingAfterTheJoinScan(t *testing.T) {
	fastClocks(t)
	wire := roomWithTV(televisionTV(cec.PowerOn))
	api := controlling(t, wire, lounge(""))
	api.waitForEntry(t, "den", "node-1", func(entry CECAdapterStatus) bool { return entry.State == AdapterScanned })
	scanned := len(wire.Sent())

	for range 20 {
		api.nudge()
		time.Sleep(10 * time.Millisecond)
	}

	if sent := wire.Sent()[scanned:]; len(sent) != 0 {
		t.Errorf("the adapter sent %v after its scan", sent)
	}
	if writes := api.writesOf("node-1"); writes < 10 {
		t.Errorf("the node workload wrote its entry %d times; the heartbeat did not run", writes)
	}
}

// A device that joins after the scan announces itself with Report
// Physical Address. The adapter asks it once for the facts the
// announcement left out, and asks nothing of it when it announces
// again.
func TestADeviceThatAnnouncesItselfIsAskedOnce(t *testing.T) {
	wire := roomWithTV(televisionTV(cec.PowerOn))
	api := controlling(t, wire, lounge(""))
	api.waitForEntry(t, "den", "node-1", func(entry CECAdapterStatus) bool { return entry.State == AdapterScanned })
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
}

// A TV that gave no power at the join scan, such as one in a deep
// standby, announces itself when it wakes. The adapter asks it for its
// power then, because an unknown power would otherwise stay unknown
// until a press asks. An announcement from a TV whose power is known
// asks nothing.
func TestATVWithNoKnownPowerIsAskedWhenItAnnouncesItself(t *testing.T) {
	wire := roomWithTV(televisionTV(cec.PowerUnknown))
	api := controlling(t, wire, lounge(""))
	api.waitForEntry(t, "den", "node-1", func(entry CECAdapterStatus) bool { return entry.State == AdapterScanned })
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
}

// askPowerRead writes a power press's request on the lounge
// Television's session, as the Deployment does.
func askPowerRead(t *testing.T, api *cecAPI, at string) {
	t.Helper()
	mustSucceed(t, ApplyTelevisionSession(api.client, "lounge", &TelevisionSession{Player: "media/den", Display: "acm-0001-receiver", PowerReadAt: at}))
}

// A power press asks the adapter for the TV's power, and the adapter
// asks the TV once and answers in status.powerRead, whatever the entry
// held: a TV that a person turned off with its own remote sends
// nothing the adapter hears.
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
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			wire := roomWithTV(televisionTV(cec.PowerOn))
			api := controlling(t, wire, lounge(""))
			api.waitForEntry(t, "den", "node-1", func(entry CECAdapterStatus) bool { return entry.State == AdapterScanned })
			wire.Add(c.tv)
			scanned := len(sentTo(wire, 0))

			askPowerRead(t, api, "2026-09-27T18:04:05.123Z")

			television := api.waitForTelevision(t, "lounge", func(television Television) bool { return television.Status.PowerRead != nil })
			mustDeepEqual(t, *television.Status.PowerRead, TelevisionPowerRead{At: "2026-09-27T18:04:05.123Z", Power: c.power})
			api.nudge()
			time.Sleep(20 * time.Millisecond)
			mustMatch(t, len(sentTo(wire, 0)), scanned+1)
		})
	}
}

// A request that is in the status when the node workload starts is
// older than the press's wait, so the adapter sends nothing for it.
func TestAPowerReadFoundAtStartSendsNothing(t *testing.T) {
	api := startCECAPI(t)
	wire := roomWithTV(televisionTV(cec.PowerOn))
	_, device := usbAdapter(wire)
	api.putDisplay("acm-0001-receiver", "node-1", "1.3.0.0")
	api.putBus(controlBus("den", CECBusAdapter{Machine: "node-1", Display: "acm-0001-receiver"}))
	waiting := lounge("")
	waiting.Status.Session = &TelevisionSession{Player: "media/den", Display: "acm-0001-receiver", PowerReadAt: "2026-09-27T18:04:05.123Z"}
	api.putTelevision(waiting)

	startNode(t, api, "node-1", device)
	api.waitForEntry(t, "den", "node-1", func(entry CECAdapterStatus) bool { return entry.State == AdapterScanned })
	scanned := len(sentTo(wire, 0))
	api.nudge()
	time.Sleep(20 * time.Millisecond)

	television, _ := api.television("lounge")
	if television.Status.PowerRead != nil {
		t.Errorf("the node workload answered %+v", *television.Status.PowerRead)
	}
	mustMatch(t, len(sentTo(wire, 0)), scanned)
}
