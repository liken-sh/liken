package main

import (
	"testing"
	"testing/synctest"
	"time"

	"github.com/liken-sh/liken/equipment-operator/cec"
	"github.com/liken-sh/liken/equipment-operator/cec/cectest"
)

// powerQuestions counts the Give Device Power Status messages that
// the adapters sent to the TV.
func powerQuestions(wire *cectest.Bus) int {
	count := 0
	for _, message := range sentTo(wire, cec.AddressTV) {
		if opcode, _ := message.Opcode(); opcode == cec.OpGiveDevicePowerStatus && !message.IsPoll() {
			count++
		}
	}
	return count
}

// settle advances the bubble's clock so that a read that should not be
// sent has time to appear. Nothing on the bus marks the end of the
// adapter's work, so the test cannot wait for an event instead.
func settle() {
	time.Sleep(200 * time.Millisecond)
}

// After the adapter has read the TV's power and the power becomes
// unknown again, the adapter reads it again when the TV next announces
// itself. The reply to the adapter's own read does not pass through
// heard, so readTVPower must call noteTVPower too.
func TestATVAskedOnceIsAskedAgainAfterItsPowerIsUnknownAgain(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		wire := busWithTV(televisionTV(cec.PowerUnknown))
		api := controlling(t, wire, lounge(""))
		api.scanned(t, "node-1")
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
	})
}

// A TV that joins after the scan sends its name, which queues an
// introduction that reads its power. Its Report Physical Address during
// the introduction queues a second read, which finds the power known
// and sends nothing.
func TestANewTVIsAskedItsPowerOnce(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		wire := cectest.NewBus()
		wire.Add(cectest.Peer{Logical: 5, Physical: 0x1000, PrimaryType: 5, OSDName: "AVR", Vendor: 0x0005cd, Version: cec.Version14, Power: cec.PowerOn})
		api := controlling(t, wire, lounge(""))
		entry := api.scanned(t, "node-1")
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
	})
}

// A powerAsk during a read of the TV's power waits for that read
// and uses its result, so the bus has one Give Device Power Status.
func TestAPressReadSharesAQuestionInFlight(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		wire := busWithTV(televisionTV(cec.PowerUnknown))
		api := controlling(t, wire, lounge(""))
		api.scanned(t, "node-1")
		slow := televisionTV(cec.PowerOn)
		slow.Slow = 500 * time.Millisecond
		wire.Add(slow)
		before := powerQuestions(wire)

		wire.Send(cec.ReportPhysicalAddress(0, 0x0000, 0))
		askPowerRead(t, api, "2026-09-27T18:04:05.123Z")

		television := api.waitForTelevision(t, "lounge", func(television Television) bool { return television.Status.PowerRead != nil })
		mustMatch(t, television.Status.PowerRead.Power, "On")
		mustMatch(t, powerQuestions(wire)-before, 1)
	})
}

// slowTV is a TV that is on and takes 400 ms to reply to each request.
func slowTV() cectest.Peer {
	tv := televisionTV(cec.PowerOn)
	tv.Slow = 400 * time.Millisecond
	return tv
}

// roomWithAReceiver is a bus with the receiver and no TV yet.
func roomWithAReceiver() *cectest.Bus {
	wire := cectest.NewBus()
	wire.Add(cectest.Peer{Logical: 5, Physical: 0x1000, PrimaryType: 5, OSDName: "AVR", Vendor: 0x0005cd, Version: cec.Version14, Power: cec.PowerOn})
	return wire
}

// pressWhileTheTVAnswers presses 1.3 s in, and checks that the press
// read On and that one Give Device Power Status was sent. An
// introduction sends three requests before the power, so its power read
// runs from 1.2 s and the press shares it. A scan sends four, so its
// power read starts at 1.6 s, after the press's read began, and the
// scan shares the press's read.
func pressWhileTheTVAnswers(t *testing.T, wire *cectest.Bus, api *cecAPI) {
	t.Helper()
	time.Sleep(1300 * time.Millisecond)

	askPowerRead(t, api, "2026-09-27T18:04:05.123Z")

	television := api.waitForTelevisionWithin(t, "lounge", 3*time.Second, func(television Television) bool { return television.Status.PowerRead != nil })
	mustMatch(t, television.Status.PowerRead.Power, "On")
	settle()
	mustMatch(t, powerQuestions(wire), 1)
}

func TestAPressSharesTheJoinScansQuestion(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		wire := roomWithAReceiver()
		wire.Add(slowTV())
		api := controlling(t, wire, lounge(""))

		pressWhileTheTVAnswers(t, wire, api)
	})
}

func TestAPressSharesAnIntroductionsQuestion(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		wire := roomWithAReceiver()
		api := controlling(t, wire, lounge(""))
		entry := api.scanned(t, "node-1")
		wire.Add(slowTV())
		wire.Send(cec.SetOSDName(0, cec.LogicalAddress(*entry.LogicalAddress), "TV"))

		pressWhileTheTVAnswers(t, wire, api)
	})
}
