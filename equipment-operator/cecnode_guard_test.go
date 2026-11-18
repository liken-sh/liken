package main

// After the wake's first Active Source, the adapter guards the input.
// When a person picks another source on the TV or the receiver, the
// guard ends and the adapter sends no more Active Source. An Active
// Source from a streaming player that woke with the room is not a
// person's choice, so the guard takes the input back from the player.

import (
	"testing"
	"testing/synctest"
	"time"

	"github.com/liken-sh/liken/equipment-operator/cec"
)

func TestAPersonsChoiceEndsTheGuard(t *testing.T) {
	cases := []struct {
		name   string
		after  time.Duration
		choice []cec.Message
	}{
		{"the TV picks the player's input in its menu", cecBootGrace, []cec.Message{
			cec.NewMessage(cec.AddressTV, cec.AddressBroadcast, cec.OpSetStreamPath, 0x15, 0x00),
			cec.ActiveSource(8, 0x1500),
		}},
		{"a person switches the receiver's front panel", 0, []cec.Message{
			cec.NewMessage(5, cec.AddressBroadcast, cec.OpRoutingChange, 0x13, 0x00, 0x15, 0x00),
			cec.ActiveSource(8, 0x1500),
		}},
		{"the TV switches to its own tuner or apps", 0, []cec.Message{
			cec.ActiveSource(cec.AddressTV, 0x0000),
		}},
	}
	t.Parallel()
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				wire := busWithTV(televisionTV(cec.PowerOn))
				session := wokeNow()
				api := awakeRoom(t, wire, session)
				api.waitUntil(t, "the wake's Active Source", func() bool { return len(claimsOf(wire)) == 1 })
				time.Sleep(c.after)

				for _, message := range c.choice {
					wire.Send(message)
				}

				television := wokeWith(t, api, session)
				applied := conditionOf(television.Status.Conditions, conditionWakeApplied)
				mustMatch(t, applied.Status, ConditionFalse)
				mustMatch(t, applied.Reason, reasonChosen)
				mustDeepEqual(t, claimsOf(wire), []string{"4->f 82 13 00"})
			})
		})
	}
}

// A TV that has just come on can switch input by itself, or because a
// streaming player that woke with it asked, and it sends the same Set
// Stream Path that its menu sends. So for cecBootGrace after the wake's
// first Active Source, a Set Stream Path is not a person's choice.
func TestASetStreamPathWhileTheTVBootsKeepsTheGuard(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		wire := busWithTV(televisionTV(cec.PowerOn))
		session := wokeNow()
		api := awakeRoom(t, wire, session)
		api.waitUntil(t, "the wake's Active Source", func() bool { return len(claimsOf(wire)) == 1 })

		wire.Send(cec.NewMessage(cec.AddressTV, cec.AddressBroadcast, cec.OpSetStreamPath, 0x15, 0x00))
		wire.Send(cec.ActiveSource(8, 0x1500))

		television := wokeWith(t, api, session)
		mustMatch(t, conditionOf(television.Status.Conditions, conditionWakeApplied).Reason, reasonConfirmed)
		mustDeepEqual(t, claimsOf(wire), []string{"4->f 82 13 00", "4->f 82 13 00"})
	})
}

// The adapter replies to a Set Stream Path for its own Display with
// Active Source.
func TestARouteToTheDisplayKeepsTheGuard(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		wire := busWithTV(televisionTV(cec.PowerOn))
		session := wokeNow()
		api := awakeRoom(t, wire, session)
		api.waitUntil(t, "the wake's Active Source", func() bool { return len(claimsOf(wire)) == 1 })

		wire.Send(cec.NewMessage(cec.AddressTV, cec.AddressBroadcast, cec.OpSetStreamPath, 0x13, 0x00))

		television := wokeWith(t, api, session)
		mustMatch(t, conditionOf(television.Status.Conditions, conditionWakeApplied).Reason, reasonConfirmed)
		mustMatch(t, len(claimsOf(wire)), 2)
	})
}

// A switch sends Routing Information when it comes out of standby or
// replies to a Routing Change (HDMI-CEC 1.3a, CEC 13.2.2). The route it
// reports can date from before the room woke, so it is not a person's
// choice.
func TestRoutingInformationKeepsTheGuard(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		wire := busWithTV(televisionTV(cec.PowerOn))
		session := wokeNow()
		api := awakeRoom(t, wire, session)
		api.waitUntil(t, "the wake's Active Source", func() bool { return len(claimsOf(wire)) == 1 })

		wire.Send(cec.NewMessage(5, cec.AddressBroadcast, cec.OpRoutingInformation, 0x15, 0x00))
		wire.Send(cec.ActiveSource(8, 0x1500))

		television := wokeWith(t, api, session)
		mustMatch(t, conditionOf(television.Status.Conditions, conditionWakeApplied).Reason, reasonConfirmed)
		mustMatch(t, len(claimsOf(wire)), 2)
	})
}

// A Routing Information from a waking receiver does not stop the
// adapter from replying to the TV's Request Active Source.
func TestRoutingInformationDuringAWakeKeepsTheAnswer(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		wire := busWithTV(televisionTV(cec.PowerOn))
		session := wokeNow()
		api := awakeRoom(t, wire, session)
		api.waitUntil(t, "the wake's Active Source", func() bool { return len(claimsOf(wire)) == 1 })

		wire.Send(cec.NewMessage(5, cec.AddressBroadcast, cec.OpRoutingInformation, 0x15, 0x00))
		askForTheSource(wire)

		api.waitUntil(t, "the answer", func() bool { return len(claimsOf(wire)) == 2 })
	})
}
