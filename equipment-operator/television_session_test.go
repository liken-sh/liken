package main

// Tests of which Television a Receiver session's events reach, and what
// the Deployment writes to its status.session. The tests of lifts and
// of concurrent writes are in television_session_lift_test.go.

import (
	"testing"
	"testing/synctest"
	"time"
)

// showing stores a Television whose status.displays lists displays,
// as the Deployment's pass derives them.
func (a *cecAPI) showing(television Television, displays ...string) {
	a.putTelevision(television)
	a.mutex.Lock()
	defer a.mutex.Unlock()
	held := a.televisions[television.Metadata.Name]
	held.Status.Displays = nil
	for _, name := range displays {
		held.Status.Displays = append(held.Status.Displays, TelevisionDisplay{Name: name, PhysicalAddress: "1.3.0.0"})
	}
}

// sessionWriteCount returns how many session writes the fake received.
func (a *cecAPI) sessionWriteCount() int {
	a.mutex.Lock()
	defer a.mutex.Unlock()
	return a.sessionWrites
}

// study returns a Television on the study bus.
func study(session *TelevisionSession) Television {
	return Television{Metadata: ObjectMeta{Name: "study"}, Spec: TelevisionSpec{CEC: &TelevisionCEC{Bus: "study"}}, Status: TelevisionStatus{Session: session}}
}

// denRoom returns the roomTelevision of Player media/den's session on
// input MPLAY, which names Display acm-0001-receiver.
func denRoom(api *cecAPI, log *logBuffer) *roomTelevision {
	monitors := map[string]string{"MPLAY": "acm-0001-receiver", "GAME": "bnq-0002-monitor"}
	return newTelevisionSessions(api.client).room(newReceiverLog(log, "den"), "media/den", "MPLAY", func(input string) string { return monitors[input] })
}

func TestTheWakeReachesTheTelevisionThatListsTheDisplay(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		api := startCECAPI(t)
		log := &logBuffer{}
		api.showing(lounge(""), "acm-0001-receiver")
		api.showing(study(nil), "bnq-0002-monitor")
		began := time.Now()

		denRoom(api, log).woke("a Play started on Player media/den")

		television, _ := api.television("lounge")
		session := television.Status.Session
		if session == nil {
			t.Fatal("Television lounge has no session, want one held by media/den")
		}
		mustMatch(t, session.Player, "media/den")
		mustMatch(t, session.Display, "acm-0001-receiver")
		mustMatch(t, session.Awake, true)
		woke, err := time.Parse(time.RFC3339, session.WokeAt)
		mustSucceed(t, err)
		if woke.Before(began.Add(-time.Millisecond)) || woke.After(time.Now()) {
			t.Errorf("wokeAt is %s, want a time between %s and now", session.WokeAt, began.UTC().Format(time.RFC3339))
		}
		other, _ := api.television("study")
		if other.Status.Session != nil {
			t.Errorf("Television study has session %+v, want none", other.Status.Session)
		}
		mustDeepEqual(t, linesWith(log, "Receiver den"), []string{
			"Receiver den: a Play started on Player media/den; asked Television lounge to wake and show Display acm-0001-receiver",
		})
	})
}

// A wake writes nothing when no Television lists the input's Display,
// or when the input names no Display.
func TestAWakeThatWritesNothing(t *testing.T) {
	cases := []struct {
		name     string
		input    string
		displays []string
	}{
		{"no Television lists the Display", "MPLAY", []string{"bnq-0002-monitor"}},
		{"the input names no Display", "TUNER", []string{"acm-0001-receiver"}},
	}
	t.Parallel()
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				api := startCECAPI(t)
				log := &logBuffer{}
				api.showing(lounge(""), c.displays...)
				room := denRoom(api, log)
				room.input = c.input

				room.woke("a Play started on Player media/den")

				mustMatch(t, api.sessionWriteCount(), 0)
				mustDeepEqual(t, linesWith(log, "Receiver den"), []string(nil))
			})
		})
	}
}

// A session that starts before the first pass ends is adopted: the
// Television gets the session's awake state and keeps the wokeAt and
// standbyAt of the same Player and Display, so nothing wakes and nothing
// goes to standby.
func TestAStartingSessionAdoptsAndWakesNothing(t *testing.T) {
	held := wokeAt(time.Now().Add(-time.Hour))
	slept := *held
	slept.Awake = false
	other := wokeNow()
	other.Player = "media/study"
	pressed := pressedOff(held)
	cases := []struct {
		name  string
		held  *TelevisionSession
		awake bool
		want  TelevisionSession
	}{
		{"a Television with no session", nil, true,
			TelevisionSession{Player: "media/den", Display: "acm-0001-receiver", Awake: true}},
		{"a restart while the TV is awake", held, true,
			TelevisionSession{Player: "media/den", Display: "acm-0001-receiver", Awake: true, WokeAt: held.WokeAt}},
		{"a restart after a sleep", &slept, true,
			TelevisionSession{Player: "media/den", Display: "acm-0001-receiver", Awake: true, WokeAt: held.WokeAt}},
		{"a session that starts idle", held, false,
			TelevisionSession{Player: "media/den", Display: "acm-0001-receiver", WokeAt: held.WokeAt}},
		{"a restart after a powerAsk", pressed, false,
			TelevisionSession{Player: "media/den", Display: "acm-0001-receiver", WokeAt: held.WokeAt, StandbyAt: pressed.StandbyAt}},
		{"another Player's session", other, true,
			TelevisionSession{Player: "media/den", Display: "acm-0001-receiver", Awake: true}},
	}
	t.Parallel()
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				api := startCECAPI(t)
				log := &logBuffer{}
				api.showing(waking(c.held), "acm-0001-receiver")

				denRoom(api, log).opened(c.awake, "a Play started on Player media/den")

				television, _ := api.television("lounge")
				mustDeepEqual(t, *television.Status.Session, c.want)
				mustDeepEqual(t, linesWith(log, "Receiver den"), []string(nil))
			})
		})
	}
}

// A session that starts and finds an identical session already on the
// Television writes nothing.
func TestAnAdoptedSessionAlreadyInPlaceWritesNothing(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		api := startCECAPI(t)
		api.showing(waking(wokeNow()), "acm-0001-receiver")

		denRoom(api, &logBuffer{}).opened(true, "a Play started on Player media/den")

		mustMatch(t, api.sessionWriteCount(), 0)
	})
}

// A session that starts on another Display removes the Player's
// session from the Television of the old Display.
func TestASessionOnAnotherDisplayLeavesTheOldTelevision(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		api := startCECAPI(t)
		api.showing(waking(wokeNow()), "acm-0001-receiver")
		api.showing(study(nil), "bnq-0002-monitor")
		room := denRoom(api, &logBuffer{})
		room.input = "GAME"

		room.opened(true, "a Play started on Player media/den")

		old, _ := api.television("lounge")
		if old.Status.Session != nil {
			t.Errorf("Television lounge has session %+v, want none after the Display moved", old.Status.Session)
		}
		moved, _ := api.television("study")
		mustDeepEqual(t, *moved.Status.Session, TelevisionSession{Player: "media/den", Display: "bnq-0002-monitor", Awake: true})
	})
}

// Of a declared and a discovered Television on one bus, the declared
// one gets the session.
func TestTheWakeReachesTheDeclaredTelevision(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		api := startCECAPI(t)
		api.showing(discoveredTV("den"), "acm-0001-receiver")
		api.showing(lounge(""), "acm-0001-receiver")

		denRoom(api, &logBuffer{}).woke("a Play started on Player media/den")

		lounge, _ := api.television("lounge")
		discovered, _ := api.television("den")
		if lounge.Status.Session == nil || discovered.Status.Session != nil {
			t.Errorf("declared Television lounge has session %+v and discovered Television den has %+v, want a session only on lounge", lounge.Status.Session, discovered.Status.Session)
		}
	})
}

// A sleep marks the Player's session asleep and keeps its wokeAt. A
// second sleep writes nothing, and another Player's session is not
// changed.
func TestASleepReachesOnlyThePlayersSession(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		api := startCECAPI(t)
		held := wokeNow()
		api.showing(waking(held), "acm-0001-receiver")
		other := wokeNow()
		other.Player = "media/study"
		api.showing(study(other))
		room := denRoom(api, &logBuffer{})

		room.slept()
		room.slept()

		asleep, _ := api.television("lounge")
		mustDeepEqual(t, asleep.Status.Session, &TelevisionSession{Player: "media/den", Display: "acm-0001-receiver", WokeAt: held.WokeAt})
		mustMatch(t, api.sessionWriteCount(), 1)
		untouched, _ := api.television("study")
		mustDeepEqual(t, untouched.Status.Session, other)
	})
}

// A refused write logs one line with the API server's error.
func TestARefusedSessionWriteIsLogged(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		api := startCECAPI(t)
		log := &logBuffer{}
		api.showing(lounge(""), "acm-0001-receiver")
		api.noSessionWrites = true

		denRoom(api, log).woke("a Play started on Player media/den")

		mustDeepEqual(t, linesWith(log, "Receiver den"), []string{
			"Receiver den: a Play started on Player media/den; asked Television lounge to wake and show Display acm-0001-receiver; the command was not applied: PATCH " +
				televisionPath("lounge") + "/status?fieldManager=" + sessionFieldManager + "&force=true: 500 Internal Server Error:",
		})
	})
}

// A session writes and logs nothing when the Television CRD is not
// installed, so a cluster with no CEC gets no log lines. A refused
// Television list writes nothing either.
func TestARoomWithNoTelevisionIsQuiet(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		for _, refused := range []bool{false, true} {
			api := startCECAPI(t)
			api.showing(waking(wokeNow()), "acm-0001-receiver")
			api.noTelevisionDefinition = !refused
			api.refuseTelevisions(refused)
			log := &logBuffer{}
			room := denRoom(api, log)

			room.opened(true, "a Play started on Player media/den")
			room.woke("a Play started on Player media/den")
			room.slept()

			mustMatch(t, api.sessionWriteCount(), 0)
			mustDeepEqual(t, linesWith(log, "Receiver den"), []string(nil))
		}
	})
}
