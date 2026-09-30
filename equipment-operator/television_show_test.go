package main

// The show ask against the fake API server: a home press writes a new
// status.session.showAt on the TV that shows the session's input, while
// the session holds the room awake, and writes nothing otherwise.

import (
	"testing"
	"time"
)

func TestAShowWritesTheTimeOnTheTelevision(t *testing.T) {
	t.Parallel()
	api := startCECAPI(t)
	log := &logBuffer{}
	held := wokeAt(time.Now().Add(-time.Minute))
	api.showing(waking(held), "acm-0001-receiver")
	began := time.Now()

	denRoom(api, log).show("the commands topic asks input.show")

	television, _ := api.television("lounge")
	session := television.Status.Session
	mustMatch(t, session.WokeAt, held.WokeAt)
	mustMatch(t, session.Awake, true)
	shown, err := time.Parse(time.RFC3339, session.ShowAt)
	mustSucceed(t, err)
	if shown.Before(began.Add(-time.Millisecond)) || shown.After(time.Now()) {
		t.Errorf("showAt %s is not the time of the ask", session.ShowAt)
	}
	mustDeepEqual(t, linesWith(log, "Receiver den"), []string{
		"Receiver den: the commands topic asks input.show; asked Television lounge to show Display acm-0001-receiver",
	})
}

// A session that does not hold the room awake asks the TV for nothing:
// the room is off, or another Player holds it.
func TestAShowThatWritesNothing(t *testing.T) {
	t.Parallel()
	asleep := wokeNow()
	asleep.Awake = false
	other := wokeNow()
	other.Player = "media/study"
	cases := []struct {
		name string
		held *TelevisionSession
		line string
	}{
		{"a session that is asleep", asleep,
			"Receiver den: the commands topic asks input.show; asked Television lounge for nothing, because Player media/den's session does not hold the room awake"},
		{"another Player's session", other,
			"Receiver den: the commands topic asks input.show; asked Television lounge for nothing, because Player media/den's session does not hold the room awake"},
		{"no session", nil,
			"Receiver den: the commands topic asks input.show; asked Television lounge for nothing, because Player media/den's session does not hold the room awake"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			api := startCECAPI(t)
			log := &logBuffer{}
			api.showing(waking(c.held), "acm-0001-receiver")

			denRoom(api, log).show("the commands topic asks input.show")

			mustMatch(t, api.sessionWriteCount(), 0)
			mustDeepEqual(t, linesWith(log, "Receiver den"), []string{c.line})
		})
	}
}
