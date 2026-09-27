package main

// The remote's power button decides from a fresh read of the TV's
// power. No timer asks the TV, so status.power can be older than the
// press: a TV that a person turned off with its own remote can send
// nothing an adapter hears. The press asks the node workload for one
// read and waits for it, and falls back to status.power when no answer
// arrives in time.

import (
	"testing"
	"time"

	"github.com/liken-sh/equipment-operator/cec"
)

// answering marks the lounge Television as the Deployment derives it
// for a TV that answers: it lists the Display of input GAME, reports a
// power that may be stale, and is Reachable.
func answering(api *cecAPI, stale string) {
	api.mutex.Lock()
	defer api.mutex.Unlock()
	television := api.televisions["lounge"]
	television.Status.Displays = []TelevisionDisplay{{Name: "acm-0001-receiver", PhysicalAddress: "1.3.0.0"}}
	television.Status.Power = stale
	television.Status.Conditions = []Condition{{Type: conditionReachable, Status: ConditionTrue, Reason: reasonAnswers}}
	api.changed()
}

// theaterRoom is the link a session of Player theater on input GAME
// uses to reach the lounge Television.
func theaterRoom(t *testing.T, api *cecAPI, h *sessionHarness) roomEvents {
	t.Helper()
	sessions := newTelevisionSessions(api.client)
	t.Cleanup(sessions.stop)
	sessions.markLive()
	return sessions.room(h.lines, "theater", "GAME", func(input string) string {
		return map[string]string{"GAME": "acm-0001-receiver"}[input]
	})
}

// A TV whose power changed by its own remote with no message the
// adapter heard still decides the press correctly, because the press
// reads the TV first.
func TestAPowerPressReadsTheTVsPowerFirst(t *testing.T) {
	cases := []struct {
		name   string
		actual cec.PowerStatus
		stale  string
		line   string
		asked  func(TelevisionSession) bool
	}{
		{"a TV turned off by its own remote", cec.PowerStandby, "On",
			"Receiver theater: the power topic asks toggle, and Television lounge reports power Standby; asked Television lounge to wake and show Display acm-0001-receiver",
			func(session TelevisionSession) bool { return session.WokeAt != "" && session.Awake }},
		{"a TV turned on by its own remote", cec.PowerOn, "Standby",
			"Receiver theater: the power topic asks toggle, and Television lounge reports power On; asked Television lounge to go to standby",
			func(session TelevisionSession) bool { return session.StandbyAt != "" }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			fastPower(t)
			wire := roomWithTV(televisionTV(c.actual))
			api := controlling(t, wire, lounge(""))
			api.waitForEntry(t, "den", "node-1", func(entry CECAdapterStatus) bool { return entry.State == AdapterScanned })
			answering(api, c.stale)
			h := newSessionHarness(t)
			h.powerTopic = testPowerTopic
			h.room = theaterRoom(t, api, h)

			pressPower(t, h)

			loungeSession(t, api, c.asked)
			mustMatch(t, waitForLines(t, h.log, "asks toggle", 1)[0], c.line)
		})
	}
}

// A press whose read gets no answer, such as while the node workload
// restarts, decides from status.power after cecPowerReadWait.
func TestAPowerPressWithNoReadDecidesFromTheStatus(t *testing.T) {
	wait := cecPowerReadWait
	cecPowerReadWait = 100 * time.Millisecond
	t.Cleanup(func() { cecPowerReadWait = wait })
	api := startCECAPI(t)
	api.showing(lounge(""), "acm-0001-receiver")
	answering(api, "On")
	h := newSessionHarness(t)
	h.powerTopic = testPowerTopic
	h.room = theaterRoom(t, api, h)

	pressPower(t, h)

	session := loungeSession(t, api, func(session TelevisionSession) bool { return session.StandbyAt != "" })
	mustMatch(t, session.PowerReadAt, "")
	mustMatch(t, waitForLines(t, h.log, "asks toggle", 1)[0],
		"Receiver theater: the power topic asks toggle, and Television lounge reports power On; asked Television lounge to go to standby")
}
