package main

// The wake's guard against a person's choice. A person who picks
// another source through the TV or the receiver during the guard gets
// that source, and the adapter takes nothing back. A bare Active Source
// from another playback device is the streaming player that wakes with
// the room, and the guard still takes the input back from it
// (cecnode_wake_test.go).

import (
	"testing"
	"time"

	"github.com/liken-sh/equipment-operator/cec"
)

func TestAPersonsChoiceEndsTheGuard(t *testing.T) {
	cases := []struct {
		name   string
		choice []cec.Message
	}{
		{"the TV picks the player's input in its menu", []cec.Message{
			cec.NewMessage(cec.AddressTV, cec.AddressBroadcast, cec.OpSetStreamPath, 0x15, 0x00),
			cec.ActiveSource(8, 0x1500),
		}},
		{"a person switches the receiver's front panel", []cec.Message{
			cec.NewMessage(5, cec.AddressBroadcast, cec.OpRoutingChange, 0x13, 0x00, 0x15, 0x00),
			cec.ActiveSource(8, 0x1500),
		}},
		{"the TV switches to its own tuner or apps", []cec.Message{
			cec.ActiveSource(cec.AddressTV, 0x0000),
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			fastWake(t)
			cecWakeGuard = time.Second
			wire := roomWithTV(televisionTV(cec.PowerOn))
			session := wokeNow()
			api := awakeRoom(t, wire, session)
			api.waitUntil(t, "the wake's Active Source", func() bool { return len(claimsOf(wire)) == 1 })

			for _, message := range c.choice {
				wire.Send(message)
			}

			television := wokeWith(t, api, session)
			applied := conditionOf(television.Status.Conditions, conditionWakeApplied)
			mustMatch(t, applied.Status, ConditionFalse)
			mustMatch(t, applied.Reason, reasonChosen)
			mustDeepEqual(t, claimsOf(wire), []string{"4->f 82 13 00"})
		})
	}
}

// A Set Stream Path for the Display itself is no choice of another
// source. The adapter answers it, and the wake ends Confirmed.
func TestARouteToTheDisplayKeepsTheGuard(t *testing.T) {
	fastWake(t)
	wire := roomWithTV(televisionTV(cec.PowerOn))
	session := wokeNow()
	api := awakeRoom(t, wire, session)
	api.waitUntil(t, "the wake's Active Source", func() bool { return len(claimsOf(wire)) == 1 })

	wire.Send(cec.NewMessage(cec.AddressTV, cec.AddressBroadcast, cec.OpSetStreamPath, 0x13, 0x00))

	television := wokeWith(t, api, session)
	mustMatch(t, conditionOf(television.Status.Conditions, conditionWakeApplied).Reason, reasonConfirmed)
	mustMatch(t, len(claimsOf(wire)), 2)
}

// A switch sends Routing Information when it comes out of standby or
// answers a Routing Change (HDMI-CEC 1.3a, CEC 13.2.2). It reports the
// route it holds, which can be from before the room woke, so it is no
// person's choice: the guard goes on, and a streaming player's claim
// after it is still taken back.
func TestRoutingInformationKeepsTheGuard(t *testing.T) {
	fastWake(t)
	cecWakeGuard = time.Second
	wire := roomWithTV(televisionTV(cec.PowerOn))
	session := wokeNow()
	api := awakeRoom(t, wire, session)
	api.waitUntil(t, "the wake's Active Source", func() bool { return len(claimsOf(wire)) == 1 })

	wire.Send(cec.NewMessage(5, cec.AddressBroadcast, cec.OpRoutingInformation, 0x15, 0x00))
	wire.Send(cec.ActiveSource(8, 0x1500))

	television := wokeWith(t, api, session)
	mustMatch(t, conditionOf(television.Status.Conditions, conditionWakeApplied).Reason, reasonConfirmed)
	mustMatch(t, len(claimsOf(wire)), 2)
}

// A Routing Information that a waking receiver sends does not stop the
// adapter from answering the TV's Request Active Source during the wake.
func TestRoutingInformationDuringAWakeKeepsTheAnswer(t *testing.T) {
	fastWake(t)
	cecWakeGuard = time.Second
	wire := roomWithTV(televisionTV(cec.PowerOn))
	session := wokeNow()
	api := awakeRoom(t, wire, session)
	api.waitUntil(t, "the wake's Active Source", func() bool { return len(claimsOf(wire)) == 1 })

	wire.Send(cec.NewMessage(5, cec.AddressBroadcast, cec.OpRoutingInformation, 0x15, 0x00))
	askForTheSource(wire)

	api.waitUntil(t, "the answer", func() bool { return len(claimsOf(wire)) == 2 })
}
