package main

// The tests of the on and off power requests. session_test.go has the
// tests of the toggle.

import (
	"testing"
	"testing/synctest"

	"github.com/liken-sh/liken/equipment-operator/denon"
)

// readTVPower starts an idle session with the receiver on or in standby,
// and sends it one power request.
func readTVPower(t *testing.T, on bool, ask string) *sessionHarness {
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
		h := readTVPower(t, true, "off")

		mustMatch(t, h.equipment.waitForCommand(t), "PWSTANDBY")
		h.refuseCommands(t, quietPeriod, "SIGAME")
	})
}

func TestAnOnAskTurnsAnOffRoomOn(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := readTVPower(t, false, "on")

		mustMatch(t, h.equipment.waitForCommand(t), denon.PowerOnCommand)
		mustMatch(t, h.equipment.waitForCommand(t), "SIGAME")
	})
}

func TestARequestForTheCurrentStateSendsNothing(t *testing.T) {
	cases := []struct {
		name string
		on   bool
		ask  string
		line string
	}{
		{"off in a room that is off", false, "off",
			"Receiver theater: status.session.powerAsk requests off, and the receiver reports power Standby; sent nothing, because the room is already off"},
		{"on in a room that is on", true, "on",
			"Receiver theater: status.session.powerAsk requests on, and the receiver reports power On; sent nothing, because the room is already on"},
	}
	t.Parallel()
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				h := readTVPower(t, c.on, c.ask)

				mustDeepEqual(t, waitForLines(t, h.log, "status.session.powerAsk requests", 1), []string{c.line})
				h.refuseCommands(t, quietPeriod, denon.PowerOnCommand, "PWSTANDBY", "SIGAME")
			})
		})
	}
}
