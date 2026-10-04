package main

// The show ask: a home press asks the receiver for the session's input,
// as the ensure does, and asks the room's TV to show the session's
// Display. television_show_test.go tests what the ask writes on the
// Television, and cecnode_show_test.go what the adapter sends the TV.

import (
	"testing"
	"testing/synctest"

	"github.com/liken-sh/equipment-operator/denon"
	"github.com/liken-sh/equipment-operator/equipment"
)

// showAsk is the ask the media operator writes for a home press.
var showAsk = ReceiverInputAsk{Action: "show", At: askAt(1)}

// A show asks the room's TV for the Display and the receiver for the
// input. The room hears the ask whatever the receiver reports, because
// the TV, not the receiver, decides whether the room is on for it.
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
			"show: status.session.inputAsk at 2026-10-04T12:15:25.001Z asks show",
		})
	})
}

// A show on a dark room asks the TV, which sends nothing unless it is
// on, and turns the receiver on no more than the ensure does.
func TestAShowInStandbyTurnsNothingOn(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newSessionHarness(t)
		room := &roomRecord{}
		h.room = room
		held := h.beginIdle(t, "GAME")

		held.inputAsk(showAsk)

		mustDeepEqual(t, room.waitFor(t, 2), []string{"opened (awake false)", "show: status.session.inputAsk at 2026-10-04T12:15:25.001Z asks show"})
		h.refuseCommands(t, quietPeriod, denon.PowerOnCommand, "SIGAME")
	})
}
