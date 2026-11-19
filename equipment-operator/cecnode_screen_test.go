package main

// Tests of the screen requests that the node workload writes to the
// Television's status.screenAsk when the TV selects the Display or goes
// to standby.

import (
	"testing"
	"testing/synctest"
	"time"

	"github.com/liken-sh/liken/equipment-operator/cec"
	"github.com/liken-sh/liken/equipment-operator/cec/cectest"
)

// sleepingRoom runs the node workload for node-1 on a bus in Control,
// with a sleeping session whose Display is on node-1.
func sleepingRoom(t *testing.T, wire *cectest.Bus) (*cecAPI, *TelevisionSession) {
	t.Helper()
	api := controlling(t, wire, lounge(""))
	api.scanned(t, "node-1")
	asleep := &TelevisionSession{Player: "media/den", Display: "acm-0001-receiver"}
	api.putTelevision(waking(asleep))
	api.nudge()
	time.Sleep(quietPeriod)
	return api, asleep
}

// pickTheDisplay sends the Set Stream Path that the TV sends when a
// person selects node-1's Display input in its menu.
func pickTheDisplay(wire *cectest.Bus) {
	wire.Send(cec.NewMessage(cec.AddressTV, cec.AddressBroadcast, cec.OpSetStreamPath, 0x13, 0x00))
}

// screenAsked waits until the Television's status.screenAsk requests
// the given screen action.
func screenAsked(t *testing.T, api *cecAPI, screen string) TelevisionScreenAsk {
	t.Helper()
	television := api.waitForTelevision(t, "lounge", func(television Television) bool {
		return television.Status.ScreenAsk != nil && television.Status.ScreenAsk.Screen == screen
	})
	return *television.Status.ScreenAsk
}

// A person selects the Player's input in the TV's menu while the
// screen sleeps. The adapter requests a screen wake and sends no Active
// Source, because the Display has no picture yet.
func TestAPickOfASleepingDisplayAsksTheScreenToWake(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		wire := busWithTV(televisionTV(cec.PowerOn))
		api, _ := sleepingRoom(t, wire)

		pickTheDisplay(wire)

		ask := screenAsked(t, api, screenWake)
		mustMatch(t, ask.Player, "media/den")
		mustMatch(t, ask.Cause, `"TV" (logical 0, 0.0.0.0) broadcast Set Stream Path 1.3.0.0`)
		mustMatch(t, len(claimsOf(wire)), 0)
	})
}

// The wake that follows the TV's selection sends Active Source without
// Image View On, because the TV is on and already shows the Display's
// input.
func TestTheWakeAfterAPickSendsActiveSourceAlone(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		wire := busWithTV(televisionTV(cec.PowerOn))
		api, _ := sleepingRoom(t, wire)
		pickTheDisplay(wire)
		screenAsked(t, api, screenWake)

		session := wokeNow()
		api.putTelevision(waking(session))

		television := wokeWith(t, api, session)
		applied := conditionOf(television.Status.Conditions, conditionWakeApplied)
		mustMatch(t, applied.Reason, reasonConfirmed)
		mustMatch(t, applied.Message, "The TV already reported On. The adapter on node-1 sent Active Source for 1.3.0.0, Display acm-0001-receiver, once, and no other source claimed the input in the 30 s after. The last Active Source on the bus is 1.3.0.0")
		mustDeepEqual(t, claimsOf(wire), []string{"4->f 82 13 00"})
		mustMatch(t, sentOf(wire, cec.OpImageViewOn), 0)
	})
}

// When a person selects another source before the screen wakes, the
// wake sends Image View On as well as Active Source. The test waits
// until the adapter has read the second Set Stream Path, because a wake
// that the adapter handles first finds the pick of the Display current.
func TestAPickThatMovedOnIsForgotten(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		wire := busWithTV(televisionTV(cec.PowerOn))
		api, _ := sleepingRoom(t, wire)
		pickTheDisplay(wire)
		screenAsked(t, api, screenWake)
		wire.Send(cec.NewMessage(cec.AddressTV, cec.AddressBroadcast, cec.OpSetStreamPath, 0x15, 0x00))
		synctest.Wait()

		session := wokeNow()
		api.putTelevision(waking(session))

		wokeWith(t, api, session)
		mustMatch(t, sentOf(wire, cec.OpImageViewOn), 1)
	})
}

// A Set Stream Path for another device's address requests nothing,
// because that device handles it.
func TestAPickOfAnotherSourceAsksNothing(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		wire := busWithTV(televisionTV(cec.PowerOn))
		api, _ := sleepingRoom(t, wire)

		wire.Send(cec.NewMessage(cec.AddressTV, cec.AddressBroadcast, cec.OpSetStreamPath, 0x15, 0x00))
		time.Sleep(quietPeriod)

		television, _ := api.television("lounge")
		mustMatch(t, television.Status.ScreenAsk == nil, true)
	})
}

// A Standby while the session is awake requests a screen sleep when it
// is broadcast, or when the TV addresses it to the adapter.
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
	t.Parallel()
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				wire := busWithTV(televisionTV(cec.PowerOn))
				session := wokeNow()
				api := awakeRoom(t, wire, session)
				wokeWith(t, api, session)

				wire.Send(c.standby)
				time.Sleep(quietPeriod)

				television, _ := api.television("lounge")
				mustMatch(t, television.Status.ScreenAsk != nil && television.Status.ScreenAsk.Screen == screenSleep, c.asks)
			})
		})
	}
}

// A Standby while the session sleeps requests nothing.
func TestAStandbyInADarkRoomAsksNothing(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		wire := busWithTV(televisionTV(cec.PowerOn))
		api, _ := sleepingRoom(t, wire)

		wire.Send(cec.Standby(cec.AddressTV, cec.AddressBroadcast))
		time.Sleep(quietPeriod)

		television, _ := api.television("lounge")
		mustMatch(t, television.Status.ScreenAsk == nil, true)
	})
}
