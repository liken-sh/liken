package main

// The explicit power asks. A TV remote's Power On Function and Power Off
// Function (HDMI-CEC 1.3a, CEC 13.13.3) name the state they want, and a
// second press leaves the room in that state. The media operator asks
// on for the first and off for the second, and the session turns the
// room on or off only when it is not in that state already.
// session_test.go holds the toggle.

import (
	"testing"
	"testing/synctest"

	"github.com/liken-sh/equipment-operator/denon"
)

// askPower starts an idle session, with the receiver on or in standby,
// and hands it one power ask.
func askPower(t *testing.T, on bool, ask string) *sessionHarness {
	t.Helper()
	h := newSessionHarness(t)
	if on {
		h.powerOn(t)
	}
	powerAsk(h.beginIdle(t, "GAME"), ask)
	return h
}

func TestAnOffAskTurnsAnOnRoomOff(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := askPower(t, true, "off")

		mustMatch(t, h.equipment.waitForCommand(t), "PWSTANDBY")
		h.refuseCommands(t, quietPeriod, "SIGAME")
	})
}

func TestAnOnAskTurnsAnOffRoomOn(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := askPower(t, false, "on")

		mustMatch(t, h.equipment.waitForCommand(t), denon.PowerOnCommand)
		mustMatch(t, h.equipment.waitForCommand(t), "SIGAME")
	})
}

// A room already in the state the ask names stays in it, and the line
// says why the session sent nothing.
func TestAnAskForTheStateTheRoomHoldsSendsNothing(t *testing.T) {
	cases := []struct {
		name string
		on   bool
		ask  string
		line string
	}{
		{"off in a room that is off", false, "off",
			"Receiver theater: status.session.powerAsk asks off, and the receiver reports power Standby; sent nothing, because the room is already off"},
		{"on in a room that is on", true, "on",
			"Receiver theater: status.session.powerAsk asks on, and the receiver reports power On; sent nothing, because the room is already on"},
	}
	t.Parallel()
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				h := askPower(t, c.on, c.ask)

				mustDeepEqual(t, waitForLines(t, h.log, "status.session.powerAsk asks", 1), []string{c.line})
				h.refuseCommands(t, quietPeriod, denon.PowerOnCommand, "PWSTANDBY", "SIGAME")
			})
		})
	}
}
