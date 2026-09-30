package main

// The asks the node workload hears on the bus for the Player's screen:
// the TV's Set Stream Path for a sleeping session's Display asks the
// screen to wake, and a Standby while the session holds the room awake
// asks it to sleep. The node workload writes each ask in the
// Television's status.screenAsk, and the Deployment relays it.

import (
	"testing"
	"time"

	"github.com/liken-sh/equipment-operator/cec"
	"github.com/liken-sh/equipment-operator/cec/cectest"
)

// sleepingRoom runs the node workload for node-1 on a bus in Control,
// with a session that stands on node-1's Display and sleeps.
func sleepingRoom(t *testing.T, wire *cectest.Bus) (*cecAPI, *TelevisionSession) {
	t.Helper()
	api := controlling(t, wire, lounge(""))
	api.waitForEntry(t, "den", "node-1", func(entry CECAdapterStatus) bool { return entry.State == AdapterScanned })
	asleep := &TelevisionSession{Player: "media/den", Display: "acm-0001-receiver"}
	api.putTelevision(waking(asleep))
	api.nudge()
	time.Sleep(quietPeriod)
	return api, asleep
}

// pickTheDisplay puts the TV's Set Stream Path for node-1's Display on
// the wire, which a TV sends when a person picks the input in its menu.
func pickTheDisplay(wire *cectest.Bus) {
	wire.Send(cec.NewMessage(cec.AddressTV, cec.AddressBroadcast, cec.OpSetStreamPath, 0x13, 0x00))
}

// screenAsked waits for the Television's status.screenAsk to name a
// screen.
func screenAsked(t *testing.T, api *cecAPI, screen string) TelevisionScreenAsk {
	t.Helper()
	television := api.waitForTelevision(t, "lounge", func(television Television) bool {
		return television.Status.ScreenAsk != nil && television.Status.ScreenAsk.Screen == screen
	})
	return *television.Status.ScreenAsk
}

// A person picks the Player's input in the TV's menu while the Player's
// screen sleeps. The Display has no picture yet, so the adapter sends
// no Active Source, and asks the Player's screen to wake.
func TestAPickOfASleepingDisplayAsksTheScreenToWake(t *testing.T) {
	fastWake(t)
	wire := roomWithTV(televisionTV(cec.PowerOn))
	api, _ := sleepingRoom(t, wire)

	pickTheDisplay(wire)

	ask := screenAsked(t, api, screenWake)
	mustMatch(t, ask.Player, "media/den")
	mustMatch(t, ask.Cause, `"TV" (logical 0, 0.0.0.0) broadcast Set Stream Path 1.3.0.0`)
	mustMatch(t, len(claimsOf(wire)), 0)
}

// The session that the pick woke claims the input with Active Source
// alone: the TV is on and already shows the Display's input, so Image
// View On asks nothing of it.
func TestTheWakeAfterAPickSendsActiveSourceAlone(t *testing.T) {
	fastWake(t)
	wire := roomWithTV(televisionTV(cec.PowerOn))
	api, _ := sleepingRoom(t, wire)
	pickTheDisplay(wire)
	screenAsked(t, api, screenWake)

	session := wokeNow()
	api.putTelevision(waking(session))

	television := wokeWith(t, api, session)
	applied := conditionOf(television.Status.Conditions, conditionWakeApplied)
	mustMatch(t, applied.Reason, reasonConfirmed)
	mustMatch(t, applied.Message, "the TV already reported On; the adapter on node-1 sent Active Source for 1.3.0.0, Display acm-0001-receiver, once, and no other source claimed the input in the 200 ms after; the last Active Source on the bus is 1.3.0.0")
	mustDeepEqual(t, claimsOf(wire), []string{"4->f 82 13 00"})
	mustMatch(t, sentOf(wire, cec.OpImageViewOn), 0)
}

// A pick counts only while the route still leads to the Display. A
// person who picks another source before the screen wakes moved on,
// and the wake that follows claims the input the ordinary way.
func TestAPickThatMovedOnIsForgotten(t *testing.T) {
	fastWake(t)
	wire := roomWithTV(televisionTV(cec.PowerOn))
	api, _ := sleepingRoom(t, wire)
	pickTheDisplay(wire)
	screenAsked(t, api, screenWake)
	wire.Send(cec.NewMessage(cec.AddressTV, cec.AddressBroadcast, cec.OpSetStreamPath, 0x15, 0x00))

	session := wokeNow()
	api.putTelevision(waking(session))

	wokeWith(t, api, session)
	mustMatch(t, sentOf(wire, cec.OpImageViewOn), 1)
}

// A pick of another source's address asks nothing: that source answers.
func TestAPickOfAnotherSourceAsksNothing(t *testing.T) {
	fastWake(t)
	wire := roomWithTV(televisionTV(cec.PowerOn))
	api, _ := sleepingRoom(t, wire)

	wire.Send(cec.NewMessage(cec.AddressTV, cec.AddressBroadcast, cec.OpSetStreamPath, 0x15, 0x00))
	time.Sleep(quietPeriod)

	television, _ := api.television("lounge")
	mustMatch(t, television.Status.ScreenAsk == nil, true)
}

// A Standby that reaches the room while the session holds it awake
// asks the Player's screen to sleep, so the room goes dark with the
// TV. A Standby from the TV counts when it is broadcast or sent to the
// adapter; one from another device counts only when it is broadcast,
// which puts the whole system in standby.
func TestAStandbyAsksTheScreenToSleep(t *testing.T) {
	cases := []struct {
		name    string
		standby cec.Message
		asks    bool
	}{
		{"the TV broadcasts Standby", cec.Standby(cec.AddressTV, cec.AddressBroadcast), true},
		{"the TV sends the adapter Standby", cec.Standby(cec.AddressTV, 4), true},
		{"the receiver broadcasts Standby", cec.Standby(5, cec.AddressBroadcast), true},
		{"the receiver sends the adapter Standby", cec.Standby(5, 4), false},
		{"the receiver sends the TV Standby", cec.Standby(5, cec.AddressTV), false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			fastWake(t)
			wire := roomWithTV(televisionTV(cec.PowerOn))
			session := wokeNow()
			api := awakeRoom(t, wire, session)
			wokeWith(t, api, session)

			wire.Send(c.standby)
			time.Sleep(quietPeriod)

			television, _ := api.television("lounge")
			mustMatch(t, television.Status.ScreenAsk != nil && television.Status.ScreenAsk.Screen == screenSleep, c.asks)
		})
	}
}

// A Standby while the session already sleeps asks nothing: the screen
// is dark.
func TestAStandbyInADarkRoomAsksNothing(t *testing.T) {
	fastWake(t)
	wire := roomWithTV(televisionTV(cec.PowerOn))
	api, _ := sleepingRoom(t, wire)

	wire.Send(cec.Standby(cec.AddressTV, cec.AddressBroadcast))
	time.Sleep(quietPeriod)

	television, _ := api.television("lounge")
	mustMatch(t, television.Status.ScreenAsk == nil, true)
}
