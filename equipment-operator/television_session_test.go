package main

// The session match against the fake API server: which Television a
// Receiver session's events reach, what the Deployment writes in its
// status.session, and when it writes nothing. A session that starts is
// adopted and wakes nothing; only a wake the session saw happen writes
// a new wokeAt. television_session_lift_test.go holds the lifts, the
// serialized writes, and the writers' fields.

import (
	"testing"
	"time"
)

// showing stores a Television whose status lists Displays, as the
// Deployment's pass derives them.
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

// sessionWriteCount answers how many session writes the fake took.
func (a *cecAPI) sessionWriteCount() int {
	a.mutex.Lock()
	defer a.mutex.Unlock()
	return a.sessionWrites
}

// study is a Television on another bus, which shows another Display.
func study(session *TelevisionSession) Television {
	return Television{Metadata: ObjectMeta{Name: "study"}, Spec: TelevisionSpec{CEC: &TelevisionCEC{Bus: "study"}}, Status: TelevisionStatus{Session: session}}
}

// denRoom is the link of Player media/den's session on input MPLAY,
// which carries Display acm-0001-receiver.
func denRoom(api *cecAPI, log *logBuffer) *roomTelevision {
	monitors := map[string]string{"MPLAY": "acm-0001-receiver", "GAME": "bnq-0002-monitor"}
	return newTelevisionSessions(api.client).room(newReceiverLog(log, "den"), "media/den", "MPLAY", func(input string) string { return monitors[input] })
}

func TestTheWakeReachesTheTelevisionThatListsTheDisplay(t *testing.T) {
	api := startCECAPI(t)
	log := &logBuffer{}
	api.showing(lounge(""), "acm-0001-receiver")
	api.showing(study(nil), "bnq-0002-monitor")
	began := time.Now()

	denRoom(api, log).woke("a Play started on Player media/den")

	television, _ := api.television("lounge")
	session := television.Status.Session
	if session == nil {
		t.Fatal("the Television holds no session")
	}
	mustMatch(t, session.Player, "media/den")
	mustMatch(t, session.Display, "acm-0001-receiver")
	mustMatch(t, session.Awake, true)
	woke, err := time.Parse(time.RFC3339, session.WokeAt)
	mustSucceed(t, err)
	if woke.Before(began.Add(-time.Millisecond)) || woke.After(time.Now()) {
		t.Errorf("wokeAt %s is not the time of the wake", session.WokeAt)
	}
	other, _ := api.television("study")
	if other.Status.Session != nil {
		t.Errorf("Television study holds %+v", other.Status.Session)
	}
	mustDeepEqual(t, linesWith(log, "Receiver den"), []string{
		"Receiver den: a Play started on Player media/den; asked Television lounge to wake and show Display acm-0001-receiver",
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
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			api := startCECAPI(t)
			log := &logBuffer{}
			api.showing(lounge(""), c.displays...)
			room := denRoom(api, log)
			room.input = c.input

			room.woke("a Play started on Player media/den")

			mustMatch(t, api.sessionWriteCount(), 0)
			mustDeepEqual(t, linesWith(log, "Receiver den"), []string(nil))
		})
	}
}

// A session that starts adopts the session the TV holds, with the
// flags it starts with, and never writes a new wokeAt: at an operator
// restart, after a brief lift, for a Television created again, and for
// a session a person slept before the restart. Nothing wakes.
func TestAStartingSessionAdoptsAndWakesNothing(t *testing.T) {
	held := wokeAt(time.Now().Add(-time.Hour))
	slept := *held
	slept.Awake = false
	other := wokeNow()
	other.Player = "media/study"
	cases := []struct {
		name  string
		held  *TelevisionSession
		awake bool
		want  TelevisionSession
	}{
		{"a Television with no session", nil, true,
			TelevisionSession{Player: "media/den", Display: "acm-0001-receiver", Awake: true}},
		{"a restart under a standing wake", held, true,
			TelevisionSession{Player: "media/den", Display: "acm-0001-receiver", Awake: true, WokeAt: held.WokeAt}},
		{"a restart after a sleep", &slept, true,
			TelevisionSession{Player: "media/den", Display: "acm-0001-receiver", Awake: true, WokeAt: held.WokeAt}},
		{"a session that starts idle", held, false,
			TelevisionSession{Player: "media/den", Display: "acm-0001-receiver", WokeAt: held.WokeAt}},
		{"another Player's session", other, true,
			TelevisionSession{Player: "media/den", Display: "acm-0001-receiver", Awake: true}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			api := startCECAPI(t)
			log := &logBuffer{}
			api.showing(waking(c.held), "acm-0001-receiver")

			denRoom(api, log).opened(c.awake, "a Play started on Player media/den")

			television, _ := api.television("lounge")
			mustDeepEqual(t, *television.Status.Session, c.want)
			mustDeepEqual(t, linesWith(log, "Receiver den"), []string(nil))
		})
	}
}

// A session that starts and finds its session already in place writes
// nothing.
func TestAnAdoptedSessionThatStandsWritesNothing(t *testing.T) {
	api := startCECAPI(t)
	api.showing(waking(wokeNow()), "acm-0001-receiver")

	denRoom(api, &logBuffer{}).opened(true, "a Play started on Player media/den")

	mustMatch(t, api.sessionWriteCount(), 0)
}

// A session that starts on another Display takes the Player's session
// off the TV that showed the old Display.
func TestASessionOnAnotherDisplayLeavesTheOldTelevision(t *testing.T) {
	api := startCECAPI(t)
	api.showing(waking(wokeNow()), "acm-0001-receiver")
	api.showing(study(nil), "bnq-0002-monitor")
	room := denRoom(api, &logBuffer{})
	room.input = "GAME"

	room.opened(true, "a Play started on Player media/den")

	old, _ := api.television("lounge")
	if old.Status.Session != nil {
		t.Errorf("the old Television holds %+v", old.Status.Session)
	}
	moved, _ := api.television("study")
	mustDeepEqual(t, *moved.Status.Session, TelevisionSession{Player: "media/den", Display: "bnq-0002-monitor", Awake: true})
}

// Of two Televisions on one bus, the one in charge gets the session.
func TestTheWakeReachesTheTelevisionInCharge(t *testing.T) {
	api := startCECAPI(t)
	api.showing(discoveredTV("den"), "acm-0001-receiver")
	api.showing(lounge(""), "acm-0001-receiver")

	denRoom(api, &logBuffer{}).woke("a Play started on Player media/den")

	lounge, _ := api.television("lounge")
	discovered, _ := api.television("den")
	if lounge.Status.Session == nil || discovered.Status.Session != nil {
		t.Errorf("lounge holds %+v, den holds %+v", lounge.Status.Session, discovered.Status.Session)
	}
}

// A sleep marks the session asleep and keeps its wake, once, and leaves
// another Player's session alone.
func TestASleepReachesOnlyThePlayersSession(t *testing.T) {
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
}

// A write the API server refuses is one line with the server's words.
func TestARefusedSessionWriteIsLogged(t *testing.T) {
	api := startCECAPI(t)
	log := &logBuffer{}
	api.showing(lounge(""), "acm-0001-receiver")
	api.noSessionWrites = true

	denRoom(api, log).woke("a Play started on Player media/den")

	mustDeepEqual(t, linesWith(log, "Receiver den"), []string{
		"Receiver den: a Play started on Player media/den; asked Television lounge to wake and show Display acm-0001-receiver; the command failed: PATCH " +
			televisionPath("lounge") + "/status?fieldManager=" + sessionFieldManager + "&force=true: 500 Internal Server Error:",
	})
}

// A cluster without the Television definition wakes no TV and writes
// no line, so a room with no CEC is quiet, and a Television list the
// API server refuses writes nothing either.
func TestARoomWithNoTelevisionIsQuiet(t *testing.T) {
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
}
