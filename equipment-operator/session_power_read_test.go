package main

// The remote's power button decides from a fresh read of the TV's
// power. No timer asks the TV, so status.power can be older than the
// press: a TV that a person turned off with its own remote can send
// nothing an adapter hears. The press asks the node workload for one
// read and waits for it, and falls back to status.power when no answer
// arrives in time.

import (
	"testing"
	"testing/synctest"
	"time"

	"github.com/liken-sh/equipment-operator/cec"
)

// answering marks the lounge Television as the Deployment derives it
// for a TV that answers: it lists the Display of input GAME, reports a
// power that may be stale, and is Reachable.
func answering(api *cecAPI, stale string) {
	answeringAs(api, stale, answers)
}

// answers and noPower are the Reachable conditions the Deployment
// derives for a TV that answers its power status, and for one that
// acknowledges its address and gave no power: a TV that did not answer
// at the join scan, or two reads in a row.
var (
	answers = Condition{Type: conditionReachable, Status: ConditionTrue, Reason: reasonAnswers}
	noPower = Condition{Type: conditionReachable, Status: ConditionFalse, Reason: reasonNoPower}
)

// answeringAs is answering with the Reachable condition a test names.
func answeringAs(api *cecAPI, stale string, reachable Condition) {
	api.mutex.Lock()
	defer api.mutex.Unlock()
	television := api.televisions["lounge"]
	television.Status.Displays = []TelevisionDisplay{{Name: "acm-0001-receiver", PhysicalAddress: "1.3.0.0"}}
	television.Status.Power = stale
	television.Status.Conditions = []Condition{reachable}
	api.changed()
}

// theaterRoom is the link a session of Player theater on input GAME
// uses to reach the lounge Television. The writer reads the Televisions
// from a running watch's store, the way the Deployment's writer reads
// the CECBus loop's.
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

// A TV whose power changed by its own remote with no message the
// adapter heard still decides the press correctly, because the press
// reads the TV first. A TV with no known power is read too: it gave
// none at the join scan or on two reads, and it can be on, so the
// receiver in standby must not decide that the room is off.
func TestAPowerPressReadsTheTVsPowerFirst(t *testing.T) {
	cases := []struct {
		name      string
		actual    cec.PowerStatus
		stale     string
		reachable Condition
		line      string
		asked     func(TelevisionSession) bool
	}{
		{"a TV turned off by its own remote", cec.PowerStandby, "On", answers,
			"Receiver theater: status.session.powerAsk asks toggle, and Television lounge reports power Standby; asked Television lounge to wake and show Display acm-0001-receiver",
			func(session TelevisionSession) bool { return session.WokeAt != "" && session.Awake }},
		{"a TV turned on by its own remote", cec.PowerOn, "Standby", answers,
			"Receiver theater: status.session.powerAsk asks toggle, and Television lounge reports power On; asked Television lounge to go to standby",
			func(session TelevisionSession) bool { return session.StandbyAt != "" }},
		{"a TV that is on with no known power", cec.PowerOn, "", noPower,
			"Receiver theater: status.session.powerAsk asks toggle, and Television lounge reports power On; asked Television lounge to go to standby",
			func(session TelevisionSession) bool { return session.StandbyAt != "" }},
	}
	t.Parallel()
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				wire := roomWithTV(televisionTV(c.actual))
				api := controlling(t, wire, lounge(""))
				api.waitForEntry(t, "den", "node-1", func(entry CECAdapterStatus) bool { return entry.State == AdapterScanned })
				answeringAs(api, c.stale, c.reachable)
				h := newSessionHarness(t)
				h.room = theaterRoom(t, api, h)

				pressPower(t, h)

				loungeSession(t, api, c.asked)
				mustMatch(t, waitForLines(t, h.log, "asks toggle", 1)[0], c.line)
			})
		})
	}
}

// A press whose read gets no answer, such as while the node workload
// restarts, decides from status.power after cecPowerReadWait.
func TestAPowerPressWithNoReadDecidesFromTheStatus(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		api := startCECAPI(t)
		api.showing(lounge(""), "acm-0001-receiver")
		answering(api, "On")
		h := newSessionHarness(t)
		h.room = theaterRoom(t, api, h)

		pressPower(t, h)
		time.Sleep(cecPowerReadWait)

		session := loungeSession(t, api, func(session TelevisionSession) bool { return session.StandbyAt != "" })
		mustMatch(t, session.PowerReadAt, "")
		mustMatch(t, waitForLines(t, h.log, "asks toggle", 1)[0],
			"Receiver theater: status.session.powerAsk asks toggle, and Television lounge reports power On; asked Television lounge to go to standby")
	})
}

// A press waits on the Television watch the Deployment already runs,
// so the presses open no watch of their own. Each press still decides
// from its fresh read: the TV's status.power is empty, so a press that
// got no answer would decide from the receiver's power instead, and the
// second press finds the standby the first one sent.
func TestAPowerPressOpensNoWatch(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		wire := roomWithTV(televisionTV(cec.PowerOn))
		api := controlling(t, wire, lounge(""))
		api.waitForEntry(t, "den", "node-1", func(entry CECAdapterStatus) bool { return entry.State == AdapterScanned })
		answeringAs(api, "", noPower)
		h := newSessionHarness(t)
		h.room = theaterRoom(t, api, h)
		api.waitUntil(t, "the writer's Television watch to open", func() bool { return api.watchesOf(televisionsPath) == 2 })
		held := h.beginIdle(t, "GAME")

		powerAsk(held, "toggle")
		api.waitUntil(t, "the TV to take Standby", func() bool { return sentOf(wire, cec.OpStandby) == 1 })
		powerAsk(held, "toggle")

		mustDeepEqual(t, waitForLines(t, h.log, "asked Television lounge to", 2), []string{
			"Receiver theater: status.session.powerAsk asks toggle, and Television lounge reports power On; asked Television lounge to go to standby",
			"Receiver theater: status.session.powerAsk asks toggle, and Television lounge reports power Standby; asked Television lounge to wake and show Display acm-0001-receiver",
		})
		mustMatch(t, api.watchesOf(televisionsPath), 2)
	})
}

// watchesOf counts the watches opened on one collection.
func (a *cecAPI) watchesOf(path string) int {
	a.mutex.Lock()
	defer a.mutex.Unlock()
	return a.watches[path]
}
