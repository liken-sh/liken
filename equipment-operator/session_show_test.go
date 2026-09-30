package main

// The show ask: a home press asks the receiver for the session's input,
// as the ensure does, and asks the room's TV to show the session's
// Display. television_show_test.go tests what the ask writes on the
// Television, and cecnode_show_test.go what the adapter sends the TV.

import (
	"testing"

	"github.com/liken-sh/equipment-operator/denon"
	"github.com/liken-sh/equipment-operator/equipment"
)

// A show asks the room's TV for the Display and the receiver for the
// input. The room hears the ask whatever the receiver reports, because
// the TV, not the receiver, decides whether the room is on for it.
func TestAShowAsksTheRoomAndTheReceiver(t *testing.T) {
	t.Parallel()
	h := newSessionHarnessWith(t, ReceiverVolume{Max: 69.5, Step: 1})
	room := &roomRecord{}
	h.room = room
	held := h.beginIdle(t, "GAME")
	held.setFlags(false, true)
	h.equipment.waitForCommands(t, "SIGAME")
	handOnTheRemote(t, h.equipment, "SIDVD")
	h.waitUntil(t, func(state equipment.State) bool { return mainZone(state).Input == "DVD" })

	unit := &receiverUnit{session: held, log: h.lines}
	unit.handleCommand([]byte(`{"command":"input.show"}`))

	h.equipment.waitForCommands(t, "SIGAME")
	mustDeepEqual(t, room.waitFor(t, 3), []string{
		"opened (awake false)",
		"woke: the screen of Player theater woke",
		"show: the commands topic asks input.show",
	})
}

// A show on a dark room asks the TV, which sends nothing unless it is
// on, and turns the receiver on no more than the ensure does.
func TestAShowInStandbyTurnsNothingOn(t *testing.T) {
	t.Parallel()
	h := newSessionHarnessWith(t, ReceiverVolume{Max: 69.5, Step: 1})
	room := &roomRecord{}
	h.room = room
	held := h.beginIdle(t, "GAME")

	held.showInput()

	mustDeepEqual(t, room.waitFor(t, 2), []string{"opened (awake false)", "show: the commands topic asks input.show"})
	h.refuseCommands(t, quietPeriod, denon.PowerOnCommand, "SIGAME")
}
