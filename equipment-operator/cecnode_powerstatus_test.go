package main

// The power the adapter reports for itself. The machine never sleeps,
// but to the TV a playback device with no picture is in standby, so
// the adapter reports On only while a Player's session holds the room
// awake on its Display.

import (
	"testing"
	"time"

	"github.com/liken-sh/equipment-operator/cec"
	"github.com/liken-sh/equipment-operator/cec/cectest"
)

// reportedPower asks the adapter at logical address 4 for its power, as
// the TV does, and answers the power of its last report.
func reportedPower(t *testing.T, api *cecAPI, wire *cectest.Bus) cec.PowerStatus {
	t.Helper()
	before := len(wire.Sent())
	wire.Send(cec.GiveDevicePowerStatus(cec.AddressTV, 4))
	var report cec.Message
	api.waitUntil(t, "the adapter's Report Power Status", func() bool {
		for _, message := range wire.Sent()[before:] {
			if opcode, _ := message.Opcode(); opcode == cec.OpReportPowerStatus && message.From == 4 {
				report = message
				return true
			}
		}
		return false
	})
	return cec.PowerStatus(report.Operands()[0])
}

func TestTheAdapterReportsItsPowerFromTheSession(t *testing.T) {
	cases := []struct {
		name  string
		room  func(t *testing.T, wire *cectest.Bus) *cecAPI
		power cec.PowerStatus
	}{
		{"no session", func(t *testing.T, wire *cectest.Bus) *cecAPI {
			api := controlling(t, wire, lounge(""))
			api.waitForEntry(t, "den", "node-1", func(entry CECAdapterStatus) bool { return entry.State == AdapterScanned })
			return api
		}, cec.PowerStandby},
		{"a session that sleeps", func(t *testing.T, wire *cectest.Bus) *cecAPI {
			api, _ := sleepingRoom(t, wire)
			return api
		}, cec.PowerStandby},
		{"a session that holds the room awake", func(t *testing.T, wire *cectest.Bus) *cecAPI {
			session := wokeNow()
			api := awakeRoom(t, wire, session)
			wokeWith(t, api, session)
			return api
		}, cec.PowerOn},
		{"a TV that picked the Display of a sleeping session", func(t *testing.T, wire *cectest.Bus) *cecAPI {
			api, _ := sleepingRoom(t, wire)
			pickTheDisplay(wire)
			time.Sleep(quietPeriod)
			return api
		}, cec.PowerToOn},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			fastWake(t)
			wire := roomWithTV(televisionTV(cec.PowerOn))
			api := c.room(t, wire)

			mustMatch(t, reportedPower(t, api, wire), c.power)
		})
	}
}
