package main

import (
	"testing"
	"testing/synctest"
	"time"
)

func TestAShowWritesTheTimeToTheTelevisionSession(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		api := startCECAPI(t)
		log := &logBuffer{}
		held := wokeAt(time.Now().Add(-time.Minute))
		api.showing(waking(held), "acm-0001-receiver")
		began := time.Now()

		denRoom(api, log).show("status.session.inputAsk at 2026-10-04T12:15:25.001Z requests show")

		television, _ := api.television("lounge")
		session := television.Status.Session
		mustMatch(t, session.WokeAt, held.WokeAt)
		mustMatch(t, session.Awake, true)
		shown, err := time.Parse(time.RFC3339, session.ShowAt)
		mustSucceed(t, err)
		if shown.Before(began.Add(-time.Millisecond)) || shown.After(time.Now()) {
			t.Errorf("showAt is %s, want a time between the start of the show and now", session.ShowAt)
		}
		mustDeepEqual(t, linesWith(log, "Receiver den"), []string{
			"Receiver den: status.session.inputAsk at 2026-10-04T12:15:25.001Z requests show; asked Television lounge to show Display acm-0001-receiver",
		})
	})
}

func TestAShowWritesNothingWhenTheSessionDoesNotHoldTheRoomAwake(t *testing.T) {
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
			"Receiver den: status.session.inputAsk at 2026-10-04T12:15:25.001Z requests show; did not ask Television lounge to show the Display, because the Television has no awake session of Player media/den on this Display"},
		{"another Player's session", other,
			"Receiver den: status.session.inputAsk at 2026-10-04T12:15:25.001Z requests show; did not ask Television lounge to show the Display, because the Television has no awake session of Player media/den on this Display"},
		{"no session", nil,
			"Receiver den: status.session.inputAsk at 2026-10-04T12:15:25.001Z requests show; did not ask Television lounge to show the Display, because the Television has no awake session of Player media/den on this Display"},
	}
	t.Parallel()
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				api := startCECAPI(t)
				log := &logBuffer{}
				api.showing(waking(c.held), "acm-0001-receiver")

				denRoom(api, log).show("status.session.inputAsk at 2026-10-04T12:15:25.001Z requests show")

				mustMatch(t, api.sessionWriteCount(), 0)
				mustDeepEqual(t, linesWith(log, "Receiver den"), []string{c.line})
			})
		})
	}
}
