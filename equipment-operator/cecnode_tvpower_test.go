package main

// The node workload asks an announcing TV for its power under hard
// bounds, and every read of the TV's power shares one question on the
// wire. A question is traffic a TV can answer by switching its input,
// and two questions at once can each take the other's answer.

import (
	"testing"
	"time"

	"github.com/liken-sh/equipment-operator/cec"
	"github.com/liken-sh/equipment-operator/cec/cectest"
)

// powerQuestions counts the Give Device Power Status messages the
// adapters sent the TV.
func powerQuestions(wire *cectest.Bus) int {
	count := 0
	for _, message := range sentTo(wire, cec.AddressTV) {
		if opcode, _ := message.Opcode(); opcode == cec.OpGiveDevicePowerStatus && !message.IsPoll() {
			count++
		}
	}
	return count
}

// settle waits a fixed time for questions that must not come. No
// message states that the adapter is done, and most messages that
// would mark the end tell the directory something about the TV.
func settle() {
	time.Sleep(200 * time.Millisecond)
}

// A TV that the adapter asked once and that answered, then lost its
// power again, is asked again when it next announces itself. The
// answer to the adapter's own question is a reply to that question and
// never reaches the loop that hears the bus, so it must clear the mark
// too.
func TestATVAskedOnceIsAskedAgainAfterItsPowerIsUnknownAgain(t *testing.T) {
	wire := roomWithTV(televisionTV(cec.PowerUnknown))
	api := controlling(t, wire, lounge(""))
	api.waitForEntry(t, "den", "node-1", func(entry CECAdapterStatus) bool { return entry.State == AdapterScanned })
	wire.Add(televisionTV(cec.PowerStandby))
	wire.Send(cec.ReportPhysicalAddress(0, 0x0000, 0))
	api.waitForEntry(t, "den", "node-1", func(entry CECAdapterStatus) bool { return entry.Devices[0].Power == "Standby" })
	wire.Add(televisionTV(cec.PowerUnknown))
	askPowerRead(t, api, "2026-09-27T18:04:05.123Z")
	api.waitForEntry(t, "den", "node-1", func(entry CECAdapterStatus) bool { return entry.Devices[0].Power == "" })
	wire.Add(televisionTV(cec.PowerOn))
	before := powerQuestions(wire)

	wire.Send(cec.ReportPhysicalAddress(0, 0x0000, 0))

	api.waitForEntry(t, "den", "node-1", func(entry CECAdapterStatus) bool { return entry.Devices[0].Power == "On" })
	mustMatch(t, powerQuestions(wire)-before, 1)
}

// A TV that joins after the scan is new to the adapter. Its first
// message, here its name, queues its introduction, which asks its
// power among its other facts. Its announcement while the introduction runs queues a
// power question too, and that question finds the power known and
// sends nothing.
func TestANewTVIsAskedItsPowerOnce(t *testing.T) {
	wire := cectest.NewBus()
	wire.Add(cectest.Peer{Logical: 5, Physical: 0x1000, PrimaryType: 5, OSDName: "AVR", Vendor: 0x0005cd, Version: cec.Version14, Power: cec.PowerOn})
	api := controlling(t, wire, lounge(""))
	entry := api.waitForEntry(t, "den", "node-1", func(entry CECAdapterStatus) bool { return entry.State == AdapterScanned })
	slow := televisionTV(cec.PowerOn)
	slow.Slow = 50 * time.Millisecond
	wire.Add(slow)

	wire.Send(cec.SetOSDName(0, cec.LogicalAddress(*entry.LogicalAddress), "TV"))
	wire.Send(cec.ReportPhysicalAddress(0, 0x0000, 0))

	api.waitForEntry(t, "den", "node-1", func(entry CECAdapterStatus) bool {
		return len(entry.Devices) > 0 && entry.Devices[0].LogicalAddress == 0 && entry.Devices[0].Power == "On"
	})
	settle()
	mustMatch(t, powerQuestions(wire), 1)
}

// A press that reads the TV while the adapter already asks it for its
// power shares that question and its answer. Two questions at once
// would each wait for a Report Power Status, and the kernel gives the
// first answer to one of them, so the other would time out and the
// press would decide from no power.
func TestAPressReadSharesAQuestionInFlight(t *testing.T) {
	wire := roomWithTV(televisionTV(cec.PowerUnknown))
	api := controlling(t, wire, lounge(""))
	api.waitForEntry(t, "den", "node-1", func(entry CECAdapterStatus) bool { return entry.State == AdapterScanned })
	slow := televisionTV(cec.PowerOn)
	slow.Slow = 500 * time.Millisecond
	wire.Add(slow)
	before := powerQuestions(wire)

	wire.Send(cec.ReportPhysicalAddress(0, 0x0000, 0))
	askPowerRead(t, api, "2026-09-27T18:04:05.123Z")

	television := api.waitForTelevision(t, "lounge", func(television Television) bool { return television.Status.PowerRead != nil })
	mustMatch(t, television.Status.PowerRead.Power, "On")
	mustMatch(t, powerQuestions(wire)-before, 1)
}
