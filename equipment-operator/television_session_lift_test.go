package main

// The TV's status.session outlives a Receiver session for a bounded
// time, the Deployment's session writes go one at a time, and each
// writer of a Television keeps the others' fields.

import (
	"sync"
	"testing"
	"testing/synctest"
	"time"
)

// A session that the media operator lifts and writes again within the
// delay keeps the TV's session and its wake, so the TV does not wake
// unasked when the session returns.
func TestABriefLiftKeepsTheSessionAndWakesNothing(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		api := startCECAPI(t)
		held := wokeAt(time.Now().Add(-time.Minute))
		api.showing(waking(held), "acm-0001-receiver")
		sessions := newTelevisionSessions(api.client)
		t.Cleanup(sessions.stop)
		monitors := func(string) string { return "acm-0001-receiver" }

		sessions.lift("media/den")
		sessions.room(newReceiverLog(&logBuffer{}, "den"), "media/den", "MPLAY", monitors).opened(true, "a Play started on Player media/den")
		time.Sleep(3 * sessionLiftDelay)

		television, _ := api.television("lounge")
		mustDeepEqual(t, television.Status.Session, held)
	})
}

// A session that does not return within the delay is removed from the
// TV, and a removal the API server refuses is tried again.
func TestALiftRemovesTheSessionAfterTheDelay(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		api := startCECAPI(t)
		api.showing(waking(wokeNow()), "acm-0001-receiver")
		other := wokeNow()
		other.Player = "media/study"
		api.showing(study(other))
		sessions := newTelevisionSessions(api.client)
		t.Cleanup(sessions.stop)
		api.noSessionWrites = true

		sessions.lift("media/den")
		time.Sleep(2 * sessionLiftDelay)
		kept, _ := api.television("lounge")
		if kept.Status.Session == nil {
			t.Fatal("a refused removal removed the session")
		}
		api.mutex.Lock()
		api.noSessionWrites = false
		api.mutex.Unlock()

		api.waitForTelevision(t, "lounge", func(television Television) bool { return television.Status.Session == nil })
		untouched, _ := api.television("study")
		mustDeepEqual(t, untouched.Status.Session, other)
	})
}

// A lift whose list of Televisions the API server refuses removes
// nothing, and is tried again until the list answers.
func TestALiftWhoseListFailsIsTriedAgain(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		api := startCECAPI(t)
		api.showing(waking(wokeNow()), "acm-0001-receiver")
		sessions := newTelevisionSessions(api.client)
		t.Cleanup(sessions.stop)
		api.refuseTelevisions(true)

		sessions.lift("media/den")
		time.Sleep(2 * sessionLiftDelay)
		kept, _ := api.television("lounge")
		if kept.Status.Session == nil {
			t.Fatal("a lift with no list removed the session")
		}
		api.refuseTelevisions(false)

		api.waitForTelevision(t, "lounge", func(television Television) bool { return television.Status.Session == nil })
	})
}

// A lift that waits at operator shutdown is dropped: the next operator
// adopts the session.
func TestAShutdownDropsAWaitingLift(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		api := startCECAPI(t)
		held := wokeNow()
		api.showing(waking(held), "acm-0001-receiver")
		sessions := newTelevisionSessions(api.client)

		sessions.lift("media/den")
		sessions.stop()
		time.Sleep(3 * sessionLiftDelay)

		television, _ := api.television("lounge")
		mustDeepEqual(t, television.Status.Session, held)
	})
}

// slowSessions makes each session write take a while and counts the
// writes in flight at once.
func (a *cecAPI) slowSessions(delay time.Duration) {
	a.mutex.Lock()
	defer a.mutex.Unlock()
	a.sessionDelay = delay
}

// Each session write reads the Television and applies what it read, so
// a sleep and a wake at once could undo each other. The writer takes
// them one at a time.
func TestSessionWritesGoOneAtATime(t *testing.T) {
	t.Parallel()
	api := startCECAPI(t)
	api.showing(waking(wokeNow()), "acm-0001-receiver")
	api.slowSessions(10 * time.Millisecond)
	room := denRoom(api, &logBuffer{})
	var writers sync.WaitGroup

	for range 5 {
		writers.Go(func() { room.woke("a Play started on Player media/den") })
		writers.Go(room.slept)
	}
	writers.Wait()

	api.mutex.Lock()
	defer api.mutex.Unlock()
	mustMatch(t, api.sessionMostAtOnce, 1)
}

// The Deployment end to end: a session that starts on a Receiver is
// adopted and wakes nothing, a Play that starts on it is a wake, and a
// deleted Receiver lifts the TV's session after the delay.
func TestAPlayReachesTheTelevisionAndADeletedReceiverLiftsIt(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		api := startCECAPI(t)
		amp := startFakeDenon(t)
		receiver := idleReceiver(amp.address(), ReceiverVolume{Max: 69.5})
		receiver.Spec.Inputs = []ReceiverInput{{Name: "GAME", Machine: "node-1", Monitor: "acm-0001-receiver"}}
		api.putReceiver(receiver)
		api.showing(lounge(""), "acm-0001-receiver")
		operator := newController(api.client, testMetrics(t))
		operator.dial = testNetwork.dial
		operator.log = &logBuffer{}
		t.Cleanup(operator.stopAll)

		mustSucceed(t, operator.pass(t.Context()))
		adopted := api.waitForTelevision(t, "lounge", func(television Television) bool { return television.Status.Session != nil })
		mustMatch(t, adopted.Status.Session.WokeAt, "")

		playing := receiver
		playing.Spec.Session.Active = true
		api.putReceiver(playing)
		mustSucceed(t, operator.pass(t.Context()))
		woken := api.waitForTelevision(t, "lounge", func(television Television) bool {
			return television.Status.Session != nil && television.Status.Session.WokeAt != ""
		})
		mustMatch(t, woken.Status.Session.Player, "house/theater")

		api.removeReceiver("theater")
		mustSucceed(t, operator.pass(t.Context()))
		time.Sleep(sessionLiftDelay)
		api.waitForTelevision(t, "lounge", func(television Television) bool { return television.Status.Session == nil })
	})
}

// removeReceiver deletes a Receiver, as a person or a prune does.
func (a *cecAPI) removeReceiver(name string) {
	a.mutex.Lock()
	defer a.mutex.Unlock()
	delete(a.receivers, name)
	a.changed()
}

// Each writer of a Television keeps the others' fields. The fake holds
// what server-side apply does for these writers: each writer applies
// under a field manager of its own and states every field it owns in
// every apply, so an apply replaces exactly that writer's fields and
// leaves every field another manager owns; the conditions are a map
// keyed by type, so each writer's conditions merge with the others'. A
// person's apply of the spec reaches no status field, because the
// status subresource splits the two paths. This test applies each
// writer's fields, then every other writer's, and checks that the
// first writer's fields survive.
func TestTheWritersKeepEachOthersFields(t *testing.T) {
	session := wokeNow()
	now := time.Now().UTC().Truncate(time.Second)
	writers := []struct {
		name    string
		apply   func(api *cecAPI, stored Television)
		survive func(t *testing.T, television Television)
	}{
		{"the person's spec", func(api *cecAPI, _ Television) { api.putTelevision(lounge(TelevisionOn)) },
			func(t *testing.T, television Television) { mustMatch(t, television.Spec.Power, TelevisionOn) }},
		{"the Deployment's session", func(api *cecAPI, _ Television) { mustSucceed(t, ApplyTelevisionSession(api.client, "lounge", session)) },
			func(t *testing.T, television Television) { mustDeepEqual(t, television.Status.Session, session) }},
		{"the Deployment's derived status", func(api *cecAPI, _ Television) {
			mustSucceed(t, ApplyTelevisionDerived(api.client, "lounge", televisionDerived{power: "On", activeSource: "1.3.0.0",
				reachable: Condition{Type: conditionReachable, Status: ConditionTrue, LastTransitionTime: now}}))
		}, func(t *testing.T, television Television) {
			mustMatch(t, television.Status.ActiveSource, "1.3.0.0")
			mustMatch(t, conditionOf(television.Status.Conditions, conditionReachable).Status, ConditionTrue)
		}},
		{"the power result", func(api *cecAPI, stored Television) {
			mustSucceed(t, ApplyTelevisionPower(api.client, &stored, "node-1", 1, Condition{Type: conditionPowerApplied, Status: ConditionTrue, LastTransitionTime: now}))
		}, func(t *testing.T, television Television) {
			mustMatch(t, television.Status.PowerGeneration, int64(1))
			mustMatch(t, conditionOf(television.Status.Conditions, conditionPowerApplied).Status, ConditionTrue)
		}},
		{"the wake result", func(api *cecAPI, stored Television) {
			mustSucceed(t, ApplyTelevisionWake(api.client, &stored, "node-1", session.WokeAt, Condition{Type: conditionWakeApplied, Status: ConditionTrue, LastTransitionTime: now}))
		}, func(t *testing.T, television Television) {
			mustMatch(t, television.Status.WokeAt, session.WokeAt)
			mustMatch(t, conditionOf(television.Status.Conditions, conditionWakeApplied).Status, ConditionTrue)
		}},
	}
	t.Parallel()
	for _, first := range writers {
		t.Run(first.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				api := startCECAPI(t)
				api.putTelevision(lounge(""))
				stored, _ := api.television("lounge")
				first.apply(api, stored)

				for _, other := range writers {
					if other.name != first.name {
						other.apply(api, stored)
					}
				}

				television, _ := api.television("lounge")
				first.survive(t, television)
			})
		})
	}
}

// liveSessions is a writer past the operator's first pass, when a
// session that appears is a change a person caused.
func liveSessions(api *cecAPI, t *testing.T) *televisionSessions {
	t.Helper()
	sessions := newTelevisionSessions(api.client)
	sessions.markLive()
	t.Cleanup(sessions.stop)
	return sessions
}

// While the operator runs, a session that appears awake is a Play
// that started on a Player with no standing session, and it wakes the
// TV once. An idle one wakes nothing.
func TestASessionThatAppearsAwakeWhileRunningWakes(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		api := startCECAPI(t)
		api.showing(lounge(""), "acm-0001-receiver")
		log := &logBuffer{}
		room := liveSessions(api, t).room(newReceiverLog(log, "den"), "media/den", "MPLAY", func(string) string { return "acm-0001-receiver" })

		room.opened(true, "a Play started on Player media/den and its screen woke")

		television, _ := api.television("lounge")
		if television.Status.Session == nil || television.Status.Session.WokeAt == "" || !television.Status.Session.Awake {
			t.Fatalf("the session did not wake the TV: %+v", television.Status.Session)
		}
		mustDeepEqual(t, linesWith(log, "Receiver den"), []string{
			"Receiver den: a Play started on Player media/den and its screen woke; asked Television lounge to wake and show Display acm-0001-receiver",
		})
		mustMatch(t, api.sessionWriteCount(), 1)
	})
}

func TestASessionThatAppearsIdleWhileRunningWakesNothing(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		api := startCECAPI(t)
		api.showing(lounge(""), "acm-0001-receiver")
		room := liveSessions(api, t).room(newReceiverLog(&logBuffer{}, "den"), "media/den", "MPLAY", func(string) string { return "acm-0001-receiver" })

		room.opened(false, "")

		television, _ := api.television("lounge")
		mustDeepEqual(t, *television.Status.Session, TelevisionSession{Player: "media/den", Display: "acm-0001-receiver"})
	})
}

// A session of the same Player and Display that returns while its lift
// waits is the same session, not a new one, so it wakes nothing. One
// that returns on another Display is a change, and wakes.
func TestASessionReturningWithinItsLiftWakesNothing(t *testing.T) {
	cases := []struct {
		name  string
		input string
		wakes bool
	}{
		{"the same Display", "MPLAY", false},
		{"another Display", "GAME", true},
	}
	t.Parallel()
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				api := startCECAPI(t)
				held := wokeAt(time.Now().Add(-time.Minute))
				api.showing(waking(held), "acm-0001-receiver")
				api.showing(study(nil), "bnq-0002-monitor")
				sessions := newTelevisionSessions(api.client)
				t.Cleanup(sessions.stop)
				monitors := map[string]string{"MPLAY": "acm-0001-receiver", "GAME": "bnq-0002-monitor"}
				monitor := func(input string) string { return monitors[input] }
				log := newReceiverLog(&logBuffer{}, "den")
				// The session stands at the operator's start, so it is adopted.
				sessions.room(log, "media/den", "MPLAY", monitor).opened(true, "a Play started on Player media/den")
				sessions.markLive()
				before := api.sessionWriteCount()

				sessions.lift("media/den")
				sessions.room(log, "media/den", c.input, monitor).opened(true, "a Play started on Player media/den")
				time.Sleep(3 * sessionLiftDelay)

				woken := false
				for _, name := range []string{"lounge", "study"} {
					television, _ := api.television(name)
					woken = woken || (television.Status.Session != nil && television.Status.Session.WokeAt != held.WokeAt && television.Status.Session.WokeAt != "")
				}
				mustMatch(t, woken, c.wakes)
				if !c.wakes {
					television, _ := api.television("lounge")
					mustDeepEqual(t, television.Status.Session, held)
					mustMatch(t, api.sessionWriteCount(), before)
				}
			})
		})
	}
}

// The Deployment end to end, by when it observes. A session that stands
// at its first pass is adopted with no wake. A session that appears
// awake while it runs, after the last one's lift ran out, wakes the TV
// once. A new wiring of the Receiver starts a new unit, whose session
// returns within the lift and wakes nothing.
func TestTheDeploymentWakesOnlyForAChangeItSees(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		api := startCECAPI(t)
		amp := startFakeDenon(t)
		inputs := []ReceiverInput{{Name: "GAME", Machine: "node-1", Monitor: "acm-0001-receiver"}}
		standing := playingReceiver(amp.address(), ReceiverVolume{Max: 69.5})
		standing.Spec.Inputs = inputs
		api.putReceiver(standing)
		api.showing(lounge(""), "acm-0001-receiver")
		operator := newController(api.client, testMetrics(t))
		operator.dial = testNetwork.dial
		operator.log = &logBuffer{}
		t.Cleanup(operator.stopAll)

		mustSucceed(t, operator.pass(t.Context()))
		adopted := api.waitForTelevision(t, "lounge", func(television Television) bool { return television.Status.Session != nil })
		mustMatch(t, adopted.Status.Session.WokeAt, "")
		mustMatch(t, adopted.Status.Session.Awake, true)

		// The media operator lifts the session, and the lift runs out.
		idle := testReceiver("theater", amp.address())
		idle.Spec.Inputs = inputs
		api.putReceiver(idle)
		mustSucceed(t, operator.pass(t.Context()))
		time.Sleep(sessionLiftDelay)
		api.waitForTelevision(t, "lounge", func(television Television) bool { return television.Status.Session == nil })
		api.putReceiver(standing)
		mustSucceed(t, operator.pass(t.Context()))
		woken := api.waitForTelevision(t, "lounge", func(television Television) bool {
			return television.Status.Session != nil && television.Status.Session.WokeAt != ""
		})

		rewired := standing
		rewired.Spec.Denon = &DenonProtocol{Address: amp.alias(t)}
		api.putReceiver(rewired)
		mustSucceed(t, operator.pass(t.Context()))
		mustMatch(t, operator.units["theater"].address, rewired.Spec.Denon.Address)
		time.Sleep(3 * sessionLiftDelay)
		kept, _ := api.television("lounge")
		mustDeepEqual(t, kept.Status.Session, woken.Status.Session)
	})
}
