package main

// media-operator writes a show request to status.session.inputAsk when
// someone presses home on the remote. The operator passes the request to
// the room's Television, and selects the session's input on the receiver
// as an ensure request does. television_show_test.go tests what the
// request writes on the Television, and cecnode_show_test.go tests what
// the adapter sends the TV.

import (
	"testing"
	"testing/synctest"

	"github.com/liken-sh/liken/equipment-operator/denon"
	"github.com/liken-sh/liken/equipment-operator/equipment"
)

var showAsk = ReceiverInputAsk{Action: "show", At: askAt(1)}

// The operator passes a show request to the room whatever power the
// receiver reports.
func TestAShowAsksTheRoomAndTheReceiver(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newSessionHarness(t)
		room := &roomRecord{}
		h.room = room
		held := h.beginIdle(t, "GAME")
		held.setFlags(false, true)
		h.equipment.waitForCommands(t, "SIGAME")
		handOnTheRemote(t, h.equipment, "SIDVD")
		h.waitUntil(t, func(state equipment.State) bool { return mainZone(state).Input == "DVD" })

		held.inputAsk(showAsk)

		h.equipment.waitForCommands(t, "SIGAME")
		mustDeepEqual(t, room.waitFor(t, 3), []string{
			"opened (awake false)",
			"woke: the screen of Player theater woke",
			"show: status.session.inputAsk at 2026-10-04T12:15:25.001Z requests show",
		})
	})
}

// A show request while the receiver is in standby does not turn the
// receiver on, because only the power key turns the equipment on.
func TestAShowInStandbyTurnsNothingOn(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newSessionHarness(t)
		room := &roomRecord{}
		h.room = room
		held := h.beginIdle(t, "GAME")

		held.inputAsk(showAsk)

		mustDeepEqual(t, room.waitFor(t, 2), []string{"opened (awake false)", "show: status.session.inputAsk at 2026-10-04T12:15:25.001Z requests show"})
		h.refuseCommands(t, quietPeriod, denon.PowerOnCommand, "SIGAME")
	})
}
