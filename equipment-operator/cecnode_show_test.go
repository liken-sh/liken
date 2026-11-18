package main

// A new status.session.showAt in a session with Awake set makes the
// adapter send Image View On and then Active Source for the session's
// Display, once. A session that is not Awake gets nothing, so a home
// press never wakes a room. cecnode_wake_test.go has the wake fixtures.

import (
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/liken-sh/liken/equipment-operator/cec"
	"github.com/liken-sh/liken/equipment-operator/cec/cectest"
)

// shownNow returns a copy of session with showAt set to the current
// time, as a home press sets it.
func shownNow(session *TelevisionSession) *TelevisionSession {
	shown := *session
	shown.ShowAt = time.Now().UTC().Format(wakeTimeLayout)
	return &shown
}

// The adapter sends Image View On whatever power the TV reports,
// because a TV that shows its own apps reports On and is on another
// input. The counts include the Image View On and Active Source of the
// wake that comes before the press.
func TestAShowSendsImageViewOnAndActiveSource(t *testing.T) {
	cases := []struct {
		name    string
		power   cec.PowerStatus
		awake   bool
		viewOns int
		claims  int
	}{
		{"a TV that is on", cec.PowerOn, true, 2, 2},
		{"a TV in standby", cec.PowerStandby, true, 2, 2},
		{"a session that is asleep", cec.PowerOn, false, 1, 1},
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
				wire.Add(televisionTV(c.power))
				shown := shownNow(session)
				shown.Awake = c.awake

				api.putTelevision(waking(shown))
				time.Sleep(quietPeriod)

				mustMatch(t, sentOf(wire, cec.OpImageViewOn), c.viewOns)
				mustMatch(t, len(claimsOf(wire)), c.claims)
				mustCommandTheTVAlone(t, wire)
			})
		})
	}
}

// A home press claims the input with the same pair as every other
// claim, Image View On and then Active Source, and sends no Inactive
// Source. Inactive Source tells the TV that the Display has no picture,
// and a TV can act on that before the Active Source arrives.
func TestAShowSendsOnlyTheClaim(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		wire := busWithTV(televisionTV(cec.PowerOn))
		session := wokeNow()
		api := awakeRoom(t, wire, session)
		wokeWith(t, api, session)
		before := len(wire.Sent())

		api.putTelevision(waking(shownNow(session)))
		api.waitUntil(t, "the show", func() bool { return len(claimsOf(wire)) == 2 })

		mustDeepEqual(t, routingSentAfter(wire, before), []string{"4->0 04", "4->f 82 13 00"})
		mustMatch(t, sentOf(wire, cec.OpInactiveSource), 0)
	})
}

// routingSentAfter returns the messages the adapter sent after the first
// skip messages, leaving out polls and Give Device Power Status.
func routingSentAfter(wire *cectest.Bus, skip int) []string {
	var sent []string
	for _, message := range wire.Sent()[skip:] {
		if opcode, _ := message.Opcode(); !message.IsPoll() && opcode != cec.OpGiveDevicePowerStatus {
			sent = append(sent, message.String())
		}
	}
	return sent
}

// A showAt is handled once, so a later pass with the same showAt sends
// nothing more.
func TestAShowRunsOnce(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		wire := busWithTV(televisionTV(cec.PowerOn))
		session := wokeNow()
		api := awakeRoom(t, wire, session)
		wokeWith(t, api, session)
		api.putTelevision(waking(shownNow(session)))
		api.waitUntil(t, "the show", func() bool { return len(claimsOf(wire)) == 2 })

		api.nudge()
		time.Sleep(quietPeriod)

		mustMatch(t, sentOf(wire, cec.OpImageViewOn), 2)
		mustDeepEqual(t, claimsOf(wire), []string{"4->f 82 13 00", "4->f 82 13 00"})
	})
}

// A showAt already in the status when the node workload starts is from
// an earlier press, so the node workload sends nothing for it.
func TestAShowFoundAtStartSendsNothing(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		api := startCECAPI(t)
		wire := busWithTV(televisionTV(cec.PowerOn))
		_, device := usbAdapter(wire)
		api.putDisplay("acm-0001-receiver", "node-1", "1.3.0.0")
		api.putBus(controlBus("den", CECBusAdapter{Machine: "node-1", Display: "acm-0001-receiver"}))
		session := shownNow(wokeNow())
		session.WokeAt = ""
		api.putTelevision(waking(session))

		startNode(t, api, "node-1", device)
		api.scanned(t, "node-1")
		api.nudge()
		time.Sleep(quietPeriod)

		mustMatch(t, sentOf(wire, cec.OpImageViewOn), 0)
		mustMatch(t, len(claimsOf(wire)), 0)
	})
}

// A home press is an action a person takes, so the node workload logs
// one line with the press and what the adapter sent.
func TestAShowWritesALine(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		api := startCECAPI(t)
		wire := busWithTV(televisionTV(cec.PowerOn))
		_, device := usbAdapter(wire)
		api.putDisplay("acm-0001-receiver", "node-1", "1.3.0.0")
		api.putBus(controlBus("den", CECBusAdapter{Machine: "node-1", Display: "acm-0001-receiver"}))
		api.putTelevision(lounge(""))
		log := loggedNode(t, api, device)
		session := wokeNow()
		wakeLive(t, api, session)
		wokeWith(t, api, session)
		shown := shownNow(session)

		api.putTelevision(waking(shown))

		api.waitUntil(t, "the line", func() bool { return len(linesWith(log, "asked to show")) == 1 })
		lines := linesWith(log, "asked to show")
		mustMatch(t, strings.Contains(lines[0], "Player media/den asked to show Display acm-0001-receiver at "+shown.ShowAt), true)
		mustMatch(t, strings.Contains(lines[0], "the adapter on node-1 sent Image View On and Active Source for 1.3.0.0"), true)
	})
}
