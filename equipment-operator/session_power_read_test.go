package main

// A press of the remote's power button reads the TV's power first.
// status.power can be out of date, because no timer polls the TV and a
// TV that a person turned off with its own remote can send the adapter
// no message. So the press asks the node workload for one read through
// status.session.powerReadAt, and falls back to status.power when no
// result arrives within cecPowerReadWait.

import (
	"testing"
	"testing/synctest"
	"time"

	"github.com/liken-sh/liken/equipment-operator/cec"
)

// setLoungeStatus sets the status of the lounge Television as the
// operator derives it for a TV that replies to Give Device Power Status:
// the Display of input GAME, a power that can be out of date, and
// Reachable true.
func setLoungeStatus(api *cecAPI, stale string) {
	setLoungeStatusWith(api, stale, reachableReplies)
}

// reachableReplies is the Reachable condition of a TV that replies to
// Give Device Power Status. reachableNoPower is the condition of a TV
// that acknowledges its address but reported no power, at the join scan
// or on two reads in a row.
var (
	reachableReplies = Condition{Type: conditionReachable, Status: ConditionTrue, Reason: reasonAnswers}
	reachableNoPower = Condition{Type: conditionReachable, Status: ConditionFalse, Reason: reasonNoPower}
)

func setLoungeStatusWith(api *cecAPI, stale string, reachable Condition) {
	api.mutex.Lock()
	defer api.mutex.Unlock()
	television := api.televisions["lounge"]
	television.Status.Displays = []TelevisionDisplay{{Name: "acm-0001-receiver", PhysicalAddress: "1.3.0.0"}}
	television.Status.Power = stale
	television.Status.Conditions = []Condition{reachable}
	api.changed()
}

// theaterRoom returns the roomEvents of a session of Player theater on
// input GAME, which controls the lounge Television. The sessions read
// the Televisions from the store of a running watch, as in the operator,
// where they read the store of the CECBus loop.
func theaterRoom(t *testing.T, api *cecAPI, h *sessionHarness) roomEvents {
	t.Helper()
	sessions := newTelevisionSessions(api.client)
	sessions.televisions = &watchStore{}
	runHeldWatch(t, api.client, watchTelevisions, nil, sessions.televisions)
	t.Cleanup(sessions.stop)
	sessions.markLive()
	return sessions.room(h.lines, "theater", "GAME", func(input string) string {
		return map[string]string{"GAME": "acm-0001-receiver"}[input]
	})
}

// A press turns the room on or off correctly when the TV's power changed through its own
// remote and the adapter received no message about it. A TV with no
// known power is read too, because it can be on, and a receiver in
// standby must not make the press decide that the room is off.
func TestAPowerPressReadsTheTVsPowerFirst(t *testing.T) {
	cases := []struct {
		name      string
		actual    cec.PowerStatus
		stale     string
		reachable Condition
		line      string
		asked     func(TelevisionSession) bool
	}{
		{"a TV turned off by its own remote", cec.PowerStandby, "On", reachableReplies,
			"Receiver theater: status.session.powerAsk requests toggle, and Television lounge reports power Standby; asked Television lounge to wake and show Display acm-0001-receiver",
			func(session TelevisionSession) bool { return session.WokeAt != "" && session.Awake }},
		{"a TV turned on by its own remote", cec.PowerOn, "Standby", reachableReplies,
			"Receiver theater: status.session.powerAsk requests toggle, and Television lounge reports power On; asked Television lounge to go to standby",
			func(session TelevisionSession) bool { return session.StandbyAt != "" }},
		{"a TV that is on with no known power", cec.PowerOn, "", reachableNoPower,
			"Receiver theater: status.session.powerAsk requests toggle, and Television lounge reports power On; asked Television lounge to go to standby",
			func(session TelevisionSession) bool { return session.StandbyAt != "" }},
	}
	t.Parallel()
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				wire := busWithTV(televisionTV(c.actual))
				api := controlling(t, wire, lounge(""))
				api.waitForEntry(t, "den", "node-1", func(entry CECAdapterStatus) bool { return entry.State == AdapterScanned })
				setLoungeStatusWith(api, c.stale, c.reachable)
				h := newSessionHarness(t)
				h.room = theaterRoom(t, api, h)

				pressPower(t, h)

				loungeSession(t, api, c.asked)
				mustMatch(t, waitForLines(t, h.log, "requests toggle", 1)[0], c.line)
			})
		})
	}
}

// When the read gets no result, for example while the node workload
// restarts, the press reads status.power after cecPowerReadWait.
func TestAPowerPressWithNoReadDecidesFromTheStatus(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		api := startCECAPI(t)
		api.showing(lounge(""), "acm-0001-receiver")
		setLoungeStatus(api, "On")
		h := newSessionHarness(t)
		h.room = theaterRoom(t, api, h)

		pressPower(t, h)
		time.Sleep(cecPowerReadWait)

		session := loungeSession(t, api, func(session TelevisionSession) bool { return session.StandbyAt != "" })
		mustMatch(t, session.PowerReadAt, "")
		mustMatch(t, waitForLines(t, h.log, "requests toggle", 1)[0],
			"Receiver theater: status.session.powerAsk requests toggle, and Television lounge reports power On; asked Television lounge to go to standby")
	})
}

// A press waits for the read on the Television watch that the operator
// already runs, and opens no watch of its own. The TV's status.power is
// empty, so with no read a press would decide from the receiver's
// power. The second press reports Standby, which only a fresh read can
// give it.
func TestAPowerPressOpensNoWatch(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		wire := busWithTV(televisionTV(cec.PowerOn))
		api := controlling(t, wire, lounge(""))
		api.waitForEntry(t, "den", "node-1", func(entry CECAdapterStatus) bool { return entry.State == AdapterScanned })
		setLoungeStatusWith(api, "", reachableNoPower)
		h := newSessionHarness(t)
		h.room = theaterRoom(t, api, h)
		api.waitUntil(t, "the writer's Television watch to open", func() bool { return api.watchesOf(televisionsPath) == 2 })
		held := h.beginIdle(t, "GAME")

		powerAsk(held, "toggle")
		api.waitUntil(t, "the TV to take Standby", func() bool { return sentOf(wire, cec.OpStandby) == 1 })
		powerAsk(held, "toggle")

		mustDeepEqual(t, waitForLines(t, h.log, "asked Television lounge to", 2), []string{
			"Receiver theater: status.session.powerAsk requests toggle, and Television lounge reports power On; asked Television lounge to go to standby",
			"Receiver theater: status.session.powerAsk requests toggle, and Television lounge reports power Standby; asked Television lounge to wake and show Display acm-0001-receiver",
		})
		mustMatch(t, api.watchesOf(televisionsPath), 2)
	})
}

// watchesOf returns how many watches were opened on path.
func (a *cecAPI) watchesOf(path string) int {
	a.mutex.Lock()
	defer a.mutex.Unlock()
	return a.watches[path]
}
