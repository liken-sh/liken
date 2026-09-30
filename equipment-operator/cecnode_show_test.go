package main

// The node workload's part of a home press: a new status.session.showAt
// on a TV that reports On gets Image View On and then Active Source
// for the session's Display, once. A TV that is off gets nothing, so a
// home press wakes no room. cecnode_wake_test.go holds the wake
// fixtures.

import (
	"testing"
	"time"

	"github.com/liken-sh/equipment-operator/cec"
)

// shownNow is a session after its wake with a home press's showAt.
func shownNow(session *TelevisionSession) *TelevisionSession {
	shown := *session
	shown.ShowAt = time.Now().UTC().Format(wakeTimeLayout)
	return &shown
}

// A TV on its own apps is On and on another input. Image View On goes
// out first even then, because a TV on an internal app switches on
// Image View On and Active Source together, the order One Touch Play
// sends, and not on a bare Active Source.
func TestAShowSendsImageViewOnAndActiveSource(t *testing.T) {
	cases := []struct {
		name    string
		power   cec.PowerStatus
		awake   bool
		viewOns int
		claims  int
	}{
		{"a TV that is on", cec.PowerOn, true, 2, 2},
		{"a TV that is off", cec.PowerStandby, true, 1, 1},
		{"a session that is asleep", cec.PowerOn, false, 1, 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			fastWake(t)
			wire := roomWithTV(televisionTV(cec.PowerOn))
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
	}
}

// Each showAt is one ask: a later pass sends nothing more for it.
func TestAShowRunsOnce(t *testing.T) {
	fastWake(t)
	wire := roomWithTV(televisionTV(cec.PowerOn))
	session := wokeNow()
	api := awakeRoom(t, wire, session)
	wokeWith(t, api, session)
	api.putTelevision(waking(shownNow(session)))
	api.waitUntil(t, "the show", func() bool { return len(claimsOf(wire)) == 2 })

	api.nudge()
	time.Sleep(quietPeriod)

	mustMatch(t, sentOf(wire, cec.OpImageViewOn), 2)
	mustDeepEqual(t, claimsOf(wire), []string{"4->f 82 13 00", "4->f 82 13 00"})
}

// A showAt in the status when the node workload starts is older than
// the press, and sends nothing, as a wake found at start does.
func TestAShowFoundAtStartSendsNothing(t *testing.T) {
	fastWake(t)
	api := startCECAPI(t)
	wire := roomWithTV(televisionTV(cec.PowerOn))
	_, device := usbAdapter(wire)
	api.putDisplay("acm-0001-receiver", "node-1", "1.3.0.0")
	api.putBus(controlBus("den", CECBusAdapter{Machine: "node-1", Display: "acm-0001-receiver"}))
	session := shownNow(wokeNow())
	session.WokeAt = ""
	api.putTelevision(waking(session))

	startNode(t, api, "node-1", device)
	api.waitForEntry(t, "den", "node-1", func(entry CECAdapterStatus) bool { return entry.State == AdapterScanned })
	api.nudge()
	time.Sleep(quietPeriod)

	mustMatch(t, sentOf(wire, cec.OpImageViewOn), 0)
	mustMatch(t, len(claimsOf(wire)), 0)
}

// The show is an operation a person causes, so it writes one line with
// what the TV reported and what the adapter sent.
func TestAShowWritesALine(t *testing.T) {
	fastWake(t)
	api := startCECAPI(t)
	wire := roomWithTV(televisionTV(cec.PowerOn))
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
	mustDeepEqual(t, linesWith(log, "asked to show"), []string{
		"Television lounge: Player media/den asked to show Display acm-0001-receiver at " + shown.ShowAt +
			"; the TV reported On, so the adapter on node-1 sent Image View On and Active Source for 1.3.0.0",
	})
}
